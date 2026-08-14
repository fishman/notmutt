# M3: UI milestone - theming, pager, keybindings, framework restructure

Builds on M1 (mailbox view) and M2 (staged tags, async progress).
Implements R5 (extractable TUI), R9 (declarative keybindings), R11
(truecolor theming). Adds the pager (the missing read UX; content
loads on open per R13). Normative text is AGENTS.md; this spec pins
the mechanism.

## 1. Goal and acceptance

The TUI stops being a raw-ANSI list and becomes a framework-native
mail UI: styled index rows (R11 machinery), a scrollable pager for
reading a thread, per-context keybinding data (R9) with a derived
keyhint bar, and a component structure that is the extractable TUI
library (R5). All four pieces land in one milestone - the restructure
is the foundation the other three sit on.

Acceptance (scripted tests; manual items last):

1. `:theme`-style variant switch via the config store re-renders the
   index with the new variant's palette (scripted: SetThemeVariant +
   ConfigChanged -> model re-resolves styles).
2. Unknown palette name, unknown attr, unknown key, and a style
   referencing a non-existent variant are load errors (strict load).
3. Index rows render with per-slot styles (number/date/author/
   subject/flags) and per-tag styles (`[index.tag.<name>]`), all
   inheriting normal.
4. `o` on a thread row opens the pager: whole thread rendered
   (headers + bodies), scrollable, `q`/`back` returns to the index
   with the cursor intact.
5. Pager content is F1-clean (control chars stripped before render);
   quoted levels 0-5 and signatures get their styles.
6. `[ui] keymap = "emacs"` swaps in the emacs default binding set;
   file bindings override per context; the keyhint bar shows the
   active context's bound keys.
7. Manual: open a real thread, scroll, return; switch theme variant
   live; the bar and hints never shift with content.

## 2. Framework restructure (R5)

The `tui` package splits from one model.go into focused files, all
behind the existing Model surface (New/Init/Update/View - already
the extractable boundary):

- `styles.go` - the R11 resolver: config theme data in,
  lipgloss.Style out (section 3).
- `index.go` - the index list: windowed row render (the 129k-row
  flatten must never be rebuilt; the windowed viewport stays, now
  styled). Includes renderRow, flags, tagGlyphs, trunc/pad cells.
- `pager.go` - the pager: content model + viewport (section 4).
- `statusline.go` - status row + progress bar (unchanged behavior,
  styled).
- `keyhints.go` - the R9-derived hint row (section 5).
- `model.go` - the state machine: mode (index|pager), context
  dispatch, cursor, the bus/event plumbing.

The index keeps its windowed render (only visible rows materialize);
BubbleTea's viewport component is used for the PAGER only (bounded
content, scrollbar + mouse wheel). The renderer moves to lipgloss
styles everywhere - the raw `\x1b[...]` concatenation is gone.

## 3. R11 theming

Config data, strict-loaded. TOML shape:

    [palette]
    base00 = "#21252b"   # base16 names; the onedark port is the default
    base01 = "#3e4451"
    base03 = "#5c6370"
    base04 = "#565c64"
    base05 = "#abb2bf"
    base08 = "#e06c75"
    base0A = "#e5c07b"
    base0B = "#98c379"
    base0C = "#56b6c2"
    base0D = "#61afef"
    base0E = "#c678dd"
    # ... base02/06/07/09/0F complete the set

    [theme]
    default = "dark"

    [theme.dark]                 # styles; missing fields inherit normal
    normal  = { fg = "base05", bg = "base00" }
    indicator = { fg = "base00", bg = "base0A" }
    status  = { fg = "base05", bg = "base01" }
    progress = { fg = "base00", bg = "base0D" }
    error   = { fg = "base08" }

    [theme.dark.index]           # index slots (R11 fixed-slot template)
    number  = { fg = "base03" }
    date    = { fg = "base0A" }
    author  = { fg = "base0D" }
    subject = { fg = "base05" }
    flags   = { fg = "base08" }
    staged  = { fg = "base04", attrs = ["bold"] }
    ghost   = { fg = "base03" }

    [theme.dark.index.tag]       # default tag glyph style
    fg = "base0E"
    [theme.dark.index.tag.inbox] # per-tag override
    fg = "base0B"
    [theme.dark.index.tag.unread]
    fg = "base08"

    [theme.dark.pager]
    hdrdefault = { fg = "base05" }
    header = { fg = "base0D" }   # per-header regex styles come with the
    quoted0 = { fg = "base0B" }  # theming milestone's regex surface; the
    quoted1 = { fg = "base0C" }  # pager milestone styles quotedN/signature/
    quoted2 = { fg = "base0D" }  # attachment at fixed levels
    quoted3 = { fg = "base0E" }
    quoted4 = { fg = "base0A" }
    quoted5 = { fg = "base08" }
    signature = { fg = "base03" }
    attachment = { fg = "base0E" }

    [ui]
    keymap = "vim"
    [ui.tags]                    # tag slot cells (R11)
    max = 2
    [ui.glyphs]                  # config data, never hardcoded (R11/R15)
    staged = "*"
    progress_fill = "#"
    progress_empty = "-"
    statusline_separator = "|"

The status line is COMPOSABLE, modeled on the powerline-go segment
pattern (workspace checkout) adapted to the TUI: segments are data
(content + style + drop priority); the left group (view name, visible
count; more segments append the same way) joins with the separator
glyph; the right group is the R15 progress region (lowest priority);
when the row exceeds the terminal width the lowest-priority segments
drop first (progress, then count; the view name survives). The
separator seam renders fg = previous bg on next bg (the chevron); on
a shared background it falls back to the previous fg - a plain "|".
Gaps pad with the status background so the row always covers the full
width.

Rules (R11):

- A style value is `{fg, bg, attrs}`; fg/bg are palette names or raw
  hex (`#rrggbb`); attrs is a list from bold/italic/underline/reverse.
- Inheritance: every style defaults to normal's fg/bg/attrs, field by
  field. A style states only what differs.
- Resolution order: style hex > variant palette > base palette.
- `[palette.<variant>]` overrides single names when variants diverge;
  `[theme.<variant>]` holds the variant's style table; `[theme]
  default` selects. A style referencing a missing palette name or a
  variant with no style table is a load error.
- Variant switching is a config-store setter (SetThemeVariant,
  validated, notifies the "theme" section -> ConfigChanged on the
  bus -> the model re-resolves). R12 DBus is a separate build tag;
  the setter is the manual `:theme` substrate.

Config structs mirror the TOML 1:1 (R8): `Palette` (base + variants),
`Theme` (default + variants: map name -> StyleTable), `Style` with a
`Resolve(palette)` method in config (pure data, no lipgloss), and
`tui/styles.go` maps resolved config.Style -> lipgloss.Style.

## 4. Pager and open flow

Content loads ONLY on open (R13 two-step; the index fill stays
content-free). `o` (index context) on a thread row:

1. tea.Cmd goroutine: worker.Call(ActThread{ThreadID}) - notmuch show
   --body=false, headers + paths only (reads unbudgeted, same as
   ActQuery).
2. For each message: open the file at Paths[0] and parse with
   go-message (R6 - the library is the parser, not neomutt code and
   not show's body dump). Extract: headers (From/Date/Subject), the
   text/plain part(s), quoted depth ("&gt; " prefix count, capped at
   5), the signature (text after the `-- ` separator), attachment
   list (filename, size from the MIME structure).
3. The result returns as a tea msg; the model switches to pager mode
   and builds the thread view: each message is a header block
   (hdrdefault/header styles) + body lines (quotedN styles,
   signature style) + attachment lines (attachment style). All text
   passes stripControls (F1) before it touches the viewport.
4. The pager renders through a bubbletea viewport (bounded content,
   scrollbar, mouse wheel); j/k scroll in the pager context (plus
   ctrl-d/ctrl-u as vim defaults; g/G to top/bottom). `back` returns
   to the index, cursor untouched.

The worker gains `ActThread` (backend.Thread already exists; the
CLI path is `notmuch show --body=false`). The MIME cache (R13) is
NOT part of this milestone - parse on open, cache when the cache
milestone lands.

## 5. R9 keybindings

Contexts: `index`, `pager`. Two default schemes, selected by
`[ui] keymap`; file bindings override per context on top (per-context
override is the R9 rule - the scheme is never a fork):

- vim (index): j/k cursor, o open, r/a/d tag actions, u undo, $ apply,
  q quit.
- vim (pager): j/k/ctrl-d/ctrl-u scroll, g/G top/bottom, q back.
- emacs (index): C-n/C-p cursor (movement keys differ; the rest of
  the map is scheme-neutral: o open, r/a/d, u, $, q quit).
- emacs (pager): C-n/C-p scroll, C-v/M-v page, C-g back.

Key lookup: `string(msg.Runes)` first, then `msg.String()` (so
"ctrl+n" binds work); the lookup runs against the ACTIVE context's
map.

Actions vocabulary per context (app.validateBindings checks the
context's map, not a global one): index = cursor-down/up, open,
quit, undo, apply + tag actions; pager = scroll-down/up,
scroll-top, scroll-bottom, page-down/up, back, quit? (quit in the
pager exits the app; back returns to the index - both bound).

Keyhint bar: one row above the status line, derived from the active
context's binding map: `j down  k up  o open  q quit` (key + action
label, sorted by key, truncated to width - the R11 slot-reservation
rule). Labels are the action names (config data); the hint row never
hardcodes a key. The help view is future work; the hint row IS the
derived surface for now.

## 6. Testing

- Unit (config): strict-load errors (unknown palette ref, unknown
  attr, unknown key, bad hex, missing variant), palette resolution
  order (style hex > variant > base), Style.Resolve.
- Unit (tui): styles resolve from a Config; renderRow uses them
  (assert the rendered string contains the palette's truecolor
  escape); tag styles per tag.
- Unit (tui): key dispatch per context (pager keys only active in
  pager mode); emacs defaults swap on UI.Keymap; keyhint row derives
  from the binding map.
- Unit (tui): pager content build - quoted depth, signature split,
  attachment list, stripControls before render (a body containing
  ESC sequences renders clean).
- Concurrency: the existing model tests keep passing; the restructure
  must not change event handling.
- Soak: unchanged (M2 staged apply is untouched by the restructure).

## 7. Knobs (not this milestone)

- Regex styles (header per-pattern, body URL/email rules, diff
  colors) - the R11 regex surface, later.
- Theme converter from the base16 .rc collection (muttrc/themes/
  palette) - a script, later.
- R12 DBus scheme sync (separate build tag).
- Commands (`:theme`, `:help`), keymap macros, help view.
- MIME cache for the pager (R13; parse-on-open for now).
- HTML part rendering via mailcap (R6), attachments save/open.

## 8. Delivered shapes (2026-08-14)

The milestone shipped. Deviations from this spec, pinned:

- Pager viewport: hand-rolled `pagerViewport` (an offset into styled
  lines, clamp) instead of bubbletea's viewport component - vendored
  bubbletea v1.1.0 has no viewport package (it lives in the separate
  bubbles module; adding it would break the R7 no-new-dependency
  rule). Scrollbar and mouse wheel are future surface, not lost
  features.
- Pager reading is a READ-POSITION cursor, not line scrolling (the
  user's vim-style UX request): j/k move the position inside the
  visible page with the window holding still; only when the position
  crosses a page edge does the window jump a FULL page (down lands on
  the new page's first line, up on its last - vim ctrl-f/ctrl-b
  flow). g/G jump absolutely (position + window); ctrl+d/ctrl+u move
  half a window through the same machinery. The position's line
  renders with the indicator style (render() copies the window so the
  wrap never persists into the styled lines).
- Key dispatch gains vim-style prefixes (R9 data-first - a binding
  wins over the prefix): digit keys accumulate a count ([count]j,
  [count]k, count loops single steps so edge crossings page), and an
  unbound "g" arms the gg chain (gg = index cursor top / pager top).
  The index context gains cursor-top/cursor-bottom actions (G and the
  chain); the edge walk is direction-aware because moveCursor's
  boundary walk cannot reach backward past a leading ghost row. The
  default schemes are untouched - the arrow keys and any overlays
  arrive through the user's [bindings.*] config file (R9 overlay).
- Thread rendering: `mail.RenderThread` returns []Line{Text, Kind,
  Quoted} with LineKind subject/header/body/signature/attachment/
  error. Per-message error lines instead of whole-thread abort (an
  error only when every message failed); unknown charsets/encodings
  are tolerated (the part reads raw) via the blank
  `_ "github.com/emersion/go-message/charset"` import. Body reads are
  capped at 8 MiB per part with a "[content truncated]" marker.
- Scheme mechanism: `vimScheme`/`emacsScheme` package vars +
  `scheme(keymap)`; `Default()` returns the vim scheme (cloned);
  `Load()` nils the Bindings field BEFORE decode so the file's
  `[bindings.*]` tables overlay the selected scheme only (the plan's
  draft shape would have leaked vim defaults into the emacs scheme).
  `[bindings.*]` is the section name - the plan's early `[binding.*]`
  was a typo, and the singular form is a pinned strict-load error.
- Strict load extended to binding contexts: a file context outside
  the scheme (e.g. `[bindings.indicx]`) is a Load error
  ("bindings.%s: unknown context %q"). R8 has no silent typos at the
  context level either - the section-level undecoded-key check cannot
  see inside the map, so validate() carries the check.
- Per-context action vocabulary: `tui.Actions` is
  map[context]map[action]bool; app.validateBindings rejects
  non-index contexts that bind non-builtin actions (tag actions are
  index-only); the tag-action collision check is index-scoped.
- Keyhint bar: one row above the status line in ALL three render
  paths (index list, pager, empty+progress); the index list height is
  height-2 (the pager viewport was already height-2 with the row
  reserved). Labels are the action names from the active context's
  binding map - no key or label hardcoded.

Acceptance status:

1. Variant switch via SetThemeVariant + ConfigChanged re-renders -
   scripted (TestThemeVariantSwitchLive).
2. Strict-load errors (palette/attr/key/variant/account/binding) -
   scripted.
3. Per-slot + per-tag index styles inheriting normal - scripted.
4. o opens the pager, q/back returns with the cursor intact -
   scripted.
5. F1-clean pager content, quoted 0-5 + signature styles - scripted.
6. emacs swap + per-key overlay + keyhint bar from the binding map -
   scripted.
7. Live-mailbox walk (open a real thread, read with the position
   cursor - j/k within the page, full-page jumps at the edges, gg/G,
   counted moves, arrows - return; switch the theme variant; the bar
   and hints never shift) - MANUAL, pending the user's walk.
