package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Truecolor is the baseline, no 256-color mapping (R11). Pin the renderer
// profile so colors never degrade: profile detection reads the terminal
// behind stdout, which is a pipe under `go test` - an Ascii profile would
// silently drop every style and break the render contract.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// Styles is the full style surface the TUI renders with. Task 2 makes
// this config-driven; the hardcoded onedark values are the reference
// port (muttrc/theme/onedark.muttrc) until then.
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
	Tag     lipgloss.Style
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
			Tag:     lipgloss.NewStyle().Foreground(c("#c678dd")),
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
