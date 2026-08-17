package mail

// CSS subset engine (docs/html-rendering-analysis.md): parses inline
// style="" attributes and <style> blocks into computed styles, matching
// selectors with cascadia (the mature, fuzzed piece - never
// reimplemented). Only the mail-relevant property subset is understood;
// everything else (position, float, flex, media queries, ...) is
// dropped on the floor. The trust boundary: style values never reach
// the render surface raw - they only produce hex colors and booleans
// that the TUI converts to SGR.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

// styleProps is the computed style the renderer acts on. Zero values
// mean "inherit from the parent" except display which is the tag
// default when empty.
type styleProps struct {
	fg        string // #rrggbb, "" = inherit
	bg        string
	bold      bool
	italic    bool
	underline bool
	align     string // left|center|right|justify, "" = inherit
	alignSet  bool   // an explicit text-align source at this node, not inherited
	display   string // block|inline|none|table|..., "" = tag default
	pre       bool   // white-space: pre* -> no wrap, keep spaces
}

// cssColor normalizes a CSS color value to #rrggbb, or "" when the
// value is not a color the renderer understands (transparent, current-
// color, gradients, ... all drop to inherit).
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

// parseDecls parses one declaration block ("color: red; font-weight:
// bold"). Property names fold to lowercase; values keep their case.
func parseDecls(s string) map[string]string {
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
func (s *styleProps) apply(decls map[string]string) {
	if v, ok := decls["color"]; ok {
		if c := cssColor(v); c != "" {
			s.fg = c
		}
	}
	if v, ok := decls["background-color"]; ok {
		if c := cssColor(v); c != "" {
			s.bg = c
		}
	}
	if v, ok := decls["font-weight"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			s.bold = n >= 600
		} else {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "bold", "bolder":
				s.bold = true
			case "normal", "lighter":
				s.bold = false
			}
		}
	}
	if v, ok := decls["font-style"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "italic", "oblique":
			s.italic = true
		case "normal":
			s.italic = false
		}
	}
	if v, ok := decls["text-decoration"]; ok {
		s.underline = strings.Contains(strings.ToLower(v), "underline")
		if strings.Contains(strings.ToLower(v), "none") {
			s.underline = false
		}
	}
	if v, ok := decls["text-align"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "left", "center", "right", "justify":
			s.align = strings.ToLower(strings.TrimSpace(v))
			s.alignSet = true
		}
	}
	if v, ok := decls["display"]; ok {
		s.display = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := decls["white-space"]; ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "pre", "pre-wrap", "pre-line":
			s.pre = true
		default:
			s.pre = false
		}
	}
}

// cssRule is one <style> block rule with its selector parsed and the
// declaration block folded.
type cssRule struct {
	sel   cascadia.Sel
	decls map[string]string
}

// parseStyleSheet parses the text of a <style> element into rules
// sorted by ascending specificity: later entries in the slice win on
// ties, and higher specificity wins over lower - the cascade order.
// Unparseable selectors and @-rules (media queries, imports) drop
// their rules entirely; the renderer degrades to the inline styles.
func parseStyleSheet(text string) []cssRule {
	text = stripCSSComments(text)
	var rules []cssRule
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
		rules = append(rules, cssRule{sel: sel, decls: parseDecls(body)})
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

// styleOf computes the node's computed style: the parent's inherited
// style, overridden by matching <style> rules in cascade order, then by
// the inline style attribute (which always wins). UA defaults (bold
// headings, italic em, underlined links) fill only what the author did
// not set - the author cascade runs after them and wins.
func styleOf(n *html.Node, parent *styleProps, rules []cssRule) *styleProps {
	s := *parent
	s.alignSet = false // align inherits, its explicit-source flag never does
	uaDefaults(n.Data, &s)
	for _, r := range rules {
		if r.sel.Match(n) {
			s.apply(r.decls)
		}
	}
	if a := attr(n, "style"); a != "" {
		s.apply(parseDecls(a))
	}
	// the legacy bgcolor attribute (body/table/tr/td/th - the Outlook-era
	// templates use it everywhere): same effect as background-color
	if v := attr(n, "bgcolor"); v != "" {
		s.apply(parseDecls("background-color:" + v))
	}
	// the legacy align attribute (Outlook-era tables): same effect as
	// text-align
	if v := attr(n, "align"); v != "" {
		s.apply(parseDecls("text-align:" + v))
	}
	return &s
}

// uaDefaults fills the UA emphasis for unstyled elements; the cascade
// runs afterwards, so author rules override. Each flag fills
// independently - a bold <b> inside an italic context stays italic.
func uaDefaults(tag string, s *styleProps) {
	switch tag {
	case "h1", "h2", "b", "strong":
		s.bold = true
	case "i", "em":
		s.italic = true
	case "u":
		s.underline = true
	case "a":
		// the UA anchor: underline always, blue only when the whole
		// chain set no color (an inherited author color wins per the
		// cascade - author beats UA)
		s.underline = true
		if s.fg == "" {
			s.fg = "#0000ee"
		}
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// namedColors is the CSS3 named color table (the 148 standard names),
// used to resolve color: red, background-color: white, ...
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
