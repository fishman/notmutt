// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
	"notmutt/lib/table"
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
	// draftSel is the key the d picker opened on (the enter/e handlers
	// resolve the row through it, never a position).
	draftSel crmKey
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

// action runs a row action on the selected row (a analyze, x dismiss).
// The guard refuses what cannot run (analyze while a job is in flight) and
// reports false without dispatching. A guard-passing action dispatches
// through the handler hook with the full row. The model records the
// outcomes it can observe (dismiss -> dismissed); draft is the model's own
// picker path (draftable + the prompt picker), and send-OK/write-back are
// the app's, observed only as a later queue page that omits the row.
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
	case crmActionDismiss:
		r.contact.Status = crmStatusDismissed
	default:
		return false
	}
	onCrmAction(name, r.contact)
	return true
}

// draftable is the d-key guard: a row drafts only with a briefing. The
// model consults it before opening the prompt picker (the picker replaces
// the action dispatch for draft - a draft is a prompt run now).
func (q *crmQueue) draftable() bool {
	r := q.cursor()
	return r != nil && r.briefing != ""
}

// rowByKey resolves a held row by key (the d picker's selected row,
// resolved at enter time, never a stored position).
func (q *crmQueue) rowByKey(k crmKey) *crmRow {
	return q.byKey[k]
}

// crmAvailable reports whether the queue surface has a wired pull source
// (the surface's open guard). The app wires one via SetCrmPullSource; the
// default build's inert source returns nothing, so the surface never opens.
func crmAvailable() bool {
	return len(crmPull()) > 0
}

// crmDateW is the created-date column's fixed width (the 2006-01-02 format).
const crmDateW = 10

// crmCols are the queue table's bounded columns: name (the elastic one),
// company, title, and the fixed date; the status follows as a free-form tail.
// The name cap is modest so a short lead never sprawls, and the title's cap
// sits under the name's - job titles run shorter than names.
var crmCols = []table.Col{
	{Floor: 12, Cap: 16},             // name
	{Floor: 8, Cap: 20},              // company
	{Floor: 8, Cap: 14},              // title
	{Floor: crmDateW, Cap: crmDateW}, // date
}

// crmSep frames the theme's column glyph with a space either side so cell text
// never sits flush against the bar (Layout measures the 3-cell gutter).
func crmSep(glyph string) string { return " " + glyph + " " }

// crmTitles is the queue's column header row; the status column is a free-form
// tail, so its title is one too.
var crmTitles = []string{"NAME", "COMPANY", "TITLE", "DATE", "STATUS"}

// crmRowValues are a contact's four identity cells (name, company, title,
// created date) in column order. All fields are foreign input - control chars
// are stripped before they reach the width math (F1). The name falls back to
// the email so a row without a name still leads with an identity.
func crmRowValues(c core.CrmContact) (name, company, title, date string) {
	name = core.SanitizeControls(strings.TrimSpace(c.First + " " + c.Last))
	if name == "" {
		name = core.SanitizeControls(c.Email)
	}
	company = core.SanitizeControls(strings.TrimSpace(c.Company))
	title = core.SanitizeControls(strings.TrimSpace(c.Title))
	if !c.CreatedAt.IsZero() {
		date = c.CreatedAt.Format("2006-01-02")
	}
	return name, company, title, date
}

// crmRowText is a queue row's display line: the four identity cells laid into
// the shared column widths with the status trailing free-form (the R3 row
// shape). Every row takes the same layout and widths, so the seams align.
func crmRowText(c core.CrmContact, l table.Layout, widths []int) string {
	name, company, title, date := crmRowValues(c)
	cells := []string{name, company, title, date}
	if s := core.SanitizeControls(c.Status); s != "" {
		cells = append(cells, s)
	}
	return l.Line(cells, widths)
}

// crmMark builds the leading cell of a queue row - the selection highlight is
// the cursor marker cell at the line start (the index/attachment standard:
// a config glyph, indicator-styled, never a full-line paint). colW reserves
// the cell on every row so the table columns never shift with the selection.
func crmMark(selected bool, colW int, cur string, sg sgrSet) string {
	if selected {
		return sg.indicator.render(cur)
	}
	return strings.Repeat(" ", colW)
}

// listRows renders the queue rows, each led by its reserved marker cell (the
// cursor glyph on the selection, blank otherwise); every row pads to width so
// alignment never shifts per row. inner is the table area (width minus the
// marker column and gap), so the bounded columns line up beneath the title row.
func (q *crmQueue) listRows(l table.Layout, inner, colW int, cur string, st Styles) []string {
	widths := l.Sizes(inner, true)
	rows := make([]string, 0, len(q.rows))
	for i, r := range q.rows {
		mark := crmMark(i == q.cur, colW, cur, st.sgr)
		line := crmRowText(r.contact, l, widths)
		rows = append(rows, padRowSGR(mark+" "+line, inner+colW+1, st.sgr.normal))
	}
	return rows
}

// titleRow is the queue's column header line, led by a blank marker cell and
// laid into the same column widths as the data rows so the columns line up
// beneath their titles. The row draws no column glyph - a blank separator as
// wide as the data gutter keeps each title on its column - and carries its own
// header style (queue.header, R11) rather than the rows' surface.
func (q *crmQueue) titleRow(l table.Layout, inner, colW int, st Styles) string {
	blank := table.Layout{Cols: l.Cols, Sep: strings.Repeat(" ", runewidth.StringWidth(l.Sep))}
	widths := blank.Sizes(inner, true)
	line := crmMark(false, colW, "", st.sgr) + " " + blank.Line(crmTitles, widths)
	return padRowSGR(line, inner+colW+1, sgrOf(st.QueueHeader))
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
// height: a column-title row and the cursor-windowed list rows on top, the
// selected row's briefing in a detail region under the list. sep is the
// theme's column separator glyph and cursor the selection marker glyph; the
// separator is framed with a pad into one frame Layout, and every row reads
// that Layout (it is set once per frame, never per row). width pads every
// line; the caller (the model frame) adds the tab bar, keyhint, and status
// rows. The briefing is foreign input - it passes SanitizeText before
// wrapping (F1).
func (q *crmQueue) render(width, height int, st Styles, sep, cursor string) []string {
	if height < 1 {
		height = 1
	}
	layout := table.Layout{Cols: crmCols, Sep: crmSep(sep)}
	sg := st.sgr
	colW := runewidth.StringWidth(cursor) // the reserved marker cell (R11)
	inner := width - colW - 1
	if inner < 1 {
		inner = 1
	}
	detail := q.detailLines(layout, width, st)
	if len(detail) > height/2 { // the list keeps at least half the frame
		detail = detail[:height/2]
	}
	listH := height - len(detail)

	rows := q.listRows(layout, inner, colW, cursor, st)
	if len(q.rows) == 0 {
		line := crmMark(false, colW, "", sg) + " " + "(queue empty)"
		rows = []string{padRowSGR(line, width, sg.normal)}
	}

	var head []string
	if len(q.rows) > 0 && listH > 1 { // the title row only when it costs a list row nothing
		head = []string{q.titleRow(layout, inner, colW, st)}
		listH--
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
	content = append(content, head...)
	content = append(content, list...)
	content = append(content, detail...)
	return content
}

// detailLines renders the selected row's briefing region (empty when the
// selection has none): a header line, then the wrapped briefing text.
func (q *crmQueue) detailLines(layout table.Layout, width int, st Styles) []string {
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
	name, company, title, date := crmRowValues(r.contact)
	head := layout.Line([]string{name, company, title, date}, layout.Sizes(width, true))
	lines = append(lines, padRowSGR(head+" - briefing", width, sg.normal))
	for _, l := range wrapCells(text, inner) {
		lines = append(lines, padRowSGR(" "+l, width, sg.normal))
	}
	return lines
}

// crmPageStep is the queue's page-key step: half the frame's content rows,
// the minimum the list region shows under a full briefing (render keeps at
// least half the frame for the list), so a page never skips a row.
func crmPageStep(height int) int {
	s := (height - 3) / 2
	if s < 1 {
		s = 1
	}
	return s
}

// crmFooter mirrors the log/task footers: the scroll and close keys derive
// from the pager binding data (R9); the a/d/x action keys are the surface's
// own literal keys - queue actions are surface-local, not bound data.
func (m Model) crmFooter() string {
	pm := m.bindings["pager"]
	var parts []string
	if s := scrollKeys(pm); len(s) > 0 {
		parts = append(parts, strings.Join(s, "/")+" scroll")
	}
	parts = append(parts, "a analyze", "d draft", "x dismiss")
	if q := keyFor(pm, "back"); q != "" {
		parts = append(parts, q+" closes")
	}
	return strings.Join(parts, "  ")
}

// renderCrm is the Q overlay (crm-queue): the queue's list/detail render
// through the shared frame (the tab bar, this footer, the status row).
func (m Model) renderCrm() string {
	if m.crm == nil {
		m.crm = newCrmQueue()
	}
	return m.frame(m.crm.render(m.width, m.height-3, m.styles, m.ui.Glyphs.BorderV, m.ui.Glyphs.Cursor), m.crmFooter())
}
