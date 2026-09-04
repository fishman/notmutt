// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// crm.go: the CRM review-queue surface - a non-mail list buffer whose rows
// are core.CrmContact (not a notmuch view). Provider-neutral: rows key on
// (Provider, ID), never position, and every action dispatches through the
// injected handler hook (hooks.go) with the full row, so the app routes on
// the row's Provider. Rows arrive as core.CrmQueue pages and CrmBriefing
// attachments; the surface stays closed until the app wires a pull source.

// Row statuses are the core.CrmContact.Status strings the model observes and
// advances. A row leaves the queue once write-back lands, not on these
// transitions alone.
const (
	crmStatusNew       = "new"
	crmStatusBriefing  = "briefing"
	crmStatusDrafted   = "drafted"
	crmStatusSent      = "sent"
	crmStatusDismissed = "dismissed"
)

// Row actions, dispatched to the handler hook with the full row.
const (
	crmActionAnalyze = "analyze"
	crmActionDraft   = "draft"
	crmActionDismiss = "dismiss"
)

// crmKey is a queue row's identity: the provider routing id plus the
// provider-local contact id (a bare contact id collides across CRMs).
type crmKey struct {
	provider string
	id       string
}

// crmRow is one held queue row: the contact snapshot the pull delivered plus
// the session-local briefing and in-flight state.
type crmRow struct {
	contact  core.CrmContact
	briefing string
	inFlight bool // an analyze job is running on the row (the a-key guard)
}

func (r *crmRow) key() crmKey {
	return crmKey{provider: r.contact.Provider, id: r.contact.ID}
}

// crmQueue is the queue surface model: the held rows in display order, the
// selection, and the detail derived from the selected row. CrmQueue pages
// reconcile the whole list to the page order (R3 diff-and-insert: each page
// is the authoritative newest-first unprocessed set): rows reorder to the
// page, existing rows keep their briefing/status by key, and rows absent from
// a page that the workflow advanced past (write-back landed) drop. Refresh
// never clobbers a briefing or an in-flight status (the reconcile-then-replay
// spirit of R14); the selection survives a reorder by key.
type crmQueue struct {
	rows  []*crmRow
	byKey map[crmKey]*crmRow
	cur   int
}

func newCrmQueue() *crmQueue {
	return &crmQueue{byKey: make(map[crmKey]*crmRow)}
}

// crmPastQueue is the merge drop predicate: the workflow handed the contact
// off. The model never observes "sent" directly (send-OK returns through the
// app), so a drafted row a fresh full page omits is one whose send write-back
// landed - it drops too.
func crmPastQueue(status string) bool {
	return status == crmStatusDrafted || status == crmStatusSent || status == crmStatusDismissed
}

func (q *crmQueue) len() int { return len(q.rows) }

func (q *crmQueue) cursor() *crmRow {
	if len(q.rows) == 0 || q.cur < 0 || q.cur >= len(q.rows) {
		return nil
	}
	return q.rows[q.cur]
}

// move shifts the selection by delta rows (the cursor keys), clamped to the
// queue; false when the queue is empty or the edge blocks.
func (q *crmQueue) move(delta int) bool {
	if len(q.rows) == 0 {
		return false
	}
	next := q.cur + delta
	if next < 0 || next >= len(q.rows) {
		return false
	}
	q.cur = next
	return true
}

// onQueue reconciles the held rows to one CrmQueue page: the page is the
// authoritative newest-first unprocessed set, so rows reorder to the page
// order (a mid-session contact lands where the page puts it, not the tail)
// while each held row keeps its briefing/status/in-flight by key. Under-review
// rows absent from the page (a partial or scoped fetch) stay after the
// page-ordered segment; past-queue absent rows leave. The selection survives
// the reorder by key.
func (q *crmQueue) onQueue(page core.CrmQueue) {
	var sel crmKey
	fallback := q.cur
	if r := q.cursor(); r != nil {
		sel = r.key()
	}
	incoming := make(map[crmKey]bool, len(page.Contacts))
	rows := make([]*crmRow, 0, len(page.Contacts)+len(q.rows))
	for _, c := range page.Contacts {
		k := crmKey{provider: c.Provider, id: c.ID}
		if incoming[k] { // a duplicate within the page stays once
			continue
		}
		incoming[k] = true
		r := q.byKey[k]
		if r == nil {
			if c.Status == "" {
				c.Status = crmStatusNew
			}
			r = &crmRow{contact: c}
			q.byKey[k] = r
		}
		rows = append(rows, r)
	}
	for _, r := range q.rows {
		if incoming[r.key()] {
			continue
		}
		if crmPastQueue(r.contact.Status) {
			delete(q.byKey, r.key())
			continue
		}
		rows = append(rows, r) // kept-but-absent under-review rows follow the page segment
	}
	q.rows = rows
	cur := fallback
	for i, r := range q.rows {
		if r.key() == sel {
			cur = i
			break
		}
	}
	switch {
	case len(q.rows) == 0:
		q.cur = 0
	case cur < 0 || cur >= len(q.rows):
		q.cur = len(q.rows) - 1
	default:
		q.cur = cur
	}
}

// onBriefing attaches a briefing to its held row and advances it to
// briefing; a briefing for an unheld row drops (analyze only runs on a held
// row), and the provider-scoped key keeps one CRM's briefing off another's row.
func (q *crmQueue) onBriefing(b core.CrmBriefing) {
	r := q.byKey[crmKey{provider: b.Provider, id: b.ContactID}]
	if r == nil {
		return
	}
	r.briefing = b.Text
	r.inFlight = false
	r.contact.Status = crmStatusBriefing
}

// onRowError clears a row's in-flight state after a failed job (an analyze
// that errored leaves the row retryable at its prior status). A provider- or
// pull-level error with no contact id matches no row and drops.
func (q *crmQueue) onRowError(e core.CrmRowError) {
	if r := q.byKey[crmKey{provider: e.Provider, id: e.ContactID}]; r != nil {
		r.inFlight = false
	}
}

// action runs a row action on the selected row (a analyze, d draft, x
// dismiss). The guard refuses what cannot run - analyze while a job is in
// flight, draft with no briefing - and reports false without dispatching. A
// guard-passing action dispatches through the handler hook with the full
// row. The model records the outcomes it can observe (draft -> drafted,
// dismiss -> dismissed); send-OK and write-back are the app's, observed only
// as a later queue page that omits the row.
func (q *crmQueue) action(name string) bool {
	r := q.cursor()
	if r == nil {
		return false
	}
	switch name {
	case crmActionAnalyze:
		if r.inFlight {
			return false
		}
		r.inFlight = true
	case crmActionDraft:
		if r.briefing == "" {
			return false // the draft guard: no briefing, no draft
		}
		r.contact.Status = crmStatusDrafted
	case crmActionDismiss:
		r.contact.Status = crmStatusDismissed
	default:
		return false
	}
	onCrmAction(name, r.contact)
	return true
}

// crmAvailable reports whether the queue surface has a wired pull source
// (the surface's open guard). The app wires one via SetCrmPullSource; the
// default build's inert source returns nothing, so the surface never opens.
func crmAvailable() bool {
	return len(crmPull()) > 0
}

// crmRowSlots is a contact's non-empty identity slots (name, company, title,
// created date) in display order. All fields are foreign input - control
// chars are stripped before render (F1).
func crmRowSlots(c core.CrmContact) []string {
	name := strings.TrimSpace(c.First + " " + c.Last)
	if name == "" {
		name = c.Email
	}
	var parts []string
	for _, s := range []string{name, c.Company, c.Title} {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if !c.CreatedAt.IsZero() {
		parts = append(parts, c.CreatedAt.Format("2006-01-02"))
	}
	return parts
}

// crmRowText is a queue row's display line: the identity slots plus the
// status (the R3 row shape).
func crmRowText(c core.CrmContact) string {
	parts := crmRowSlots(c)
	if c.Status != "" {
		parts = append(parts, c.Status)
	}
	return core.SanitizeControls(strings.Join(parts, " | "))
}

// crmRowHead is a queue row's identity slots without the status (the detail
// region header - the status already reads in the list row).
func crmRowHead(c core.CrmContact) string {
	return core.SanitizeControls(strings.Join(crmRowSlots(c), " | "))
}

// listRows renders the queue rows, the selection carrying the indicator
// style (the taskRows shape); every row pads to width so alignment never
// shifts per row.
func (q *crmQueue) listRows(width int, st Styles) []string {
	rows := make([]string, 0, len(q.rows))
	for i, r := range q.rows {
		outer := st.sgr.normal
		if i == q.cur {
			outer = st.sgr.indicator
		}
		rows = append(rows, padRowSGR(crmRowText(r.contact), width, outer))
	}
	return rows
}

// wrapCells wraps s to at most width visible cells, splitting on whitespace
// and preserving paragraph breaks; a word wider than width breaks hard.
func wrapCells(s string, width int) []string {
	if width < 1 {
		return []string{s}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		lineW := 0
		flush := func() {
			if line != "" || lineW > 0 {
				out = append(out, line)
				line, lineW = "", 0
			}
		}
		for _, word := range strings.Fields(para) {
			w := runewidth.StringWidth(word)
			if lineW > 0 && lineW+1+w > width {
				flush()
			}
			if w > width { // a single word wider than the line: hard-break
				for _, r := range word {
					rw := runewidth.RuneWidth(r)
					if lineW > 0 && lineW+rw > width {
						flush()
					}
					line += string(r)
					lineW += rw
				}
				continue
			}
			if lineW > 0 {
				line += " "
				lineW++
			}
			line += word
			lineW += w
		}
		flush()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// render builds the queue surface's content lines for a frame of the given
// height: the cursor-windowed list rows on top and the selected row's
// briefing in a detail region under the list. width pads every line; the
// caller (the model frame) adds the tab bar, keyhint, and status rows. The
// briefing is foreign input - it passes SanitizeText before wrapping (F1).
func (q *crmQueue) render(width, height int, st Styles) []string {
	if height < 1 {
		height = 1
	}
	sg := st.sgr
	detail := q.detailLines(width, st)
	if len(detail) > height/2 { // the list keeps at least half the frame
		detail = detail[:height/2]
	}
	listH := height - len(detail)

	rows := q.listRows(width, st)
	if len(rows) == 0 {
		rows = []string{padRowSGR("(queue empty)", width, sg.normal)}
	}
	// window the list so the cursor is visible above the detail region.
	top := 0
	if len(rows) > listH && q.cur >= listH {
		top = q.cur - listH + 1
	}
	if top > 0 && top+listH > len(rows) {
		top = len(rows) - listH
	}
	if top < 0 {
		top = 0
	}
	list := rows
	if len(list) > listH {
		list = rows[top : top+listH]
	}
	for len(list) < listH {
		list = append(list, padRowSGR("", width, sg.normal))
	}
	content := make([]string, 0, height)
	content = append(content, list...)
	content = append(content, detail...)
	return content
}

// detailLines renders the selected row's briefing region (empty when the
// selection has none): a header line, then the wrapped briefing text.
func (q *crmQueue) detailLines(width int, st Styles) []string {
	r := q.cursor()
	if r == nil || r.briefing == "" {
		return nil
	}
	sg := st.sgr
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	text := core.SanitizeText(r.briefing)
	lines := make([]string, 0, 2+len(text)/max(inner, 1))
	lines = append(lines, padRowSGR(crmRowHead(r.contact)+" - briefing", width, sg.normal))
	for _, l := range wrapCells(text, inner) {
		lines = append(lines, padRowSGR(" "+l, width, sg.normal))
	}
	return lines
}
