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
// workflow drives, the [ai] entry its chat calls run on, and the gated
// mail-grounding closure RunDraft consumes.
type crmAdapterState struct {
	client crm.Client
	aiCfg  config.AIProvider
	ground crm.MailGroundFn
}

// crmAdapter is the resolved wire state; nil while the CRM is dormant or
// its setup failed. Written once in crmWire before the subscriber starts;
// the workflow jobs read it, never rebuild the client. Session-local.
var crmAdapter *crmAdapterState

// crmDraftRef is the compose map's value: the CRM routing id and row the
// opened compose belongs to, for the send hook (Task 11) to write back.
type crmDraftRef struct {
	Provider  string
	ContactID string
}

// crmComposeMu guards crmComposeRefs: entries are added on the CrmDraft
// reaction and removed on SendResult (Task 11) - a mutex keeps that safe
// when the send path races in from another goroutine.
var (
	crmComposeMu   sync.Mutex
	crmComposeRefs = map[string]crmDraftRef{}
)

// crmPullSource is the CRM pull-command source seam (SetCrmPullSource).
// The surface-wiring task fills the command list; a nil list keeps the
// CRM queue surface closed.
func crmPullSource() []tui.CrmCommand { return nil }

// crmWire is the CRM follow-up workflow's app adapter: dormant unless
// [crm] names a provider, then it builds that provider's neutral client
// (the switch on cfg.Crm.Provider - the only vendor string on the app
// surface), resolves the [ai] entry the workflow runs on, and reacts to
// the refresh key (launch a pull) and to generated CrmDrafts (open the
// prefilled compose). The job bodies live in lib/crm/workflow.go; the
// send write-back reaction attaches here in Task 11.
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
		// NewClient retains the key for the client's lifetime, so the wire
		// does not clear it (the ai-caller-clears rule yields here).
		client = hubspot.NewClient(ctx, key)
	default:
		return // a provider with no client case: config load already rejects it
	}
	crmAdapter = &crmAdapterState{client: client, aiCfg: aiCfg, ground: crmMailGround(cfg, worker)}
	// the pull trigger and the compose-open reaction: each manual refresh
	// launches a pull on its own goroutine (RunPull's mutex absorbs overlap,
	// so no gate here); a generated CrmDraft opens the prefilled compose.
	go func() {
		ch := bus.Subscribe()
		for e := range ch {
			switch e := e.(type) {
			case core.RefreshRequested:
				go crm.RunPull(bus, client, hs.MarkerProperty, hs.CreatedAfter)
			case core.CrmDraft:
				crmOpenDraftCompose(bus, cfg, root, e)
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
// send hook (Task 11), and publish ComposeOpened for the TUI to attach.
func crmOpenDraftCompose(bus *core.Bus, cfg config.Config, root string, d core.CrmDraft) {
	if st := crmDraftCompose(cfg, root, d); st != nil {
		st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
		crmComposeMu.Lock()
		crmComposeRefs[st.ID] = crmDraftRef{Provider: d.Provider, ContactID: d.ContactID}
		crmComposeMu.Unlock()
		bus.Publish(compose.ToEvent(st))
	}
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
