// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"notmutt/config"
	"notmutt/core"
)

// pullMu serializes pull runs: the app adapter launches RunPull on a fresh
// goroutine per pull request, and an overlapping run no-ops (the refresher's
// non-blocking guard, refresh.go) instead of stacking ListUnprocessed calls
// against the CRM. The guard is process-wide, not per provider; a future
// second-provider task keys it by client.Provider() if concurrent pulls ever
// matter.
var pullMu sync.Mutex

// RunPull lists the client's unprocessed contacts and publishes them as
// core.CrmContact rows in one core.CrmQueue page (the R3 refresh shape: each
// pull publishes its own page the queue view diff-inserts, never a rebuild).
// marker and createdAfter pass through to ListUnprocessed unchanged;
// createdAfter may be empty. Rows carry client.Provider() as their routing id
// - never a vendor literal. Search rows carry no company association, so
// Company stays empty (analyze refetches and fills it) and Status starts at
// statusNew. A ListUnprocessed error publishes CrmRowError and stops; rows
// already published stay visible.
func RunPull(bus *core.Bus, client Client, marker, createdAfter string) {
	if !pullMu.TryLock() {
		return
	}
	defer pullMu.Unlock()

	provider := client.Provider()
	contacts, err := client.ListUnprocessed(context.Background(), marker, createdAfter)
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, Err: err})
		return
	}
	rows := make([]core.CrmContact, 0, len(contacts))
	for _, c := range contacts {
		rows = append(rows, core.CrmContact{
			Provider:  provider,
			ID:        c.ID,
			Email:     c.Email,
			First:     c.FirstName,
			Last:      c.LastName,
			Title:     c.JobTitle,
			CreatedAt: c.CreatedAt,
			Status:    statusNew,
		})
	}
	bus.Publish(core.CrmQueue{Contacts: rows})
}

// briefingSystem is the analyst role prompt for the briefing chat call: it
// turns the assembled context into the concise, factual briefing the user
// reads before writing a follow-up email - who the contact is, the company,
// and the concrete facts/news with their sources. Nothing is sent
// automatically; the briefing only grounds the user's own draft.
const briefingSystem = `You are an analyst. Turn the contact, company, and research context below into a concise, factual briefing for the user: a few short paragraphs on who the contact is, what the company does, and the concrete facts and news with their sources.
State only what the context supports - no fluff and no invented detail. The briefing grounds a follow-up email the user writes; you draft nothing to send.`

// RunAnalyze briefs one queue row for the user before they write a follow-up.
// The pull row carries no company association, so the contact is refetched
// first to learn CompanyID; an empty CompanyID skips the company fetch and
// researches on a zero Company (research and briefing degrade to "unknown").
// Research (over chat), the non-mail briefing context, then a second chat
// call produce the readable briefing text, published as core.CrmBriefing with
// client.Provider() as the routing id - never a vendor literal. The row
// itself is never changed here: success publishes only the briefing, and any
// step error publishes one core.CrmRowError and leaves the row at its prior
// status. chat is the adapter's ai.Chat on the resolved [ai] entry; nil is a
// caller error. No mail input reaches chat (briefing takes none).
func RunAnalyze(bus *core.Bus, client Client, aiCfg config.AIProvider, contact Contact, chat ChatFn) {
	provider := client.Provider()
	ctx := context.Background()
	if chat == nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: errors.New("crm: analyze: nil chat fn")})
		return
	}

	fc, err := client.Contact(ctx, contact.ID)
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: fmt.Errorf("crm: contact: %w", err)})
		return
	}

	var company Company
	if fc.CompanyID != "" {
		company, err = client.Company(ctx, fc.CompanyID)
		if err != nil {
			bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: fmt.Errorf("crm: company: %w", err)})
			return
		}
	}

	rs, err := Research(ctx, aiCfg, company, chat)
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: fmt.Errorf("crm: analyze: research: %w", err)})
		return
	}

	contextStr, err := briefing(fc, company, rs)
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: err})
		return
	}

	text, err := chat(ctx, aiCfg, aiCfg.Model, briefingSystem, contextStr, func(string) {})
	if err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: contact.ID, Err: fmt.Errorf("crm: analyze: briefing: %w", err)})
		return
	}

	bus.Publish(core.CrmBriefing{Provider: provider, ContactID: contact.ID, Text: text})
}

// draftSystem is the writer role prompt for the draft chat call: the reply
// DefaultDraftSubject is the compose subject the CRM prompt run prefills
// (the contact and briefing supply the body; the user edits both).
const DefaultDraftSubject = "Following up"

// MailGroundFn supplies gated mail context for a follow-up draft: the adapter
// returns "" unless an inbound thread from email exists and its account's
// [ai-data.<account>] grant permits it (no grant = no context). Nil is a
// legitimate no-context outcome, never an error; only the adapter reads mail,
// through aicmd.BuildContext behind the grant.
type MailGroundFn func(ctx context.Context, email string) (string, error)

// RunMark records a follow-up on one contact by writing the marker property -
// the write-back after a sent mail or a dismissal. Success publishes nothing:
// a marked contact drops from the next pull page, so row-leave is pull-driven,
// and the sent mail (never the marker) is the durable artifact. A
// MarkFollowedUp error publishes one core.CrmRowError with client.Provider()
// as the routing id and the crm: mark: wrap; the row stays retryable because
// a later pull still returns the unmarked contact. Nil client is a caller
// error.
func RunMark(bus *core.Bus, client Client, id, marker string) {
	if client == nil {
		bus.Publish(core.CrmRowError{ContactID: id, Err: errors.New("crm: mark: nil client")})
		return
	}
	provider := client.Provider()
	if err := client.MarkFollowedUp(context.Background(), id, marker); err != nil {
		bus.Publish(core.CrmRowError{Provider: provider, ContactID: id, Err: fmt.Errorf("crm: mark: %w", err)})
	}
}
