package tui

import (
	"fmt"
	"strings"

	"notmutt/compose"
	"notmutt/core"
)

// composeForm is one form line with its cursor slot: 0 = account,
// 1-4 = From/To/Cc/Subject, 5+i = attachment i, -1 = separator (never
// highlighted).
type composeForm struct {
	slot int
	text string
}

// renderCompose builds the attached dialogue frame (spec section 5):
// the form rows, the attachment rows, the preview pane filling the
// rest, the keyhint and status rows. The frame is ALWAYS exactly
// m.height lines - the frame discipline applies to the compose
// surface like everywhere else.
func (m *Model) renderCompose() string {
	if m.fuzzy != nil {
		return m.renderFuzzy()
	}
	st := m.tabs[m.tabIdx-1]
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
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
	var b strings.Builder
	vis := m.formView.window()
	for i, text := range vis {
		outer := m.styles.Normal
		if form[m.formView.offset+i].slot == m.formIdx {
			outer = m.styles.Indicator
		}
		b.WriteString(padRow(text, m.width, outer))
		b.WriteByte('\n')
	}
	previewRows := rows - formRows
	if previewRows > 0 {
		var preview string
		switch {
		case st.Phase == compose.PhaseFailed:
			preview = "send failed:\n" + st.Output
		default:
			preview = compose.BodyWithSig(st.Body, st.SignatureBody)
		}
		lines := strings.Split(core.SanitizeControls(preview), "\n")
		for i := 0; i < previewRows; i++ {
			line := ""
			if i < len(lines) {
				line = lines[i]
			}
			b.WriteString(padRow(line, m.width, m.styles.Normal))
			b.WriteByte('\n')
		}
	}
	// the abort confirm and the attach prompt swap the keyhint row
	// 1:1 (the frame height invariant); the prompt shows the typed
	// path - pasted text can carry ESC, sanitized at render (F1)
	switch {
	case st.Phase == compose.PhaseAborting:
		b.WriteString(padRow("abort? q to confirm, any other key to cancel", m.width, m.styles.Indicator))
	case m.prompt != nil:
		b.WriteString(padRow(core.SanitizeControls(m.prompt.label+m.prompt.input), m.width, m.styles.Indicator))
	default:
		b.WriteString(m.keyhint())
	}
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return b.String()
}

// composeForm renders the form rows: account/From/To/Cc/Subject on
// their own lines (the account selectable separately from the From
// address), the attachment rows, separators. Address lists cap at two
// display rows (alignment never shifts; "+N more" names the
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
		{slot: 0, text: "Account: " + st.Account},
		{slot: 1, text: "From: " + st.From},
		{slot: 2, text: "To: " + capList(st.To)},
		{slot: 3, text: "Cc: " + capList(st.Cc)},
		{slot: 4, text: "Subject: " + st.Subject},
		{slot: -1, text: "---"},
	}
	for i, a := range st.Attachments {
		if i >= 3 {
			rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", len(st.Attachments)-3)})
			break
		}
		rows = append(rows, composeForm{slot: 5 + i, text: fmt.Sprintf("[ ] %s (%d bytes)", a.Name, a.Size)})
	}
	rows = append(rows, composeForm{slot: -1, text: "---"})
	// the form rows render mail-derived text (Subject/To/Cc from the
	// replied-to message's headers) - same sanitizer as the index rows
	// and the preview pane (F1)
	for i := range rows {
		rows[i].text = core.SanitizeControls(rows[i].text)
	}
	return rows
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
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
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
	return b.String()
}
