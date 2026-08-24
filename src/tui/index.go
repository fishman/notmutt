// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

// renderRow renders the fixed-slot template (R11): number, flags,
// attachment, signed, date, author, tags, subject. The number and tag
// slots are variable-width, growing to the widest on the page. The
// subject is the flexible last slot - the thread tree run prepends in
// it, so the subject moves with the thread indent (mutt's %T
// placement); it renders in full, padRow clamps the row to the
// terminal width. Optional slots reserve width; every slot renders
// through its style, so the line carries per-slot SGR runs (the outer
// row style applies later by padRow). Glyphs and the tag-slot cap
// come from config data. Account tags never render here - the account
// lives in the status bar (R2), not the mail title.
func renderRow(n int, row core.Row, st Styles, ui config.UI, numWidth, tagWidth int, selected bool, accountTags map[string]bool, query string, mark core.MsgMark) string {
	sg := st.sgr
	// the row keeps its slot styles; the selection is the cursor
	// marker cell (config glyph, indicator-styled) at the line start
	var b strings.Builder
	if selected {
		b.WriteString(sg.indicator.render(ui.Glyphs.Cursor))
	} else {
		b.WriteString(sg.normal.render(" "))
	}
	b.WriteString(sg.number.render(padCellsRight(strconv.Itoa(n), numWidth)))
	b.WriteByte(' ')
	if row.Msg == nil {
		// ghost root: message-derived slots stay blank, "[...]" fills the
		// subject slot so the template stays aligned
		b.WriteString(padCellsRight("", numWidth))
		b.WriteString(" ")
		b.WriteString(padCellsRight("", 2))
		b.WriteString(padCellsRight("", 2))
		b.WriteString(padCellsRight("", 15))
		b.WriteByte(' ')
		b.WriteString(padCellsRight("", 16))
		b.WriteByte(' ')
		if tagWidth > 0 {
			b.WriteString(padCellsRight("", tagWidth))
			b.WriteByte(' ')
		}
		b.WriteString(sg.tree.render(treePrefix(row, ui.Glyphs)))
		if row.MoreTop > 0 {
			// the top-side overflow indicator: how many thread rows stay
			// hidden above the tree window, rendered in the free space
			// over the thread (the walk-up slides into them)
			b.WriteString(sg.tree.render("+" + strconv.Itoa(row.MoreTop) + " more"))
			return b.String()
		}
		if row.More > 0 {
			// the overflow indicator: how many thread rows stay hidden
			// below the tree window, rendered in the free space under
			// the thread (the page move scrolls through them)
			b.WriteString(sg.tree.render("+" + strconv.Itoa(row.More) + " more"))
			return b.String()
		}
		b.WriteString("[...] " + strconv.Itoa(row.Count))
		return b.String()
	}
	tags := rowTagList(row)
	flagStr := sg.flags.render(flags(tags))
	if row.Staged {
		// staged rows render the resolved display tags with the staged
		// glyph (config data, R11 tag-transforms). The slot keeps its
		// fixed width - alignment never shifts per row.
		flagStr = sg.staged.render(padCellsRight(ui.Glyphs.Staged+flagChars(tags), 3))
	}
	b.WriteString(flagStr)
	b.WriteString(padCellsRight(attachIcon(row.Msg, ui.Tags), 2))
	b.WriteString(padCellsRight(signedIcon(row.Msg, ui.Tags), 2))
	b.WriteString(sg.date.render(padCellsRight(formatDate(row.Msg.Timestamp), 15)))
	b.WriteByte(' ')
	author := core.SanitizeControls(displayAuthor(row.Msg.Author))
	b.WriteString(renderHighlighted(padCellsRight(truncCells(author, 16), 16), query, sg.author, sg.search))
	b.WriteByte(' ')
	if tagWidth > 0 {
		// the tag slot sits right after the sender (R2 surface split:
		// account tags live in the status bar, display tags stay in the
		// title). The run pads to the page width - the subject column
		// never shifts within a page.
		b.WriteString(padTagRun(tagGlyphs(tags, ui.Tags.Max, sg.tag, ui.Tags, accountTags), tagWidth, sg.tag))
		b.WriteByte(' ')
	}
	// the tree run is prepended in the subject slot (mutt's %T): the
	// subject moves with the thread indent, so there is no column
	// alignment to keep - the fixed columns never move, the title
	// eats the depth and padRow clamps the row at the frame width
	// the thread-position mark (the opened message): the subject run
	// and its tree indicator tint - the recent-5 cyan, the last
	// other-side message purple bold; the rest of the row keeps its
	// slot colors
	subjectStyle := sg.subject
	if mark == core.MarkOther {
		subjectStyle = sg.pagerOther
	} else if mark == core.MarkRecent {
		subjectStyle = sg.pagerRecent
	}
	subject := core.SanitizeControls(row.Msg.Subject)
	if row.Collapsed && row.Count > 1 {
		// the collapsed marker: the tree glyph plus the hidden-row count
		// in the [index.collapsed] style - without it a collapsed thread looks like a plain root
		b.WriteString(sg.collapsed.render(ui.Glyphs.Tree + strconv.Itoa(row.Count-1)))
		b.WriteByte(' ')
	} else {
		b.WriteString(subjectStyle.render(treePrefix(row, ui.Glyphs)))
	}
	b.WriteString(renderHighlighted(subject, query, subjectStyle, sg.search))
	return b.String()
}

// treePrefix renders the thread tree run (R3): the root marker for a
// thread with children (depth 0, Count > 1), then indentation plus the
// branch/leaf marker. The vertical at column c draws iff the ancestor
// at depth c+1 (Depth-1-c levels up, neomutt's conditional pfx) has a
// next sibling. Glyphs carry their own trailing space (config data),
// so zero-width prefixes leave the flat layout byte-identical.
func treePrefix(r core.Row, g config.Glyphs) string {
	if r.Depth == 0 {
		if r.Count > 1 {
			return g.Tree
		}
		return ""
	}
	var b strings.Builder
	lo := max(0, r.Depth-len(r.Siblings))
	for c := lo; c < r.Depth-1; c++ {
		if r.Siblings[len(r.Siblings)-1-c] {
			b.WriteString(g.TreeChild)
		} else {
			b.WriteString(strings.Repeat(" ", runewidth.StringWidth(g.TreeChild)))
		}
	}
	if len(r.Siblings) > 0 && r.Siblings[0] {
		b.WriteString(g.TreeBranch)
	} else {
		b.WriteString(g.TreeLeaf)
	}
	return b.String()
}

// renderHighlighted renders s through style, every query occurrence
// in hl (the / search match, case-insensitive); empty query renders
// plain. Index's offsets are rune-safe, so runs never split a character.
func renderHighlighted(s, query string, style, hl sgr) string {
	if query == "" {
		return style.render(s)
	}
	lower, q := strings.ToLower(s), strings.ToLower(query)
	var b strings.Builder
	for from := 0; ; {
		i := strings.Index(lower[from:], q)
		if i < 0 {
			b.WriteString(style.render(s[from:]))
			return b.String()
		}
		i += from
		b.WriteString(style.render(s[from:i]))
		b.WriteString(hl.render(s[i : i+len(q)]))
		from = i + len(q)
	}
}

// rowTagList is the tag list a row renders in its tag slot: staged rows show the staged set, ghost rows none.
func rowTagList(row core.Row) []string {
	if row.Msg == nil {
		return nil
	}
	if row.Staged {
		return row.StagedTags
	}
	return row.Msg.Tags
}

func flags(tags []string) string {
	return padCellsRight(flagChars(tags), 3)
}

// flagLetters is the canonical status-flag lookup table: tag -> the
// letter it owns in the row-start flags cell (mutt's %S index flags as
// tags, R1: client state is tags, not flags). A tag with an entry
// renders its letter in the flags slot, never the tag slot (flagTag).
// One table, both consumers.
var flagLetters = map[string]byte{
	"new":       'N',
	"unread":    'N',
	"replied":   'R',
	"forwarded": 'F',
	"deleted":   'D',
	"trash":     'D',
}

// flagTag reports whether tag is a flag-slot tag (a flagLetters key): it owns a row-start flags letter and never renders in the tag slot.
func flagTag(tag string) bool {
	_, ok := flagLetters[tag]
	return ok
}

func flagChars(tags []string) string {
	var f strings.Builder
	for _, t := range tags {
		if l, ok := flagLetters[t]; ok {
			f.WriteByte(l)
		}
	}
	return f.String()
}

// signedIcon renders the signed marker slot at the row start, right
// after the attachment slot: the signed tag's icon (config data,
// ui.tags.icons) when signed. The caller pads the slot to 2 cells -
// the double-width lock emoji fits without shifting columns (R11).
func signedIcon(m *core.Message, t config.UITags) string {
	if slices.Contains(m.Tags, "signed") {
		if t.ShowIcons && t.Icons["signed"] != "" {
			return t.Icons["signed"]
		}
		return "S"
	}
	return " "
}

// attachIcon renders the attachment slot at the row start: the marker
// tag's icon (config data, ui.tags.attach + icons) when the message
// carries the marker tag or the cache found attachments. The marker
// tag never repeats in the tag slot (tagGlyphs skips it); the caller
// pads the slot to 2 cells so double-width glyphs fit (R11).
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

// displayAuthor strips the RFC 5322 quoting from a From value for the
// index row: the walk carries the raw header (`"Name" <a@b>`), and
// the display shows the name without the quotes - a bare address or an
// unparseable value renders as-is.
func displayAuthor(raw string) string {
	if p, err := mail.ParseAddress(raw); err == nil {
		if p.Name != "" {
			return p.Name
		}
		return p.Address
	}
	return raw
}

// tagGlyphs renders up to max tags as styled glyphs in display order
// (the tag-groups priority list supplies the order later, spec section
// 6), each through its per-tag style (R11), falling back to the
// default tag style. An icon entry in ui.tags.icons renders instead of
// the name (muttrc tag-transforms). Skipped: flag-slot tags (flagTag)
// and the signed marker tag - they own row-start cells (flagChars,
// signedIcon); the attachment marker tag - it owns the attachment
// slot; account tags - the account lives in the status bar (R2),
// never the mail title.
func tagGlyphs(tags []string, max int, tagStyle func(string) sgr, t config.UITags, accountTags map[string]bool) string {
	var b strings.Builder
	n := 0
	for _, tag := range tags {
		if flagTag(tag) || tag == "signed" || tag == t.Attach || accountTags[tag] {
			continue
		}
		if n >= max {
			break
		}
		if t.ShowIcons && t.Icons[tag] != "" {
			// icons are config glyphs (1-2 cells): natural width, one separator - padding would leave gaps
			b.WriteString(tagStyle(tag).render(t.Icons[tag]))
			b.WriteByte(' ')
			n++
			continue
		}
		// names render in full - the tag slot never truncates a tag name
		b.WriteString(tagStyle(tag).render(core.SanitizeControls(tag)))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
}

// tagRunWidth is the visible-cell width of a tag glyph run: icons at
// their natural width, names in full, one separator space per pair.
// The per-page tag slot width is the widest run among the visible
// rows; rows pad to it, so the subject column aligns within the page.
// The skip list mirrors tagGlyphs.
func tagRunWidth(tags []string, max int, t config.UITags, accounts map[string]bool) int {
	n, cells := 0, 0
	for _, tag := range tags {
		if flagTag(tag) || tag == "signed" || tag == t.Attach || accounts[tag] {
			continue
		}
		if n >= max {
			break
		}
		w := runewidth.StringWidth(tag)
		if t.ShowIcons && t.Icons[tag] != "" {
			w = runewidth.StringWidth(t.Icons[tag])
		}
		if n > 0 {
			cells++ // separator space between glyphs
		}
		cells += w
		n++
	}
	return cells
}

// padTagRun pads a styled tag run to the page's slot width; the pad renders in the slot's style, so blank cells carry the row's background.
func padTagRun(run string, width int, tagStyle func(string) sgr) string {
	if width <= 0 {
		return ""
	}
	if w := runewidth.StringWidth(stripANSI(run)); w < width {
		return run + tagStyle("").render(strings.Repeat(" ", width-w))
	}
	return run
}

// truncCells truncates s to at most w terminal cells; padCellsRight pads it to exactly w (wcwidth, not runes).
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

// stripANSI removes SGR sequences (ESC [ ... m) from s; other control chars never reach rendered lines (SanitizeControls ran).
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

// truncateStyled truncates a styled string to at most w visible cells; SGR runs are zero-width and kept whole, so the line stays parseable.
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

// skipStyled drops the first x visible cells of a styled string; the
// last completed SGR open re-emits when the cut lands inside its run,
// so the tail keeps the style it would otherwise lose. A reset inside
// the skipped region closes the tracked open. A line fully scrolled
// past returns empty - never the head again (the flip a width-guard
// would cause). The mirror of truncateStyled for horizontal scroll.
func skipStyled(s string, x int) string {
	if x <= 0 {
		return s
	}
	var b strings.Builder
	cells := 0
	open := ""
	cur := ""
	inSeq := false
	skipping := true
	for _, r := range s {
		if inSeq {
			cur += string(r)
			if r == 'm' {
				if skipping {
					// track the last open while skipping: a reset closes it, so the cut re-emits only what is still in effect
					if cur == "\x1b[0m" || cur == "\x1b[m" {
						open = ""
					} else {
						open = cur
					}
				} else {
					// past the cut the sequences pass through whole: the tail keeps its own style transitions
					b.WriteString(cur)
				}
				cur, inSeq = "", false
			}
			continue
		}
		if r == '\x1b' {
			inSeq = true
			cur = "\x1b"
			continue
		}
		if skipping {
			cw := runewidth.RuneWidth(r)
			if cells >= x {
				skipping = false
				if open != "" {
					b.WriteString(open)
				}
				b.WriteRune(r)
				cells += cw
				continue
			}
			if cells+cw > x {
				skipping = false // the straddling wide char drops: a partial wide char cannot render
				if open != "" {
					b.WriteString(open)
				}
				continue
			}
			cells += cw
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// padRow wraps line in outer so the row style covers the full width
// (R11: rows never exceed width, alignment never shifts). Slot styles
// reset it mid-line, so the row style's opening re-applies after every
// reset; the line's slot colors survive inside.
func padRow(line string, w int, outer lipgloss.Style) string {
	return padRowSGR(line, w, sgrOf(outer))
}

// padRowSGR is the hot-path padRow: the row style's SGR fragments are precomputed, so the wrap is pure string ops (byte-identical to the Style-based form).
func padRowSGR(line string, w int, outer sgr) string {
	inner := line
	if width := runewidth.StringWidth(stripANSI(line)); width >= w {
		inner = truncateStyled(line, w)
	} else {
		inner += strings.Repeat(" ", w-width)
	}
	return outer.open + strings.ReplaceAll(inner, "\x1b[0m", "\x1b[0m"+outer.open) + "\x1b[0m"
}
