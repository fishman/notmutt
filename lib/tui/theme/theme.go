// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package theme resolves named colors and inherited styles for terminal clients.
package theme

type Style struct {
	Fg, Bg string
	Attrs  []string
}

type Palette struct {
	Base     map[string]string
	Variants map[string]map[string]string
}

func ValidHex(color string) bool {
	if len(color) != 7 || color[0] != '#' {
		return false
	}
	for _, digit := range color[1:] {
		if digit >= '0' && digit <= '9' || digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F' {
			continue
		}
		return false
	}
	return true
}

func (p Palette) color(name, variant string) string {
	if name == "" || ValidHex(name) {
		return name
	}
	if color, ok := p.Variants[variant][name]; ok {
		return color
	}
	return p.Base[name]
}

// Resolve applies palette overrides and normal-style inheritance to named styles.
func Resolve(p Palette, variant string, styles map[string]Style) map[string]Style {
	normal := styles["normal"]
	normal.Fg = p.color(normal.Fg, variant)
	normal.Bg = p.color(normal.Bg, variant)
	normal.Attrs = append([]string(nil), normal.Attrs...)
	resolved := make(map[string]Style, len(styles))
	for name, style := range styles {
		if name == "normal" {
			resolved[name] = normal
			continue
		}
		if style.Fg == "" {
			style.Fg = normal.Fg
		} else {
			style.Fg = p.color(style.Fg, variant)
		}
		if style.Bg == "" {
			style.Bg = normal.Bg
		} else {
			style.Bg = p.color(style.Bg, variant)
		}
		if len(style.Attrs) == 0 {
			style.Attrs = append([]string(nil), normal.Attrs...)
		} else {
			style.Attrs = append([]string(nil), style.Attrs...)
		}
		resolved[name] = style
	}
	return resolved
}
