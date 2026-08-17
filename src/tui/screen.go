package tui

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// pushFrame writes the built frame to the screen: the SGR runs are
// parsed into per-cell styles (the R11 engine's styles travel as SGR
// strings through the frame builders; this adapter is where they land
// as tcell.Style - decision record 23's trust boundary). Only OUR
// frames arrive here, so the parser is the defense: SGR sequences
// update the current style, C0 control characters (past the mail
// path's F1 sanitize) drop, everything else is one rune. Every cell
// of every row is written - rows pad to the width with the row's
// final style (SGR state semantics: the row's last SGR continues to
// EOL, exactly like the terminal would render it), and the frame's
// rows fill the screen - so tcell's internal diff erases stale cells.
// Rows beyond the frame get the screen base; the renderers pad their
// rows (the empty-view contract), so the base only shows below an
// abnormally short frame.
func pushFrame(s tcell.Screen, frame string, cursorX, cursorY int, showCursor bool) {
	style := tcell.StyleDefault
	w, h := s.Size()
	rows := strings.Split(frame, "\n")
	for y := 0; y < h; y++ {
		row := ""
		if y < len(rows) {
			row = rows[y]
		}
		cs, end := parseSGR(row, style)
		x := 0
		for _, c := range cs {
			if x >= w {
				break
			}
			if c.r >= ' ' && c.r != 0x7f {
				s.SetContent(x, y, c.r, nil, c.st)
				x += runewidth.RuneWidth(c.r)
			} else {
				x++
			}
		}
		for ; x < w; x++ {
			s.SetContent(x, y, ' ', nil, end)
		}
	}
	if showCursor {
		s.ShowCursor(cursorX, cursorY)
	} else {
		s.HideCursor()
	}
	s.Show()
}

type sgrCell struct {
	r  rune
	st tcell.Style
}

// parseSGR walks one frame row: CSI ... m sequences update the style
// (0 resets, attrs and colors set), a lone ESC drops (the sequence
// boundary), everything else is a cell. Colors: 30-37/90-97 and
// 38;2;r;g;b (the R11 engine's truecolor emitter), backgrounds
// 40-47/100-107 and 48;2. Unstyled text keeps the current style -
// the builders concatenate styled fragments, and SGR state semantics
// are what make that correct. The final style comes back for the
// row's padding.
func parseSGR(row string, base tcell.Style) ([]sgrCell, tcell.Style) {
	st := base
	var out []sgrCell
	for i := 0; i < len(row); {
		if row[i] != 0x1b {
			r, size := utf8.DecodeRuneInString(row[i:])
			out = append(out, sgrCell{r: r, st: st})
			i += size
			continue
		}
		if i+1 >= len(row) || row[i+1] != '[' {
			i++ // a lone ESC: drop (never a cell)
			continue
		}
		j := strings.IndexByte(row[i:], 'm')
		if j < 0 {
			break
		}
		st = applySGR(st, row[i+2:i+j])
		i += j + 1
	}
	return out, st
}

// applySGR applies one CSI parameter list to the style. The parameter
// grammar is small (our emitters write only these forms), so the
// parser is a strict param-walk, not a regex.
func applySGR(st tcell.Style, params string) tcell.Style {
	if params == "" {
		return st.Normal() // ESC[m is the empty reset
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}
		switch {
		case n == 0:
			st = st.Normal()
		case n == 1:
			st = st.Bold(true)
		case n == 3:
			st = st.Italic(true)
		case n == 4:
			st = st.Underline(true)
		case n == 7:
			st = st.Reverse(true)
		case n == 22:
			st = st.Bold(false)
		case n == 23:
			st = st.Italic(false)
		case n == 24:
			st = st.Underline(false)
		case n == 27:
			st = st.Reverse(false)
		case n >= 30 && n <= 37:
			st = st.Foreground(tcell.PaletteColor(n - 30))
		case n == 39:
			st = st.Foreground(tcell.ColorDefault)
		case n >= 90 && n <= 97:
			st = st.Foreground(tcell.PaletteColor(n - 90 + 8))
		case n >= 40 && n <= 47:
			st = st.Background(tcell.PaletteColor(n - 40))
		case n == 49:
			st = st.Background(tcell.ColorDefault)
		case n >= 100 && n <= 107:
			st = st.Background(tcell.PaletteColor(n - 100 + 8))
		case (n == 38 || n == 48) && i+1 < len(parts):
			if parts[i+1] == "5" && i+2 < len(parts) {
				if pn, err := strconv.Atoi(parts[i+2]); err == nil {
					if n == 38 {
						st = st.Foreground(tcell.PaletteColor(pn))
					} else {
						st = st.Background(tcell.PaletteColor(pn))
					}
				}
				i += 2
			} else if parts[i+1] == "2" && i+4 < len(parts) {
				r, er := strconv.Atoi(parts[i+2])
				g, eg := strconv.Atoi(parts[i+3])
				b, eb := strconv.Atoi(parts[i+4])
				if er == nil && eg == nil && eb == nil {
					c := tcell.NewRGBColor(int32(r), int32(g), int32(b))
					if n == 38 {
						st = st.Foreground(c)
					} else {
						st = st.Background(c)
					}
				}
				i += 4
			}
		}
	}
	return st
}
