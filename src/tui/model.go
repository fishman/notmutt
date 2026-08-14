package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// Actions is the index-context action vocabulary (R9): every binding
// value must be one of these; the app validates the loaded bindings
// against it at startup (unknown action = load error).
var Actions = map[string]bool{
	"cursor-down": true, "cursor-up": true, "quit": true,
	"toggle-read": true, "archive": true, "delete": true,
	"undo": true, "apply": true,
}

type Model struct {
	view   *core.View
	ch     <-chan core.Event
	keys   map[string]string
	rows   []core.Row
	width  int
	height int
}

func New(view *core.View, ch <-chan core.Event, keys map[string]string) Model {
	return Model{view: view, ch: ch, keys: keys}
}

func (m Model) Init() tea.Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch m.keys[string(msg.Runes)] {
		case "cursor-down":
			m.moveCursor(1)
		case "cursor-up":
			m.moveCursor(-1)
		case "quit":
			return m, tea.Quit
		case "toggle-read", "archive", "delete", "undo":
			m.stage(m.keys[string(msg.Runes)])
		case "apply":
			onApply()
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

// stage routes the staging actions (R14): toggle-read flips from the
// applied state, archive/delete stage +tag, undo discards. Ghost rows
// are guarded like the M1 cursor keys.
func (m *Model) stage(action string) {
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	id := row.Msg.ID
	switch action {
	case "toggle-read":
		has := false
		for _, t := range m.view.MsgTags(id) {
			if t == "unread" {
				has = true
			}
		}
		m.view.Stage(id, core.TagOp{Tag: "unread", Add: !has})
	case "archive":
		m.view.Stage(id, core.TagOp{Tag: "archive", Add: true})
	case "delete":
		m.view.Stage(id, core.TagOp{Tag: "deleted", Add: true})
	case "undo":
		m.view.Undo(id)
	}
	m.rows = m.view.Rows()
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
		if rows[i].Staged {
			line = "\x1b[1m" + line + "\x1b[0m" // [index.staged] default: bold
		}
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
	tags := row.Msg.Tags
	flagStr := flags(tags)
	if row.Staged {
		// staged rows render the resolved display tags with the staged
		// glyph (default "*", config data per R11 tag-transforms; the
		// hardcoded default holds until the theming milestone)
		tags = row.StagedTags
		flagStr = padCellsRight("*"+flagChars(tags), 3)
	}
	b.WriteString(flagStr)
	b.WriteString(attachIcon(row.Msg))
	b.WriteByte(' ')
	b.WriteString(padCellsRight(formatDate(row.Msg.Timestamp), 15))
	b.WriteByte(' ')
	author := stripControls(row.Msg.Author)
	b.WriteString(padCellsRight(truncCells(author, 16), 16))
	b.WriteByte(' ')
	subject := stripControls(row.Msg.Subject)
	b.WriteString(truncCells(subject, 40))
	b.WriteByte(' ')
	b.WriteString(tagGlyphs(tags))
	return b.String()
}

func flags(tags []string) string {
	return padCellsRight(flagChars(tags), 3)
}

func flagChars(tags []string) string {
	var f strings.Builder
	for _, t := range tags {
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
	return f.String()
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

func tagGlyphs(tags []string) string {
	// max 2 tags, first two of the display order; the tag-groups
	// priority list supplies the order later (spec section 6)
	var b strings.Builder
	n := 0
	for _, t := range tags {
		if t == "unread" {
			continue
		}
		if n >= 2 {
			break
		}
		b.WriteString(padCellsRight(truncCells(stripControls(t), 4), 4))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
}

// stripControls drops C0/DEL/C1 control runes so mail content can never
// inject terminal escapes (F1).
func stripControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7F && r <= 0x9F) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
	t := truncCells(s, w)
	return t + strings.Repeat(" ", w-runewidth.StringWidth(t))
}
