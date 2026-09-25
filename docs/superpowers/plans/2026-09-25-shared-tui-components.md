# Shared TUI Components Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish a separately versioned TUI component module and use its theme, table, top tab bar, and status bar from notmutt and ClashPulse.

**Architecture:** `lib/tui` at the notmutt repository root owns pure theme resolution and terminal-cell layout. `lib/tui/table` migrates the existing table. `lib/tui/chrome` returns runs tagged with style IDs; notmutt paints through lipgloss and ClashPulse through tcell. Clients own their labels, actions, frame lifecycle, and sensitive-data sanitization.

**Tech Stack:** Go 1.26.6, `github.com/mattn/go-runewidth` v0.0.27, existing lipgloss v2 and tcell v3 in consumer applications.

**Spec:** `docs/superpowers/specs/2026-09-25-shared-tui-components-design.md`

## Global Constraints

- Canonical source and `go.mod`: repository-root `lib/tui`; module path `github.com/fishman/notmutt/lib/tui`. Do not copy a second editable source tree under `src/`.
- Library imports no `notmutt/*`, ClashPulse, tcell, or lipgloss packages. Keep the surface limited to theme, table, top tabs, and status runs.
- Notmutt must preserve its TOML schema, active-tab retention, frame height, color resolution, and existing regression tests. Move table tests unchanged, retaining their assertions.
- ClashPulse remains an IPC client with event-driven tcell rendering. Do not export subscription URLs, secrets, or mail content through library APIs, tests, or logs.
- Root module uses `replace github.com/fishman/notmutt/lib/tui => ../lib/tui` until release. ClashPulse pins an actual released `v0.1.0`, without committed local replace or vendored graph.
- Release tag: `lib/tui/v0.1.0`. Validate module proxy resolution before changing ClashPulse's pinned requirement. Keep notmutt's patched vendor files intact when regenerating its vendor directory.

---

### Task 1: Theme resolution module

**Files:** Create `lib/tui/go.mod`, `lib/tui/theme/theme.go`, `lib/tui/theme/theme_test.go`.

**Interfaces:** Produce `theme.Style{Fg,Bg string; Attrs []string}`, `theme.Palette{Base map[string]string; Variants map[string]map[string]string}`, and `theme.Resolve(p Palette, variant string, styles map[string]Style) map[string]Style`. `styles["normal"]` is the inheritance root; values with empty components inherit normal. `theme.ValidHex(string) bool` is the strict `#RRGGBB` validator. Returned slices must not alias the input.

- [ ] **Step 1: Write failing tests** for base/variant/raw precedence, empty-component inheritance, preserved arbitrary IDs, and no aliasing. A representative contract:

```go
func TestResolveVariantAndInheritance(t *testing.T) {
    palette := Palette{Base: map[string]string{"fg":"#101010", "bg":"#202020"}, Variants: map[string]map[string]string{"light":{"fg":"#eeeeee"}}}
    styles := map[string]Style{"normal":{Fg:"fg", Bg:"bg", Attrs:[]string{"bold"}}, "queue.header":{Fg:"#abcdef"}}
    got := Resolve(palette, "light", styles)
    if got["queue.header"].Fg != "#abcdef" || got["queue.header"].Bg != "#202020" || got["queue.header"].Attrs[0] != "bold" { t.Fatalf("resolved = %#v", got) }
    got["queue.header"].Attrs[0] = "mutated"
    if styles["normal"].Attrs[0] != "bold" { t.Fatal("result aliases source attrs") }
}
```

- [ ] **Step 2: Run** `go test ./theme -run TestResolveVariantAndInheritance -v` from `lib/tui`. Confirm it fails because `Resolve` is absent.
- [ ] **Step 3: Implement** the exact types above. Resolve each channel independently using `ValidHex`, variant lookup, and base lookup; copy inherited attrs with `append([]string(nil), normal.Attrs...)`.
- [ ] **Step 4: Run** `go test ./theme` in `lib/tui`; ensure zero failures.
- [ ] **Step 5: Commit** `feat(tui): resolve named theme variants` (code commit, no co-author trailer).

### Task 2: Move fixed-column table into the module

**Files:** Move `src/lib/table/table.go` to `lib/tui/table/table.go` and `src/lib/table/table_test.go` to `lib/tui/table/table_test.go`. Modify `lib/tui/go.mod` to require `github.com/mattn/go-runewidth v0.0.27`; update `src/tui/crm.go` import only after the root module requirement is introduced in Task 5.

**Interfaces:** Preserve `table.Col`, `table.Layout`, `(*Layout).Sizes(width int, tail bool) []int`, `(*Layout).Line(cells []string, sizes []int) string` exactly. No compatibility alias.

- [ ] **Step 1: Before moving code, run** `go test ./lib/table` from `src` and save the passing baseline. The existing tests already exercise column edges and wide separators; preserve them byte-for-byte while moving.
- [ ] **Step 2: Move** both files as a single rename, using the LSP rename-file action when supported. Do not modify assertions or public signatures.
- [ ] **Step 3: Run** `go test ./table` from `lib/tui`; confirm it passes the unchanged tests. Then run `go test ./lib/table` from `src` and confirm the old package is gone.
- [ ] **Step 4: Commit** `refactor(tui): move fixed-column table into module`.

### Task 3: Styled-run top tab bar

**Files:** Create `lib/tui/chrome/chrome.go`, `lib/tui/chrome/tabs_test.go`.

**Interfaces:** `chrome.Run{Text, Style string}`; `chrome.Tabs(labels []string, active, width int, barStyle, activeStyle string) []Run`. The returned runs occupy at most `width` terminal cells, include the full-width background when possible, preserve the active label under narrow widths, and never emit ANSI. Labels are already sanitized by the caller. Width math uses `runewidth`.

- [ ] **Step 1: Write failing tests** for active-tail retention, wide Unicode labels, narrow width, and no trailing newline. Example:

```go
func TestTabsRetainsActiveTail(t *testing.T) {
    runs := Tabs([]string{"Inbox", "Draft", "Important"}, 2, 18, "tabbar", "tabbar.active")
    var text string
    for _, run := range runs { text += run.Text }
    if !strings.Contains(text, "Important") || runewidth.StringWidth(text) != 18 { t.Fatalf("tabs = %q", text) }
}
```

- [ ] **Step 2: Run** `go test ./chrome -run TestTabsRetainsActiveTail -v` from `lib/tui`; confirm it fails before implementation.
- [ ] **Step 3: Implement** the renderer using the current notmutt trailing-tab drop rule (`src/tui/statusline.go:205-244`) and cell-aware truncation; do not import application types.
- [ ] **Step 4: Run** `go test ./chrome` from `lib/tui` and commit `feat(tui): render reusable tab bars`.

### Task 4: Styled-run segmented status bar

**Files:** Modify `lib/tui/chrome/chrome.go`; create `lib/tui/chrome/status_test.go`.

**Interfaces:** `chrome.Segment{Runs []Run; Priority int}` and `chrome.Status(width int, background string, left, right []Segment) []Run`. The client supplies left/right segments and sets priorities; values at priority >=10 survive fitting, lower priorities drop first. A run with empty Style inherits background. Status pads right group against the right edge and returns a full-width row. Style boundaries survive clipping; no ANSI is inserted.

- [ ] **Step 1: Write failing tests** for dropping low-priority progress while retaining title, right alignment, empty/narrow widths, and Unicode cell widths. Example:

```go
func TestStatusDropsProgressBeforeTitle(t *testing.T) {
    left := []Segment{{Runs: []Run{{Text:"inbox", Style:"status.view"}}, Priority:10}}
    right := []Segment{{Runs: []Run{{Text:"downloading", Style:"progress"}}, Priority:0}}
    runs := Status(9, "status", left, right)
    var text string
    for _, run := range runs { text += run.Text }
    if !strings.Contains(text, "inbox") || strings.Contains(text, "downloading") { t.Fatalf("status = %q", text) }
}
```

- [ ] **Step 2: Run** `go test ./chrome -run TestStatusDropsProgressBeforeTitle -v`; confirm red.
- [ ] **Step 3: Implement** priority fitting without mutating caller slices. Count widths in terminal cells and return separate runs for styled bar segments.
- [ ] **Step 4: Run** `go test ./...` from `lib/tui`; commit `feat(tui): render segmented status bars`.

### Task 5: Notmutt theme and component cutover

**Files:** Modify `src/go.mod`, `src/config/config.go`, `src/tui/styles.go`, `src/tui/statusline.go`, `src/tui/model.go`, `src/tui/crm.go`, and affected notmutt tests. Update `src/vendor` via `go mod vendor` while preserving its tcell patches. Remove obsolete local table package after Task 2; do not leave re-exports.

**Interfaces:** `config.Theme.Resolved` retains its public signature `(map[string]config.Style, []config.Style)` and its header-color ordering. Build the raw ID map once and delegate property resolution to `theme.Resolve`. Convert resulting generic values back into existing `config.Style` values; remove duplicated palette/hex/inheritance logic only after all callers migrate. Replace `tabBar` width/drop loop with `chrome.Tabs`, and `statusLineWidth` fitting with `chrome.Status`. `statusData` and the mail-derived segment builders remain client-owned. Convert chrome runs to existing lipgloss styles by ID in one adapter; progress contains separate runs for filled/empty glyphs.

- [ ] **Step 1: Run** `go test ./config ./tui ./lib/table` from `src` before changes. Use LSP references for exported config types if available; otherwise locate all call sites before replacing them.
- [ ] **Step 2: Write a consumer regression** asserting theme variant precedence and the terminal-width tab/status rows still match the current frame. Keep the existing locked frame/table tests unchanged.
- [ ] **Step 3: Run** the new targeted tests before changing code; confirm they fail because the new module API is not wired.
- [ ] **Step 4: Add** `require github.com/fishman/notmutt/lib/tui v0.0.0` and `replace github.com/fishman/notmutt/lib/tui => ../lib/tui` in `src/go.mod`, then migrate imports. Render each `chrome.Run` through the resolved lipgloss style for its `Style` ID, leaving mail-specific text in `statusData`.
- [ ] **Step 5: Run** `go test ./config ./tui ./app` from `src`; compare frame widths and output of existing tests. Preserve all regression tests.
- [ ] **Step 6: Regenerate** vendor files and restore any intentional vendored tcell modifications lost by `go mod vendor`; verify `go test -mod=vendor ./...` and `go test -tags "lua mcp crm" ./...` from `src`.
- [ ] **Step 7: Commit** `refactor(tui): consume standalone chrome and theme`.

### Task 6: Publish the standalone module

**Files:** `lib/tui/go.mod`, `lib/tui/go.sum` (if dependencies require), `src/vendor/modules.txt`; no ClashPulse files until the tag is fetchable.

- [ ] **Step 1: Run** `go test ./...`, `go vet ./...`, and `go list -m all` from `lib/tui`. Confirm graph contains only the pinned runewidth dependency and its required dependencies. Confirm public files carry the appropriate license and no `notmutt/*` imports.
- [ ] **Step 2: Create** tag `lib/tui/v0.1.0` on the module commit, then publish the branch and tag to `origin`. Verify `go list -m -json github.com/fishman/notmutt/lib/tui@v0.1.0` resolves from a clean consumer environment; a local `replace` result is not evidence.
- [ ] **Step 3: If proxy resolution or upstream publication fails, stop the ClashPulse dependency update and report the exact blocker.** Do not invent a version or commit a temporary replace.

### Task 7: ClashPulse integration and dependency review

**Files:** Modify `../clashpulse/go.mod`, `../clashpulse/go.sum`, `../clashpulse/tui/render.go`, `../clashpulse/tui/render_test.go`, `../clashpulse/docs/dependencies.md`, and `../clashpulse/THIRD_PARTY_LICENSES.txt` only if the graph introduces a new license. Keep ClashPulse `ui` and IPC layers unchanged.

**Interfaces:** Pin `github.com/fishman/notmutt/lib/tui v0.1.0`. The ClashPulse adapter maps resolved `theme.Style` values to `tcell.Style` (foreground, background, bold, italic, underline, reverse); it paints `chrome.Run` text with `screen.PutStrStyled`. For the six tabs, construct labels from existing `viewTitle`, then call `chrome.Tabs`. For progress/notice/help rows, construct `chrome.Segment` slices from existing model text, call `chrome.Status`, and paint the runs. Use `table.Layout` for aligned list/header rows without changing stable IDs or selection behavior.

- [ ] **Step 1: Write failing TUI render tests** against an actual tcell mock terminal for active tab after clipping, right status alignment, and aligned title/data column edges. Use this concrete screen contract for the active tab:

```go
func TestSharedChromeKeepsActiveTabVisible(t *testing.T) {
    terminal := vt.NewMockTerm(vt.MockOptSize{X: 24, Y: 8})
    screen, err := tcell.NewTerminfoScreenFromTty(terminal, tcell.OptNegotiation(false), tcell.OptAltScreen(false))
    if err != nil { t.Fatal(err) }
    if err := screen.Init(); err != nil { t.Fatal(err) }
    defer screen.Fini()
    model := NewModel()
    model.Tab = TabSettings
    render(screen, model, &renderCache{})
    var row strings.Builder
    for x := 0; x < 24; x++ { row.WriteString(terminal.GetCell(vt.Coord{X:x, Y:1}).C) }
    if !strings.Contains(row.String(), "Settings") { t.Fatalf("active tab clipped: %q", row.String()) }
}
```

Import `github.com/gdamore/tcell/v3`, `github.com/gdamore/tcell/v3/vt`, and `strings` in the test. Follow the same mock-terminal read for the actual status row (`Y:height-3`) and the column-header/data rows (`Y:3` and `Y:4`); assert visual cell offsets, not source text.
- [ ] **Step 2: Run** the targeted ClashPulse TUI tests and confirm the new rendered chrome contract fails before the migration.
- [ ] **Step 3: Pin** `v0.1.0`, migrate the renderer to shared components, and delete `tabLine` and fixed role colors that the theme map replaces. Keep modal secrecy and event-driven repaint rules unchanged.
- [ ] **Step 4: Compare** `go list -m all` and license inventory before/after; update the dependency record and `THIRD_PARTY_LICENSES.txt` if the graph adds license text.
- [ ] **Step 5: Run** `go test ./tui ./...` and `go vet ./...` from ClashPulse; compile TUI packages for Linux/macOS/Windows where the runner supports each target. Verify one actual tcell screen render.
- [ ] **Step 6: Commit** `refactor(tui): use shared theme and chrome` in ClashPulse, with no AI trailer.

### Task 8: Final cross-project proof

**Files:** None unless a check exposes an actual bug.

- [ ] **Step 1: Run** notmutt's untagged and `lua mcp crm` matrices, module `go test ./...`, and ClashPulse `go test ./...` plus `go vet ./...`; capture the actual outputs.
- [ ] **Step 2: Check** notmutt's active tab, CRM column headers, progress/status order, live theme switch, and ClashPulse's six tabs on their actual TUI screens; no focus changes or secret leaks.
- [ ] **Step 3: Confirm** `lib/tui/v0.1.0` resolves without a consumer `replace` and both worktrees contain only intended changes. Report any platform checks not run explicitly.
