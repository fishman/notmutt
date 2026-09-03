# HTML layout engine: tables, auto layout, spans, nesting (plan 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the table-as-leaf stub with a real CSS table engine in `lib/html`: a table-family box tree that consumes x/net/html's already-normalized table grammar (no repair - the parser's implied tbody/foster-parenting do it; see the conformance rule below), weasyprint-shaped auto layout (per-column min/max measured once, used widths by distribution, shrink-to-fit capped at the content width), the UA 2px border-spacing gutter and 1px cell padding, a real colspan/rowspan grid, and nested tables laying out as real block tables inside their cell - all strictly additive with zero behavior change to the running mail walker.

**Architecture:** Weasyprint-shaped two-stage renderer per the spec (`docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`). Plan 4 is the "Stage 1: tables" section. Tables stop being opaque leaves: `buildElement` builds the table-family subtree (table/row-group/row/cell/caption boxes with a grid slot tag), the builder drops whitespace-only text between furniture and passes x/net/html's well-formed rows/groups/cells through unchanged (table roles resolve only for real table-family tags), then a new `table.go` measures each cell's content once into per-column min/max, distributes the used width, and emits the grid into the existing `[]Row` stream by adding a `Cells` fragment row (one stream row per visual line, fragments of several cells side by side at their own absolute X). Nothing in `mail/` is touched and the walker keeps rendering unchanged.

**Tech Stack:** Go, x/net/html, cascadia (existing deps). Test cmd: `cd src && go test -count=1 ./lib/html/`. Full gate: `go test -count=1 -tags "lua mcp" ./...`, `go vet ./lib/html/`, `gofmt -l lib/html/`.

**Spec refs:** Sections "box model and build" (blockification, anonymous table repair), "UA floor" (th bold), "tables" (auto layout, shrink-to-fit cap, colspan/rowspan grid, th bold not centered, vertical-align not modeled, nested tables). WeasyPrint refs: `layout/table.py` (`auto_table_layout`, `distribute_excess_width`), `layout/preferred.py` (`table_and_columns_preferred_widths`, `table_cell_min_max_content_width`), `formatting_structure/build.py` (anonymous-table rules), `css/html5_ua.css` (`table { border-spacing: 2px; border-collapse: separate }`, `td, th { padding: 1px }`).

**Threat model (locked):** a malicious sender can ship a megabyte HTML part. Every loop that scales with input must be O(n); any super-linear path is a content-reachable DoS on the read surface. Tables add new hazards that must stay single-pass: (1) each cell's text is measured exactly once for min/max content width (one atomize pass for measurement, one for layout - a bounded constant 2, never one per column or per row); (2) column count derives from the widest row by cell count, and a colspan is clamped to the row's remaining columns - an attacker cannot mint a million empty spacer columns from a 20-character `colspan=1000000` attribute; (3) span-width excess distribution touches only the columns the cell spans (bounded by the clamped colspan); (4) rowspan occupancy costs one check per column per populated row, and a rowspan blanks only rows that actually carry other cells (fully-occupied rows emit nothing), so blank placements are bounded by the cell count that mentions them; (5) nested tables are bounded by the node count - each table's min/max content extents are memoized on its box the first time any measure pass reaches it (content extents are width-independent, so the cache is always valid, across widths and across LayoutBlock calls over the same tree). A deep chain of nested single-cell tables therefore costs ONE bottom-up measure pass over the whole tree, never one full subtree re-measure per ancestor (which would be O(depth^2) atomize/grid work - a content-reachable DoS in exactly this hostile shape). No cell text is ever re-measured in a loop, no column array is rescanned per row, and no margin/seam list is rescanned.

**Conformance rule (locked 2026-09-03, supersedes decision 28 and any Task 1 text below):** things x/net/html already handles are never reimplemented in `lib/html`. x/net/html (vendored, the only parser, every input crosses `xhtml.Parse`) performs HTML5 table tree construction itself - it inserts an implied `tbody` for stray rows/cells, foster-parents non-whitespace content out of a table/row, and keeps only whitespace text between furniture. So a `RoleTable` box's children arrive well-formed (`table` > `row-group` > `row` > `cell`) and the box builder consumes that grammar unchanged; it must not re-express it. The builder drops whitespace-only text between furniture (x/net/html keeps it; the renderer must not give it height) and passes children through. What survives a parse is author `display:table-*` on NON-table elements - x/net/html keys off element names and never reads computed `display`, so that is not its job, but it is also not a mail pattern: table roles resolve only for real table-family tags, and a `display:table` div or `display:table-cell` span renders as a block (the mail-safe default the walker already uses). Task 1a conforms the committed Task 1 to this rule; Tasks 2-5 text below reads as amended by it.

---

## File structure

- Modify: `src/lib/html/box.go` - `Box.Tbl` grid-slot field; `buildElement` builds the table family instead of returning RoleTable leaves; `fillFlowChildren` extracted for shared block/cell content building; `tableKids` drops whitespace-only text between furniture (table roles resolve only for real table-family tags - Task 1a; no `fixTable`/`anonTableBox`).
- Modify: `src/lib/html/html.go` - `Style.BoldSet` (explicit font-weight source flag); `apply`'s `font-weight` branch sets it (gates the UA th bold to stage-1 only).
- Modify: `src/lib/html/block.go` - `Row.Cells` fragment field; `flow` routes `RoleTable` grid tables to `tableRows`.
- Create: `src/lib/html/table.go` - grid construction, content measurement, width distribution, row emission.
- Test: `src/lib/html/table_test.go` (new; build repair, width, span, nested tests), edit `box_test.go` (`TestTableIsLeaf` -> `TestTableExpandsToGridTree`), edit `block_test.go` helper `rowsText` to recurse into `Cells`.

## Decisions locked for this plan

- **A table is a real box tree, not a leaf.** `RoleTable` boxes carry `Tbl` naming their grid slot (`table`, `row-group`, `row`, `cell`, `caption`, `column-group`, `column`). Only `table`, `row-group`, `row`, `cell`, `caption` get flow content; `column-group`/`column` are empty (column styling is out of scope). `buildElement` builds cell/caption content like a block container (blockified, mixed content split into anonymous runs), so cell content structure is clean and layout never re-derives it.
- **The box builder consumes x/net/html's table grammar; it performs no table-grammar repair.** x/net/html's HTML5 tree construction (the vendored, only parser) already normalizes tables: an implied `tbody` wraps stray rows/cells and non-whitespace content is foster-parented out of a table/row, so a `RoleTable` box's children arrive well-formed and never need a stray repaired. The builder's only job past that is dropping whitespace-only text between furniture (x/net/html keeps it; the renderer must not give it height) and building each furniture element's content. Author `display:table-*` on a non-table element survives the parse but is out of scope (not a mail pattern): table roles resolve only for real table-family tags, and a remapped div/span renders as a block. This mirrors the library's input contract - an HTML5-parsed DOM, like cascadia's x/net/html-node contract; a normalizer for non-HTML5 DOM sources is a future opt-in, not a stage-1 feature. Task 1a implements this; the original anonymous-repair decision text is superseded.
- **th bold is stage-1-only, gated on a sticky `BoldSet`.** Weasyprint's `th { font-weight: bold }` cannot go into the shared `uaDefaults` emphasis table: the running mail walker computes styles through `StyleOf` and would start rendering `<th>` bold too (drift). Instead the box builder promotes `st.Bold = true` on a `th` cell when no font-weight is declared anywhere on the inheritance chain (`!st.BoldSet`), before building the cell's children so text leaves inherit it. `BoldSet` is set by `apply`'s font-weight branch, is not reset by `StyleOf` (it is sticky: an author `font-weight` on any ancestor suppresses UA th bold, matching CSS inheritance authority). th is not centered (the weasyprint html5_ua.css used here does not center th, and the spec says not centered).
- **Auto layout, weasyprint clamp.** Per-column min-content and max-content are measured from all rows (base single-span cells first, then colspan excess distributed onto its spanned columns). Used table width U: max-content when `available >= tableMax`, the available width when `tableMin < available < tableMax` (fill), min-content when `available <= tableMin`; equivalently `U = min(max(tableMin, available), tableMax)`. Under normalize, `available < tableMin` caps U at the container width and columns shrink below min-content (cell text char-breaks) - the spec's "tables cap at the content box width, never overflow". Column box widths include each cell's horizontal padding; cell content width = column box width minus 2px.
- **The 2px gutter and 1px padding are UA constants, probe-measured.** `border-spacing: 2px` (between columns, and at the table's left/right edges), `td/th { padding: 1px }` each side. No border-collapse is modeled (the cascade does not parse it, so separate is always in force). The single 2px gutter replaces the old shared-cap model's per-cell cap entirely.
- **A grid row emits one stream row per visual line; a cell line is a `Row.Cells` fragment.** Horizontal juxtaposition cannot live in the flat vertical `Row` model, so a table's emitted row carries `Cells []Row` - each fragment a cell's content line (or a nested table's grid row) positioned at an absolute px X. `Line`/`HR`/`Markers` on the fragment render normally. `Cells` is mutually exclusive with `Line` on the owning row.
- **Vertical rhythm inside a table is not modeled; grid rows are contiguous.** Weasyprint's 2px vertical border-spacing and cell vertical padding sit below one terminal line and quantize to zero blank rows at stage 2 (round(2/16) = 0), identical whether modeled or not - and a text probe cannot measure them cleanly (font leading swamps 2px). Grid rows therefore emit contiguously (Gap 0 between them); only the table's first emitted line consumes the ambient seam. Cell vertical-align is not modeled (spec: no sub-row in a terminal; the backend's concern).
- **Intra-cell block rhythm is flattened in a grid row.** A cell's content lines (a wrapped paragraph, a nested table's rows) are top-aligned fragments on successive visual lines of the grid row. Vertical margins a cell's own block children would introduce are dropped inside table rows (divergence, recorded in BUGS.org): a table row is one horizontal strip, so a 16px paragraph gap inside one cell cannot push only that cell down. The overwhelmingly common mail table cell is a single unwrapped line, where the model is exact.
- **Captions are built but not laid out** (deferred; recorded in BUGS.org). The repair keeps a caption's content tree so a later plan can render it above the table; this plan's grid walks only row-group children.
- **`columns`/`col`/`colgroup` are parsed and stored as empty boxes** (stray-column content is dropped); column `<col width>` styling is out of scope.
- **Additive only.** `mail/html.go` (the walker) is untouched; the pinned `html_*_test.go` suite must stay green unweakened at the Task 5 gate. The one existing table test, `TestTableIsLeaf`, describes the pre-plan stub and is updated to `TestTableExpandsToGridTree`; it is not a pinned regression. `rowsText` in `block_test.go` is a helper, not a regression test, and gains `Cells` recursion.

---

### Task 1: Build-side table family + anonymous repair + th bold

**Files:**
- Modify: `src/lib/html/html.go` - `Style.BoldSet`, the `font-weight` branch.
- Modify: `src/lib/html/box.go` - `Role` comment, `Box.Tbl`, `fillFlowChildren` extraction, `buildElement` table-family switch, `tableKids`/`fixTable`.
- Test: `src/lib/html/table_test.go` (new, build tests), `src/lib/html/box_test.go` (`TestTableIsLeaf` -> `TestTableExpandsToGridTree`).

This task changes nothing the walker reads (the walker never calls `buildElement`), and it changes no layout: `flow` has no table case yet, so no `LayoutBlock` test lays out a table until Task 2.

- [ ] **Step 1: Update the stale leaf test to the expanded-grid contract (fails)**

`src/lib/html/box_test.go`, replace `TestTableIsLeaf`:

```go
func TestTableExpandsToGridTree(t *testing.T) {
	bs := buildBody(`<table><tr><td>a</td></tr></table>`)
	tbl := bs[0]
	if tbl.Role != RoleTable || tbl.Tbl != "table" {
		t.Fatalf("table role/tbl = %v/%q, want RoleTable/table", tbl.Role, tbl.Tbl)
	}
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" || tbl.Children[0].Tag != "" {
		t.Fatalf("table child = %+v, want an anonymous row-group wrapping the tr", tbl.Children)
	}
	row := tbl.Children[0].Children[0]
	if len(tbl.Children[0].Children) != 1 || row.Tbl != "row" || row.Tag != "tr" {
		t.Fatalf("row-group child = %+v, want the tr as a row", tbl.Children[0].Children)
	}
	cell := row.Children[0]
	if len(row.Children) != 1 || cell.Tbl != "cell" || cell.Tag != "td" {
		t.Fatalf("row child = %+v, want a td cell", row.Children)
	}
	if len(cell.Children) != 1 || cell.Children[0].Role != RoleText || cell.Children[0].Text != "a" {
		t.Fatalf("cell content = %+v, want one RoleText 'a'", cell.Children)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd src && go test -count=1 -run TestTableExpandsToGridTree ./lib/html/`
Expected: FAIL - `tbl.Tbl` does not exist / table has no children.

- [ ] **Step 3: Add `BoldSet` to `Style` and set it in `apply`**

`src/lib/html/html.go`, in the `Style` struct after `Bold`:

```go
	Bold     bool
	BoldSet  bool // an explicit font-weight source at this node, sticky down the tree (UA th-bold gate)
```

In `apply`, the `font-weight` branch gains the flag set (first line):

```go
	if v, ok := decls["font-weight"]; ok {
		s.BoldSet = true
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			s.Bold = n >= 600
		} else {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "bold", "bolder":
				s.Bold = true
			case "normal", "lighter":
				s.Bold = false
			}
		}
	}
```

`StyleOf` must not reset `BoldSet` (it is sticky by design, unlike the `*Set` geometry flags that are reset because they mean "this node declared").

- [ ] **Step 4: Add `Box.Tbl` and update the `Role` comment**

`src/lib/html/box.go`:

```go
	RoleTable              // table grid (leaf in this plan)
```
becomes
```go
	RoleTable              // table-family box (table/row-group/row/cell/caption/column)
```

and in the `Box` struct, after `WS`:

```go
	WS       WS
	Tbl      string // table grid slot: table|row-group|row|cell|caption|column-group|column ("" outside tables)
	Marker   string // list-item marker type: disc|circle|square|decimal
```

- [ ] **Step 5: Extract `fillFlowChildren` and route the table family in `buildElement`**

`src/lib/html/box.go`. The current gather/blockify tail of `buildElement` (the loop over `n.FirstChild` plus the role promotion and split) becomes a shared helper used by block containers AND cells/captions:

```go
// fillFlowChildren gathers an element's in-flow children into b: text
// leaves share the parent's style pointer; child elements build
// recursively; a list item under its list gets its marker. Mixed
// block/inline content is split into anonymous runs (blockification), so
// a block or cell box holds uniformly block-level or uniformly
// inline-level children. Geometry (uaMargins) was layered on st before
// the children built, so text leaves inherit it by pointer sharing.
func fillFlowChildren(b *Box, n *xhtml.Node, st *Style, rules []CSSRule, listDepth int) {
	nextDepth := listDepth
	if b.Tag == "ul" || b.Tag == "ol" {
		nextDepth++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			b.Children = append(b.Children, &Box{Role: RoleText, St: st, WS: st.WS, Text: c.Data})
		case xhtml.ElementNode:
			child := buildElement(c, st, rules, nextDepth)
			if child == nil {
				continue
			}
			if (b.Tag == "ul" || b.Tag == "ol") && isListItem(child) {
				child.Marker = listMarker(b.Tag, nextDepth)
			}
			b.Children = append(b.Children, child)
		}
	}
	if hasBlockChild(b.Children) {
		b.Children = splitRuns(b.Children, st)
	}
}
```

Then route the table family and swap the inline gather for the helper. The existing `b := &Box{Role: role, Tag: tag, Node: n, St: st, WS: st.WS}` line stays; replace everything from the `if role != RoleBlock && role != RoleInline {` guard through the trailing `return b` (today lines 150-182) with:

```go
	if role == RoleTable {
		b.Tbl = tableSlot(d)
		switch b.Tbl {
		case "cell":
			if tag == "th" && !st.BoldSet {
				st.Bold = true // UA th bold; stage-1 only (the mail walker never builds)
			}
			fillFlowChildren(b, n, st, rules, listDepth)
		case "caption":
			fillFlowChildren(b, n, st, rules, listDepth) // content built; caption layout deferred
		case "table":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 0)
		case "row-group":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 1)
		case "row":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 2)
		}
		return b
	}
	if role != RoleBlock && role != RoleInline {
		return b // br/img leaves keep their subtree/attrs on Node for later plans
	}
	fillFlowChildren(b, n, st, rules, listDepth)
	if role == RoleInline && hasBlockChild(b.Children) {
		b.Role = RoleBlock
		role = RoleBlock
		st.Display = "block" // blockification rewrites computed display
	}
	return b
```

(The second `splitRuns` that the old tail ran is now inside `fillFlowChildren`; the RoleInline blockification above still flips the box role. The outer `splitRuns` call in `Build` is unchanged and still handles the top level.)

`tableSlot` maps an effective display keyword to a grid slot:

```go
// tableSlot maps a table-family display keyword to its grid slot. Non-table
// displays return "".
func tableSlot(d string) string {
	switch d {
	case "table":
		return "table"
	case "table-row-group", "table-header-group", "table-footer-group":
		return "row-group"
	case "table-row":
		return "row"
	case "table-cell":
		return "cell"
	case "table-caption":
		return "caption"
	case "table-column-group":
		return "column-group"
	case "table-column":
		return "column"
	}
	return ""
}
```

- [ ] **Step 6: Add `tableKids` and `fixTable` (anonymous repair)**

`src/lib/html/box.go`, appended after `splitRuns`:

```go
// tableKids gathers a table-context element's children into boxes,
// dropping whitespace-only text nodes (anonymous-table repair) and the
// display:none/head skips; non-whitespace text is kept so a stray run gets
// a cell instead of vanishing.
func tableKids(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) []*Box {
	var out []*Box
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			if strings.TrimSpace(c.Data) == "" {
				continue
			}
			out = append(out, &Box{Role: RoleText, St: parent, WS: parent.WS, Text: c.Data})
		case xhtml.ElementNode:
			if b := buildElement(c, parent, rules, listDepth); b != nil {
				out = append(out, b)
			}
		}
	}
	return out
}

// anonTableBox builds one anonymous grid wrapper sharing the container's
// style pointer (anonymous boxes carry no margins or geometry of their own).
func anonTableBox(kind string, st *Style) *Box {
	return &Box{Role: RoleTable, Tbl: kind, St: st, WS: st.WS}
}

// fixTable repairs a table-context child list into the grid children of a
// box at the given level (0: a table collecting row-groups/captions; 1: a
// row-group collecting rows; 2: a row collecting cells). Anonymous repair
// (weasyprint formatting_structure/build.py): a stray row gets an anonymous
// group, a stray cell gets an anonymous row (and group at level 0/1), and a
// stray run of inline content gets an anonymous cell->row(->group) chain.
// Whitespace-only text is already gone (tableKids).
func fixTable(cs []*Box, st *Style, level int) []*Box {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		cell := anonTableBox("cell", st)
		cell.Children = run
		if hasBlockChild(run) {
			cell.Children = splitRuns(run, st)
		}
		row := anonTableBox("row", st)
		row.Children = []*Box{cell}
		switch level {
		case 0:
			g := anonTableBox("row-group", st)
			g.Children = []*Box{row}
			out = append(out, g)
		case 1:
			out = append(out, row)
		default:
			out = append(out, cell)
		}
		run = nil
	}
	wrapCell := func(cell *Box) {
		row := anonTableBox("row", st)
		row.Children = []*Box{cell}
		switch level {
		case 0:
			g := anonTableBox("row-group", st)
			g.Children = []*Box{row}
			out = append(out, g)
		case 1:
			out = append(out, row)
		default:
			out = append(out, cell)
		}
	}
	for _, c := range cs {
		switch {
		case c.Tbl == "cell" && level == 2: // a proper td under its row
			flush()
			out = append(out, c)
		case level == 0 && (c.Tbl == "row-group" || c.Tbl == "caption"):
			flush()
			out = append(out, c)
		case level == 0 && c.Tbl == "row": // stray tr under a table
			flush()
			g := anonTableBox("row-group", st)
			g.Children = []*Box{c}
			out = append(out, g)
		case level == 1 && c.Tbl == "row":
			flush()
			out = append(out, c)
		case c.Tbl == "cell": // stray td under a table or row-group
			flush()
			wrapCell(c)
		default: // inline runs, stray block/nested-table content: an anonymous cell
			run = append(run, c)
		}
	}
	flush()
	return out
}
```

`tableKids` uses `strings.TrimSpace`, so `box.go` (which imports only `xhtml` today) must add `"strings"` to its import block.

- [ ] **Step 7: Run the build repair tests**

Add `src/lib/html/table_test.go` with the Task 1 build tests:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestTableStrayWhitespaceDrops(t *testing.T) {
	bs := buildBody("<table>\n  <tr><td>a</td></tr>\n  </table>")
	tbl := bs[0]
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" {
		t.Fatalf("pretty-printed whitespace must vanish; children = %+v", tbl.Children)
	}
}

func TestTableStrayTdGetsAnonymousRow(t *testing.T) {
	bs := buildBody(`<table><td>a</td></table>`)
	tbl := bs[0]
	grp := tbl.Children[0]
	if len(tbl.Children) != 1 || grp.Tbl != "row-group" || grp.Tag != "" {
		t.Fatalf("stray td child = %+v, want one anonymous row-group", tbl.Children)
	}
	row := grp.Children[0]
	if row.Tbl != "row" || row.Tag != "" {
		t.Fatalf("row-group child = %+v, want an anonymous row", grp.Children)
	}
	cell := row.Children[0]
	if cell.Tbl != "cell" || cell.Tag != "td" {
		t.Fatalf("row child = %+v, want the td cell", row.Children)
	}
}

func TestTableStrayTrGetsAnonymousGroup(t *testing.T) {
	bs := buildBody(`<table><tr><td>a</td></tr></table>`)
	tbl := bs[0]
	grp := tbl.Children[0]
	if len(tbl.Children) != 1 || grp.Tbl != "row-group" || grp.Tag != "" {
		t.Fatalf("stray tr child = %+v, want one anonymous row-group", tbl.Children)
	}
	if len(grp.Children) != 1 || grp.Children[0].Tag != "tr" {
		t.Fatalf("anonymous group child = %+v, want the real tr", grp.Children)
	}
}

func TestTableStrayRunGetsAnonymousCell(t *testing.T) {
	bs := buildBody(`<table>stray<tr><td>a</td></tr></table>`)
	tbl := bs[0]
	if len(tbl.Children) != 2 {
		t.Fatalf("children = %d, want anon-cell group then the tr group", len(tbl.Children))
	}
	cell := tbl.Children[0].Children[0].Children[0]
	if cell.Tbl != "cell" || cell.Tag != "" || len(cell.Children) != 1 || cell.Children[0].Text != "stray" {
		t.Fatalf("stray run cell = %+v, want anonymous cell wrapping 'stray'", cell)
	}
}

func TestTheadTbodyFootAreRowGroups(t *testing.T) {
	bs := buildBody(`<table><thead><tr><td>h</td></tr></thead><tbody><tr><td>b</td></tr></tbody></table>`)
	tbl := bs[0]
	if len(tbl.Children) != 2 || tbl.Children[0].Tag != "thead" || tbl.Children[1].Tag != "tbody" {
		t.Fatalf("children = %+v, want thead then tbody as row-groups", tbl.Children)
	}
	if tbl.Children[0].Tbl != "row-group" || tbl.Children[1].Tbl != "row-group" {
		t.Fatalf("thead/tbody Tbl = %q/%q, want row-group", tbl.Children[0].Tbl, tbl.Children[1].Tbl)
	}
}
```

- [ ] **Step 8: Add the th-bold and cell-block-content tests**

Same file, appended:

```go
func TestThBoldNotCentered(t *testing.T) {
	bs := buildBody(`<table><tr><th>h</th><td>d</td></tr></table>`)
	row := bs[0].Children[0].Children[0]
	th, td := row.Children[0], row.Children[1]
	if !th.St.Bold {
		t.Fatal("th must be UA-bold")
	}
	if th.Children[0].St.Bold != true {
		t.Fatal("th text leaf must inherit the bold style")
	}
	if td.St.Bold {
		t.Fatal("td must not be bold")
	}
	if th.St.Align != "" {
		t.Fatalf("th Align = %q, want '' (not centered)", th.St.Align)
	}
}

func TestThAuthorFontWeightBeatsUA(t *testing.T) {
	bs := buildBody(`<table><tr><th style="font-weight:normal">h</th><th>i</th></tr></table>`)
	row := bs[0].Children[0].Children[0]
	if row.Children[0].St.Bold {
		t.Fatal("th with font-weight:normal must not be bold")
	}
	if !row.Children[1].St.Bold {
		t.Fatal("bare th stays UA-bold")
	}
}

func TestCellContentBlockifies(t *testing.T) {
	// a td holding a div carries block content, not a flat inline run
	bs := buildBody(`<table><tr><td><div>a</div></td></tr></table>`)
	row := bs[0].Children[0].Children[0]
	cell := row.Children[0]
	if len(cell.Children) != 1 || cell.Children[0].Role != RoleBlock || cell.Children[0].Tag != "div" {
		t.Fatalf("cell children = %+v, want the div block", cell.Children)
	}
}

func TestCaptionStoredNotGrid(t *testing.T) {
	bs := buildBody(`<table><caption>cap</caption><tr><td>a</td></tr></table>`)
	tbl := bs[0]
	if len(tbl.Children) != 2 || tbl.Children[0].Tbl != "caption" {
		t.Fatalf("children = %+v, want caption then anon row-group", tbl.Children)
	}
	if len(tbl.Children[0].Children) != 1 || tbl.Children[0].Children[0].Text != "cap" {
		t.Fatalf("caption content = %+v, want the built text", tbl.Children[0].Children)
	}
}
```

- [ ] **Step 9: Run the whole Task 1 suite**

Run: `cd src && go test -count=1 -run 'TestTable|TestThBold|TestThAuthor|TestCellContent|TestCaption|TestThead|TestTableExpandsToGridTree' ./lib/html/`
Expected: PASS.

- [ ] **Step 10: Full package gate (no drift from the builder change)**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS - existing box/inline/block tests still pass. No test lays out a table yet (no `flow` case), so the new children are inert until Task 2.

- [ ] **Step 11: Commit**

```bash
git add src/lib/html/html.go src/lib/html/box.go src/lib/html/box_test.go src/lib/html/table_test.go
git commit -m "feat(html): build table-family boxes with anonymous repair"
```

(Code commit: no co-author line.)

---

### Task 1a: Conform the table build to x/net/html's grammar (no repair)

Supersedes Task 1's anonymous-repair mechanism per the conformance rule at the top. Applies as a NEW commit on top of Task 1's `b70f963` (do not amend history): delete the repair machinery and the tests that fed it parser-impossible shapes; gate table roles to real table tags; pin the consume side against x/net/html's actual output.

**Files:**
- Modify: `src/lib/html/box.go` - delete `fixTable` + `anonTableBox`; table/row-group/row children = `tableKids` alone; table roles resolve only for real table-family tags; fix the `Role`/`tableKids` doc comments that name stray repair.
- Modify: `src/lib/html/box_test.go` - `TestTableExpandsToGridTree` asserts x/net/html's real output (implied `tbody`, not an anonymous `Tag == ""` group).
- Modify: `src/lib/html/table_test.go` - delete the `rawTable`/`tnode`/`tnodeText` helpers and the three stray tests; add the foster + display-remap pins below.
- Test: `src/lib/html/table_test.go`.

- [ ] **Step 1: Delete the repair machinery in `box.go`**

Remove `fixTable` and `anonTableBox` (box.go ~321-399). The `table`/`row-group`/`row` routing in `buildElement` becomes:

```go
		case "table", "row-group", "row":
			b.Children = tableKids(n, st, rules, listDepth)
```

`tableKids` stays (whitespace-only text drop + recursive element build) but its doc comment drops the stray-run rationale: x/net/html keeps whitespace text between furniture (the renderer must not give it height) and nests real rows/cells, so `tableKids` only removes that whitespace and builds each furniture element. Non-whitespace text cannot reach a table/row context from `xhtml.Parse` (it is foster-parented out).

- [ ] **Step 2: Gate table roles to real table-family tags**

`roleOf(d)` still maps display keywords to `RoleTable`, but a non-table tag whose author sets `display:table-*` must fall to block (the walker's mail-safe default; x/net/html cannot know computed `display` and it is not a mail pattern). In `buildElement`, where `role = roleOf(d)` is computed, demote:

```go
		role = roleOf(d)
		if role == RoleTable && !isTableTag(tag) {
			role = RoleBlock // author display:table-* on a non-table tag: block, not a grid
		}
```

with

```go
// isTableTag reports whether the element is a real HTML table-family tag,
// whose UA default display is a table keyword. Table roles resolve only for
// these: x/net/html's parse already normalizes their children, and author
// display:table-* on any other element renders as block.
func isTableTag(tag string) bool {
	switch tag {
	case "table", "thead", "tbody", "tfoot", "tr", "td", "th", "caption", "colgroup", "col":
		return true
	}
	return false
}
```

A real `<table style="display:table">` still routes (tag gate passes); a `<div style="display:table">` demotes to `RoleBlock` and its children build as ordinary block flow, so a nested real `<table>` inside it still becomes a table. A real `td`/`tr` whose author demotes it out of the family (`td { display:block }`) also leaves the family; its box then sits inside a row/group whose grid must SKIP non-cell/non-row children rather than assume (see Task 2's grid note).

- [ ] **Step 3: Rework `TestTableExpandsToGridTree` to assert the real parse**

`box_test.go`: replace the `rawTable(...)` body with a plain `buildBody` (a literal `<tr>` under `<table>` now yields x/net/html's implied `tbody`, which is what the builder consumes):

```go
func TestTableExpandsToGridTree(t *testing.T) {
	// <table><tr><td>a</td></tr></table> - x/net/html wraps the literal <tr>
	// in an implied <tbody>, so the well-formed table>row-group>row>cell shape
	// is the parser's output, not a builder repair.
	bs := buildBody(`<table><tr><td>a</td></tr></table>`)
	tbl := bs[0]
	if tbl.Role != RoleTable || tbl.Tbl != "table" {
		t.Fatalf("table role/tbl = %v/%q, want RoleTable/table", tbl.Role, tbl.Tbl)
	}
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" || tbl.Children[0].Tag != "tbody" {
		t.Fatalf("table child = %+v, want the implied tbody row-group", tbl.Children)
	}
	row := tbl.Children[0].Children[0]
	if len(tbl.Children[0].Children) != 1 || row.Tbl != "row" || row.Tag != "tr" {
		t.Fatalf("row-group child = %+v, want the tr row", tbl.Children[0].Children)
	}
	cell := row.Children[0]
	if len(row.Children) != 1 || cell.Tbl != "cell" || cell.Tag != "td" {
		t.Fatalf("row child = %+v, want a td cell", row.Children)
	}
	if len(cell.Children) != 1 || cell.Children[0].Role != RoleText || cell.Children[0].Text != "a" {
		t.Fatalf("cell content = %+v, want one RoleText 'a'", cell.Children)
	}
}
```

- [ ] **Step 4: Delete the rawTable stray tests and add the consume-side pins**

`table_test.go`: delete the `rawTable`/`tnode`/`tnodeText` helpers and `TestTableStrayTdGetsAnonymousRow` / `TestTableStrayTrGetsAnonymousGroup` / `TestTableStrayRunGetsAnonymousCell`. Add:

```go
func TestTableFosteredRunLandsBeforeTable(t *testing.T) {
	// <table>stray<tr>... - x/net/html foster-parents the non-whitespace run
	// out of the table (before it in the body); the builder must not invent an
	// anonymous cell to hold it.
	bs := buildBody(`<table>stray<tr><td>a</td></tr></table>`)
	if len(bs) != 2 || bs[0].Role != RoleText || bs[0].Text != "stray" {
		t.Fatalf("body = %+v, want the stray run then the table", bs)
	}
	tbl := bs[1]
	if tbl.Role != RoleTable || len(tbl.Children) != 1 || tbl.Children[0].Tag != "tbody" {
		t.Fatalf("table = %+v, want one implied tbody row-group", tbl.Children)
	}
}

func TestAuthorDisplayTableOnDivRendersBlock(t *testing.T) {
	// display:table on a non-table element is not a grid: x/net/html cannot
	// nest it (parser keys off element names) and mail never uses it, so the
	// div renders as block (walker parity) and its children as ordinary flow.
	bs := buildBody(`<div style="display:table"><em style="display:table-cell">a</em></div>`)
	if len(bs) != 1 || bs[0].Role != RoleBlock || bs[0].Tag != "div" {
		t.Fatalf("display:table div = %+v, want RoleBlock (not RoleTable)", bs[0])
	}
}
```

Keep `TestTableStrayWhitespaceDrops`, `TestTheadTbodyFootAreRowGroups`, `TestThBoldNotCentered`, `TestThAuthorFontWeightBeatsUA`, `TestCellContentBlockifies`, `TestCaptionStoredNotGrid` unchanged (they already exercise real-parse output).

- [ ] **Step 5: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing. The pre-existing box/inline/block tests must still pass (no test lays out a table yet - no `flow` case - so the new children stay inert until Task 2).

- [ ] **Step 6: Commit**

```bash
git add src/lib/html/box.go src/lib/html/box_test.go src/lib/html/table_test.go
git commit -m "feat(html): consume x/net/html table grammar without re-implementing it"
```

(Code commit: no co-author line.)

---

### Task 2: Auto-layout widths + row emission (`[]Row` with Cells fragments)

**Files:**
- Modify: `src/lib/html/block.go` - `Row.Cells`, `flow` table case.
- Modify: `src/lib/html/block_test.go` - `rowsText` recurses into `Cells`.
- Create: `src/lib/html/table.go`.
- Test: `src/lib/html/table_test.go`.

Task-2 boundary (staged; spans and block-in-cell land in Tasks 3 and 4): every cell occupies one grid column, and cell content is the uniform-inline case. `buildGrid`/`columnWidths`/`cellRows`/`tableRows` are written to be replaced in place by Task 3/4, but they are complete for the single-span inline case and leave the suite green.

- [ ] **Step 1: Write the failing layout tests**

Append to `src/lib/html/table_test.go`:

```go
// fragText renders one fragment row's text: a cell content line, recursing
// into Cells for a nested table's grid row.
func fragText(r Row) string {
	if len(r.Cells) > 0 {
		var b strings.Builder
		for _, c := range r.Cells {
			b.WriteString(fragText(c))
		}
		return b.String()
	}
	var b strings.Builder
	for _, a := range r.Line.Atoms {
		b.WriteString(a.text)
	}
	return b.String()
}

func TestTableTwoCellsShrinkwrap(t *testing.T) {
	rs := LayoutBlock(buildBody(`<table><tr><td>a</td><td>b</td></tr></table>`), 20, mono(1), false)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.X != 0 || r.W != 12 {
		t.Fatalf("table row X/W = %d/%d, want 0/12 (shrunk to max-content, not 20)", r.X, r.W)
	}
	if len(r.Cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(r.Cells))
	}
	// columns 3px wide each (1px content + 1px padding each side); col0 box at
	// x2 (2px leading border-spacing), content at x3; col1 box at x7, content
	// at x8; the 2px gutter sits between the boxes.
	for i, want := range []struct {
		x int
		s string
	}{{3, "a"}, {8, "b"}} {
		c := r.Cells[i]
		if c.X != want.x || fragText(c) != want.s {
			t.Fatalf("cell %d = X%d %q, want X%d %q", i, c.X, fragText(c), want.x, want.s)
		}
	}
}

func TestTableFillsBetweenMinAndMax(t *testing.T) {
	// col0 "aaa bbb": content min 3, max 7 -> column [5, 9].
	// col1 "cc dd ee ff gg": content min 2, max 14 -> column [4, 16].
	// tableMin = 5+4+6 = 15, tableMax = 9+16+6 = 31. Available 23 is between:
	// the table fills the available width and columns interpolate at ratio 1/2
	// -> col0 = 5+2 = 7, col1 = 4+6 = 10 (content widths 5 and 8).
	bs := buildBody(`<table><tr><td>aaa bbb</td><td>cc dd ee ff gg</td></tr></table>`)
	rs := LayoutBlock(bs, 23, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2 (both cells wrap to two lines)", len(rs))
	}
	if rs[0].W != 23 || rs[1].W != 23 {
		t.Fatalf("row W = %d/%d, want 23/23", rs[0].W, rs[1].W)
	}
	for i, want := range []string{"aaa", "cc dd ee"} {
		if len(rs[0].Cells) != 2 || fragText(rs[0].Cells[i]) != want {
			t.Fatalf("line0 cell %d = %q, want %q", i, fragText(rs[0].Cells[i]), want)
		}
	}
	if rs[0].Cells[0].X != 3 || rs[0].Cells[1].X != 12 {
		t.Fatalf("line0 cell X = %d/%d, want 3/12", rs[0].Cells[0].X, rs[0].Cells[1].X)
	}
	if fragText(rs[1].Cells[0]) != "bbb" || rs[1].Cells[0].X != 3 {
		t.Fatalf("line1 col0 = X%d %q, want X3 bbb", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
	if fragText(rs[1].Cells[1]) != "ff gg" || rs[1].Cells[1].X != 12 {
		t.Fatalf("line1 col1 = X%d %q, want X12 'ff gg'", rs[1].Cells[1].X, fragText(rs[1].Cells[1]))
	}
}

func TestTableStaysMinWhenContainerTight(t *testing.T) {
	// Available 8 is below tableMin 15 (col0 [5,9] for "aaa bbb", col1 [4,4]
	// for "cc"): author mode takes min-content, so the columns hold their
	// min widths (5/4) and the table overflows the tight 8px container.
	bs := buildBody(`<table><tr><td>aaa bbb</td><td>cc</td></tr></table>`)
	rs := LayoutBlock(bs, 8, mono(1), false)
	if rs[0].W != 15 {
		t.Fatalf("table W = %d, want 15 (min-content, overflowing the tight container)", rs[0].W)
	}
}
```

`table_test.go` grows an import block (`fragText` uses `strings.Builder`; Task 1 only needed `testing`):

```go
import (
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run them to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestTableTwoCells|TestTableFillsBetween|TestTableStaysMin' ./lib/html/`
Expected: FAIL - `Row.Cells` does not exist / `tableRows` is undefined.

- [ ] **Step 3: Add `Cells` to `Row` and route tables in `flow`**

`src/lib/html/block.go`, in the `Row` struct after `Markers`:

```go
	Cells   []Row       // table grid row: per-cell fragments side by side (mutually exclusive with Line/HR)
```

and in `flow`, a case before the `hasBlockChild` branch:

```go
		switch {
		case c.Tbl == "table":
			rows = append(rows, tableRows(c, cx, cw, s, m, norm)...)
		case c.Tag == "hr":
```

(A RoleTable grid table's children are row-group boxes, so the old `hasBlockChild` recursion must not reach them; the table case runs first. The table box's own margins were already folded into `cx`/`cw` by the `geom` call above.)

- [ ] **Step 4: Make `rowsText` recurse into `Cells`**

`src/lib/html/block_test.go`, replace the `rowsText` helper body:

```go
// rowsText renders the non-hr rows' text for assertions. A table row's
// Cells fragments render in order (their texts concatenate; the stage-2
// gutter is horizontal blank columns, not text).
func rowsText(rs []Row) []string {
	var rowText func(Row) string
	rowText = func(r Row) string {
		if len(r.Cells) > 0 {
			var b strings.Builder
			for _, f := range r.Cells {
				b.WriteString(rowText(f))
			}
			return b.String()
		}
		var b strings.Builder
		for _, a := range r.Line.Atoms {
			b.WriteString(a.text)
		}
		return b.String()
	}
	var out []string
	for _, r := range rs {
		if r.HR {
			continue
		}
		out = append(out, rowText(r))
	}
	return out
}
```

- [ ] **Step 5: Implement `table.go` (single-span inline cells)**

Create `src/lib/html/table.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "math"

// Table grid layout (weasyprint auto-table-layout analog). A table box's
// children are row-group boxes - x/net/html's implied tbody gives every row
// a group, so no builder repair is needed (Task 1a). Layout walks them row
// by row, measures each cell's content once into per-column min/max,
// distributes the used width, then emits the grid rows into the px Row
// stream. A grid row is one horizontal strip: its stream Row carries Cells
// fragments, one per cell content line, at absolute X. The grid readers skip
// non-conforming children (a row-group box that is not a row, a row box that
// is not a cell): author CSS that demotes a real table tag out of the family
// leaves such a box, and layout must skip it, never assume - there is no
// anonymous repair to wrap it.

const (
	tableSpacing = 2 // UA table border-spacing, px (probe-measured; html5_ua.css)
	tablePad     = 1 // UA td/th padding, px each side (html5_ua.css)
)

// gridCell is one placed cell in a grid row.
type gridCell struct {
	box     *Box
	start   int // first grid column the cell occupies
	colspan int // clamped to the row's columns (1 until Task 3)
}

type gridRow struct {
	cells []gridCell
}

// spanOf reads a cell's colspan/rowspan attributes. Task-2 boundary: the
// values are parsed and stored, but buildGrid/columnWidths ignore spans
// until Task 3.
func spanOf(cell *Box) (colspan, rowspan int) {
	colspan, rowspan = 1, 1
	if cell.Node != nil {
		if v := Attr(cell.Node, "colspan"); v != "" {
			if n := mustInt(v); n > 1 {
				colspan = n
			}
		}
		if v := Attr(cell.Node, "rowspan"); v != "" {
			if n := mustInt(v); n > 1 {
				rowspan = n
			}
		}
	}
	return
}

// runExtents measures one inline run's content px at infinite width: min is
// the widest unbreakable piece (a word); max is the whole run laid on one
// line (words plus their separators). A br splits the run: segments stack
// vertically, so each segment is measured alone and the run's extents are
// the max over segments.
func runExtents(as []atom, m Metrics) (minW, maxW int) {
	smin, smax := 0, 0
	flush := func() {
		if smin > minW {
			minW = smin
		}
		if smax > maxW {
			maxW = smax
		}
		smin, smax = 0, 0
	}
	for _, a := range as {
		if a.br {
			flush()
			continue
		}
		w := a.width(m)
		smax += w
		if a.sep {
			continue // a separator is a break point, not an unbreakable piece
		}
		if w > smin {
			smin = w
		}
	}
	flush()
	return minW, maxW
}

// cellExtents measures one cell's min and max column-box width (content +
// both paddings). Task-2/3 boundary: cell content is the uniform-inline
// case; block-in-cell (divs, nested tables) lands in Task 4.
func cellExtents(cell *Box, m Metrics) (minW, maxW int) {
	minW, maxW = runExtents(flattenInline(cell.Children), m)
	return minW + 2*tablePad, maxW + 2*tablePad
}

// buildGrid places every cell of a table's row-group children into grid
// rows, left to right, and returns the grid plus its column count. Column
// count is the widest row by cell count; a colspan is clamped to the row's
// remaining columns (Task 3 honors it). Rows that end up with no cell are
// dropped (an empty row emits no content line). Task-2 boundary: every cell
// occupies one column.
func buildGrid(t *Box) (rows []gridRow, cols int) {
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			n := 0
			for _, cb := range rb.Children {
				if cb.Tbl == "cell" {
					n++
				}
			}
			if n > cols {
				cols = n
			}
		}
	}
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			var gr gridRow
			cur := 0
			for _, cb := range rb.Children {
				if cb.Tbl != "cell" {
					continue
				}
				gr.cells = append(gr.cells, gridCell{box: cb, start: cur, colspan: 1})
				cur++
			}
			if len(gr.cells) > 0 {
				rows = append(rows, gr)
			}
		}
	}
	return rows, cols
}

// assignColumns resolves used column box widths (px) from measured column
// min/max at the available content width. auto-table-layout clamp
// (weasyprint auto_table_layout): U = tableMax when available >= tableMax
// (shrinkwrap), U = available when tableMin < available < tableMax (fill),
// U = tableMin when available <= tableMin in author mode; under normalize a
// tight available width caps U at the container so columns squeeze below
// min-content and cell text char-breaks. The width left for columns
// themselves is dist = U - spacing*(cols+1); each column lands between its
// min and max proportionally to the (max-min) gap (CSS tables-3), all min
// below colMin, all max above colMax. Rounding is pushed onto the last
// column, so the widths always sum exactly to dist.
func assignColumns(min, max []int, cols, avail int, norm bool) (U int, colX, colW []int) {
	colMin, colMax := 0, 0
	for j := 0; j < cols; j++ {
		colMin += min[j]
		colMax += max[j]
	}
	switch {
	case avail >= colMax+tableSpacing*(cols+1):
		U = colMax + tableSpacing*(cols+1)
	case avail <= colMin+tableSpacing*(cols+1) && !norm:
		U = colMin + tableSpacing*(cols+1)
	default:
		U = avail
	}
	dist := U - tableSpacing*(cols+1)
	if dist < 0 {
		dist = 0
	}
	colW = make([]int, cols)
	switch {
	case dist <= colMin:
		if colMin > 0 { // squeeze at/below min-content (normalize char-break)
			for j := 0; j < cols; j++ {
				colW[j] = min[j] * dist / colMin
			}
		}
	case dist >= colMax:
		copy(colW, max)
	default:
		sum := 0
		for j := 0; j < cols-1; j++ {
			colW[j] = int(math.Round(float64(min[j]) + float64(max[j]-min[j])*float64(dist-colMin)/float64(colMax-colMin)))
			sum += colW[j]
		}
		colW[cols-1] = dist - sum // the last column absorbs rounding
		if colW[cols-1] < 0 {
			colW[cols-1] = 0
		}
	}
	colX = make([]int, cols)
	cur := tableSpacing
	for j := 0; j < cols; j++ {
		colX[j] = cur
		cur += colW[j] + tableSpacing
	}
	return U, colX, colW
}

// columnWidths measures a grid's single-span columns (Task-2 boundary) and
// resolves their widths. Task 3 replaces the measurement with
// measureColumns and keeps this call shape.
func columnWidths(rows []gridRow, cols, avail int, norm bool, m Metrics) (U int, colX, colW []int) {
	min := make([]int, cols)
	max := make([]int, cols)
	for _, gr := range rows {
		for _, c := range gr.cells {
			cmin, cmax := cellExtents(c.box, m)
			if cmin > min[c.start] {
				min[c.start] = cmin
			}
			if cmax > max[c.start] {
				max[c.start] = cmax
			}
		}
	}
	return assignColumns(min, max, cols, avail, norm)
}

// cellRows lays out a cell's uniform-inline content at its content width,
// returning one Row per filled line, X relative to the cell content box's
// left edge (0).
func cellRows(cell *Box, w int, m Metrics, norm bool) []Row {
	var rs []Row
	for _, line := range LayoutInline(cell, w, m, norm) {
		rs = append(rs, Row{X: line.X, W: w, Box: cell, Line: line})
	}
	return rs
}

// tableRows emits a table box into the row stream at content (x, w),
// consuming the ambient seam on its first emitted line. Task-2 boundary:
// cells occupy one column each; cell content is uniform-inline.
func tableRows(t *Box, x, w int, s *seam, m Metrics, norm bool) []Row {
	rows, cols := buildGrid(t)
	if len(rows) == 0 || cols == 0 {
		return nil
	}
	U, colX, colW := columnWidths(rows, cols, w, norm, m)
	var out []Row
	for _, gr := range rows {
		type laid struct {
			cell gridCell
			rs   []Row
		}
		var all []laid
		maxLines := 0
		for _, c := range gr.cells {
			boxW := colW[c.start] // single span: one column (Task 3 sums spans)
			contentW := boxW - 2*tablePad
			if contentW < 0 {
				contentW = 0
			}
			contentX := x + colX[c.start] + tablePad
			ls := cellRows(c.box, contentW, m, norm)
			for i := range ls {
				ls[i].W = contentW
				ls[i] = shiftRow(ls[i], contentX)
			}
			all = append(all, laid{c, ls})
			if len(ls) > maxLines {
				maxLines = len(ls)
			}
		}
		if maxLines == 0 {
			continue // every cell empty: the grid row emits no content line
		}
		for k := 0; k < maxLines; k++ {
			var frags []Row
			for _, l := range all {
				if k < len(l.rs) {
					frags = append(frags, l.rs[k])
				}
			}
			gap := 0
			if len(out) == 0 {
				gap = s.take() // the table's first emitted line consumes the seam
			}
			out = append(out, Row{Gap: gap, X: x, W: U, Box: t, Cells: frags})
		}
	}
	return out
}

// shiftRow translates a row and everything nested in its Cells by dx px: a
// cell fragment that hosts a nested table row carries that table's column
// fragments, and they all share the same horizontal coordinate space.
func shiftRow(r Row, dx int) Row {
	r.X += dx
	for i := range r.Cells {
		r.Cells[i] = shiftRow(r.Cells[i], dx)
	}
	return r
}
```

`math` needs importing in `table.go` (`assignColumns` uses `math.Round`). `mustInt` and `Attr` are in-package.

- [ ] **Step 6: Run the Task 2 tests**

Run: `cd src && go test -count=1 -run 'TestTableTwoCells|TestTableFillsBetween|TestTableStaysMin|TestTable' ./lib/html/`
Expected: PASS. If the width expectations fail, re-probe weasyprint (appendix) before changing them - do not tune the test to the code. In particular `TestTableStaysMin` documents the author-mode overflow (`U = tableMin` even when the container is narrower); the normalize cap is exercised in Task 3's squeeze path or a later normalize-mode test.

- [ ] **Step 7: Verify wide flat tables stay linear**

Run: `cd src && go test -count=1 -run TestTableFillsBetweenMinAndMax ./lib/html/`, then a scratch run over a fabricated body of 20,000 single-cell rows (each a few runes) to confirm it completes promptly. Expected: completes in well under a second (one atomize per cell for measure, one for layout; the seam is never rescanned).

- [ ] **Step 8: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing.

- [ ] **Step 9: Commit**

```bash
git add src/lib/html/block.go src/lib/html/block_test.go src/lib/html/table.go src/lib/html/table_test.go
git commit -m "feat(html): table auto layout and Cells row emission"
```

(Code commit: no co-author line.)

---

### Task 3: colspan/rowspan real grid occupancy

**Files:**
- Modify: `src/lib/html/table.go` - `spanOf` already parses; `buildGrid` honors colspan/rowspan with occupancy; `measureColumns` splits base and span measurement and distributes span excess (fed by `columnWidths` into `assignColumns`); `tableRows` computes a spanning cell's box width across its columns.
- Test: `src/lib/html/table_test.go`.

- [ ] **Step 1: Write the failing span tests**

Append to `src/lib/html/table_test.go`:

```go
func TestColspanDistributesOverColumns(t *testing.T) {
	// base row gives col0 (aaa -> content 3, box 5) and col1 (bbb -> box 5).
	// The colspan-2 cell's text is 16px + 2px padding = 18 > base max sum
	// (5+5+2 spacing = 12): excess 6 distributes to the two equal base
	// columns, +3 each -> columns 8/8. tableMin = tableMax = 8+8+6 = 22.
	bs := buildBody(`<table><tr><td>aaa</td><td>bbb</td></tr>` +
		`<tr><td colspan="2">abcdefghijklmnop</td></tr></table>`)
	rs := LayoutBlock(bs, 40, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if rs[0].W != 22 {
		t.Fatalf("table W = %d, want 22 (shrinkwrapped max-content, not the 40 container)", rs[0].W)
	}
	// row0: aaa at x3, bbb at x13 (col1 box x12, content +1)
	if fragText(rs[0].Cells[0]) != "aaa" || rs[0].Cells[0].X != 3 {
		t.Fatalf("row0 col0 = X%d %q, want X3 aaa", rs[0].Cells[0].X, fragText(rs[0].Cells[0]))
	}
	if fragText(rs[0].Cells[1]) != "bbb" || rs[0].Cells[1].X != 13 {
		t.Fatalf("row0 col1 = X%d %q, want X13 bbb", rs[0].Cells[1].X, fragText(rs[0].Cells[1]))
	}
	// row1: the spanning cell's box is 8 + 2 spacing + 8 = 18 wide; its text
	// starts at the first column's content x3 and fills all 16px.
	if len(rs[1].Cells) != 1 || fragText(rs[1].Cells[0]) != "abcdefghijklmnop" || rs[1].Cells[0].X != 3 {
		t.Fatalf("row1 = X%d %q, want X3 spanning text", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
	if rs[1].Cells[0].W != 16 {
		t.Fatalf("spanning content W = %d, want 16", rs[1].Cells[0].W)
	}
}

func TestRowspanOccupiesLaterRowColumn(t *testing.T) {
	// col0's cell rowspans 2: it renders in row0 and leaves row1's col0
	// blank. col1 has b (row0) and c (row1). All columns are 3px.
	bs := buildBody(`<table><tr><td rowspan="2">a</td><td>b</td></tr>` +
		`<tr><td>c</td></tr></table>`)
	rs := LayoutBlock(bs, 20, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if fragText(rs[0].Cells[0]) != "a" || rs[0].Cells[0].X != 3 {
		t.Fatalf("row0 col0 = X%d %q, want X3 a", rs[0].Cells[0].X, fragText(rs[0].Cells[0]))
	}
	if fragText(rs[0].Cells[1]) != "b" || rs[0].Cells[1].X != 8 {
		t.Fatalf("row0 col1 = X%d %q, want X8 b", rs[0].Cells[1].X, fragText(rs[0].Cells[1]))
	}
	if len(rs[1].Cells) != 1 || fragText(rs[1].Cells[0]) != "c" || rs[1].Cells[0].X != 8 {
		t.Fatalf("row1 = %d cells X%d %q, want only c at X8 (rowspan blanks col0)", len(rs[1].Cells), rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
}

func TestRowspanSkipsBusyColumnInWiderRow(t *testing.T) {
	// row0: a (colspan 2) plus b -> grid is 3 wide. row1: a rowspan cell at
	// col0 spans down into row2, so row2's single cell steps to col1? No:
	// row1 occupies col0, so row2's cell lands at col1 while col0 stays
	// blank. This pins the busy-column skip, not just the simple two-column
	// case.
	bs := buildBody(`<table><tr><td rowspan="2">a</td><td>b</td><td>c</td></tr>` +
		`<tr><td>d</td><td>e</td></tr></table>`)
	rs := LayoutBlock(bs, 30, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if len(rs[1].Cells) != 2 {
		t.Fatalf("row1 cells = %d, want 2 (d at col1, e at col2; col0 blank)", len(rs[1].Cells))
	}
	if fragText(rs[1].Cells[0]) != "d" || rs[1].Cells[0].X != 8 {
		t.Fatalf("row1 cell0 = X%d %q, want X8 d (stepped past the rowspanned col0)", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
}
```

Verify the rowspan test geometries by hand: `a`, `b`, `c` are single runes, all columns 3px. row0: col0 content x3, col1 box x7 content x8. row1: `d` at col1 x8, `e` at col2 (box x12, content x13). The three-column table: colX = 2, 7, 12. So `d` X8, `e` X13. In the second test's HTML, row1 has `d` and `e` under a rowspan at col0; `d` lands at col1 X8.

- [ ] **Step 2: Run them to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestColspan|TestRowspan' ./lib/html/`
Expected: FAIL - colspan cells are clamped to one column / rowspans are ignored, so text overlaps or lands on the wrong X.

- [ ] **Step 3: Rewrite `buildGrid` for colspan/rowspan occupancy**

`src/lib/html/table.go`, replace `buildGrid`:

```go
// buildGrid places every cell of a table's row-group children into grid
// rows and returns the grid plus its column count. Column count is the
// widest row by cell count - a colspan never mints empty spacer columns
// beyond the cells that define them, so a 20-character colspan attribute
// cannot build a million-column grid. Cells are placed left to right,
// stepping past columns still claimed by a rowspan above; a colspan is
// clamped to the free columns remaining in the row. Rows that end up with
// no cell are dropped (they emit no content line), but rowspan countdowns
// still tick across them.
func buildGrid(t *Box) (rows []gridRow, cols int) {
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			n := 0
			for _, cb := range rb.Children {
				if cb.Tbl == "cell" {
					n++
				}
			}
			if n > cols {
				cols = n
			}
		}
	}
	busy := make(map[int]int) // grid col -> rows still claimed by a rowspan above
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			var gr gridRow
			cur := 0
			for _, cb := range rb.Children {
				if cb.Tbl != "cell" {
					continue
				}
				cs, rs := spanOf(cb)
				for cur < cols && busy[cur] > 0 {
					cur++
				}
				if cur >= cols {
					break // only rowspan-claimed columns remain: drop the rest
				}
				if cs > cols-cur {
					cs = cols - cur
				}
				for {
					free := 0
					for cur+free < cols && free < cs && busy[cur+free] == 0 {
						free++
					}
					if free == cs {
						break
					}
					cur += free + 1 // a busy column blocks the span; retry after it
					for cur < cols && busy[cur] > 0 {
						cur++
					}
					if cur >= cols {
						break
					}
				}
				if cur >= cols {
					break
				}
				gr.cells = append(gr.cells, gridCell{box: cb, start: cur, colspan: cs})
				if rs > 1 {
					busy[cur] = rs // countdown includes this row
				}
				cur += cs
			}
			if len(gr.cells) > 0 {
				rows = append(rows, gr)
			}
			for c, n := range busy { // one row passed: tick every claim down
				if n <= 1 {
					delete(busy, c)
				} else {
					busy[c] = n - 1
				}
			}
		}
	}
	return rows, cols
}
```

- [ ] **Step 4: Split measurement into base + span distribution in `columnWidths`**

`src/lib/html/table.go`, extract the measurement (used by both `columnWidths` and Task 4's nested `tableExtents`):

```go
// measureColumns accumulates each grid column's min/max column-box width
// from the cells that start in it. Single-span cells seed the base; a
// spanning cell's excess over the current sum of its columns is distributed
// proportionally to the columns' max widths (weasyprint preferred.py, which
// passes max_content_widths as the weights for both the min and max
// passes). Spans run in row-major order and see earlier distributions.
func measureColumns(rows []gridRow, cols int, m Metrics) (min, max []int) {
	min = make([]int, cols)
	max = make([]int, cols)
	distribute := func(arr []int, a, b, excess int, weight []int) {
		total := 0
		for j := a; j <= b; j++ {
			total += weight[j]
		}
		if total == 0 {
			share := excess / (b - a + 1)
			rem := excess % (b - a + 1)
			for j := a; j <= b; j++ {
				arr[j] += share
				if j-a < rem {
					arr[j]++
				}
			}
			return
		}
		used := 0
		for j := a; j < b; j++ {
			add := excess * weight[j] / total
			arr[j] += add
			used += add
		}
		arr[b] += excess - used
	}
	// pass 1: base from single-span cells
	for _, gr := range rows {
		for _, c := range gr.cells {
			if c.colspan != 1 {
				continue
			}
			cmin, cmax := cellExtents(c.box, m)
			if cmin > min[c.start] {
				min[c.start] = cmin
			}
			if cmax > max[c.start] {
				max[c.start] = cmax
			}
		}
	}
	// pass 2: spanning cells distribute their excess (row-major order)
	for _, gr := range rows {
		for _, c := range gr.cells {
			if c.colspan == 1 {
				continue
			}
			a, b := c.start, c.start+c.colspan-1
			if b >= cols {
				b = cols - 1
			}
			spanSum := func(arr []int) int {
				sum := tableSpacing * (b - a)
				for j := a; j <= b; j++ {
					sum += arr[j]
				}
				return sum
			}
			cmin, cmax := cellExtents(c.box, m)
			if ex := cmax - spanSum(max); ex > 0 {
				distribute(max, a, b, ex, max)
			}
			if ex := cmin - spanSum(min); ex > 0 {
				distribute(min, a, b, ex, max)
			}
		}
	}
	return min, max
}
```

Then `columnWidths` shrinks to measure via `measureColumns` and hand the result to `assignColumns` (Task 2 - its clamp, interpolation, and squeeze logic stay exactly as written there). The old base-measure loops and the width loop that followed are deleted:

```go
func columnWidths(rows []gridRow, cols, avail int, norm bool, m Metrics) (U int, colX, colW []int) {
	min, max := measureColumns(rows, cols, m)
	return assignColumns(min, max, cols, avail, norm)
}
```

- [ ] **Step 5: Sum a spanning cell's columns in `tableRows` emission**

`src/lib/html/table.go`, in `tableRows` replace the single-cell geometry inside the grid-row loop:

```go
		for _, c := range gr.cells {
			boxW := 0
			for j := c.start; j < c.start+c.colspan && j < cols; j++ {
				boxW += colW[j]
				if j > c.start {
					boxW += tableSpacing
				}
			}
			contentW := boxW - 2*tablePad
			if contentW < 0 {
				contentW = 0
			}
			contentX := x + colX[c.start] + tablePad
			ls := cellRows(c.box, contentW, m, norm)
			for i := range ls {
				ls[i].W = contentW
				ls[i] = shiftRow(ls[i], contentX)
			}
			all = append(all, laid{c, ls})
			if len(ls) > maxLines {
				maxLines = len(ls)
			}
		}
```

- [ ] **Step 6: Run the span tests**

Run: `cd src && go test -count=1 -run 'TestColspan|TestRowspan|TestTable' ./lib/html/`
Expected: PASS.

- [ ] **Step 7: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing.

- [ ] **Step 8: Commit**

```bash
git add src/lib/html/table.go src/lib/html/table_test.go
git commit -m "feat(html): colspan and rowspan grid occupancy"
```

(Code commit: no co-author line.)

---

### Task 4: Block content and nested tables inside cells

**Files:**
- Modify: `src/lib/html/table.go` - `cellExtents` measures block children (`flowExtents`/`boxExtents`/`tableExtents`); `tableExtents` memoizes its result on the box (below); `cellRows` lays out block content via `LayoutBlock` and flattens intra-cell vertical rhythm; the table case in `boxExtents` makes nested tables feed outer column widths.
- Modify: `src/lib/html/box.go` - three measure-cache fields on `Box` (after `Tbl`).
- Test: `src/lib/html/table_test.go`.

Task-3 boundary lifted: a cell may hold block content (divs, paragraphs, lists) and nested RoleTable boxes. The cell is laid out by the ordinary block engine at the column content width, so nested tables recurse into `tableRows` through `flow`.

**Memoized nested-table extents (the O(n) requirement, not an optimization):** without caching, `tableExtents` is re-entered by every ancestor's measure pass AND by every ancestor's layout descent, and each entry re-runs `buildGrid` + `measureColumns` over the whole subtree beneath it - so a chain of D single-cell nested tables does O(D^2) grid/atomize work (the exact megabyte-of-nested-tables hostile shape the threat model names). Content min/max are width-independent (measured at infinite width), so each table caches them on its `Box` the first time any measure reaches it; later ancestor measures and layout reads O(1). Per table the cost is then its own cells' text measured once (extent pass) plus once more when it lays out - a bounded constant, never a per-ancestor rescan.

- [ ] **Step 1: Write the failing nested-table test**

Append to `src/lib/html/table_test.go`:

```go
func TestNestedTableIndentsInsideCell(t *testing.T) {
	// Outer: one cell holding a nested 2-column table. Nested col0 text is
	// 10px -> column box 12; col1 text is 20px -> column box 22; nested table
	// is 12+22+2*3 = 40px wide. Outer cell content is that nested table, so
	// the outer column is 40 + 2px padding = 42 and the outer table is 42 +
	// 2*2 = 46px (measured against weasyprint: nested table left edge = outer
	// spacing 2 + outer pad 1 = x3; inner col0 box x5, content x6).
	bs := buildBody(`<table><tr><td><table>` +
		`<tr><td>aaaaaaaaaa</td><td>bbbbbbbbbbbbbbbbbbbb</td></tr>` +
		`</table></td></tr></table>`)
	rs := LayoutBlock(bs, 100, mono(1), false)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1", len(rs))
	}
	if rs[0].W != 46 {
		t.Fatalf("outer table W = %d, want 46", rs[0].W)
	}
	if got := fragText(rs[0]); got != "aaaaaaaaaabbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("nested text = %q (len %d), want 10 a's then 20 b's", got, len(got))
	}
	outer := rs[0].Cells[0]
	if len(outer.Cells) != 2 {
		t.Fatalf("outer cell fragments = %d, want the nested row's 2 columns", len(outer.Cells))
	}
	if outer.Cells[0].X != 6 || outer.Cells[1].X != 20 {
		t.Fatalf("nested fragment X = %d/%d, want 6/20 (nested col0 content x6, col1 x20)", outer.Cells[0].X, outer.Cells[1].X)
	}
}

func TestTextThenNestedTableStackInCell(t *testing.T) {
	// A cell holding text and then a nested table blockifies into two block
	// children: the text run renders on its own line, then the nested table
	// lines follow - two stream rows total (both children single-line).
	bs := buildBody(`<table><tr><td>hi<table><tr><td>x</td></tr></table></td></tr></table>`)
	rs := LayoutBlock(bs, 60, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"hi", "x"}) {
		t.Fatalf("rows = %q, want [hi x]", got)
	}
}
```

`TestTextThenNestedTableStackInCell` uses `reflect.DeepEqual` and `rowsText`, so `table_test.go` grows `"reflect"` in its import block (which has `"strings"` and `"testing"` since Task 2).

The geometry check: outer table tx = 0, col box [2,44), content x3. Nested table laid out at content width 40; its row X relative to the outer cell content is 0, and `tableRows` shifts the whole fragment by the outer cell content X (3), `Cells` included. Nested col0 content relative x3 -> absolute 6; nested col1 content relative x17 -> absolute 20.

- [ ] **Step 2: Run them to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestNestedTable|TestTextThenNested' ./lib/html/`
Expected: FAIL - a nested RoleTable child is invisible to the inline-only `cellExtents`/`cellRows` (Task 3's boundary), so the outer column collapses to 2px padding and the nested content never emits.

- [ ] **Step 3: Measure block content (nested tables included)**

`src/lib/html/box.go`, add three measure-cache fields to `Box` next to the Task 1 `Tbl` field:
`tblMin, tblMax int` and `tblMeas bool`. The zero value is "unmeasured"; a box
is measured then never mutated, and content min/max are width-independent, so
the cache never goes stale even across a second LayoutBlock over the same tree.

`src/lib/html/table.go`, replace `cellExtents` and add the block/nested measuring helpers:

```go
// flowExtents measures a vertical flow of boxes at infinite width: each
// child contributes its own min/max, the flow takes the max across children
// (blocks stack vertically, so the widest child governs the content width).
func flowExtents(cs []*Box, m Metrics) (minW, maxW int) {
	for _, c := range cs {
		cmin, cmax := boxExtents(c, m)
		if cmin > minW {
			minW = cmin
		}
		if cmax > maxW {
			maxW = cmax
		}
	}
	return minW, maxW
}

// boxExtents measures one box as a flow child: its content's min/max plus
// its horizontal insets (ml + pl on the left, mr on the right - the same
// insets flow applies). A nested table contributes its own border-box width
// (its margins/padding plus columns and spacing edges), so outer columns
// widen to seat it exactly as layout will.
func boxExtents(b *Box, m Metrics) (minW, maxW int) {
	_, mr, _, ml, pl := geom(b)
	if b.Tbl == "table" {
		tmin, tmax := tableExtents(b, m)
		return ml + pl + tmin + mr, ml + pl + tmax + mr
	}
	if b.Tbl == "cell" || len(b.Children) == 0 {
		return 0, 0 // stray cell/leaf contributes nothing as a flow child
	}
	inset := ml + mr + pl
	var cmin, cmax int
	if hasBlockChild(b.Children) {
		cmin, cmax = flowExtents(b.Children, m)
	} else {
		cmin, cmax = runExtents(flattenInline(b.Children), m)
	}
	return inset + cmin, inset + cmax
}

// tableExtents measures a nested table's min and max border-box width
// (column widths plus the surrounding border-spacing) at infinite width,
// memoized on the box (tblMin/tblMax/tblMeas, the box.go fields from the
// note above). Content min/max are width-independent (measured at infinite
// width), so the first measure pass to reach a table computes its extents and
// every later ancestor measure and the table's own layout read the cache.
// Without the memo, each ancestor's measure and layout pass would re-descend
// the whole subtree below it - O(depth^2) buildGrid/atomize work on a deep
// chain of single-cell tables (a content-reachable DoS). measureColumns
// below the memo still runs per table when it lays out (assignColumns needs
// per-column arrays), but its cellExtents reads deeper tables' caches, so it
// costs only the table's own direct cell text - a constant, never a subtree.
func tableExtents(t *Box, m Metrics) (minW, maxW int) {
	if t.tblMeas {
		return t.tblMin, t.tblMax
	}
	rows, cols := buildGrid(t)
	if cols == 0 {
		t.tblMeas = true
		return 0, 0
	}
	min, max := measureColumns(rows, cols, m)
	sumMin, sumMax := tableSpacing*(cols+1), tableSpacing*(cols+1)
	for j := 0; j < cols; j++ {
		sumMin += min[j]
		sumMax += max[j]
	}
	t.tblMin, t.tblMax, t.tblMeas = sumMin, sumMax, true
	return sumMin, sumMax
}

// cellExtents measures one cell's min and max column-box width: content
// (inline runs or block children, recursively) plus both paddings.
func cellExtents(cell *Box, m Metrics) (minW, maxW int) {
	if hasBlockChild(cell.Children) {
		minW, maxW = flowExtents(cell.Children, m)
	} else {
		minW, maxW = runExtents(flattenInline(cell.Children), m)
	}
	return minW + 2*tablePad, maxW + 2*tablePad
}
```

- [ ] **Step 4: Lay out block cell content with the block engine**

`src/lib/html/table.go`, replace `cellRows`:

```go
// cellRows lays out a cell's content at its content width and returns the
// cell's visual lines, X relative to the cell content box's left edge (0).
// Uniform-inline content fills lines directly; block content (divs, lists,
// a nested table) runs through the ordinary block engine, so a nested
// RoleTable recurses into tableRows. Intra-cell vertical margins are
// flattened (Gap 0): a grid row is one horizontal strip, so a paragraph
// gap inside one cell cannot push only that cell down.
func cellRows(cell *Box, w int, m Metrics, norm bool) []Row {
	if !hasBlockChild(cell.Children) {
		var rs []Row
		for _, line := range LayoutInline(cell, w, m, norm) {
			rs = append(rs, Row{X: line.X, W: w, Box: cell, Line: line})
		}
		return rs
	}
	rs := LayoutBlock(cell.Children, w, m, norm)
	for i := range rs {
		rs[i].Gap = 0
	}
	return rs
}
```

- [ ] **Step 5: Run the nested tests**

Run: `cd src && go test -count=1 -run 'TestNestedTable|TestTextThenNested|TestTable|TestColspan|TestRowspan' ./lib/html/`
Expected: PASS.

- [ ] **Step 6: Verify deep nesting stays linear**

Run a scratch test over a fabricated chain of 5,000 nested single-cell tables (each nesting a one-cell table), plus the existing `TestTableFillsBetweenMinAndMax`. Expected: completes promptly. Without the Step 3 memo this is O(depth^2) - 25M re-entered buildGrid/measure passes that visibly stall; with it, the first outer measure warms every nested table's cache bottom-up and the rest reads O(1) - a constant, never a per-depth rescan of the text.

- [ ] **Step 7: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing.

- [ ] **Step 8: Commit**

```bash
git add src/lib/html/table.go src/lib/html/box.go src/lib/html/table_test.go
git commit -m "feat(html): nested tables and block content in cells"
```

(Code commit: no co-author line.)

---

### Task 5: Full suite gate (no drift)

**Files:** none (`BUGS.org` at the repo root).

- [ ] **Step 1: Full tagged suite**

Run: `cd src && go test -count=1 -tags "lua mcp" ./...`
Expected: PASS - including the pinned mail `html_*_test.go` tests. They run the walker, which never calls `buildElement`, `tableRows`, or reads `Row.Cells`/`Tbl`/`BoldSet`; the shared cascade only gained one inert sticky flag.

- [ ] **Step 2: vet + gofmt**

Run: `cd src && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: vet clean, gofmt lists nothing.

- [ ] **Step 3: Update BUGS.org**

No OPEN entry corresponds to this plan's work (checked: the two html OPEN entries were inline layout and are closed; the only current OPEN entries are the self-sent-mail and F-display-filter bugs, unrelated). Record the plan's three deliberate divergences as OPEN entries so stage 2 or a later table plan owns them, matching the existing entries' prose style (what diverges, why, where it would be fixed):

- "html tables: table captions are built but not rendered (stage-1 deferral)" - OPEN, referenced to plan 4.
- "html tables: intra-cell block vertical margins are flattened inside a grid row" - OPEN, plan 4 divergence.
- "html tables: a colspan never mints empty spacer columns beyond the row's cell count (weasyprint would create them)" - OPEN, the O(n) clamp, plan 4.
- "html tables: author display:table-* on a non-table element renders as block, not a grid (weasyprint anonymous-repairs it)" - OPEN, plan 4 Task 1a (x/net/html cannot nest it; not a mail pattern; a normalizer for non-HTML5 DOM sources would be the future opt-in).

- [ ] **Step 4: Commit**

```bash
git add BUGS.org
git commit -m "docs: record html table stage-1 divergences"
```

(Doc commit: `Co-Authored-By: Deepseek` trailer.)

---

## Self-review notes

- **Spec coverage:** table-family box build consuming x/net/html's grammar + table roles gated to real tags (Task 1 + Task 1a); auto layout per-column min/max + distribution + shrink-to-fit cap -> Task 2; 2px gutter + 1px padding -> Task 2 (UA constants); colspan/rowspan grid -> Task 3; nested tables as real block tables -> Task 4; th bold stage-1-only, not centered -> Task 1. The builder performs no table-grammar repair (Task 1a - x/net/html's implied tbody/foster-parenting already normalize; see the conformance rule at the top). Vertical-align, borders, and caption rendering are explicitly not modeled (spec + locked decisions); author display:table-* on non-table elements renders as block (Task 1a divergence). Images inside cells are the image plan's concern (their atoms measure 0 px here, as they do today in inline layout); this plan's tests use text.
- **Placeholders:** none; every task carries verbatim code and expected commands. Functions are defined exactly where each task references them (`tableSlot`, `fillFlowChildren`, `tableKids`, `runExtents`, `cellExtents`, `measureColumns`, `columnWidths`, `cellRows`, `tableRows`, `buildGrid`, `tableExtents`, `boxExtents`, `flowExtents`, `spanOf`; `fixTable`/`anonTableBox` existed only in the superseded Task 1 and are deleted by Task 1a).
- **Staged boundaries are explicit, not silent:** Task 2's single-span inline `buildGrid`/`columnWidths`/`cellRows` are replaced in Task 3 (spans) and Task 4 (block content), and the plan says so at each task head. Each task's code is complete for what that task tests.
- **Consistency:** `Row.Cells` is read nowhere in `mail/`; the only consumer is the new `table.go` and the `rowsText` helper. `hasBlockChild`, `splitRuns`, `geom`, `LayoutBlock`, `LayoutInline`, `flattenInline`, `buildBody`, `mono`, `el`, `Attr`, `mustInt` are existing in-package helpers. `rowsText` gains recursion; the existing block tests assert on rows without `Cells`, where it is behavior-identical.
- **Regression discipline:** the pinned mail `html_*_test.go` suite is untouched; `TestMCPScopeEnforcement` is untouched. `TestTableIsLeaf` was a stub description of the pre-plan leaf, not a pinned behavior, and becomes `TestTableExpandsToGridTree`. No regression test is weakened.
- **O(n) per stage:** Task 1 is one build pass per node (repair wraps without rescanning). Task 2 measures each cell's text once (measure pass) and lays it out once (layout pass); column count and widths are single passes over the grid. Task 3 keeps that and adds span distribution that touches only each spanning cell's clamped columns; rowspan costs one map tick per actual row plus one skip per rowspan-claimed column in a populated row. Task 4 recurses into nested tables with each table's border-box min/max memoized on its box at first measure (content min/max are width-independent, so the cache is valid across widths and LayoutBlock calls). The first outer measure warms the whole nested subtree bottom-up once; later ancestor measures and each table's own layout read O(1) caches - a constant, never a per-ancestor subtree rescan (the O(n) requirement, not an optimization).

## Appendix: weasyprint parity probes (developer cross-check, not CI)

The Task 2-4 expectations were measured on real weasyprint (`/home/timebomb/.local/bin/weasyprint`, pipx venv at `/home/timebomb/.local/pipx/venvs/weasyprint/lib/python3.14/site-packages/weasyprint`) by rendering fixed-size pages at 96 dpi (`@page { size: Wpx Hpx; margin: 0 }`, `pdftoppm -png -r 96`) and pixel-scanning painted cell bands. Rows used fixed-width PNG images inside cells (integer geometry) or repeated text for ratio inference; glyph banding adds ~1px noise to text probes, so structural values (gutter, padding, column X) come from image/empty cells.

Measured values that the plan's tests derive from:
- UA defaults from `weasyprint/css/html5_ua.css`: `table { border-spacing: 2px; border-collapse: separate }`, `td, th { padding: 1px }`, and no `th { text-align: center }`.
- `table.py auto_table_layout` clamp: `available <= min` -> table = min; `min < available < max` -> table = available (fills); `available >= max` -> table = max (shrink-to-fit). Measured with a two-column text table at wrappers 400/200/130px: it shrinkwraps at 400 and fills/distributes at 200 and 130.
- Distribution matches proportional (max-min) interpolation: measured column splits 113/81 and 60/63 against predicted 114/80 and 61.7/62.4 (1px scan noise).
- Colspan: two base columns each max 10 (image cells) plus a 60px-wide colspan-2 cell -> both columns measure 29 (excess 38 over base sum + 2px spacing, split equally over equal base widths).
- Rowspan: col0 spans two rows (blue), col1 holds a red cell in row1 and a green cell in row2; the two row boxes are 2px apart vertically; the rowspanned col0 is blank in row2.
- Nested table indent chain: an outer single-cell table whose cell holds a 2-column nested table gives outer cell box x2..43 (width 42 = nested table width 40 + 2px outer padding), outer content x3, nested col0 box x5 (content x6), nested col1 box x19, with the 2px gutter between inner columns. Nested table left edge = outer table left + 2px spacing + 1px cell padding.

Re-probe recipe (any expectation): write a tiny html (see the p2_fit/p3_dist/span/rowspan/nest shapes), render at 96dpi with a colored per-cell stylesheet, then scan the ppm for the colored bands' x/y extents with a short python script. If a committed expectation disagrees with a fresh probe, the probe wins - rerun the measurement and fix the expectation, never tune the test to the code.
