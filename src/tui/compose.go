package tui

import (
	"fmt"
	"strings"

	"notmutt/compose"
	"notmutt/core"
)

// composeForm is one form line with its cursor slot: 0-3 =
// From/To/Cc/Subject, 4+i = attachment i, -1 = separator (never
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
	var b strings.Builder
	for _, f := range form {
		outer := m.styles.Normal
		if f.slot == m.formIdx {
			outer = m.styles.Indicator
		}
		b.WriteString(padRow(f.text, m.width, outer))
		b.WriteByte('\n')
	}
	previewRows := rows - len(form)
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
	// the abort confirm swaps the keyhint row (the two-press q)
	if st.Phase == compose.PhaseAborting {
		b.WriteString(padRow("abort? q to confirm, any other key to cancel", m.width, m.styles.Indicator))
	} else {
		b.WriteString(keyhintRow(m.bindings["compose"], m.width))
	}
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return b.String()
}

// composeForm renders the form rows: From/To/Cc/Subject, the
// attachment rows, separators. Address lists cap at two display rows
// (alignment never shifts; "+N more" names the overflow).
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
		{slot: 0, text: fmt.Sprintf("From: %s  [%s]", st.From, st.Account)},
		{slot: 1, text: "To: " + capList(st.To)},
		{slot: 2, text: "Cc: " + capList(st.Cc)},
		{slot: 3, text: "Subject: " + st.Subject},
		{slot: -1, text: "---"},
	}
	for i, a := range st.Attachments {
		if i >= 3 {
			rows = append(rows, composeForm{slot: -1, text: fmt.Sprintf("... +%d more", len(st.Attachments)-3)})
			break
		}
		rows = append(rows, composeForm{slot: 4 + i, text: fmt.Sprintf("[ ] %s (%d bytes)", a.Name, a.Size)})
	}
	rows = append(rows, composeForm{slot: -1, text: "---"})
	return rows
}

// renderFuzzy builds the selector popup frame: the title, the ranked
// matches, the query row, the fuzzy keyhint, the status row. Exactly
// m.height lines - the popup replaces the compose frame (a clean
// diff, never an overlay). ponytail: the popup shows at most rows-2
// matches (the slice cuts the tail); large lists scroll later.
func (m *Model) renderFuzzy() string {
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	lines := []string{m.fuzzy.title}
	for i, idx := range m.fuzzy.filtered() {
		outer := m.styles.Normal
		if i == m.fuzzy.sel {
			outer = m.styles.Indicator
		}
		lines = append(lines, padRow(m.fuzzy.entries[idx], m.width, outer))
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
