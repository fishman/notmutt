package tui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

// renderRow renders the fixed-slot template (R11): number, flags,
// attachment, date, author, subject, tags. The number slot is the one
// variable width: it grows to the widest row number (the caller passes
// the count-derived width) so the column aligns without padding waste.
// Optional slots reserve width; every slot renders through its style,
// so the line carries per-slot SGR runs (the outer row style is applied
// later by padRow). Glyphs and the tag-slot cap come from config data,
// never hardcoded.
func renderRow(n int, row core.Row, st Styles, ui config.UI, numWidth int, selected bool) string {
	// the cursor row is monochrome (R11): one highlight background and one
	// text color - the indicator style replaces every slot style
	numStyle, flagStyle, dateStyle, authorStyle, subjectStyle := st.Index.Number, st.Index.Flags, st.Index.Date, st.Index.Author, st.Index.Subject
	if selected {
		numStyle, flagStyle, dateStyle, authorStyle, subjectStyle = st.Indicator, st.Indicator, st.Indicator, st.Indicator, st.Indicator
	}
	var b strings.Builder
	b.WriteString(numStyle.Render(padCellsRight(strconv.Itoa(n), numWidth)))
	b.WriteByte(' ')
	if row.Msg == nil {
		// ghost root: message-derived slots stay blank, "[...]" fills the
		// subject slot so the template stays aligned
		b.WriteString(padCellsRight("", numWidth))
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
	flagStr := flagStyle.Render(flags(tags))
	if row.Staged {
		// staged rows render the resolved display tags with the staged
		// glyph (config data, R11 tag-transforms). The slot keeps its
		// fixed width - alignment never shifts per row.
		tags = row.StagedTags
		flagStr = st.Index.Staged.Render(padCellsRight(ui.Glyphs.Staged+flagChars(tags), 3))
	}
	b.WriteString(flagStr)
	b.WriteString(padCellsRight(attachIcon(row.Msg, ui.Tags), 2))
	b.WriteString(dateStyle.Render(padCellsRight(formatDate(row.Msg.Timestamp), 15)))
	b.WriteByte(' ')
	author := core.SanitizeControls(row.Msg.Author)
	b.WriteString(authorStyle.Render(padCellsRight(truncCells(author, 16), 16)))
	b.WriteByte(' ')
	subject := core.SanitizeControls(row.Msg.Subject)
	b.WriteString(subjectStyle.Render(truncCells(subject, 40)))
	b.WriteByte(' ')
	tagStyle := st.Index.Tag
	if selected {
		tagStyle = func(string) lipgloss.Style { return st.Indicator }
	}
	b.WriteString(tagGlyphs(tags, ui.Tags.Max, tagStyle, ui.Tags))
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

// attachIcon renders the attachment slot at the row start: the marker
// tag's icon (config data, ui.tags.attach + icons) when the message
// carries the marker tag or the cache found attachments. The marker tag
// never repeats in the tag slot (tagGlyphs skips it). The caller pads the
// slot to 2 cells - double-width glyphs (the paperclip emoji) fit without
// shifting the following columns (R11 slot reservation).
func attachIcon(m *core.Message, t config.UITags) string {
	if (t.Attach != "" && slices.Contains(m.Tags, t.Attach)) || len(m.Atts) > 0 {
		if t.ShowIcons && t.Icons[t.Attach] != "" {
			return t.Icons[t.Attach]
		}
		return "A"
	}
	return " "
}

func formatDate(ts int64) string {
	return time.Unix(ts, 0).Format("06/01/02 15:04")
}

// tagGlyphs renders up to max tags as styled glyphs, first of the
// display order; the tag-groups priority list supplies the order later
// (spec section 6). Each glyph renders through its per-tag style (R11),
// falling back to the default tag style. A tag with an icon entry in the
// ui.tags.icons dict renders the icon instead of its name (muttrc
// tag-transforms); the attachment marker tag is skipped - it owns the
// attachment slot at the row start.
func tagGlyphs(tags []string, max int, tagStyle func(string) lipgloss.Style, t config.UITags) string {
	var b strings.Builder
	n := 0
	for _, tag := range tags {
		if tag == "unread" || tag == t.Attach {
			continue
		}
		if n >= max {
			break
		}
		label := tag
		if t.ShowIcons && t.Icons[tag] != "" {
			label = t.Icons[tag]
		}
		b.WriteString(tagStyle(tag).Render(padCellsRight(truncCells(core.SanitizeControls(label), 4), 4)))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
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
// chars never reach rendered lines (SanitizeControls ran on the content).
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
	if runewidth.StringWidth(stripANSI(s)) <= w {
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
