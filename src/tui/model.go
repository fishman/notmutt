package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// Actions is the BUILTIN action vocabulary (R9): cursor movement, quit,
// and the buffer/apply ops. Tag actions are NOT in here - they come
// from the [tag-actions] config map; the app validates every binding
// value against both at startup (unknown action = load error).
var Actions = map[string]bool{
	"cursor-down": true, "cursor-up": true, "quit": true,
	"undo": true, "apply": true,
}

type Model struct {
	view       *core.View
	ch         <-chan core.Event
	bus        *core.Bus
	keys       map[string]string
	tagActions map[string]string
	rows       []core.Row
	width      int
	height     int
	job        string
	progress   core.Progress
	progressOn bool
}

// New builds the model. bus is the progress snapshot source (nil in
// tests: the progress bar falls back to event payloads).
func New(view *core.View, ch <-chan core.Event, keys map[string]string, tagActions map[string]string, bus *core.Bus) Model {
	return Model{view: view, ch: ch, bus: bus, keys: keys, tagActions: tagActions}
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
		case "undo":
			m.undo()
		case "apply":
			onApply()
		default:
			m.stage(m.keys[string(msg.Runes)])
		}
	case EventMsg:
		switch e := msg.Event.(type) {
		case core.Progress:
			m.job = e.Job
			if m.bus == nil {
				m.progress = e
				m.progressOn = e.Done < e.Total
			}
		}
		m.refreshProgress()
		m.rows = m.view.Rows()
		if m.progressOn {
			return m, tea.Batch(EventCmd(m.ch), progressTickCmd())
		}
		return m, EventCmd(m.ch)
	case progressTick:
		m.refreshProgress()
		if m.progressOn {
			return m, progressTickCmd()
		}
		return m, nil
	}
	return m, nil
}

// refreshProgress re-reads the bus snapshot for the current job and
// virtual folder - progress is scoped per view, so switching views
// shows that view's bar (or none when it is idle). The snapshot write
// never drops, so a completion event dropped from the channel still
// clears the bar on the next tick/event (the stuck-bar failure mode:
// backpressure swallowed the tail of a burst).
func (m *Model) refreshProgress() {
	if m.bus == nil {
		return
	}
	if p, ok := m.bus.LatestProgress(m.job, m.view.Name); ok {
		m.progress = p
		m.progressOn = p.Done < p.Total
	}
}

type progressTick struct{}

const progressTickInterval = 200 * time.Millisecond

func progressTickCmd() tea.Cmd {
	return tea.Tick(progressTickInterval, func(time.Time) tea.Msg { return progressTick{} })
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

// stage runs a tag action on the cursor message (R14). A tag in any
// tag group is a folder tag and stages +tag - exclusive-group
// resolution dedups at render/apply; a tag in no group is soft (unread
// is canonical) and toggles from the applied state. Ghost rows are
// guarded like the M1 cursor keys.
func (m *Model) stage(action string) {
	tag, ok := m.tagActions[action]
	if !ok {
		return
	}
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	id := row.Msg.ID
	add := true
	if !inGroup(tag, m.view.Groups()) {
		add = !slices.Contains(m.view.MsgTags(id), tag)
	}
	m.view.Stage(id, core.TagOp{Tag: tag, Add: add})
	m.rows = m.view.Rows()
}

func inGroup(tag string, groups []core.TagGroup) bool {
	for _, g := range groups {
		if slices.Contains(g.Tags, tag) {
			return true
		}
	}
	return false
}

// undo discards the cursor message's staged ops (R14): pure buffer
// drop, no DB traffic. Ghost rows are guarded like the M1 cursor keys.
func (m *Model) undo() {
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	m.view.Undo(row.Msg.ID)
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
		if m.progressOn {
			return "empty\n" + m.statusLine() + "\n"
		}
		return "empty\n"
	}
	cur := m.CursorIndex()
	// the bottom row is the status line (R15); the list window is
	// height-1, the R11 slot-reservation rule
	listHeight := m.height - 1
	if listHeight < 1 {
		listHeight = 1
	}
	top := cur - listHeight/2
	if top < 0 {
		top = 0
	}
	bottom := top + listHeight
	if bottom > len(rows) {
		bottom = len(rows)
		top = bottom - listHeight
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
	b.WriteString(m.statusLine())
	b.WriteByte('\n')
	return b.String()
}

const progressWidth = 40

// statusLine is the bottom row: view name + visible count on the left,
// the async progress bar right-aligned in a fixed-width region (R15).
// Completion (Done == Total) clears the bar; labels are job-kind
// derived, never mail content (F6). The `progress` style identifier and
// the filled-cell glyph (default "#") are hardcoded defaults until the
// theming milestone.
func (m Model) statusLine() string {
	left := fmt.Sprintf("%s %d", m.view.Name, len(m.rows))
	if !m.progressOn {
		return left
	}
	label := fmt.Sprintf("%s %d/%d", m.progress.Job, m.progress.Done, m.progress.Total)
	fill := progressWidth - runewidth.StringWidth(label) - 1
	if fill < 0 {
		fill = 0
	}
	right := label + " " + progressBar(m.progress, fill)
	if pad := m.width - runewidth.StringWidth(left) - progressWidth; pad > 0 {
		return left + strings.Repeat(" ", pad) + right
	}
	return left
}

func progressBar(p core.Progress, cells int) string {
	if cells < 0 {
		return ""
	}
	fill := 0
	if p.Total > 0 && p.Done < p.Total {
		fill = int(float64(p.Done) * float64(cells) / float64(p.Total))
	}
	return strings.Repeat("#", fill) + strings.Repeat("-", cells-fill)
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
