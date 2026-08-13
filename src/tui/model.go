package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

type Model struct {
	view   *core.View
	ch     <-chan core.Event
	rows   []core.Row
	width  int
	height int
}

func New(view *core.View, ch <-chan core.Event) Model {
	return Model{view: view, ch: ch}
}

func (m Model) Init() tea.Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch string(msg.Runes) {
		case "j":
			m.moveCursor(1)
		case "k":
			m.moveCursor(-1)
		case "q":
			return m, tea.Quit
		case "t":
			m.toggleRead()
		}
	case EventMsg:
		m.rows = m.view.Rows()
		return m, EventCmd(m.ch)
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	rows := m.view.Rows()
	m.rows = rows
	if len(rows) == 0 {
		return
	}
	idx := m.CursorIndex()
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	if rows[idx].Msg == nil {
		// ghost rows are pass-through: walk in the move direction to the
		// nearest real message; at a boundary, do not move
		for {
			idx += delta
			if idx < 0 || idx >= len(rows) {
				return
			}
			if rows[idx].Msg != nil {
				break
			}
		}
	}
	m.view.SetCursor(rows[idx].Msg.ID)
}

func (m *Model) toggleRead() {
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	has := false
	for _, t := range row.Msg.Tags {
		if t == "unread" {
			has = true
		}
	}
	// optimistic local flip; the refresh cycle converges from DB truth
	if has {
		row.Msg.Tags = removeTag(row.Msg.Tags, "unread")
	} else {
		row.Msg.Tags = append(row.Msg.Tags, "unread")
	}
	onTagOp(row.Msg.ID, !has)
}

func removeTag(tags []string, tag string) []string {
	out := tags[:0]
	for _, t := range tags {
		if t != tag {
			out = append(out, t)
		}
	}
	return out
}

func (m Model) CursorIndex() int {
	row, ok := m.view.CursorRow()
	if !ok {
		return 0
	}
	if row.Msg == nil {
		// cursor anchored on a ghost row (fresh view, empty cursor id):
		// its position is the thread's first row
		for i, r := range m.rows {
			if r.Ghost && r.ThreadID == row.ThreadID {
				return i
			}
		}
		return 0
	}
	for i, r := range m.rows {
		if r.Msg == nil {
			continue
		}
		if r.Msg.ID == row.Msg.ID {
			return i
		}
	}
	return 0
}

func (m Model) View() string {
	if m.rows == nil {
		m.rows = m.view.Rows()
	}
	rows := m.rows
	if len(rows) == 0 {
		return "empty\n"
	}
	cur := m.CursorIndex()
	top := cur - m.height/2
	if top < 0 {
		top = 0
	}
	bottom := top + m.height
	if bottom > len(rows) {
		bottom = len(rows)
		top = bottom - m.height
		if top < 0 {
			top = 0
		}
	}
	var b strings.Builder
	for i := top; i < bottom; i++ {
		line := renderRow(i+1, rows[i])
		if i == cur {
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// renderRow renders the fixed-slot template (R11): number, flags,
// attachment, date, author, subject, tags. Optional slots reserve width.
func renderRow(n int, row core.Row) string {
	var b strings.Builder
	b.WriteString(padCellsRight(strconv.Itoa(n), 4))
	b.WriteByte(' ')
	if row.Msg == nil {
		// ghost root: message-derived slots stay blank, "[...]" fills the
		// subject slot so the template stays aligned
		b.WriteString(padCellsRight("", 3))
		b.WriteString(" ")
		b.WriteByte(' ')
		b.WriteString(padCellsRight("", 15))
		b.WriteByte(' ')
		b.WriteString(padCellsRight("", 16))
		b.WriteByte(' ')
		b.WriteString(truncCells("[...] "+strconv.Itoa(row.Count), 40))
		return b.String()
	}
	b.WriteString(flags(row.Msg))
	b.WriteString(attachIcon(row.Msg))
	b.WriteByte(' ')
	b.WriteString(padCellsRight(formatDate(row.Msg.Timestamp), 15))
	b.WriteByte(' ')
	b.WriteString(padCellsRight(truncCells(row.Msg.Author, 16), 16))
	b.WriteByte(' ')
	b.WriteString(truncCells(row.Msg.Subject, 40))
	b.WriteByte(' ')
	b.WriteString(tagGlyphs(row.Msg))
	return b.String()
}

func flags(m *core.Message) string {
	var f strings.Builder
	for _, t := range m.Tags {
		switch t {
		case "unread":
			f.WriteByte('U')
		case "replied":
			f.WriteByte('R')
		case "forwarded":
			f.WriteByte('F')
		case "deleted":
			f.WriteByte('D')
		}
	}
	return padCellsRight(f.String(), 3)
}

func attachIcon(m *core.Message) string {
	if len(m.Atts) > 0 {
		return "A"
	}
	return " "
}

func formatDate(ts int64) string {
	return time.Unix(ts, 0).Format("06/01/02 15:04")
}

func tagGlyphs(m *core.Message) string {
	// max 2 tags, first two of the message's own order; the tag-groups
	// slice supplies the priority list later (spec section 6)
	var b strings.Builder
	n := 0
	for _, t := range m.Tags {
		if t == "unread" {
			continue
		}
		if n >= 2 {
			break
		}
		b.WriteString(padCellsRight(truncCells(t, 4), 4))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
}

// truncCells truncates s to at most w terminal cells; padCellsRight pads
// it to exactly w cells (wcwidth, not runes).
func truncCells(s string, w int) string {
	var b strings.Builder
	cells := 0
	for _, r := range s {
		cw := runewidth.RuneWidth(r)
		if cells+cw > w {
			break
		}
		b.WriteRune(r)
		cells += cw
	}
	return b.String()
}

func padCellsRight(s string, w int) string {
	cells := runewidth.StringWidth(s)
	if cells >= w {
		return truncCells(s, w)
	}
	return s + strings.Repeat(" ", w-cells)
}
