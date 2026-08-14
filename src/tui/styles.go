package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"notmutt/config"
)

// Truecolor is the baseline, no 256-color mapping (R11). Pin the renderer
// profile so colors never degrade: profile detection reads the terminal
// behind stdout, which is a pipe under `go test` - an Ascii profile would
// silently drop every style and break the render contract.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// Styles is the full style surface the TUI renders with. ResolveStyles
// builds it from config data; the hardcoded onedark values in
// DefaultStyles are the reference port (muttrc/theme/onedark.muttrc)
// fallback.
type Styles struct {
	Normal    lipgloss.Style
	Indicator lipgloss.Style
	Status    lipgloss.Style
	Progress  lipgloss.Style
	Index     IndexStyles
	Pager     PagerStyles
}

type IndexStyles struct {
	Number  lipgloss.Style
	Date    lipgloss.Style
	Author  lipgloss.Style
	Subject lipgloss.Style
	Flags   lipgloss.Style
	Staged  lipgloss.Style
	Ghost   lipgloss.Style
	Tag     func(name string) lipgloss.Style // per-tag styles (R11)
}

type PagerStyles struct {
	Header     lipgloss.Style
	HdrDefault lipgloss.Style
	Quoted     [6]lipgloss.Style
	Signature  lipgloss.Style
	Attachment lipgloss.Style
}

func DefaultStyles() Styles {
	c := func(hex string) lipgloss.Color { return lipgloss.Color(hex) }
	return Styles{
		Normal:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#21252b")),
		Indicator: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#e5c07b")),
		Status:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#3e4451")),
		Progress:  lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		Index: IndexStyles{
			Number:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Date:    lipgloss.NewStyle().Foreground(c("#e5c07b")),
			Author:  lipgloss.NewStyle().Foreground(c("#61afef")),
			Subject: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Flags:   lipgloss.NewStyle().Foreground(c("#e06c75")),
			Staged:  lipgloss.NewStyle().Foreground(c("#565c64")).Bold(true),
			Ghost:   lipgloss.NewStyle().Foreground(c("#5c6370")),
			Tag: func(string) lipgloss.Style {
				return lipgloss.NewStyle().Foreground(c("#c678dd"))
			},
		},
		Pager: PagerStyles{
			Header:     lipgloss.NewStyle().Foreground(c("#61afef")),
			HdrDefault: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Quoted: [6]lipgloss.Style{
				lipgloss.NewStyle().Foreground(c("#98c379")),
				lipgloss.NewStyle().Foreground(c("#56b6c2")),
				lipgloss.NewStyle().Foreground(c("#61afef")),
				lipgloss.NewStyle().Foreground(c("#c678dd")),
				lipgloss.NewStyle().Foreground(c("#e5c07b")),
				lipgloss.NewStyle().Foreground(c("#e06c75")),
			},
			Signature:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Attachment: lipgloss.NewStyle().Foreground(c("#c678dd")),
		},
	}
}

// ResolveStyles converts the config theme data into the render style
// set. Style ids the config does not define resolve to normal. An
// empty theme (no config file provided one) falls back to the
// hardcoded onedark defaults - the reference port.
func ResolveStyles(theme config.Theme, palette config.Palette) Styles {
	if theme.Default == "" || len(theme.Variants) == 0 {
		return DefaultStyles()
	}
	ids := theme.Resolved(palette, theme.Default)
	to := func(id string, base lipgloss.Style) lipgloss.Style {
		s, ok := ids[id]
		if !ok {
			return base
		}
		if s.Fg != "" {
			base = base.Foreground(lipgloss.Color(s.Fg))
		}
		if s.Bg != "" {
			base = base.Background(lipgloss.Color(s.Bg))
		}
		for _, a := range s.Attrs {
			switch a {
			case "bold":
				base = base.Bold(true)
			case "italic":
				base = base.Italic(true)
			case "underline":
				base = base.Underline(true)
			case "reverse":
				base = base.Reverse(true)
			}
		}
		return base
	}
	normal := to("normal", lipgloss.NewStyle())
	return Styles{
		Normal:    normal,
		Indicator: to("indicator", normal),
		Status:    to("status", normal),
		Progress:  to("progress", normal),
		Index: IndexStyles{
			Number: to("index.number", normal), Date: to("index.date", normal),
			Author: to("index.author", normal), Subject: to("index.subject", normal),
			Flags: to("index.flags", normal), Staged: to("index.staged", normal),
			Ghost: to("index.ghost", normal),
			Tag: func(name string) lipgloss.Style {
				if _, ok := ids["index.tag."+name]; ok {
					return to("index.tag."+name, normal)
				}
				return to("index.tag", normal)
			},
		},
		Pager: PagerStyles{
			Header: to("pager.header", normal), HdrDefault: to("pager.hdrdefault", normal),
			Quoted: [6]lipgloss.Style{
				to("pager.quoted0", normal), to("pager.quoted1", normal),
				to("pager.quoted2", normal), to("pager.quoted3", normal),
				to("pager.quoted4", normal), to("pager.quoted5", normal),
			},
			Signature: to("pager.signature", normal), Attachment: to("pager.attachment", normal),
		},
	}
}
