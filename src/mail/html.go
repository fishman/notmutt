// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The HTML flow renderer (docs/html-rendering-analysis.md): x/net/html
// parses (error-tolerant, fuzz-exercised - the trust boundary), the
// CSS cascade from lib/html computes styles, and the flow walk emits
// pager lines. Layout is CSS 2.1 block flow + inline runs + column-
// aligned tables; everything else (position, flex, media queries)
// drops. Images render as placeholders - the bytes travel with the
// line, the TUI decodes and paints only on the render-images key
// (privacy gate); remote srcs never fetch (tracking pixels stay dead).

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"notmutt/core"
	"notmutt/lib/html"
)

const (
	htmlWrapWidth = 120  // the open job has no TUI width; shr.el's 120-cap is the reference, the plain path wraps at the same fixed width
	maxHTMLLines  = 5000 // render budget: a hostile doc cannot balloon the thread
)

// RenderHTML renders an HTML mail body to pager lines. Returns nil for
// an empty result (the caller falls back to the raw text). width is the
// caller's terminal width: the wrap caps at htmlWrapWidth, so a wide
// terminal keeps the fixed-width layout and a narrow one reflows; 0
// means the cap.
func RenderHTML(body string, atts []Attachment, width int) []core.Line {
	lines, _ := renderHTML(body, atts, width, false)
	return lines
}

// RenderHTMLWithLinks renders the same content in link mode (the
// pager F key, easyjump-style): every link - an anchor href or a bare
// URL word - gets a "[N]" label inline in front of it, and the label
// order is the returned list (label N opens Links[N-1]). Both come
// from the walk's document order, so the labels and the list always
// agree.
func RenderHTMLWithLinks(body string, atts []Attachment, width int) ([]core.Line, []string) {
	return renderHTML(body, atts, width, true)
}

func renderHTML(body string, atts []Attachment, width int, labelLinks bool) ([]core.Line, []string) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, nil // x/net/html recovers from malformed input by spec; guard anyway
	}
	if width <= 0 || width > htmlWrapWidth {
		width = htmlWrapWidth
	}
	w := &htmlWalker{
		atts:       atts,
		rules:      collectStyleBlocks(doc),
		linesLeft:  maxHTMLLines,
		width:      width,
		labelLinks: labelLinks,
	}
	w.walk(doc, &html.Style{})
	w.flush()
	if w.truncated {
		w.lines = append(w.lines, core.Line{Text: "[content truncated]", Kind: core.LineBody})
	}
	if len(w.lines) == 0 {
		return nil, w.links
	}
	return w.lines, w.links
}

type htmlWalker struct {
	atts      []Attachment
	rules     []html.CSSRule
	lines     []core.Line
	linesLeft int
	truncated bool
	// defaultBG is the mail's declared background (the <html>/<body>
	// color, CSS or the bgcolor attribute): every rendered line carries
	// it, so the html view's region - pad and blank lines included -
	// respects the mail's background instead of the theme's.
	defaultBG string
	// inline state
	words []word
	cells int
	align string
	pre   bool
	width int
	// block spacing: one blank line between content blocks
	blankPending bool
	blockSeen    bool
	// list counters: one entry per open ol/ul (the top counts the
	// current item; a ul counts too but renders bullets)
	lists []int
	// pendingMark is an li marker that attaches to the item's first
	// content line at the next non-empty flush; it survives empty
	// flushes (a nested block's leading flush) so a marked item never
	// renders as a bare "1." line
	pendingMark *word
	// link mode (the F key): labelLinks arms the "[N]" labels and the
	// links list (label order = list order); the mode is per-render,
	// never persisted
	labelLinks bool
	links      []string
}

// word is one pending word with its computed style. label marks the
// F key's link marker ("[N]") - its run never merges with mail text.
type word struct {
	text  string
	st    *html.Style
	label bool
	img   *core.Image // inline image: the word is its placeholder
}

// cellLine is one line of a table cell's text: its words and the line's
// alignment (nested-table rows join the line; the row's align is the
// last aligned cell's). img is a resolved cell image - the line then
// carries no words and emits as a full-width image line.
type cellLine struct {
	words []word
	align string
	img   *core.Image
	bg    string // the cell's background: the image line's clear fill
}

var blockTags = map[string]bool{
	"address": true, "article": true, "aside": true, "blockquote": true,
	"body": true, "dd": true, "details": true, "dialog": true, "div": true,
	"dl": true, "dt": true, "fieldset": true, "figcaption": true,
	"figure": true, "footer": true, "form": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hr": true, "html": true, "legend": true, "li": true, "main": true,
	"nav": true, "ol": true, "p": true, "pre": true, "section": true,
	"table": true, "tbody": true, "td": true, "tfoot": true, "th": true,
	"thead": true, "tr": true, "ul": true,
}

var skipTags = map[string]bool{
	"base": true, "head": true, "iframe": true, "link": true, "meta": true,
	"noscript": true, "script": true, "style": true, "template": true,
	"title": true,
}

// walk flows the subtree in its computed style: block elements flush
// and recurse as block contexts, inline elements accumulate into the
// pending word buffer.
func (w *htmlWalker) walk(n *xhtml.Node, st *html.Style) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if w.linesLeft <= 0 {
			w.truncated = true
			return
		}
		switch c.Type {
		case xhtml.TextNode:
			w.addText(c.Data, st)
		case xhtml.ElementNode:
			cs := html.StyleOf(c, st, w.rules)
			tag := c.Data
			if cs.Display == "none" || skipTags[tag] {
				continue
			}
			if tag == "html" || tag == "body" {
				// the mail's declared background (CSS or the bgcolor
				// attribute; the body's own color overrides the html
				// default), or the light fallback: mail renders for a
				// light page - a mail without a background is
				// unreadable on a dark theme
				if cs.Bg != "" {
					w.defaultBG = cs.Bg
				} else if w.defaultBG == "" {
					w.defaultBG = "#ffffff"
				}
				// the default foreground must read on that background:
				// an unstyled mail inherits the contrast color instead
				// of the theme's light text on a dark terminal. The
				// contrast derives only when no author color set it -
				// FgSet does not inherit, so the body's background
				// recomputes what the html element derived against the
				// fallback (the dark-bg mail bug: dark on dark)
				if !cs.FgSet {
					cs.Fg = html.ContrastFG(w.defaultBG)
				}
			}
			switch {
			case tag == "img":
				w.image(c, cs)
			case tag == "br" && !cs.Pre:
				w.flush()
			case tag == "table" && isBlock(tag, cs):
				w.table(c, cs)
			case isBlock(tag, cs):
				w.flush()
				prev := w.align
				// only an explicit text-align at this node sets the line's
				// alignment; inherited text-align (button-internal
				// align="center" scaffolds) must not re-align a line
				if a := cs.Align; (a == "center" || a == "right") && cs.AlignSet {
					w.align = a
				}
				switch tag {
				case "ol", "ul":
					w.lists = append(w.lists, 0)
				case "li":
					if len(w.lists) > 0 {
						w.lists[len(w.lists)-1]++
						w.pendingMark = &word{text: html.ListMark(w.lists[len(w.lists)-1]), st: cs}
					}
				}
				w.walk(c, cs)
				if tag == "ol" || tag == "ul" {
					w.lists = w.lists[:len(w.lists)-1]
				}
				w.flush()
				w.align = prev
				if w.blockSeen {
					w.blankPending = true
					w.blockSeen = false
				}
			case tag == "a":
				// the F key's link label: the anchor's href gets its
				// "[N]" label inline before the anchor text (an
				// anchor without an href is plain text)
				if w.labelLinks {
					if href := html.Attr(c, "href"); href != "" {
						href = sanitize(href)
						w.links = append(w.links, href)
						w.addWord(fmt.Sprintf("[%d]", len(w.links)), cs, true)
					}
				}
				w.walk(c, cs)
			default:
				w.walk(c, cs)
			}
		}
	}
}

func isBlock(tag string, cs *html.Style) bool {
	if blockTags[tag] {
		return true
	}
	switch cs.Display {
	case "block", "flex", "grid", "list-item", "table", "table-cell",
		"table-footer-group", "table-header-group", "table-row",
		"table-row-group":
		return true
	}
	return false
}

// addText ingests a text node: pre content preserves spaces and line
// breaks (tab-expanded, sanitized per line), inline content collapses
// to space-separated words.
func (w *htmlWalker) addText(txt string, st *html.Style) {
	if w.linesLeft <= 0 {
		w.truncated = true
		return
	}
	if w.pre {
		for _, line := range strings.Split(strings.TrimSuffix(txt, "\n"), "\n") {
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				w.flush()
				continue
			}
			w.words = append(w.words, word{text: sanitize(expandTabs(line)), st: st})
			w.flush()
		}
		return
	}
	for _, f := range strings.Fields(txt) {
		if w.labelLinks {
			// a bare URL word (no anchor) is a link too: label it and
			// record the trimmed target
			if ls := html.Links(f, true); len(ls) > 0 {
				w.links = append(w.links, ls[0])
				w.addWord(fmt.Sprintf("[%d]", len(w.links)), st, true)
			}
		}
		w.addWord(f, st, false)
	}
}

func (w *htmlWalker) addWord(text string, st *html.Style, label bool) {
	if w.linesLeft <= 0 {
		w.truncated = true
		return
	}
	text = sanitize(text) // F1: DOM text is raw - ESC/C0 never reach the pager
	tw := html.TextWidth(text)
	// a single word wider than the line hard-splits into chunks
	for tw > w.width {
		chunk := html.TakeCells(text, w.width)
		if w.cells > 0 {
			w.flush()
		}
		w.words = append(w.words, word{text: chunk, st: st, label: label})
		w.cells = html.TextWidth(chunk)
		text = text[len(chunk):]
		tw = html.TextWidth(text)
		if w.cells > 0 {
			w.flush()
		}
	}
	if w.cells > 0 && w.cells+1+tw > w.width {
		w.flush()
	}
	if w.cells > 0 {
		w.words = append(w.words, word{text: " ", st: w.words[len(w.words)-1].st})
		w.cells++
	}
	w.words = append(w.words, word{text: text, st: st, label: label})
	w.cells += tw
}

// flush wraps the pending words into a line: trailing space drops, the
// block context's alignment pads to the wrap width, and a pending
// block-boundary blank leads the line.
func (w *htmlWalker) flush() {
	if w.linesLeft <= 0 {
		return
	}
	if len(w.words) == 0 {
		return // a pending mark rides along until content exists
	}
	if w.pendingMark != nil {
		w.words = append([]word{*w.pendingMark}, w.words...)
		w.pendingMark = nil
	}
	if w.words[len(w.words)-1].text == " " {
		w.words = w.words[:len(w.words)-1]
		w.cells--
	}
	var runs []core.Run
	switch w.align {
	case "center":
		if pad := (w.width - w.cells) / 2; pad > 0 {
			runs = append(runs, core.Run{Text: strings.Repeat(" ", pad)})
		}
	case "right":
		if pad := w.width - w.cells; pad > 0 {
			runs = append(runs, core.Run{Text: strings.Repeat(" ", pad)})
		}
	}
	runs = append(runs, runWords(w.words)...)
	w.words = nil
	w.cells = 0
	w.emitLine(runs)
}

// emitLine appends a fully-built line, consuming a pending blank and
// tracking the block content and the line budget.
func (w *htmlWalker) emitLine(runs []core.Run) {
	if w.linesLeft <= 0 {
		w.truncated = true
		return
	}
	if w.blankPending {
		w.lines = append(w.lines, core.Line{Bg: w.defaultBG})
		w.blankPending = false
	}
	line := core.Line{Kind: core.LineBody, Runs: runs, Bg: w.defaultBG}
	line.Text = joinRunText(runs)
	w.lines = append(w.lines, line)
	w.linesLeft--
	w.blockSeen = true
}

// image emits an <img> as its own line: the placeholder text, or the
// image line when the src resolves - cid: attachments and data: URIs
// carry their bytes, http(s) srcs carry their URL (the remote images
// mode fetches them on the render-images key; never fetched here).
func (w *htmlWalker) image(c *xhtml.Node, st *html.Style) {
	w.flush()
	img := resolveImage(c, w.atts, w.width)
	if img == nil {
		w.addWord("[image]", st, false)
		return
	}
	w.emitLine(nil)
	i := len(w.lines) - 1
	w.lines[i].Image = img
	w.lines[i].Text = img.Alt
}

// resolveImage resolves an <img> src to its bytes + alt text: the
// cid: attachment or data: URI bytes, or a remote http(s) image as a
// URL-only placeholder (Data stays empty - the fetch is the remote
// mode's keypress-gated step).
func resolveImage(c *xhtml.Node, atts []Attachment, layoutCells int) *core.Image {
	src := html.Attr(c, "src")
	var data []byte
	var url string
	switch {
	case strings.HasPrefix(src, "cid:"):
		id := strings.Trim(strings.TrimSpace(src[4:]), "<>")
		for _, a := range atts {
			if strings.EqualFold(strings.Trim(a.ContentID, "<>"), id) {
				data = a.Data
				break
			}
		}
	case strings.HasPrefix(src, "data:image/") && strings.Contains(src, ";base64,"):
		data, _ = base64.StdEncoding.DecodeString(src[strings.IndexByte(src, ',')+1:])
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		url = src
	}
	if len(data) == 0 && url == "" {
		return nil
	}
	alt := html.Attr(c, "alt")
	if alt == "" {
		alt = "[image]"
	}
	img := &core.Image{Data: data, URL: url, Alt: sanitize(alt)}
	img.DispW, img.DispH = imgSize(c, layoutCells)
	return img
}

// imgSize reads an <img>'s declared display size: the width/height
// attributes or the style declarations. px numbers pass through;
// percentages resolve against the layout width (the mail's section
// sizing - a chart fills the space its layout reserves). 0 =
// unspecified, the decode falls back to a window fit.
func imgSize(c *xhtml.Node, layoutCells int) (w, h int) {
	decls := html.ParseDecls(html.Attr(c, "style"))
	read := func(name string) int {
		raw := decls[name]
		if raw == "" {
			raw = html.Attr(c, name)
		}
		if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(raw, "px"))); err == nil {
			return n
		}
		if pct := strings.TrimSuffix(raw, "%"); pct != raw {
			if n, err := strconv.Atoi(strings.TrimSpace(pct)); err == nil {
				return n * layoutCells * 10 / 100
			}
		}
		return 0
	}
	return read("width"), read("height")
}

// table renders a table as column-aligned rows (w3m's approach: pad
// each column to its widest cell, wrapped at a per-column cap). Cell
// content is extracted as word-lines, so styles survive into the rows.
func (w *htmlWalker) table(t *xhtml.Node, st *html.Style) {
	rows := cellRows(t, st, w.rules, &w.links, w.labelLinks, w.defaultBG, w.width)
	if len(rows) == 0 {
		return
	}
	ncols := 0
	for _, r := range rows {
		if len(r) > ncols {
			ncols = len(r)
		}
	}
	// the wrap cap comes from the CONTENT columns: empty spacer cells
	// (the layout tables of the Outlook/Substack era) must not halve or
	// third the text width
	content := 0
	for ci := 0; ci < ncols; ci++ {
		has := false
		for _, r := range rows {
			if ci >= len(r) {
				continue
			}
			for _, ln := range r[ci] {
				if len(ln.words) > 0 {
					has = true
					break
				}
			}
			if has {
				break
			}
		}
		if has {
			content++
		}
	}
	if content == 0 {
		return
	}
	colCap := (w.width - 2*(ncols-1)) / content
	if colCap < 8 {
		colCap = 8
	}
	widths := make([]int, ncols)
	for _, r := range rows {
		for ci := 0; ci < ncols; ci++ {
			if ci >= len(r) {
				continue
			}
			var wrapped []cellLine
			for _, ln := range r[ci] {
				if ln.img != nil {
					wrapped = append(wrapped, ln) // an image line never wraps
					continue
				}
				for _, wl := range wrapWords(ln.words, colCap) {
					wrapped = append(wrapped, cellLine{words: wl, align: ln.align})
				}
				if len(ln.words) == 0 {
					wrapped = append(wrapped, cellLine{}) // an inter-block blank stays blank
				}
			}
			r[ci] = wrapped
			if cw := cellWidth(r[ci]); cw > widths[ci] {
				widths[ci] = cw
			}
		}
	}
	w.flush()
	for _, r := range rows {
		rowLines := 0
		for ci := 0; ci < ncols; ci++ {
			// a ragged table: shorter rows have fewer cells - the
			// widths pass guards the same bound, the emit pass must too
			if ci < len(r) && len(r[ci]) > rowLines {
				rowLines = len(r[ci])
			}
		}
		for li := 0; li < rowLines; li++ {
			// a cell image emits as its own full-width line (the line
			// model holds one image); the text join below pads the
			// image cells' columns blank
			for ci := 0; ci < ncols; ci++ {
				if ci < len(r) && li < len(r[ci]) && r[ci][li].img != nil {
					before := len(w.lines)
					w.emitLine(nil)
					if len(w.lines) == before {
						continue // line budget exhausted: the image drops with the truncated tail
					}
					i := len(w.lines) - 1
					w.lines[i].Image = r[ci][li].img
					w.lines[i].Text = r[ci][li].img.Alt
					if bg := r[ci][li].bg; bg != "" {
						w.lines[i].Bg = bg
					}
				}
			}
			var runs []core.Run
			var imgs []core.ImagePos
			rowX := 0
			for ci := 0; ci < ncols; ci++ {
				// a ragged row has no cell at ci: it renders as an empty
				// column span, the same shape as an exhausted cell
				var lines []cellLine
				if ci < len(r) {
					lines = r[ci]
				}
				if li < len(lines) {
					cell := lines[li]
					pad := widths[ci] - html.TextWidth(joinWordText(cell.words))
					pre, post := 0, pad
					if cell.align == "right" {
						pre, post = pad, 0
					} else if cell.align == "center" {
						pre, post = pad/2, pad-pad/2
					}
					if pre > 0 {
						runs = append(runs, core.Run{Text: strings.Repeat(" ", pre)})
						rowX += pre
					}
					for _, rn := range runWords(cell.words) {
						if rn.Image != nil {
							imgs = append(imgs, core.ImagePos{Image: rn.Image, X: rowX})
						}
						rowX += html.TextWidth(rn.Text)
						runs = append(runs, rn)
					}
					if post > 0 {
						runs = append(runs, core.Run{Text: strings.Repeat(" ", post)})
						rowX += post
					}
				} else {
					runs = append(runs, core.Run{Text: strings.Repeat(" ", widths[ci])})
					rowX += widths[ci]
				}
				if ci < ncols-1 {
					runs = append(runs, core.Run{Text: "  "})
					rowX += 2
				}
			}
			before := len(w.lines)
			w.emitLine(runs)
			if len(w.lines) == before {
				continue // line budget exhausted: the inline images drop with the truncated tail
			}
			if len(imgs) > 0 {
				i := len(w.lines) - 1
				w.lines[i].Imgs = imgs
			}
		}
	}
	w.flush()
	if w.blockSeen {
		w.blankPending = true
		w.blockSeen = false
	}
}

// cellRows collects the table's tr rows, each cell a list of word-
// lines (block boundaries and <br> inside a cell start a new line).
// The HTML5 parser inserts an implicit tbody between the table and
// its rows, so the row groups descend into it.
func cellRows(t *xhtml.Node, st *html.Style, rules []html.CSSRule, links *[]string, labelLinks bool, defaultBG string, layoutCells int) [][][]cellLine {
	var rows [][][]cellLine
	for r := t.FirstChild; r != nil; r = r.NextSibling {
		if r.Type != xhtml.ElementNode {
			continue
		}
		if r.Data == "tbody" || r.Data == "thead" || r.Data == "tfoot" {
			rows = append(rows, cellRows(r, st, rules, links, labelLinks, defaultBG, layoutCells)...)
			continue
		}
		if r.Data != "tr" {
			continue
		}
		var cells [][]cellLine
		for c := r.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != xhtml.ElementNode || (c.Data != "td" && c.Data != "th") {
				continue
			}
			cells = append(cells, collectCell(c, html.StyleOf(c, st, rules), rules, links, labelLinks, defaultBG, layoutCells))
		}
		if len(cells) > 0 {
			rows = append(rows, cells)
		}
	}
	return rows
}

// collectCell extracts a cell's text flow as word-lines: the same
// block/inline walk as htmlWalker, but text-only. A nested table's
// rows flatten into the current line - the Outlook/Substack era wraps
// every layout element in presentation tables, and dropping them (the
// old skip) emptied the article. Only an explicitly right-aligned
// cell starts a new line under that alignment (the READ-IN-APP
// column); inherited text-align - the align="center" button scaffolds
// inside the row - never re-aligns a line. Link mode (labelLinks)
// labels anchors and bare URLs inside cells like the main walk - the
// layout-table era wraps every link in a td.
func collectCell(n *xhtml.Node, st *html.Style, rules []html.CSSRule, links *[]string, labelLinks bool, defaultBG string, layoutCells int) []cellLine {
	var out []cellLine
	var cur []word
	align := ""
	// lineAlign is a one-shot line alignment set by an explicitly
	// right-aligned cell split: it applies to the next flushed line and
	// then dies. It never rides the align prev/restore chains, so a
	// split cannot leak into the enclosing flow (the buttons row) or be
	// resurrected by a nested table's restore (READ IN APP).
	lineAlign := ""
	var blankPending, blockSeen bool
	var lists []int
	var pendingMark *word
	flush := func() {
		if len(cur) == 0 && pendingMark == nil {
			return
		}
		if len(cur) > 0 {
			if pendingMark != nil {
				cur = append([]word{*pendingMark}, cur...)
				pendingMark = nil
			}
			if blankPending {
				out = append(out, cellLine{})
				blankPending = false
			}
			a := align
			if lineAlign != "" {
				a = lineAlign
				lineAlign = ""
			}
			out = append(out, cellLine{words: cur, align: a})
			cur = nil
			blockSeen = true
		}
	}
	inRow := false
	var walk func(n *xhtml.Node, st *html.Style)
	var walkRow func(n *xhtml.Node, st *html.Style)
	walk = func(n *xhtml.Node, st *html.Style) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch c.Type {
			case xhtml.TextNode:
				for _, f := range strings.Fields(c.Data) {
					if labelLinks {
						if ls := html.Links(f, true); len(ls) > 0 {
							*links = append(*links, ls[0])
							cur = append(cur, word{text: fmt.Sprintf("[%d]", len(*links)), st: st, label: true})
						}
					}
					cur = append(cur, word{text: sanitize(f), st: st})
				}
			case xhtml.ElementNode:
				cs := html.StyleOf(c, st, rules)
				tag := c.Data
				if cs.Display == "none" || skipTags[tag] {
					continue
				}
				switch {
				case tag == "br" && !cs.Pre:
					flush()
				case tag == "img":
					// a resolved block image becomes its own full-width
					// line (the column join can hold one image per
					// line); an inline image (the icon rows) joins its
					// line's words - the placeholder run rides the
					// word, the row keeps the mail's one-line layout
					if img := resolveImage(c, nil, layoutCells); img != nil {
						if cs.Display != "block" {
							cur = append(cur, word{text: img.Alt, st: cs, img: img})
							break
						}
						flush()
						// the block's clear fill: the cell's declared bg or
						// the mail's page background - an image on a white
						// page must clear to white, never to the theme
						bg := cs.Bg
						if bg == "" {
							bg = defaultBG
						}
						out = append(out, cellLine{img: img, bg: bg})
						break
					}
					if a := html.Attr(c, "alt"); a != "" {
						cur = append(cur, word{text: sanitize(a), st: cs})
					} else {
						cur = append(cur, word{text: "[image]", st: cs})
					}
				case tag == "table":
					if !inRow {
						flush()
					}
					prev := align
					walkRow(c, cs)
					if !inRow {
						flush()
					}
					align = prev
				case isBlock(tag, cs):
					flush()
					prev := align
					// only an explicit text-align at this node sets the
					// line's alignment; inherited text-align must not
					// re-align a line
					if a := cs.Align; (a == "center" || a == "right") && cs.AlignSet {
						align = a
						lineAlign = "" // an explicit block alignment wins over a pending cell split
					}
					switch tag {
					case "ol", "ul":
						lists = append(lists, 0)
					case "li":
						if len(lists) > 0 {
							lists[len(lists)-1]++
							pendingMark = &word{text: html.ListMark(lists[len(lists)-1]), st: cs}
						}
					}
					walk(c, cs)
					if tag == "ol" || tag == "ul" {
						lists = lists[:len(lists)-1]
					}
					flush()
					align = prev
					if blockSeen {
						blankPending = true
						blockSeen = false
					}
				case tag == "a":
					// the F key's link label in a cell: same rule as the
					// main walk - the anchor's href gets its "[N]" label
					// inline; an anchor without an href is plain text
					if labelLinks {
						if href := html.Attr(c, "href"); href != "" {
							href = sanitize(href)
							*links = append(*links, href)
							cur = append(cur, word{text: fmt.Sprintf("[%d]", len(*links)), st: cs, label: true})
						}
					}
					walk(c, cs)
				default:
					walk(c, cs)
				}
			}
		}
	}
	walkRow = func(n *xhtml.Node, st *html.Style) {
		prevRow := inRow
		inRow = true
		defer func() { inRow = prevRow }()
		for r := n.FirstChild; r != nil; r = r.NextSibling {
			if r.Type != xhtml.ElementNode {
				continue
			}
			if r.Data == "tbody" || r.Data == "thead" || r.Data == "tfoot" {
				walkRow(r, st)
				continue
			}
			if r.Data != "tr" {
				continue
			}
			for c := r.FirstChild; c != nil; c = c.NextSibling {
				if c.Type != xhtml.ElementNode || (c.Data != "td" && c.Data != "th") {
					continue
				}
				tcs := html.StyleOf(c, st, rules)
				if tcs.Align == "right" && tcs.AlignSet {
					if lineAlign != "right" {
						flush()
					}
					lineAlign = "right"
				}
				walk(c, tcs)
			}
		}
	}
	walk(n, st)
	flush()
	return out
}

// wrapWords wraps one word-line at the column cap; a word wider than
// the cap hard-splits.
func wrapWords(words []word, cap int) [][]word {
	var out [][]word
	var cur []word
	cells := 0
	for _, wd := range words {
		tw := html.TextWidth(wd.text)
		for tw > cap {
			chunk := html.TakeCells(wd.text, cap)
			out = append(out, cur)
			cur = []word{{text: chunk, st: wd.st}}
			cells = html.TextWidth(chunk)
			out = append(out, cur)
			cur, cells = nil, 0
			wd.text = wd.text[len(chunk):]
			tw = html.TextWidth(wd.text)
		}
		if cells > 0 && cells+1+tw > cap {
			out = append(out, cur)
			cur, cells = nil, 0
		}
		if cells > 0 {
			cur = append(cur, word{text: " ", st: wd.st})
			cells++
		}
		cur = append(cur, wd)
		cells += tw
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// html.TakeCells cuts the longest true byte prefix of s that fits in cap
func sanitize(s string) string {
	return core.SanitizeControls(s)
}

// runWords merges a word-line into runs: adjacent words with identical
// styles merge (the inter-word space inherits the preceding word's
// style); style changes and the F key's label markers split runs (the
// marker keeps its own run - the TUI's highlight anchor).
func runWords(words []word) []core.Run {
	var runs []core.Run
	for _, wd := range words {
		r := runFor(wd.st)
		r.Label = wd.label
		r.Image = wd.img
		r.Text = wd.text
		if len(runs) > 0 && r == runs[len(runs)-1] {
			runs[len(runs)-1].Text += wd.text
			continue
		}
		runs = append(runs, r)
	}
	return runs
}

// runFor maps a computed style to its run representation: "" fg/bg and
// zero attrs for the unstyled base (run equality gates run merging).
func runFor(st *html.Style) core.Run {
	var r core.Run
	if st == nil {
		return r
	}
	r.Fg, r.Bg = st.Fg, st.Bg
	if st.Bold {
		r.Attrs |= core.AttrBold
	}
	if st.Italic {
		r.Attrs |= core.AttrItalic
	}
	if st.Underline {
		r.Attrs |= core.AttrUnderline
	}
	return r
}

// joinRunText is the line's plain text (the Lua render contract reads
// Line.Text; runs are the styled overlay).
func joinRunText(runs []core.Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

func joinWordText(words []word) string {
	var b strings.Builder
	for _, wd := range words {
		b.WriteString(wd.text)
	}
	return b.String()
}

func cellWidth(lines []cellLine) int {
	max := 0
	for _, l := range lines {
		if w := html.TextWidth(joinWordText(l.words)); w > max {
			max = w
		}
	}
	return max
}

// collectStyleBlocks gathers the <style> element texts of the document
// into one cascade.
func collectStyleBlocks(doc *xhtml.Node) []html.CSSRule {
	var rules []html.CSSRule
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode {
				if c.Data == "style" && c.FirstChild != nil {
					rules = append(rules, html.ParseStyleSheet(c.FirstChild.Data)...)
				}
				walk(c)
			}
		}
	}
	walk(doc)
	return rules
}
