# HTML inline-table display keeps grid identity (bug fix)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A real `<table style="display:inline-table">` nested inside another table's cell must keep its grid identity so the ancestor measures it at real width. Today `roleOf` and `tableSlot` do not enumerate `inline-table`, so the inner table is blockified into a `RoleBlock` carrying orphan row-group/row/cell furniture; the ancestor's column measure hits the stray `td` (`Tbl == "cell"` returns 0,0) and shrink-wraps the column, char-breaking the cell's text. Fix: map `inline-table` to `RoleTable` / grid slot `"table"`.

**Architecture:** Two switch cases in `src/lib/html/box.go` (`roleOf` gains `inline-table` in its `RoleTable` arm; `tableSlot` maps `inline-table` to `"table"`). `buildElement` already routes any `RoleTable` box whose tag is a real table-family tag through `tableKids` (`!isTableTag` demotes a div/span `inline-table` to block - the conformance rule, unchanged), so once `inline-table` reads as `RoleTable` the whole grid path (children, measurement, emission) applies as-is. No layout, measurement, or stage-2 code changes. A regression test in `table_test.go` pins the nested shape first.

**Tech Stack:** Go, x/net/html, cascadia (existing deps). Test cmd: `cd src && go test -count=1 ./lib/html/`. Full gate: `go test -count=1 -tags "lua mcp" ./...`, `go vet ./lib/html/`, `gofmt -l lib/html/`.

**Spec refs:** `docs/superpowers/specs/2026-09-03-html-layout-engine-design.md` ("box model and build": role resolution, blockification; the spec does not enumerate `inline-table` - this fix is a conformance application of the plan-4 rule "table roles resolve only for real table-family tags", not a design change). Root cause and fixture analysis: this is the linuxfoundation-registration.html failure (render = 1066 pager lines, near-total char soup; every body glyph on its own line) traced to `boxExtents` (box.go:118) returning 0,0 for a stray cell in an ancestor's extents pass.

**Threat model (locked):** this fix adds no loop and no allocation - two string cases in existing switch statements, hit once per box build. The content-reachable DoS budget (single-pass measurement, memoized `tableExtents`) is untouched. A `<div style="display:inline-table">` still demotes to block via the existing `!isTableTag` guard (conformance rule); only a real `<table>` tag reaches the grid path.

**Conformance rule (locked 2026-09-03, unchanged):** table roles resolve only for real table-family tags. This fix does not relax that: it makes the real `<table>` tag's `inline-table` keyword behave as the table it already is.

---

## File structure

- Modify: `src/lib/html/box.go` - two switch cases: `roleOf` gains `"inline-table"` in its `RoleTable` arm; `tableSlot` gains `case "inline-table": return "table"`.
- Test: `src/lib/html/table_test.go` - one regression test pinning the nested inline-table column as a single flowing line.

## Decisions locked for this plan

- **`inline-table` is a table, not an inline demotion.** Per CSS, `display:inline-table` creates an inline-level table: same table formatting context (row/column grid), different outer layout. notmutt does not model inline outer layout (no inline-table placement), so the mail-safe render is the full block table grid - identical to a sibling plain `<table>`. The demotion was never a deliberate choice; `roleOf`/`tableSlot` simply never listed the keyword and it fell through to `RoleInline` -> blockification. Both functions now enumerate it.
- **`inline-block` stays `RoleBlock`.** It is a distinct display (atomic inline-level box, no table context) and is not the fixture's pattern; a real table tag declaring `inline-block` is out of scope for this fix (recorded, not silently changed). Registration fixture uses `inline-table` only (24 hits, 0 `inline-block`).
- **Additive, stage-1 only.** `mail/` is untouched. The pinned `html_*_test.go` suite must stay green unweakened at the gate.
- **Regression is LOCKED** (project rule): once written and green it must not be edited, weakened, or removed without explicit user confirmation.

---

### Task 1: Pin the nested inline-table column, then map the keyword

**Files:**
- Modify: `src/lib/html/box.go` (roleOf:284-287, tableSlot:248-250).
- Test: `src/lib/html/table_test.go` (new `TestNestedInlineTableRendersOnOneLine`).

This task changes no layout code: the two cases reroute the existing RoleTable grid path. The regression test fails first on the current code, then passes on the fix.

- [ ] **Step 1: Write the failing regression test**

Append to `src/lib/html/table_test.go`:

```go
func TestNestedInlineTableRendersOnOneLine(t *testing.T) {
	// A real <table style="display:inline-table"> nested in another table's
	// cell must keep grid identity. Before the fix, roleOf fell through to
	// RoleInline and blockification demoted it to a RoleBlock carrying orphan
	// row-group/row/cell furniture, so the outer table's column measure hit the
	// stray cell and shrink-wrapped the column, wrapping the text. The fixture
	// that caught this (linuxfoundation-registration.html) char-broke to ~1066
	// pager lines. Same table with no display override renders fine - the
	// collapse only bites under an ancestor that must measure it.
	bs := buildBody(`<table><tr><td>aaa bbb</td><td><table style="display:inline-table"><tr><td>hello world</td></tr></table></td></tr></table>`)
	rs := LayoutBlock(bs, 60, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"aaa bbbhello world"}) {
		t.Fatalf("rows = %q, want one line with the inline-table text intact", got)
	}
}
```

- [ ] **Step 2: Run it to confirm it FAILS**

Run: `cd src && go test -count=1 -run TestNestedInlineTableRendersOnOneLine ./lib/html/ -v`
Expected: FAIL with `rows = ["aaa bbbhello" "world"]` - the inline-table's text is wrapped across two rows (its column shrink-wrapped to ~0 px). This is the red run; do not proceed until it fails.

- [ ] **Step 3: Add the two switch cases**

`src/lib/html/box.go`, `tableSlot` (currently `case "table":`):

```go
	case "table", "inline-table":
		return "table"
```

`src/lib/html/box.go`, `roleOf` (currently `case "table", "table-row-group", ...`):

```go
	case "table", "inline-table", "table-row-group", "table-header-group",
		"table-footer-group", "table-row", "table-cell", "table-caption",
		"table-column-group", "table-column":
		return RoleTable
```

- [ ] **Step 4: Run the regression test to confirm it PASSES**

Run: `cd src && go test -count=1 -run TestNestedInlineTableRendersOnOneLine ./lib/html/ -v`
Expected: PASS, `rows = ["aaa bbbhello world"]` (one row; the outer table measured the inline-table column at its real 17px max-content).

- [ ] **Step 5: Run the full lib/html suite and the pinned stage-2 contract**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS (the new test plus every existing box/block/table/inline/geometry test).

Run: `cd src && go test -count=1 -run 'TestStage2|TestRenderHTML' ./mail/`
Expected: PASS - the locked `html_*_test.go` and `html_stage2_test.go` suites, unweakened.

- [ ] **Step 6: Fixture smoke (optional manual, not a committed test)**

The fixture is real-mail test data; do not commit a test that asserts its exact line count. For local confidence only:
`cd src && go test -count=1 -run TestStage2FixtureSmoke ./mail/` after adding a scratch test that renders `testdata/html/linuxfoundation-registration.html` and asserts line count drops from ~1066 to tens of lines with long content lines present. Delete the scratch test afterward.

- [ ] **Step 7: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt
git add src/lib/html/box.go src/lib/html/table_test.go
git commit -m "fix(html): inline-table keeps grid identity under an ancestor table"
```

No AI marker and no co-author line on this code commit (project rule: all code owned by its human author). Do not stage sibling-session files (BUGS.org, Makefile, TODO.org, crm workflow changes, untracked docs/testdata) if they appear in `git status`.

---

## Out of scope / recorded

- `display:inline-block` on a real table tag stays `RoleBlock` (no grid). Not the fixture's pattern; revisit only if a real mail needs it.
- Author `display:inline-table` on a non-table element (div/span) already demotes to block via `!isTableTag`; unchanged and untested here.
- No stage-2 or `mail/` changes; no walker behavior change (the walker was deleted at the plan-6 cutover).
