// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"errors"
	"testing"
	"time"

	"notmutt/core"
)

// crmContact builds a fabricated queue row (never personal or mail content).
// The provider id stays neutral - a neutral surface must not assume a vendor.
func crmContact(id, email, first, last string) core.CrmContact {
	return core.CrmContact{
		Provider:  "test-crm",
		ID:        id,
		Email:     email,
		First:     first,
		Last:      last,
		Title:     "CTO",
		Company:   "Acme",
		CreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Status:    crmStatusNew,
	}
}

func crmFind(q *crmQueue, provider, id string) *crmRow {
	return q.byKey[crmKey{provider: provider, id: id}]
}

// TestCrmQueueNewToBriefing pins the first status transition: a pulled row
// starts "new" and a matching CrmBriefing advances it to "briefing" with the
// briefing text held on the row.
func TestCrmQueueNewToBriefing(t *testing.T) {
	q := newCrmQueue()
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{crmContact("201", "alpha@example.com", "Alpha", "Able")}})

	r := q.cursor()
	if r == nil {
		t.Fatal("no cursor row after a one-row queue")
	}
	if r.contact.Status != crmStatusNew {
		t.Fatalf("fresh row status = %q, want %q", r.contact.Status, crmStatusNew)
	}

	q.onBriefing(core.CrmBriefing{Provider: "test-crm", ContactID: "201", Text: "Acme ships widgets to beta buyers."})
	if r.contact.Status != crmStatusBriefing {
		t.Errorf("status after briefing = %q, want %q", r.contact.Status, crmStatusBriefing)
	}
	if r.briefing == "" {
		t.Error("briefing text not held on the row")
	}
	if r.inFlight {
		t.Error("briefing arrival left the row in flight")
	}
}

// TestCrmQueueDraftRequiresBriefing pins the d guard: draft is refused with
// no briefing on the selected row (handler untouched), allowed once the
// briefing lands, and the row advances to drafted.
func TestCrmQueueDraftRequiresBriefing(t *testing.T) {
	var gotAction string
	var got core.CrmContact
	SetCrmActionHandler(func(action string, c core.CrmContact) {
		gotAction, got = action, c
	})
	t.Cleanup(func() { SetCrmActionHandler(func(string, core.CrmContact) {}) })

	q := newCrmQueue()
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{
		crmContact("201", "alpha@example.com", "Alpha", "Able"),
		crmContact("202", "atlas@example.com", "Atlas", "Beta"),
	}})

	// cursor on alpha with no briefing: draft refused, nothing dispatched
	if q.action(crmActionDraft) {
		t.Error("draft allowed on a row with no briefing")
	}
	if gotAction != "" {
		t.Errorf("handler ran for a refused draft: %q", gotAction)
	}
	if r := q.cursor(); r.contact.Status != crmStatusNew {
		t.Errorf("refused draft changed the status to %q", r.contact.Status)
	}

	// a briefing for alpha makes draft allowed on alpha only
	q.onBriefing(core.CrmBriefing{Provider: "test-crm", ContactID: "201", Text: "Acme ships widgets."})
	if !q.action(crmActionDraft) {
		t.Fatal("draft refused on a row with a briefing")
	}
	if gotAction != crmActionDraft || got.ID != "201" {
		t.Errorf("handler got (%q, id %q), want (draft, 201)", gotAction, got.ID)
	}
	if r := q.cursor(); r.contact.Status != crmStatusDrafted {
		t.Errorf("status after draft = %q, want %q", r.contact.Status, crmStatusDrafted)
	}

	// the guard is row-scoped: moving to atlas (no briefing) refuses again
	if !q.move(1) {
		t.Fatal("cursor move to the second row failed")
	}
	if q.action(crmActionDraft) {
		t.Error("draft allowed on a row with no briefing of its own")
	}
}

// TestCrmQueueMergeDiffInsert pins the page merge: new ids append, existing
// rows keep their briefing/status across a refresh, and a row absent from a
// fresh page that the workflow advanced past (dismissed) leaves while rows
// still under review stay.
func TestCrmQueueMergeDiffInsert(t *testing.T) {
	SetCrmActionHandler(func(string, core.CrmContact) {})
	t.Cleanup(func() { SetCrmActionHandler(func(string, core.CrmContact) {}) })

	q := newCrmQueue()
	alpha := crmContact("201", "alpha@example.com", "Alpha", "Able")
	atlas := crmContact("202", "atlas@example.com", "Atlas", "Beta")
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{alpha, atlas}})
	q.onBriefing(core.CrmBriefing{Provider: "test-crm", ContactID: "201", Text: "Acme ships widgets."})

	// a fresh page adds a third row; the existing two keep their state
	acme := crmContact("203", "acme@example.com", "Acme", "Gamma")
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{alpha, atlas, acme}})
	if q.len() != 3 {
		t.Fatalf("queue len = %d after insert page, want 3", q.len())
	}
	if r := crmFind(q, "test-crm", "201"); r == nil {
		t.Fatal("held row 201 lost on the refresh page")
	} else if r.briefing != "Acme ships widgets." || r.contact.Status != crmStatusBriefing {
		t.Errorf("refresh clobbered row 201: briefing %q status %q", r.briefing, r.contact.Status)
	}
	if last := q.rows[q.len()-1]; last.contact.ID != "203" {
		t.Errorf("new row not appended: last id %q, want 203", last.contact.ID)
	}

	// dismiss alpha, then a page that omits it drops it (write-back landed)
	if q.cursor().contact.ID != "201" {
		t.Fatal("cursor not on the first row for the dismiss")
	}
	if !q.action(crmActionDismiss) {
		t.Fatal("dismiss refused")
	}
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{atlas, acme}})
	if q.len() != 2 {
		t.Fatalf("queue len = %d after the dismissing page, want 2", q.len())
	}
	if crmFind(q, "test-crm", "201") != nil {
		t.Error("dismissed row absent from the fresh page still held")
	}
}

// TestCrmQueueBriefingRowSurvivesPartialPage pins the reconcile-then-replay
// edge: a briefing row absent from one page (a partial or scoped fetch) is
// kept, never clobbered - only write-back-landed rows leave on omission.
func TestCrmQueueBriefingRowSurvivesPartialPage(t *testing.T) {
	q := newCrmQueue()
	alpha := crmContact("201", "alpha@example.com", "Alpha", "Able")
	atlas := crmContact("202", "atlas@example.com", "Atlas", "Beta")
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{alpha, atlas}})
	q.onBriefing(core.CrmBriefing{Provider: "test-crm", ContactID: "201", Text: "Acme ships widgets."})

	// a page listing only the new row: the briefed row stays
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{atlas}})
	if r := crmFind(q, "test-crm", "201"); r == nil || r.briefing == "" {
		t.Error("briefing row dropped by a partial page")
	}
	if q.len() != 2 {
		t.Errorf("queue len = %d after a partial page, want 2", q.len())
	}
}

// TestCrmQueueAnalyzeGuard pins the a guard: analyze dispatches once, is
// refused while the job is in flight on that row, and a row error clears the
// flight so the row retries.
func TestCrmQueueAnalyzeGuard(t *testing.T) {
	var got []string
	SetCrmActionHandler(func(action string, c core.CrmContact) { got = append(got, action) })
	t.Cleanup(func() { SetCrmActionHandler(func(string, core.CrmContact) {}) })

	q := newCrmQueue()
	q.onQueue(core.CrmQueue{Contacts: []core.CrmContact{crmContact("201", "alpha@example.com", "Alpha", "Able")}})

	if !q.action(crmActionAnalyze) {
		t.Fatal("first analyze refused")
	}
	if q.action(crmActionAnalyze) {
		t.Error("analyze allowed while a job is in flight on the row")
	}
	q.onRowError(core.CrmRowError{Provider: "test-crm", ContactID: "201", Err: errors.New("boom")})
	if !q.action(crmActionAnalyze) {
		t.Error("analyze not retryable after the row error cleared the flight")
	}
	if len(got) != 2 {
		t.Errorf("handler ran %d times, want 2 (two dispatches)", len(got))
	}
	if r := q.cursor(); r.contact.Status != crmStatusNew {
		t.Errorf("status after a failed analyze = %q, want %q (row stays retryable)", r.contact.Status, crmStatusNew)
	}
}

// TestCrmHooksInertDefault pins the inert-default hooks: with no source the
// surface stays closed, a nil setter changes nothing, and the default action
// handler is a no-op.
func TestCrmHooksInertDefault(t *testing.T) {
	if cmds := crmPull(); len(cmds) != 0 {
		t.Errorf("default pull source returned %d commands, want 0", len(cmds))
	}
	if crmAvailable() {
		t.Error("surface available with no pull source wired")
	}
	SetCrmPullSource(nil)
	if crmAvailable() {
		t.Error("nil pull source made the surface available")
	}
	// the default handler must not panic
	onCrmAction(crmActionDismiss, core.CrmContact{Provider: "test-crm", ID: "201"})

	SetCrmPullSource(func() []CrmCommand {
		return []CrmCommand{{Name: "test-crm", Desc: "review the new-contact queue"}}
	})
	t.Cleanup(func() { SetCrmPullSource(func() []CrmCommand { return nil }) })
	if !crmAvailable() {
		t.Error("wired pull source did not make the surface available")
	}
	if cmds := crmPull(); len(cmds) != 1 || cmds[0].Name != "test-crm" {
		t.Errorf("wired pull source = %v, want the one test-crm command", cmds)
	}
}
