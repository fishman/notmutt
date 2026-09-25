# Shared TUI components for notmutt and ClashPulse

## Status and scope

Design approved 2026-09-25. A standalone Go module at `lib/tui` publishes reusable table geometry, theme resolution, a top tab bar, and a segmented status bar. Both applications consume it. Mail/client state machines, keybinding dispatch, frame ownership, IPC, and tcell screen I/O stay with their applications. This module is a rendering library, not a widget or event-loop framework.

The module path is `github.com/fishman/notmutt/lib/tui`. Its canonical source is `lib/tui` at the repository root, matching the already released `lib/xdg` and `lib/localipc` modules. No duplicate source under `src/lib/tui`; no imports of `notmutt/*` or `github.com/fishman/clashpulse/*`. The root notmutt module requires it via `replace ... => ../lib/tui`. ClashPulse pins a published exact version without a committed replace. The first release tag is `lib/tui/v0.1.0` and must resolve through the Go module proxy before updating ClashPulse.

## Theme contract

The module owns a named-color palette (`Base`, per-variant overrides), style definitions (`Fg`, `Bg`, `Attrs`), and variant resolution. Precedence: a raw hex in a style wins; otherwise a variant palette value wins over the base value. Empty style properties inherit from `normal`, including attrs. Lookup accepts any named style ID, including consumer-defined IDs; no mail-specific ID vocabulary enters the module. Unsupported/invalid style data is rejected at the consumer's config boundary, not silently ignored by the shared resolver.

Notmutt retains its existing strict TOML file shape and typed mail-oriented style tables. Its config layer delegates color/style resolution to the module, and the client maps resolved values to lipgloss styles as before. Its theme store still emits live notifications for a variant change. ClashPulse maps `base`, `muted`, `accent`, `selected`, `error`, `modal`, `tabbar`, `tabbar.active`, and `status` to the same resolved model using a built-in dark variant. This first release adds no user-facing ClashPulse theme setting; its GUI and TUI keep the same settings surface. A later theme selector must use ClashPulse's typed configuration and be exposed in both clients.

## Component contracts

- `table.Col`, `table.Layout`, `Layout.Sizes`, and `Layout.Line` move from `src/lib/table` to `lib/tui/table` without changing terminal-cell width, fixed slots, separator rules, or existing CRM queue behavior. Remove the old package and migrate all imports; do not keep an alias.
- The top tab bar takes prepared labels, active index, width, and resolved styles. It computes cell-aware clipping, drops trailing tabs before the active tab, and returns styled runs and explicit padding for the full width. Consumers own label content and sanitization. Notmutt preserves its current active-tab retention and visual hierarchy; ClashPulse replaces its duplicated `tabLine` formatting with this component.
- The status bar takes left and right groups of consumer-created segments (`Text`, `Style`, `Priority`), a background style, and width. It drops lower-priority segments first while preserving explicitly anchored items, truncates to terminal cells, aligns the right group, and fills the row. It returns runs rather than terminal escape sequences. Notmutt continues to derive mail tags, progress, and message text itself; ClashPulse continues to derive job/progress/notice text itself. Neither consumer imports the other's domain types.
- Both consumers paint shared runs with their own existing renderer: notmutt converts them to lipgloss-styled output; ClashPulse writes segments via tcell. The module does not write to a terminal or hold UI state.

## Integration and release

Notmutt migrates its CRM table, status-row layout, and top tab strip in one cutover, preserving its existing frame geometry and keyboard behavior. ClashPulse uses the same table to align list/header columns, tab bar for its six tabs, status bar for progress/notice/help, and theme styles for its current role colors. Its IPC and event loop remain unchanged. The GUI remains on Fyne; shared theme settings must not make GUI/TUI behavior diverge.

Review the new module's direct/transitive dependencies, provenance, licenses, and API before tagging. Notmutt vendors the local module without erasing intentional vendor patches. Publish the tag, verify `go list -m` can fetch it, and pin exactly `v0.1.0` in ClashPulse. Update ClashPulse's dependency record and license notices if its graph changes. Never commit a temporary local replace in ClashPulse. Later module changes receive independent tags; neither application imports internal module packages.

## Checks and risks

- Module tests: variant/base/raw color precedence, normal inheritance, cell-width/Unicode alignment, narrow rows, active-tab retention, priority drops, and right-aligned status padding.
- Notmutt: existing table/status/theme/tab/frame regressions retain observable contracts, including exactly terminal-height frames. Run both build-tag matrices and a CLI smoke render.
- ClashPulse: exercise its actual tcell rendering surface, component output in all six tabs, empty and narrow screens, and existing model/UI tests. Run `go test ./...` and `go vet ./...`; cross-platform TUI compilation is required before release claims.
- No message content, subscription URLs, controller secrets, or credentials in library tests or logs. Consumers sanitize their own labels and status data before passing them to the module.
- Release is blocked if the Go proxy does not resolve the exact tag or the consumer cannot build against it without a local replace; local tests alone do not establish release readiness.
