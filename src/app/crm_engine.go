// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"notmutt/app/ai"
	"notmutt/app/aicmd"
	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/lib/crm"
	"notmutt/lib/crm/hubspot"
	"notmutt/notmuch"
	"notmutt/tui"
)

// crmAdapterState is the crm wire's resolved state: the neutral client the
// workflow drives, the [ai] entry its chat calls run on, the marker
// property the write-back records, the bus jobs publish on, and the gated
// mail-grounding closure RunDraft consumes. provider is the client's
// routing id - the row filter the action handler applies.
type crmAdapterState struct {
	client   crm.Client
	provider string
	aiCfg    config.AIProvider
	marker   string
	bus      *core.Bus
	ground   crm.MailGroundFn
}

// crmAdapter is the resolved wire state; nil while the CRM is dormant or
// its setup failed. Written once in crmWire before the subscriber starts;
// the workflow jobs read it, never rebuild the client. Session-local.
var crmAdapter *crmAdapterState

// crmHubspotNew is the hubspot client factory seam crmWire builds its
// client through: the production value constructs against the live API,
// and the integration test swaps it to point a real client at an httptest
// server via hubspot.NewClientURL.
var crmHubspotNew = func(ctx context.Context, key []byte) crm.Client {
	return hubspot.NewClient(ctx, key)
}

// crmBriefMu guards crmBriefText, the adapter's briefing cache (the draft
// seam's session map): each CrmBriefing the wire observes stores its text
// keyed by (provider, contact id) so the action handler can hand RunDraft
// the briefing the row's draft is grounded on - the 2-arg action hook
// carries the contact row, never the text.
var (
	crmBriefMu   sync.Mutex
	crmBriefText = map[string]string{}
)

func crmBriefKey(provider, id string) string {
	return provider + "\x00" + id
}

func crmCacheBriefing(b core.CrmBriefing) {
	crmBriefMu.Lock()
	crmBriefText[crmBriefKey(b.Provider, b.ContactID)] = b.Text
	crmBriefMu.Unlock()
}

func crmBriefingText(provider, id string) string {
	crmBriefMu.Lock()
	defer crmBriefMu.Unlock()
	return crmBriefText[crmBriefKey(provider, id)]
}

// crmDraftRef is the compose map's value: the CRM routing id and row the
// opened compose belongs to, for the send hook to write back.
type crmDraftRef struct {
	Provider  string
	ContactID string
}

// crmComposeMu guards crmComposeRefs: entries are added on the CrmDraft
// reaction and removed when the SendResult they route arrives (OK:true) - a
// mutex keeps that safe when the send path races in from another goroutine.
// A CRM-origin compose closed without sending emits no SendResult and leaves
// its entry for the session: accepted deliberately (bounded - one small pair
// per discarded draft, session-local, lost on exit).
var (
	crmComposeMu   sync.Mutex
	crmComposeRefs = map[string]crmDraftRef{}
)

// crmPullSource is the CRM pull-command source seam (SetCrmPullSource):
// one command when the wire is active, so the queue surface opens only for
// a configured, resolved CRM. The command name is the provider routing id;
// the surface currently offers no picker, so the list is availability-only.
func crmPullSource() []tui.CrmCommand {
	a := crmAdapter
	if a == nil || a.client == nil {
		return nil
	}
	return []tui.CrmCommand{{Name: a.provider, Desc: "review the " + a.provider + " follow-up queue"}}
}

// crmWireContact converts a queue row (core.CrmContact) to the neutral
// contact type the workflow jobs consume. CompanyID is lost (the row
// carries the display Company only); RunAnalyze refetches the contact and
// recovers it, and RunDraft/RunMark never need it.
func crmWireContact(c core.CrmContact) crm.Contact {
	return crm.Contact{ID: c.ID, Email: c.Email, FirstName: c.First, LastName: c.Last, JobTitle: c.Title, CreatedAt: c.CreatedAt}
}

// crmRowAction is the queue surface's action seam (SetCrmActionHandler):
// rows of another provider never dispatch here, and each action launches
// the matching workflow step on its own goroutine (the bus carries the
// outcome events back to the surface). "draft" needs the briefing text the
// adapter cached from the CrmBriefing event; a missing cache line means the
// surface's own guard let a draft through on an uncached briefing - skip.
func crmRowAction(action string, c core.CrmContact) {
	a := crmAdapter
	if a == nil || a.client == nil || c.Provider != a.provider {
		return
	}
	contact := crmWireContact(c)
	switch action {
	case "analyze":
		go crm.RunAnalyze(a.bus, a.client, a.aiCfg, contact, ai.Chat)
	case "draft":
		text := crmBriefingText(c.Provider, c.ID)
		if text == "" {
			return
		}
		go crm.RunDraft(a.bus, a.client, a.aiCfg, contact, text, a.ground, ai.Chat)
	case "dismiss":
		// Marking writes the follow-up property; double-marking is accepted -
		// the write is idempotent, and a re-x after a slow mark re-writes it.
		go crm.RunMark(a.bus, a.client, contact.ID, a.marker)
	}
}

// crmWire is the CRM follow-up workflow's app adapter: dormant unless
// [crm] names a provider, then it builds that provider's neutral client
// (the switch on cfg.Crm.Provider - the only vendor string on the app
// surface), resolves the [ai] entry the workflow runs on, and reacts to
// the refresh key (launch a pull) and to generated CrmDrafts (open the
// prefilled compose). The job bodies live in lib/crm/workflow.go; a
// SendResult for a CRM-origin compose writes the follow-up marker back
// through the client.
func crmWire(ctx context.Context, bus *core.Bus, worker workerAPI, cfg config.Config, root string) {
	if cfg.Crm.Provider == "" {
		return
	}
	hs := cfg.Crm.Hubspot
	var client crm.Client
	var aiCfg config.AIProvider
	switch cfg.Crm.Provider {
	case "hubspot":
		// Resolve the [ai] entry before the token subprocess: the common
		// misconfig - a valid token_cmd but no [ai] section - fails cheap
		// instead of spawning the secret-printing subprocess and then bailing.
		var err error
		aiCfg, err = resolveAIProvider(cfg, hs.AI)
		if err != nil {
			diag.Warn("crm: disabled", "err", err.Error())
			return
		}
		key, err := ai.FetchKey(ctx, hs.TokenCmd)
		if err != nil {
			diag.Warn("crm: disabled", "err", err.Error())
			return
		}
		// The factory seam (crmHubspotNew) lets the integration test point a
		// real client at an httptest server. NewClient retains the key for the
		// client's lifetime, so the wire does not clear it (the
		// ai-caller-clears rule yields here).
		client = crmHubspotNew(ctx, key)
	default:
		return // a provider with no client case: config load already rejects it
	}
	crmAdapter = &crmAdapterState{
		client:   client,
		provider: client.Provider(),
		aiCfg:    aiCfg,
		marker:   hs.MarkerProperty,
		bus:      bus,
		ground:   crmMailGround(cfg, worker),
	}
	// the pull trigger, the briefing cache, the compose-open reaction, and
	// the send write-back: each manual refresh launches a pull on its own
	// goroutine (RunPull's mutex absorbs overlap, so no gate here); a
	// generated CrmBriefing caches the text the draft seam needs; a generated
	// CrmDraft opens the prefilled compose; a SendResult for such a compose
	// marks the contact followed up.
	ch := bus.Subscribe()
	go func() {
		for e := range ch {
			switch e := e.(type) {
			case core.RefreshRequested:
				go crm.RunPull(bus, client, hs.MarkerProperty, hs.CreatedAfter)
			case core.CrmBriefing:
				crmCacheBriefing(e)
			case core.CrmDraft:
				crmOpenDraftCompose(bus, cfg, root, e)
			case core.SendResult:
				crmWriteBackOnSend(bus, client, hs.MarkerProperty, e)
			}
		}
	}()
}

// crmMailGround builds the adapter's gated mail-grounding closure for
// RunDraft: a worker query from:"<email>" finds the newest inbound thread,
// its owning account resolves from the thread tags, and - only when that
// account's [ai-data] grant permits - aicmd.BuildContext runs over the thread
// with a synthetic command whose Data is the grant. No thread or no grant
// returns "" - mail content never reaches a prompt except through
// BuildContext's Data allowlist.
func crmMailGround(cfg config.Config, worker workerAPI) crm.MailGroundFn {
	return func(ctx context.Context, email string) (string, error) {
		if email == "" {
			return "", nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// a CRM-controlled value must look like an address before it shapes
		// a query: reject anything outside the email charset (letters, digits,
		// . + _ - @) as the no-grounding state, never a query fragment
		for i := 0; i < len(email); i++ {
			c := email[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
				c == '.' || c == '+' || c == '-' || c == '_' || c == '@') {
				return "", nil
			}
		}
		query := `from:"` + strings.ReplaceAll(email, `"`, "") + `"`
		var found []core.Message
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: query, Limit: 5, Flat: true, Emit: func(msgs []core.Message) bool {
			found = append(found, msgs...)
			return true
		}})
		if err != nil || rpl.Err != nil {
			return "", fmt.Errorf("search from %s: %w", email, errors.Join(err, rpl.Err))
		}
		// Both backends sort newest-first (cgo SORT_NEWEST_FIRST, cli
		// --sort=newest-first), so the capped query's first row is the newest
		// inbound message from the address.
		if len(found) == 0 || found[0].ThreadID == "" {
			return "", nil // no inbound thread from the address
		}
		rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: found[0].ThreadID})
		if err != nil || rpl.Err != nil {
			return "", fmt.Errorf("thread %s: %w", found[0].ThreadID, errors.Join(err, rpl.Err))
		}
		account := ""
		if m := newestOf(rpl.Msgs); m != nil {
			account = resolveAccount(cfg, tagsOf(m), nil)
		}
		allowed, ok := cfg.AIDataGrant(account)
		if !ok || len(allowed) == 0 {
			return "", nil // no grant: no mail context
		}
		text, err := aicmd.BuildContext(&aicmd.Command{Data: allowed}, rpl.Msgs, cfg.MyAddrs(), allowed, "", "")
		if err != nil {
			return "", fmt.Errorf("context: %w", err)
		}
		return text, nil
	}
}

// crmOpenDraftCompose reacts to a generated follow-up draft: build the
// prefilled compose, register its tab id -> {Provider, ContactID} for the
// send hook (crmWriteBackOnSend), and publish ComposeOpened for the TUI to
// attach.
func crmOpenDraftCompose(bus *core.Bus, cfg config.Config, root string, d core.CrmDraft) {
	if st := crmDraftCompose(cfg, root, d); st != nil {
		st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		crmComposeMu.Lock()
		crmComposeRefs[st.ID] = crmDraftRef{Provider: d.Provider, ContactID: d.ContactID}
		crmComposeMu.Unlock()
		bus.Publish(compose.ToEvent(st))
	}
}

// crmWriteBackOnSend reacts to a send result for a CRM-origin compose: an OK
// result consumes the compose map entry (the row leaves on the next pull
// page, which omits a contact whose marker landed) and, when the entry's
// provider matches the wired client, launches RunMark on its own goroutine.
// A failed send leaves both the compose and the entry alone - the dialogue
// retries, and a retried OK still marks. The mark's own outcome is
// unobservable here (RunMark is void), so the entry drops at consumption
// time; a failed mark keeps the row retryable via a fresh pull/action.
func crmWriteBackOnSend(bus *core.Bus, client crm.Client, marker string, e core.SendResult) {
	if !e.OK {
		return
	}
	crmComposeMu.Lock()
	ref, ok := crmComposeRefs[e.TabID]
	if ok {
		delete(crmComposeRefs, e.TabID)
	}
	crmComposeMu.Unlock()
	if !ok || ref.Provider != client.Provider() {
		return
	}
	go crm.RunMark(bus, client, ref.ContactID, marker)
}

// crmDraftCompose builds a compose dialogue from a CRM follow-up draft: the
// account chain supplies the sender identity (no thread context - the first
// configured account), To = the contact's email, subject/body from the
// generated draft (the lib publishes raw text; the body wraps app-side).
func crmDraftCompose(cfg config.Config, root string, d core.CrmDraft) *compose.State {
	st := newCompose(cfg, root, nil, nil)
	if d.Email != "" {
		st.To = []string{d.Email}
	}
	st.Subject = core.SanitizeControls(d.Subject)
	width := cfg.Compose.WrapWidth
	if width <= 0 {
		width = config.DefaultWrapWidth
	}
	st.Body = wrapEmail(core.SanitizeText(d.Body), width)
	return st
}
