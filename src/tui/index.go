package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// renderRow renders the fixed-slot template (R11): number, flags,
// attachment, date, author, subject, tags. Optional slots reserve width;
// every slot renders through its style, so the line carries per-slot SGR
// runs (the outer row style is applied later by padRow).
func renderRow(n int, row core.Row, st Styles) string {
	var b strings.Builder
	b.WriteString(st.Index.Number.Render(padCellsRight(strconv.Itoa(n), 4)))
	b.WriteByte(' ')
	if row.Msg == nil {
		// ghost root: message-derived slots stay blank, "[...]" fills the
		// subject slot so the template stays aligned
		b.WriteString(padCellsRight("", 3))
		b.WriteString(" ")
		b.WriteByte(' ')
		b.WriteString(padCellsRight("", 15))
		b.WriteByte(' ')
		b.WriteString(padCellsRight("", 16))
		b.WriteByte(' ')
		b.WriteString(truncCells("[...] "+strconv.Itoa(row.Count), 40))
		return b.String()
	}
	tags := row.Msg.Tags
	flagStr := st.Index.Flags.Render(flags(tags))
	if row.Staged {
		// staged rows render the resolved display tags with the staged
		// glyph (default "*", config data per R11 tag-transforms); the
		// hardcoded default holds until the theming milestone. The slot
		// keeps its fixed width - alignment never shifts per row.
		tags = row.StagedTags
		flagStr = st.Index.Staged.Render(padCellsRight("*"+flagChars(tags), 3))
	}
	b.WriteString(flagStr)
	b.WriteString(attachIcon(row.Msg))
	b.WriteByte(' ')
	b.WriteString(st.Index.Date.Render(padCellsRight(formatDate(row.Msg.Timestamp), 15)))
	b.WriteByte(' ')
	author := stripControls(row.Msg.Author)
	b.WriteString(st.Index.Author.Render(padCellsRight(truncCells(author, 16), 16)))
	b.WriteByte(' ')
	subject := stripControls(row.Msg.Subject)
	b.WriteString(st.Index.Subject.Render(truncCells(subject, 40)))
	b.WriteByte(' ')
	b.WriteString(st.Index.Tag.Render(tagGlyphs(tags)))
	return b.String()
}

func flags(tags []string) string {
	return padCellsRight(flagChars(tags), 3)
}

func flagChars(tags []string) string {
	var f strings.Builder
	for _, t := range tags {
		switch t {
		case "unread":
			f.WriteByte('U')
		case "replied":
			f.WriteByte('R')
		case "forwarded":
			f.WriteByte('F')
		case "deleted":
			f.WriteByte('D')
		}
	}
	return f.String()
}

func attachIcon(m *core.Message) string {
	if len(m.Atts) > 0 {
		return "A"
	}
	return " "
}

func formatDate(ts int64) string {
	return time.Unix(ts, 0).Format("06/01/02 15:04")
}

func tagGlyphs(tags []string) string {
	// max 2 tags, first two of the display order; the tag-groups
	// priority list supplies the order later (spec section 6)
	var b strings.Builder
	n := 0
	for _, t := range tags {
		if t == "unread" {
			continue
		}
		if n >= 2 {
			break
		}
		b.WriteString(padCellsRight(truncCells(stripControls(t), 4), 4))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
}

// stripControls drops C0/DEL/C1 control runes so mail content can never
// inject terminal escapes (F1).
func stripControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7F && r <= 0x9F) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncCells truncates s to at most w terminal cells; padCellsRight pads
// it to exactly w cells (wcwidth, not runes).
func truncCells(s string, w int) string {
	var b strings.Builder
	cells := 0
	for _, r := range s {
		cw := runewidth.RuneWidth(r)
		if cells+cw > w {
			break
		}
		b.WriteRune(r)
		cells += cw
	}
	return b.String()
}

func padCellsRight(s string, w int) string {
	t := truncCells(s, w)
	return t + strings.Repeat(" ", w-runewidth.StringWidth(t))
}

// stripANSI removes SGR sequences (ESC [ ... m) from s; other control
// chars never reach rendered lines (stripControls ran on the content).
func stripANSI(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var b strings.Builder
	inSeq := false
	for _, r := range s {
		if inSeq {
			if r == 'm' {
				inSeq = false
			}
			continue
		}
		if r == '\x1b' {
			inSeq = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncateStyled truncates a styled string to at most w visible cells;
// SGR runs are zero-width and kept whole, so the line stays parseable.
func truncateStyled(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	var b strings.Builder
	cells := 0
	inSeq := false
	for _, r := range s {
		if inSeq {
			b.WriteRune(r)
			if r == 'm' {
				inSeq = false
			}
			continue
		}
		if r == '\x1b' {
			inSeq = true
			b.WriteRune(r)
			continue
		}
		cw := runewidth.RuneWidth(r)
		if cells+cw > w {
			return b.String()
		}
		b.WriteRune(r)
		cells += cw
	}
	return b.String()
}

// padRow wraps line in outer so the row style covers the full width
// (R11: rows never exceed width, alignment never shifts). The slot
// styles reset it mid-line, so the row style's opening sequence is
// re-applied after every reset; the line's own slot colors survive
// inside.
func padRow(line string, w int, outer lipgloss.Style) string {
	open := strings.TrimSuffix(outer.Render(""), "\x1b[0m")
	inner := line
	if width := runewidth.StringWidth(stripANSI(line)); width >= w {
		inner = truncateStyled(line, w)
	} else {
		inner += strings.Repeat(" ", w-width)
	}
	return open + strings.ReplaceAll(inner, "\x1b[0m", "\x1b[0m"+open) + "\x1b[0m"
}
