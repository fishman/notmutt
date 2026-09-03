// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package html holds the HTML layout primitives for terminal
// renderers: the CSS cascade engine (x/net/html + cascadia), the
// cell-width helpers, and the hyperlink scanner (aerc port in
// links.go). The flow walker that emits a line model is the mail
// renderer's job (docs/html-rendering-analysis.md).
package html

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/cascadia"
	"github.com/mattn/go-runewidth"
	"golang.org/x/net/html"
)

// WS is the computed white-space class (CSS white-space). Zero = normal.
type WS int

const (
	WSNormal WS = iota
	WSNowrap
	WSPre
	WSPreWrap
	WSPreLine
)

// parseWS maps a white-space value to its class; unknown values
// (break-spaces, typos) land on normal like weasyprint's validator.
func parseWS(v string) WS {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "nowrap":
		return WSNowrap
	case "pre":
		return WSPre
	case "pre-wrap":
		return WSPreWrap
	case "pre-line":
		return WSPreLine
	}
	return WSNormal
}

// Style is the computed style the renderer acts on. Zero values mean
// "inherit", except Display which is the tag default when empty.
type Style struct {
	Fg        string // #rrggbb, "" = inherit
	FgSet     bool   // an explicit color source at this node, not inherited
	Bg        string
	Bold      bool
	BoldSet   bool // an explicit font-weight source at this node, sticky down the tree (UA th-bold gate)
	Italic    bool
	Underline bool
	Align     string // left|center|right|justify, "" = inherit
	AlignSet  bool   // an explicit text-align source at this node, not inherited
	Display   string // block|inline|none|table|..., "" = tag default
	Pre       bool   // white-space: pre* -> no wrap, keep spaces (legacy; mail reads it, WS is precise)
	WS        WS     // white-space class (precise); zero = normal. Inherits.
	WSSet     bool   // an explicit white-space declaration at this node, not inherited
	Label     bool   // synthesized F-key link marker run (mail injects label boxes); not inherited content

	// Resolved-px box geometry (stage 1). Non-inherited: StyleOf zeroes
	// these each node. *Set marks a side the author declared; the UA floor
	// fills only unset sides. The mail walker never reads these.
	MarginTop, MarginRight, MarginBottom, MarginLeft             int
	MarginTopSet, MarginRightSet, MarginBottomSet, MarginLeftSet bool
	PadLeft                                                      int // padding-left px (ul/ol gutter; UA-only)

	// Image sizing (replaced elements): width/height/max-width, px or %.
	// Non-inherited, zeroed per node like the margins above. Height is
	// px-only in effect - a percentage height is auto (see specImg).
	Width, Height, MaxWidth CSSLen
}

// CSSLen is one length value for image sizing. Px is the number; when Pct
// is false it is CSS px, when true it is a percentage of the containing
// block's width. The zero value (Px 0, Pct false) means auto/none.
type CSSLen struct {
	Px  int
	Pct bool
}

// cssColor normalizes a CSS color value to #rrggbb, or "" when not a
// color the renderer understands (transparent, current-color, ...).
func cssColor(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "#"):
		switch len(v) {
		case 4: // #rgb
			return "#" + strings.Repeat(v[1:2], 2) + strings.Repeat(v[2:3], 2) + strings.Repeat(v[3:4], 2)
		case 7:
			return v
		case 9: // #rrggbbaa: alpha drops
			return v[:7]
		}
	case strings.HasPrefix(v, "rgb"):
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(v[strings.IndexByte(v, '(')+1:], " "), ")"), ",")
		if len(parts) < 3 {
			return ""
		}
		var out [3]int
		for i, p := range parts[:3] {
			p = strings.TrimSpace(p)
			var n int
			if strings.HasSuffix(p, "%") {
				n = 255 * mustInt(p[:len(p)-1]) / 100
			} else {
				n = mustInt(p)
			}
			if n < 0 || n > 255 {
				return ""
			}
			out[i] = n
		}
		return fmt.Sprintf("#%02x%02x%02x", out[0], out[1], out[2])
	}
	if c, ok := namedColors[v]; ok {
		return c
	}
	return ""
}

func mustInt(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// parseLen folds one CSS length to px: em resolves against the 16px base
// (the only length scale), px passes through, auto and a bare 0 are 0.
// Percentages and other units are not values.
func parseLen(v string) (int, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "0" || v == "auto":
		return 0, true
	case strings.HasSuffix(v, "px"):
		if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v, "px"))); err == nil && n >= 0 {
			return n, true
		}
	case strings.HasSuffix(v, "em") || strings.HasSuffix(v, "rem"):
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(v, "rem"), "em")), 64)
		if err == nil && f >= 0 {
			return int(math.Round(f * 16)), true
		}
	}
	return 0, false
}

// parseSizeLen folds one image-sizing length: px passes through, a
// percentage keeps Pct true (it resolves against the containing width at
// layout). auto, 0, em, and other units are not values (rejected, so the
// property stays unset, which means auto).
func parseSizeLen(v string) (CSSLen, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.HasSuffix(v, "px") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "px")); err == nil && n > 0 {
			return CSSLen{Px: n}, true
		}
	}
	if strings.HasSuffix(v, "%") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil && n >= 0 {
			return CSSLen{Px: n, Pct: true}, true
		}
	}
	return CSSLen{}, false
}

// marginSides expands a margin value list to top/right/bottom/left.
func marginSides(v string) ([]string, bool) {
	f := strings.Fields(v)
	switch len(f) {
	case 1:
		return []string{f[0], f[0], f[0], f[0]}, true
	case 2:
		return []string{f[0], f[1], f[0], f[1]}, true
	case 3:
		return []string{f[0], f[1], f[2], f[1]}, true
	case 4:
		return f, true
	}
	return nil, false
}

func setMargin(s *Style, side string, v string) {
	if px, ok := parseLen(v); ok {
		switch side {
		case "top":
			s.MarginTop, s.MarginTopSet = px, true
		case "right":
			s.MarginRight, s.MarginRightSet = px, true
		case "bottom":
			s.MarginBottom, s.MarginBottomSet = px, true
		case "left":
			s.MarginLeft, s.MarginLeftSet = px, true
		}
	}
}

// ParseDecls parses one declaration block ("color: red; font-weight:
// bold"). Property names fold to lowercase; values keep their case.
func ParseDecls(s string) map[string]string {
	decls := map[string]string{}
	for _, d := range strings.Split(s, ";") {
		i := strings.IndexByte(d, ':')
		if i < 0 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(d[:i]))
		val := strings.TrimSpace(d[i+1:])
		if prop == "" || val == "" {
			continue
		}
		decls[prop] = val
	}
	return decls
}

// apply folds one declaration map into the style.
func (s *Style) apply(decls map[string]string) {
	if v, ok := decls["color"]; ok {
		if c := cssColor(v); c != "" {
			s.Fg = c
			s.FgSet = true
		}
	}
	if v, ok := decls["background-color"]; ok {
		if c := cssColor(v); c != "" {
			s.Bg = c
		}
	} else if v, ok := decls["background"]; ok {
		// shorthand: first color token; longhand above wins when both present
		for _, tok := range strings.Fields(v) {
			if c := cssColor(tok); c != "" {
				s.Bg = c
				break
			}
		}
	}
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
	if v, ok := decls["font-style"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "italic", "oblique":
			s.Italic = true
		case "normal":
			s.Italic = false
		}
	}
	if v, ok := decls["text-decoration"]; ok {
		s.Underline = strings.Contains(strings.ToLower(v), "underline")
		if strings.Contains(strings.ToLower(v), "none") {
			s.Underline = false
		}
	}
	if v, ok := decls["text-align"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "left", "center", "right", "justify":
			s.Align = strings.ToLower(strings.TrimSpace(v))
			s.AlignSet = true
		}
	}
	if v, ok := decls["display"]; ok {
		s.Display = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := decls["white-space"]; ok {
		s.WS = parseWS(v)
		s.WSSet = true
		switch s.WS {
		case WSPre, WSPreWrap, WSPreLine:
			s.Pre = true
		default:
			s.Pre = false
		}
	}
	if v, ok := decls["margin"]; ok {
		if sides, ok := marginSides(v); ok {
			for i, side := range []string{"top", "right", "bottom", "left"} {
				setMargin(s, side, sides[i])
			}
		}
	}
	for _, side := range []string{"top", "right", "bottom", "left"} {
		if v, ok := decls["margin-"+side]; ok {
			setMargin(s, side, v)
		}
	}
	if v, ok := decls["width"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.Width = l
		}
	}
	if v, ok := decls["height"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.Height = l
		}
	}
	if v, ok := decls["max-width"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.MaxWidth = l
		}
	}
}

// CSSRule is one <style> block rule with its selector parsed and the
// declaration block folded.
type CSSRule struct {
	sel   cascadia.Sel
	decls map[string]string
}

// ParseStyleSheet parses a <style> element's text into rules sorted by
// ascending specificity (later entries win on ties - the cascade).
// Unparseable selectors and @-rules (media queries, imports) drop.
func ParseStyleSheet(text string) []CSSRule {
	text = stripCSSComments(text)
	var rules []CSSRule
	for {
		text = strings.TrimLeft(text, " \t\r\n")
		if text == "" {
			break
		}
		open, close := strings.IndexByte(text, '{'), strings.IndexByte(text, '}')
		if open < 0 || close < 0 || open > close {
			break
		}
		selText := strings.TrimSpace(text[:open])
		body := text[open+1 : close]
		text = text[close+1:]
		if selText == "" {
			continue
		}
		sel, err := cascadia.Parse(selText)
		if err != nil {
			continue
		}
		rules = append(rules, CSSRule{sel: sel, decls: ParseDecls(body)})
	}
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i].sel.Specificity(), rules[j].sel.Specificity()
		for k := 0; k < 3; k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	return rules
}

// stripCSSComments removes /* ... */ comments (mail sends them).
func stripCSSComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+2+j+2:]
	}
}

// StyleOf computes the node's style: the parent's inherited style,
// then matching rules in cascade order, then the inline style
// attribute (always wins). UA defaults fill only what the author
// did not set.
func StyleOf(n *html.Node, parent *Style, rules []CSSRule) *Style {
	s := *parent
	s.AlignSet = false // align inherits, its explicit-source flag never does
	s.FgSet = false    // same for color: the contrast derivation must override an inherited value
	s.WSSet = false    // white-space inherits; its explicit-source flag never does
	// display is not inherited (CSS): don't carry the parent's display into children
	s.Display = ""
	s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft = 0, 0, 0, 0
	s.MarginTopSet, s.MarginRightSet, s.MarginBottomSet, s.MarginLeftSet = false, false, false, false
	s.PadLeft = 0 // geometry is not inherited
	s.Width, s.Height, s.MaxWidth = CSSLen{}, CSSLen{}, CSSLen{}
	uaDefaults(n.Data, &s)
	for _, r := range rules {
		if r.sel.Match(n) {
			s.apply(r.decls)
		}
	}
	if a := Attr(n, "style"); a != "" {
		s.apply(ParseDecls(a))
	}
	// legacy bgcolor (Outlook-era templates): same effect as background-color
	if v := Attr(n, "bgcolor"); v != "" {
		s.apply(ParseDecls("background-color:" + v))
	}
	// legacy align (Outlook-era tables): same effect as text-align
	if v := Attr(n, "align"); v != "" {
		s.apply(ParseDecls("text-align:" + v))
	}
	return &s
}

// uaDefaults fills UA emphasis for unstyled elements; the cascade runs
// after, so author rules override. Flags fill independently.
func uaDefaults(tag string, s *Style) {
	switch tag {
	case "h1", "h2", "b", "strong":
		s.Bold = true
	case "i", "em":
		s.Italic = true
	case "u":
		s.Underline = true
	case "a":
		// underline always; blue only when no color was set (author beats UA)
		s.Underline = true
		if s.Fg == "" {
			s.Fg = "#0000ee"
		}
	}
}

// Attr returns the node's attribute value, "" when absent.
func Attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// TakeCells cuts the longest true byte prefix of s that fits in cap
// cells, on the SOURCE bytes: a recoded replacement char would misalign
// the caller's s[len(chunk):] slice (the fuzz catch).
func TakeCells(s string, cap int) string {
	i := 0
	cells := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if cells+runewidth.RuneWidth(r) > cap {
			break
		}
		i += size
		cells += runewidth.RuneWidth(r)
	}
	return s[:i]
}

// TextWidth is the string's width in terminal cells (wide runes count
// double - the wcwidth rule).
func TextWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runewidth.RuneWidth(r)
	}
	return n
}

// RuneWidth reports a rune's terminal cell width (0 for control chars, 2 for
// wide/emoji) - the rune-level twin of TextWidth for per-rune stepping.
func RuneWidth(r rune) int { return runewidth.RuneWidth(r) }

// ContrastFG picks a readable foreground for a background: Rec.709
// luma, dark text on light, light on dark.
func ContrastFG(bg string) string {
	n, err := strconv.ParseUint(strings.TrimPrefix(bg, "#"), 16, 32)
	if err != nil {
		return "#1a1a1a"
	}
	luma := (0.299*float64(n>>16&255) + 0.587*float64(n>>8&255) + 0.114*float64(n&255)) / 255
	if luma > 0.5 {
		return "#1a1a1a"
	}
	return "#f5f5f5"
}

// IsLight reports whether a #rrggbb color is light by Rec.709 luma (the
// ContrastFG threshold). Malformed input reports false - dark-mode color
// transforms gate on this so a dark-declared mail is never inverted.
func IsLight(c string) bool {
	n, err := strconv.ParseUint(strings.TrimPrefix(c, "#"), 16, 32)
	if err != nil {
		return false
	}
	return (0.299*float64(n>>16&255)+0.587*float64(n>>8&255)+0.114*float64(n&255))/255 > 0.5
}

// AdaptBG maps a mail background onto the theme's dark background by
// reflection through the white axis: per channel out = clamp(255 +
// themeBG - c). An isometry, so pairwise distances are exact, and
// AdaptBG(white) == themeBG. Hue flips - acceptable for a background.
// "" and malformed inputs pass through unchanged.
func AdaptBG(c, themeBG string) string {
	if c == "" || themeBG == "" {
		return c
	}
	cc, err := parseRGB(c)
	if err != nil {
		return c
	}
	bg, err := parseRGB(themeBG)
	if err != nil {
		return c
	}
	return fmt.Sprintf("#%02x%02x%02x",
		clamp255(255+bg[0]-cc[0]), clamp255(255+bg[1]-cc[1]), clamp255(255+bg[2]-cc[2]))
}

// AdaptFG maps a text color to the dark background keeping its hue:
// invert only the lightness (HSL, L' = 1 - L, H and S unchanged), so a
// blue link stays blue, then walk L toward white until the WCAG ratio
// against bg clears the original's ratio against white (the "works"
// guarantee - HSL luminance skews by hue, the walk closes the gap). A
// hue that cannot clear at any lightness falls back to near-white.
// "" and malformed inputs pass through unchanged.
func AdaptFG(c, bg string) string {
	if c == "" || bg == "" {
		return c
	}
	cc, err := parseRGB(c)
	if err != nil {
		return c
	}
	bb, err := parseRGB(bg)
	if err != nil {
		return c
	}
	target := wcagRatio(cc, white)
	h, s, l := rgbToHSL(cc)
	for l = 1 - l; ; {
		cur := hslToRGB(h, s, l)
		if wcagRatio(cur, bb) >= target {
			return fmt.Sprintf("#%02x%02x%02x", cur[0], cur[1], cur[2])
		}
		if l >= 1 {
			return "#f5f5f5"
		}
		l = math.Min(1, l+0.05)
	}
}

var white = [3]int{255, 255, 255}

func parseRGB(s string) ([3]int, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return [3]int{}, fmt.Errorf("not a #rrggbb color")
	}
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return [3]int{}, err
	}
	return [3]int{int(n >> 16 & 255), int(n >> 8 & 255), int(n & 255)}, nil
}

func clamp255(n int) int {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return n
}

// rgbToHSL converts sRGB to HSL; h in [0,360), s and l in [0,1].
func rgbToHSL(c [3]int) (h, s, l float64) {
	r, g, b := float64(c[0])/255, float64(c[1])/255, float64(c[2])/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2
	if d := max - min; d != 0 {
		switch max {
		case r:
			h = 60 * math.Mod((g-b)/d, 6)
		case g:
			h = 60 * ((b-r)/d + 2)
		default:
			h = 60 * ((r-g)/d + 4)
		}
		if h < 0 {
			h += 360
		}
		s = d / (1 - math.Abs(2*l-1))
	}
	return
}

// hslToRGB converts HSL to sRGB; h in [0,360), s and l in [0,1].
func hslToRGB(h, s, l float64) [3]int {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return [3]int{round255(r + m), round255(g + m), round255(b + m)}
}

func round255(v float64) int {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return int(v*255 + 0.5)
}

// wcagRatio is the WCAG 2.x contrast ratio (AA 4.5:1) between two colors.
func wcagRatio(a, b [3]int) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// relLum is the WCAG 2.x relative luminance: linearized sRGB channels
// weighted 0.2126/0.7152/0.0722.
func relLum(c [3]int) float64 {
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}

func lin(v int) float64 {
	x := float64(v) / 255
	if x <= 0.03928 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}

// ListMark is the ordered-list numbering (or the bullet for an
// unordered list).
func ListMark(n int) string {
	if n > 0 {
		return strconv.Itoa(n) + "."
	}
	return "*"
}

// namedColors is the CSS3 named color table (148 standard names).
var namedColors = map[string]string{
	"aliceblue": "#f0f8ff", "antiquewhite": "#faebd7", "aqua": "#00ffff",
	"aquamarine": "#7fffd4", "azure": "#f0ffff", "beige": "#f5f5dc",
	"bisque": "#ffe4c4", "black": "#000000", "blanchedalmond": "#ffebcd",
	"blue": "#0000ff", "blueviolet": "#8a2be2", "brown": "#a52a2a",
	"burlywood": "#deb887", "cadetblue": "#5f9ea0", "chartreuse": "#7fff00",
	"chocolate": "#d2691e", "coral": "#ff7f50", "cornflowerblue": "#6495ed",
	"cornsilk": "#fff8dc", "crimson": "#dc143c", "cyan": "#00ffff",
	"darkblue": "#00008b", "darkcyan": "#008b8b", "darkgoldenrod": "#b8860b",
	"darkgray": "#a9a9a9", "darkgreen": "#006400", "darkgrey": "#a9a9a9",
	"darkkhaki": "#bdb76b", "darkmagenta": "#8b008b", "darkolivegreen": "#556b2f",
	"darkorange": "#ff8c00", "darkorchid": "#9932cc", "darkred": "#8b0000",
	"darksalmon": "#e9967a", "darkseagreen": "#8fbc8f", "darkslateblue": "#483d8b",
	"darkslategray": "#2f4f4f", "darkslategrey": "#2f4f4f", "darkturquoise": "#00ced1",
	"darkviolet": "#9400d3", "deeppink": "#ff1493", "deepskyblue": "#00bfff",
	"dimgray": "#696969", "dimgrey": "#696969", "dodgerblue": "#1e90ff",
	"firebrick": "#b22222", "floralwhite": "#fffaf0", "forestgreen": "#228b22",
	"fuchsia": "#ff00ff", "gainsboro": "#dcdcdc", "ghostwhite": "#f8f8ff",
	"gold": "#ffd700", "goldenrod": "#daa520", "gray": "#808080",
	"green": "#008000", "greenyellow": "#adff2f", "grey": "#808080",
	"honeydew": "#f0fff0", "hotpink": "#ff69b4", "indianred": "#cd5c5c",
	"indigo": "#4b0082", "ivory": "#fffff0", "khaki": "#f0e68c",
	"lavender": "#e6e6fa", "lavenderblush": "#fff0f5", "lawngreen": "#7cfc00",
	"lemonchiffon": "#fffacd", "lightblue": "#add8e6", "lightcoral": "#f08080",
	"lightcyan": "#e0ffff", "lightgoldenrodyellow": "#fafad2", "lightgray": "#d3d3d3",
	"lightgreen": "#90ee90", "lightgrey": "#d3d3d3", "lightpink": "#ffb6c1",
	"lightsalmon": "#ffa07a", "lightseagreen": "#20b2aa", "lightskyblue": "#87cefa",
	"lightslategray": "#778899", "lightslategrey": "#778899", "lightsteelblue": "#b0c4de",
	"lightyellow": "#ffffe0", "lime": "#00ff00", "limegreen": "#32cd32",
	"linen": "#faf0e6", "magenta": "#ff00ff", "maroon": "#800000",
	"mediumaquamarine": "#66cdaa", "mediumblue": "#0000cd", "mediumorchid": "#ba55d3",
	"mediumpurple": "#9370db", "mediumseagreen": "#3cb371", "mediumslateblue": "#7b68ee",
	"mediumspringgreen": "#00fa9a", "mediumturquoise": "#48d1cc", "mediumvioletred": "#c71585",
	"midnightblue": "#191970", "mintcream": "#f5fffa", "mistyrose": "#ffe4e1",
	"moccasin": "#ffe4b5", "navajowhite": "#ffdead", "navy": "#000080",
	"oldlace": "#fdf5e6", "olive": "#808000", "olivedrab": "#6b8e23",
	"orange": "#ffa500", "orangered": "#ff4500", "orchid": "#da70d6",
	"palegoldenrod": "#eee8aa", "palegreen": "#98fb98", "paleturquoise": "#afeeee",
	"palevioletred": "#db7093", "papayawhip": "#ffefd5", "peachpuff": "#ffdab9",
	"peru": "#cd853f", "pink": "#ffc0cb", "plum": "#dda0dd",
	"powderblue": "#b0e0e6", "purple": "#800080", "rebeccapurple": "#663399",
	"red": "#ff0000", "rosybrown": "#bc8f8f", "royalblue": "#4169e1",
	"saddlebrown": "#8b4513", "salmon": "#fa8072", "sandybrown": "#f4a460",
	"seagreen": "#2e8b57", "seashell": "#fff5ee", "sienna": "#a0522d",
	"silver": "#c0c0c0", "skyblue": "#87ceeb", "slateblue": "#6a5acd",
	"slategray": "#708090", "slategrey": "#708090", "snow": "#fffafa",
	"springgreen": "#00ff7f", "steelblue": "#4682b4", "tan": "#d2b48c",
	"teal": "#008080", "thistle": "#d8bfd8", "tomato": "#ff6347",
	"turquoise": "#40e0d0", "violet": "#ee82ee", "wheat": "#f5deb3",
	"white": "#ffffff", "whitesmoke": "#f5f5f5", "yellow": "#ffff00",
	"yellowgreen": "#9acd32",
}
