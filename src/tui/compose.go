package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"notmutt/compose"
	"notmutt/core"
)

// composeForm is one form line: the settings rows carry a label +
// value (rendered as a two-column table, never highlighted), the
// attachment rows carry a cursor slot (8+i), the static rows
// (dividers, content-type) carry plain text. Every non-focusable row
// carries the sentinel slot -1 (the settings rows included - only
// the attachment slots are ever focused).
type composeForm struct {
	slot  int
	label string // settings row label, rendered right-aligned in compose.label
	value string // settings row value
	text  string // plain rows (dividers, content-type, attachments)
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
	if m.fuzzy != nil {
		return m.renderFuzzy()
	}
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
	form := m.composeForm(st)
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
		if f.slot == m.formIdx {
			outer = m.styles.Indicator
		}
		line := f.text
		if f.label != "" {
			line = composeLabel(f.label, f.value, labelW, m.width, m.styles)
		}
		b.WriteString(padRow(line, m.width, outer))
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
	return m.overlayDialogue(b.String())
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
		content = "send failed:\n" + st.Output
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
// account), the Security row, the content-type entry (derived from
// the body), the attachment rows, separators. Address lists cap at
// two display rows (alignment never shifts; "+N more" names the
// overflow).
func (m *Model) composeForm(st compose.State) []composeForm {
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
		{slot: -1, text: "---"},
		{slot: -1, text: "[ ] " + compose.ContentTypeOf(st.Body)},
	}
	for i, a := range st.Attachments {
		if i >= 3 {
			rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", len(st.Attachments)-3)})
			break
		}
		rows = append(rows, composeForm{slot: 8 + i, text: fmt.Sprintf("[ ] %s (%d bytes)", a.Name, a.Size)})
	}
	rows = append(rows, composeForm{slot: -1, text: "---"})
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

// renderFuzzy builds the selector popup frame: the title, the ranked
// matches, the query row, the fuzzy keyhint, the status row. Exactly
// m.height lines - the popup replaces the compose frame (a clean
// diff, never an overlay). The query row always renders - the user's
// filter input stays visible mid-type - and the match list clips to
// fill the frame (large lists scroll later).
func (m *Model) renderFuzzy() string {
	rows := m.height - 3
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteByte('\n')
	lines := []string{m.fuzzy.title}
	// the query row always renders (the user's filter input must stay
	// visible mid-type); the match list clips to fill
	matchRows := rows - 2
	if matchRows < 0 {
		matchRows = 0
	}
	matches := m.fuzzy.filtered()
	if matchRows > len(matches) {
		matchRows = len(matches)
	}
	for i := 0; i < matchRows; i++ {
		outer := m.styles.Normal
		if i == m.fuzzy.sel {
			outer = m.styles.Indicator
		}
		lines = append(lines, padRow(m.fuzzy.entries[matches[i]], m.width, outer))
	}
	lines = append(lines, padRow(m.fuzzy.title+" "+m.fuzzy.query, m.width, m.styles.Indicator))
	for len(lines) < rows {
		lines = append(lines, padRow("", m.width, m.styles.Normal))
	}
	for _, l := range lines[:rows] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString(keyhintRow(m.bindings["fuzzy"], m.width))
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return m.overlayDialogue(b.String())
}
