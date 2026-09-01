# Dark-mode HTML rendering - design spec

The HTML view respects the mail's own colors today: the walker defaults
unstyled mail to a white background (`#ffffff`, src/mail/html.go:162)
and paints declared colors verbatim, so on a dark theme most HTML mail
renders as a white box with black text. This spec adds a dark mode that
takes only the theme's background color and maps every mail color onto
it with a reflection that preserves every pairwise color distance, so
readability carries over exactly - no contrast re-check.

## 1. Goal and acceptance

`[html] dark-mode = auto|on|off` (default `auto`, follows the resolved
theme variant). In dark mode the HTML view's background is the theme's
`normal.Bg` (the only theme input - the chrome styles never leak in);
mail backgrounds map onto it by reflection (`adaptBG`), text colors by
hue-preserving lightness inversion with a contrast guard (`adaptFG`).
Unstyled text derives from the existing `ContrastFG` against the
adapted background.

Acceptance (scripted tests; item 6 is manual):

1. `adaptBG("#ffffff", "#282c34") == "#282c34"` - the assumed white
   background lands exactly on the theme background.
2. `adaptFG("#333333", "#282c34")` has luma > 0.7 - dark text reads on
   the dark background.
3. Hue preserved: `adaptFG("#0066cc", "#282c34")` keeps hue ~210 (the
   blue link stays blue, not yellow) and its WCAG ratio against
   `#282c34` clears the original's ratio against white - same spectrum,
   same readability.
4. A mail declaring a dark background (`<body style="background:#111">`)
   stays dark: the bg transform is gated on the declared bg's luma, so
   a mail that already renders dark is not inverted into light.
5. `dark-mode = off` (and `auto` on a light variant) renders exactly as
   today - white default, declared colors verbatim.
6. Manual: an unstyled HTML mail and a `<table bgcolor=...>` mail both
   read comfortably on the onedark dark theme; a blue link stays blue
   and readable; an inline image stays a light box (raw pixels are not
   re-colored).

## 2. The transforms

`src/lib/html/html.go`, next to `ContrastFG`. Two functions - one for
backgrounds, one for text. The split exists because the two goals
conflict: the only exact isometry that maps white onto a dark point is
the reflection, and it rotates every hue 180 degrees; keeping a hue
requires a per-color lightness scale that is not a global isometry.
Backgrounds take the exact reflection (the mail's white lands exactly
on the theme bg); text takes hue-preserving lightness inversion, which
reads "same distance from white becomes same distance from dark."

```go
// adaptBG maps a mail background onto the theme's dark background by
// reflection through the white axis: per channel out = clamp(255 +
// themeBG - c). An isometry, so pairwise distances are exact, and
// adaptBG(white) == themeBG. Hue flips - acceptable for a background.
func adaptBG(c, themeBG string) string

// adaptFG maps a text color to the dark background keeping its hue:
// invert only the lightness (HSL, L' = 1 - L, H and S unchanged), so a
// blue link stays blue, then walk L toward white until the WCAG ratio
// against bg clears the original ratio against white (the "works"
// guarantee - HSL luminance skews by hue, the walk closes the gap).
func adaptFG(c, bg string) string
```

- Inputs are normalized `#rrggbb` (already the Style contract).
- `adaptFG`: HSL inversion first; the guard only moves the lightness
  up (dark mode) when contrast is short, and only within the hue's
  achievable range - a hue that cannot clear the ratio at any lightness
  falls back to near-white, never to a different hue.
- Unstyled text (no declared fg) skips `adaptFG` entirely - it derives
  from `ContrastFG(bg)` which already yields near-white on a dark bg.

## 3. Background resolution

`src/mail/html.go` `htmlWalker`:

- New option struct on `renderHTML`:
  `{ Dark bool; ThemeBG string }`; ThemeBG = the resolved theme
  `normal.Bg` hex ("" = not dark mode).
- Mail declares no background: `defaultBG = ThemeBG` when Dark, else
  `#ffffff` (today).
- Mail declares a background (CSS `background-color` or legacy
  `bgcolor`): `defaultBG = adaptBG(declared, ThemeBG)` when Dark,
  else declared verbatim.
- Gate: only adapt when the declared bg's Rec.709 luma > 0.5 (the
  `ContrastFG` threshold, same math). A dark-declared mail's bg stays
  as-is - dark mail is not inverted into light.
- Unstyled text (no declared fg): `ContrastFG(defaultBG)` as today -
  on the adapted dark bg it already yields near-white text, no
  transform needed.

## 4. Declared colors

- Run `Bg` set by the cascade (anything `cssColor` returns) maps
  through `adaptBG` when Dark; run `Fg` through `adaptFG` against the
  line's adapted background. `""` (inherit) stays `""`.
- The guard's original ratio is computed against white for the mail's
  assumed background, or against the declared light bg when one exists
  - the dark render then matches the light render's readability.
- The body/html background handling in the walker's `walk` (the
  `html`/`body` branch, src/mail/html.go:155-170) and the table-cell
  `bg` path use the adapted `defaultBG` exactly as they use the current
  one - the walker's flow is unchanged, only the color values differ.
- Images are untouched: terminal images paint raw decoded bytes, so a
  light-background photo or logo stays a light box on the dark theme.
  Documented limitation, not a bug.

## 5. Config and plumbing

- `[html] dark-mode` string enum, default `auto`: `auto` follows
  `theme.Default` (the R11/R12 resolved variant), `on`/`off` override.
  Unknown value = load error (strict config, R8).
- The open job (both html-mode sites, src/app/app.go:576 and :774)
  resolves the option and the theme bg from config alone:
  `theme.Resolved(palette, theme.Default)["normal"].Bg` (the same
  resolution `ResolveStyles` uses, src/tui/styles.go:263) - no TUI
  dependency in the open path.
- Transform at render time, not paint time: the adapted runs travel in
  `core.Line`/`Run` as today; the pager (src/tui/pager.go) is
  unchanged.

## 6. Tests

`src/lib/html/` and `src/mail/` unit tests:

- `adaptBG`: acceptance item 1, the isometry sample, the clamp at 255,
  `""` and malformed inputs unchanged.
- `adaptFG`: acceptance items 2-3 - hue preserved, lightness inverted,
  ratio after >= ratio before (the guard walk), a hue that cannot clear
  the ratio at any lightness falls back to near-white, `""` unchanged.
- Walker: unstyled mail in dark mode renders with `Line.Bg == themeBG`;
  a `bgcolor="#f4f4f4"` table renders adapted; a dark-declared body
  passes through (the luma gate); `dark-mode = off` matches today's
  render byte-for-byte on a fixed fixture.
