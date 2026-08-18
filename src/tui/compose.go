// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"notmutt/compose"
	"notmutt/core"
	"notmutt/i18n"
)

// composeForm is one form line: the settings rows carry a label +
// value (rendered as a two-column table, never highlighted), the
// attachment-list rows carry a cursor slot (8 the message-text row,
// 9+i attachment i), the static rows (dividers) carry plain text.
// Every non-focusable row carries the sentinel slot -1 (the settings
// rows included - only the attachment-list slots are ever focused).
type composeForm struct {
	slot    int
	label   string // settings row label, rendered right-aligned in compose.label
	value   string // settings row value
	text    string // plain rows (dividers, content-type, attachments)
	divider bool   // section bar (--- Attachments / --- Preview): compose.divider style
}

// partCell renders one part's wire facts (compose.PartFacts - what
// Assemble writes, never hardcoded here) as the row's mime column:
// [type, encoding, charset?, size].
func partCell(f compose.PartFacts, size int64) string {
	s := fmt.Sprintf("[%s, %s", f.Type, f.Encoding)
	if f.Charset != "" {
		s += ", " + f.Charset
	}
	return fmt.Sprintf("%s, %s]", s, sizeStr(size))
}

// renderCompose builds the attached dialogue frame (spec section 5,
// the mutt layout): the tab bar on the first line, the keyhint on the
// second, the form rows (the sender info, the Security divider, the
// content-type entry and the attachments), the preview pane (the
// pager widget) filling the rest, the status line on the last. The
// prompt dialogue splices as a boxed overlay above the status when
// open. The frame is ALWAYS exactly m.height lines - the frame
// discipline applies to the compose surface like everywhere else.
func (m *Model) renderCompose() string {
	st := m.tabs[m.tabIdx-1]
	rows := m.height - 3
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteByte('\n')
	b.WriteString(m.keyhint())
	b.WriteByte('\n')
	// the cursor marker column: the index's selection marker cell is
	// reserved on every row (the R11 slot rule), so the marker never
	// shifts content - the cursor row shows the glyph, the others a
	// blank cell, and a constant gap space follows the mark on every
	// row. The row content is laid out for the frame minus the column
	// and the gap, so the mime info stays right-aligned to the edge.
	colW := runewidth.StringWidth(m.ui.Glyphs.Cursor)
	form := m.composeForm(st, m.width-colW-1, m.formIdx)
	// the form is a viewport (the pager widget): when the rows outgrow
	// the frame, the window scrolls with the cursor (formIdx). The
	// frame discipline holds - the keyhint/status rows stay anchored.
	texts := make([]string, len(form))
	for i, f := range form {
		texts[i] = f.text
	}
	m.formView.setLines(texts)
	formRows := len(form)
	if formRows > rows-1 {
		formRows = rows - 1
	}
	if formRows < 1 {
		formRows = 1
	}
	m.formView.setSize(m.width, formRows)
	if r := formRowOf(form, m.formIdx); r >= 0 {
		m.formView.ensureVisible(r)
	}
	labelW := 0
	for _, f := range form {
		if f.label != "" && len(f.label)+1 > labelW {
			labelW = len(f.label) + 1
		}
	}
	vis := m.formView.window()
	for i := range vis {
		f := form[m.formView.offset+i]
		outer := m.styles.Normal
		if f.divider {
			outer = m.styles.ComposeDivider
		}
		line := f.text
		if f.label != "" {
			line = composeLabel(f.label, f.value, labelW, m.width-colW-1, m.styles)
		}
		mark := strings.Repeat(" ", colW)
		if f.slot == m.formIdx {
			// the cursor is the index's selection marker: the glyph in
			// the indicator style in the reserved column (the same
			// style as the index rows), never a full-line highlight
			mark = m.styles.sgr.indicator.render(m.ui.Glyphs.Cursor)
		}
		b.WriteString(padRow(mark+" "+line, m.width, outer))
		b.WriteByte('\n')
	}
	previewRows := rows - formRows
	if previewRows > 0 {
		m.syncPreviewPager(st)
		m.previewPager.setSize(m.width, previewRows, m.styles)
		b.WriteString(m.previewPager.render())
		b.WriteByte('\n')
	}
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	if m.composeTab().Phase == compose.PhaseSending {
		return m.sendOverlay(b.String())
	}
	return b.String()
}

// sendOverlay replaces the compose frame's body rows with the send box
// while the delivery is in flight (R4): the spinner frame (advanced by
// sendTick) and the note that tab switching stays live - the send runs
// on its own goroutine, the dialogue waits. The splice is compose-
// surface only: switching tabs parks the box with the dialogue, the
// mail surface shows nothing while the job runs.
func (m Model) sendOverlay(frame string) string {
	lines := strings.Split(frame, "\n")
	if m.height < 6 || m.width < 3 || len(lines) < 5 {
		return frame
	}
	content := []string{
		"Sending " + spinnerChar(m.spin),
		"[ ] switch tabs while sending",
	}
	return strings.Join(spliceBox(lines, m.width, m.ui, m.styles, content), "\n")
}

// spinnerChar is the send spinner's current frame - ASCII frames, a
// presentation constant (the R15 glyphs are config data; the spinner
// is animation state, not a themed glyph).
const spinFrames = "|/-\\"

func spinnerChar(i int) string {
	return string(spinFrames[i%len(spinFrames)])
}

// syncPreviewPager rebuilds the preview pager only when the rendered
// content changes (body/signature edits, a send failure); the scroll
// position survives otherwise. The pager INSTANCE is stable - render
// runs on a value copy of the model, so a reassignment here would be
// lost; the in-place rebuild (lines + styled + offset) survives, and
// the next setSize re-styles the window.
func (m *Model) syncPreviewPager(st compose.State) {
	content := compose.BodyWithSig(st.Body, st.SignatureBody)
	if st.Phase == compose.PhaseFailed {
		content = i18n.T("send failed") + ":\n" + st.Output
	}
	if content != m.previewContent {
		m.previewContent = content
		m.previewPager.lines = previewLinesOf(content)
		m.previewPager.styled = nil
		m.previewPager.vp.offset = 0
	}
}

// previewLinesOf converts the compose content to pager lines: body
// lines, the "-- " marker and everything after it as signature. The
// F1 sanitize runs here - the compose body is editor text, not the
// mail path's pre-sanitized lines.
func previewLinesOf(content string) []core.Line {
	var lines []core.Line
	sig := false
	for _, l := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if l == "-- " {
			sig = true
		}
		kind := core.LineBody
		if sig {
			kind = core.LineSignature
		}
		lines = append(lines, core.Line{Text: core.SanitizeControls(l), Kind: kind})
	}
	return lines
}

// composeForm renders the form rows: the sender info (account, From,
// To, Cc, Bcc, Subject, Reply-To, Fcc - Fcc static, set from the
// account), the Security row, then the attachment list with mutt's
// markers: the message-text row (marker I, entry 1, its mime type,
// the buffer file path) and the attached files (marker A, deletable),
// separators. Address lists cap at two display rows (alignment never
// shifts; "+N more" names the overflow). The attachment list is a
// three-row window around the cursor slot (cursor): the selected
// attachment is always visible, "+N more" rows above and below name
// the hidden count, and the window slides with the selection. w is
// the row content width (the frame minus the reserved cursor column).
func (m *Model) composeForm(st compose.State, w, cursor int) []composeForm {
	capList := func(addrs []string) string {
		if len(addrs) == 0 {
			return ""
		}
		if len(addrs) <= 2 {
			return strings.Join(addrs, ", ")
		}
		return strings.Join(addrs[:2], ", ") + fmt.Sprintf(", +%d more", len(addrs)-2)
	}
	rows := []composeForm{
		{slot: -1, label: "Account", value: st.Account},
		{slot: -1, label: "From", value: st.From},
		{slot: -1, label: "To", value: capList(st.To)},
		{slot: -1, label: "Cc", value: capList(st.Cc)},
		{slot: -1, label: "Bcc", value: capList(st.Bcc)},
		{slot: -1, label: "Subject", value: st.Subject},
		{slot: -1, label: "Reply-To", value: capList(st.ReplyTo)},
		{slot: -1, label: "Fcc", value: st.Fcc},
		{slot: -1, label: "Security", value: st.Security.String()},
		{slot: -1, text: "--- Attachments", divider: true},
	}
	// the message-text row: marker I (mutt's inline part), entry 1,
	// the buffer file path, its wire facts
	rows = append(rows, composeForm{slot: 8, text: attachRow("I", 1, st.BodyPath,
		partCell(compose.InlineFacts(&st), int64(len(compose.BodyWithSig(st.Body, st.SignatureBody)))), w)})
	n := len(st.Attachments)
	sel := cursor - 9
	if sel < 0 || sel >= n {
		sel = 0
	}
	lo := sel - 2 // the cursor's row is the window's third
	if lo < 0 {
		lo = 0
	}
	if lo+3 > n {
		lo = n - 3
	}
	if lo < 0 {
		lo = 0
	}
	hi := lo + 3
	if hi > n {
		hi = n
	}
	if lo > 0 {
		rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", lo)})
	}
	for i := lo; i < hi; i++ {
		rows = append(rows, composeForm{slot: 9 + i,
			text: attachRow("A", i+2, st.Attachments[i].Name, partCell(compose.AttachmentFacts(st.Attachments[i]), st.Attachments[i].Size), w)})
	}
	if hi < n {
		rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", n-hi)})
	}
	rows = append(rows, composeForm{slot: -1, text: "--- Preview", divider: true})
	// the form rows render mail-derived text (Subject/To/Cc from the
	// replied-to message's headers) - same sanitizer as the index rows
	// and the preview pane (F1). The labels are constants, never
	// sanitized.
	for i := range rows {
		rows[i].text = core.SanitizeControls(rows[i].text)
		rows[i].value = core.SanitizeControls(rows[i].value)
	}
	return rows
}

// attachRow lays one attachment-list row out as a 4-column table
// (mutt's attach-menu shape): the type marker (I message text, A
// attachment), the entry number right-aligned in a fixed column, the
// name/path left-aligned, the mime info right-aligned to the row
// edge. Column widths never shift with content (R11 slot discipline);
// a long name truncates.
func attachRow(marker string, num int, name, mime string, w int) string {
	prefix := fmt.Sprintf("%s %*d ", marker, attachNumW, num)
	fileW := w - len(prefix) - runewidth.StringWidth(mime)
	if fileW < 1 {
		fileW = 1
	}
	if pad := fileW - runewidth.StringWidth(name); pad >= 0 {
		return prefix + name + strings.Repeat(" ", pad) + mime
	}
	return prefix + truncCells(name, fileW) + mime
}

// attachNumW is the entry-number column width: right-aligned and
// fixed, room to 9999 entries before the column ever shifts.
const attachNumW = 4

// sizeStr formats a part size the way mutt's attach menu does: K and
// M with one decimal, a trailing .0 dropped (0.1K, 40K, 1.2M).
func sizeStr(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	case n < 1<<20:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/(1<<10)), ".0") + "K"
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/(1<<20)), ".0") + "M"
	}
}

// composeLabel renders one settings row as a two-column table: the
// label right-aligned in a fixed column (the colons align at the
// seam), the value truncated to the remaining row width. The label
// carries the compose.label style (theme blue); the value cell keeps
// the normal style, so the caller's row padding never restyles it.
func composeLabel(label, value string, labelW, w int, st Styles) string {
	lbl := st.ComposeLabel.Width(labelW).Align(lipgloss.Right).Render(label + ":")
	val := st.Normal.Render(truncCells(value, w-labelW-1))
	return lbl + " " + val
}

// formRowOf is the row index of the cursor slot (-1 when the slot has
// no row, e.g. a hidden attachment past the "+N more" cap): the form
// viewport's follow-cursor target.
func formRowOf(form []composeForm, slot int) int {
	for i, f := range form {
		if f.slot == slot {
			return i
		}
	}
	return -1
}
