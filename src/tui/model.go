package tui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/config"
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
	theme      config.Theme
	palette    config.Palette
	ui         config.UI
	styles     Styles
	rows       []core.Row
	width      int
	height     int
	job        string
	progress   core.Progress
	progressOn bool
}

// New builds the model. bus is the progress snapshot source (nil in
// tests: the progress bar falls back to event payloads). The theme
// data resolves into the render style set at construction; a
// ConfigChanged{Section: "theme"} event re-resolves it (variant
// switches re-render live).
func New(view *core.View, ch <-chan core.Event, keys map[string]string, tagActions map[string]string, bus *core.Bus, theme config.Theme, palette config.Palette, ui config.UI) Model {
	return Model{view: view, ch: ch, bus: bus, keys: keys, tagActions: tagActions, theme: theme, palette: palette, ui: ui, styles: ResolveStyles(theme, palette)}
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
	case core.ConfigChanged:
		m.onConfig(msg)
	case EventMsg:
		switch e := msg.Event.(type) {
		case core.ConfigChanged:
			m.onConfig(e)
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

// onConfig re-resolves the render styles when the theme section
// changes: variant switches land as ConfigChanged on the bus (wrapped
// in EventMsg by the bridge) or as a direct message.
func (m *Model) onConfig(e core.ConfigChanged) {
	if e.Section == "theme" {
		m.styles = ResolveStyles(m.theme, m.palette)
	}
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
	if id := rows[idx].Msg.ID; id != "" {
		m.view.SetCursor(id)
	} else {
		// stub row (search summary, no message id): anchor by index so
		// the cursor tracks through it; the viewport hydrate replaces
		// the stub with the real message and re-anchors by id
		m.view.SetCursorIndex(idx)
	}
}

// stage runs a tag action on the cursor row (R14). A tag in any
// tag group is a folder tag and stages +tag - exclusive-group
// resolution dedups at render/apply; a tag in no group is soft (unread
// is canonical) and toggles from the applied state. Ghost rows are
// guarded like the M1 cursor keys. The staged identity is the row's
// message id, or the thread identity for summary rows (search
// summaries carry no message id): a tag op on a summary is a
// thread-level op - apply emits thread:<id>, notmuch's natural unit.
func (m *Model) stage(action string) {
	tag, ok := m.tagActions[action]
	if !ok {
		return
	}
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	identity := row.Msg.ID
	if identity == "" {
		identity = "t:" + row.ThreadID
	}
	add := true
	if !inGroup(tag, m.view.Groups()) {
		add = !slices.Contains(m.view.Tags(identity), tag)
	}
	m.view.Stage(identity, core.TagOp{Tag: tag, Add: add})
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

// undo discards the cursor row's staged ops (R14): pure buffer
// drop, no DB traffic. Ghost rows are guarded like the M1 cursor keys.
func (m *Model) undo() {
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return
	}
	identity := row.Msg.ID
	if identity == "" {
		identity = "t:" + row.ThreadID
	}
	m.view.Undo(identity)
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
	if row.Msg.ID != "" {
		for i, r := range m.rows {
			if r.Msg == nil {
				continue
			}
			if r.Msg.ID == row.Msg.ID {
				return i
			}
		}
	}
	// stub rows carry no message id: the view's last row index is the
	// anchor (CursorRow's fallback)
	idx := m.view.CursorRowIndex()
	if idx >= len(m.rows) {
		idx = len(m.rows) - 1
	}
	if idx < 0 {
		return 0
	}
	return idx
}

func (m Model) View() string {
	if m.rows == nil {
		m.rows = m.view.Rows()
	}
	st := m.styles
	rows := m.rows
	if len(rows) == 0 {
		if m.progressOn {
			return "empty\n" + m.statusLineWith(st, m.ui) + "\n"
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
		line := renderRow(i+1, rows[i], st, m.ui)
		outer := st.Normal
		if i == cur {
			outer = st.Indicator
		} else if rows[i].Ghost {
			outer = st.Index.Ghost
		}
		if rows[i].Staged {
			// staged rows keep the row style and gain the staged look
			// ([index.staged] default: bold + muted fg); the slot styles
			// only override fg, so bold carries through the whole line
			outer = st.Index.Staged.Inherit(outer)
		}
		if m.width > 0 {
			// bubbletea's first View() runs before WindowSizeMsg: width 0
			// must not blank the rows (padRow would truncate them away)
			line = padRow(line, m.width, outer)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(m.statusLineWith(st, m.ui))
	b.WriteByte('\n')
	return b.String()
}

// statusLineWith builds the status data from the model's view and
// progress state and renders the row at the window width.
func (m Model) statusLineWith(st Styles, ui config.UI) string {
	return statusLineWidth(st, ui, m.statusData(), m.width)
}

func (m Model) statusData() statusData {
	d := statusData{view: m.view.Name, visible: len(m.rows), on: m.progressOn}
	if m.progressOn {
		p := m.progress
		d.prog = &p
	}
	return d
}
