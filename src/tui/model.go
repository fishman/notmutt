package tui

import (
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// Actions is the BUILTIN action vocabulary (R9): cursor movement, quit,
// open, and the buffer/apply ops. Tag actions are NOT in here - they
// come from the [tag-actions] config map; the app validates every
// binding value against both at startup (unknown action = load error).
var Actions = map[string]bool{
	"cursor-down": true, "cursor-up": true, "quit": true,
	"undo": true, "apply": true, "open": true,
}

type Model struct {
	view       *core.View
	ch         <-chan core.Event
	bus        *core.Bus
	bindings   map[string]map[string]string
	tagActions map[string]string
	st         *config.Store
	ui         config.UI
	styles     Styles
	rows       []core.Row
	width      int
	height     int
	mode       string // "index" default; "pager" while a thread is open
	pager      *pager
	job        string
	progress   core.Progress
	progressOn bool
}

// New builds the model. bus is the progress snapshot source (nil in
// tests: the progress bar falls back to event payloads). bindings is
// the per-context key table (R9); keys dispatch against the current
// mode's table. The theme resolves into the render style set at
// construction from the store's current config; a ConfigChanged{Section:
// "theme"} event re-reads the store and re-resolves it (variant
// switches re-render live).
func New(view *core.View, ch <-chan core.Event, bindings map[string]map[string]string, tagActions map[string]string, bus *core.Bus, st *config.Store, ui config.UI) Model {
	cfg := st.Config()
	return Model{view: view, ch: ch, bus: bus, bindings: bindings, tagActions: tagActions, st: st, ui: ui, styles: ResolveStyles(cfg.Theme, cfg.Palette), mode: "index"}
}

func (m Model) Init() tea.Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.mode == "pager" && m.pager != nil {
			// the status row and the keyhint placeholder row sit below
			// the pager window (Task 4 fills the placeholder)
			m.pager.setSize(m.width, m.height-2)
		}
	case tea.KeyMsg:
		km := m.bindings[m.mode]
		if km == nil {
			km = m.bindings["index"]
		}
		switch action := actionForKey(msg, km); action {
		case "cursor-down":
			m.moveCursor(1)
		case "cursor-up":
			m.moveCursor(-1)
		case "open":
			if m.mode == "index" {
				m.openCursorThread()
			}
		case "quit":
			return m, tea.Quit
		case "undo":
			m.undo()
		case "apply":
			onApply()
		case "scroll-down":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.LineDown(1)
			}
		case "scroll-up":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.LineUp(1)
			}
		case "page-down":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.HalfPageDown()
			}
		case "page-up":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.HalfPageUp()
			}
		case "scroll-top":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.GotoTop()
			}
		case "scroll-bottom":
			if m.mode == "pager" && m.pager != nil {
				m.pager.vp.GotoBottom()
			}
		case "back":
			if m.mode == "pager" {
				m.mode = "index"
			}
		default:
			m.stage(action)
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
		case core.ThreadLoaded:
			m.onThreadLoaded(e)
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
// changes. The event only names the section - the store owns the
// config, so the model re-reads it: SetThemeVariant mutates the
// store's internal config, and this re-read is what makes the switch
// live. Events arrive wrapped in EventMsg by the bridge or as a
// direct message.
func (m *Model) onConfig(e core.ConfigChanged) {
	if e.Section == "theme" {
		cfg := m.st.Config()
		m.styles = ResolveStyles(cfg.Theme, cfg.Palette)
	}
}

// onThreadLoaded fills the pager from the worker's thread messages and
// switches to pager mode. A failed load falls back to index and drops
// the pager (a stale pager would serve old content on a later reload).
// The thread-id guard makes a repeated load of the already-open thread
// a no-op (idempotent handler): content and scroll position survive.
func (m *Model) onThreadLoaded(e core.ThreadLoaded) {
	if e.Err != nil {
		m.mode, m.pager = "index", nil
		return
	}
	if e.ThreadID != pagerThreadID(m.pager) {
		lines, err := mail.RenderThread(e.Msgs)
		if err != nil {
			m.mode, m.pager = "index", nil
			return
		}
		m.pager = newPager(e.ThreadID, lines)
		if m.width > 0 {
			m.pager.setSize(m.width, m.height-2)
		}
		// populate the scroll window now - render is lazy in View, and
		// a scroll key before the first repaint must not clamp to an
		// empty window
		m.pager.render(m.styles)
	}
	m.mode = "pager"
}

// openCursorThread hands the cursor row's thread to the open seam (the
// app loads it and publishes ThreadLoaded). Ghost and stub rows carry
// the thread id in the row itself; the message fallback covers rows
// built before the thread id landed on them.
func (m *Model) openCursorThread() {
	row, ok := m.view.CursorRow()
	if !ok {
		return
	}
	tid := row.ThreadID
	if tid == "" && row.Msg != nil {
		tid = row.Msg.ThreadID
	}
	if tid != "" {
		onOpen(tid)
	}
}

// actionForKey resolves the pressed key: runes first (plain keys),
// then BubbleTea's canonical name ("ctrl+n", "alt+v", ...) so control
// keys are bindable.
func actionForKey(msg tea.KeyMsg, km map[string]string) string {
	if a, ok := km[string(msg.Runes)]; ok {
		return a
	}
	return km[msg.String()]
}

func pagerThreadID(p *pager) string {
	if p == nil {
		return ""
	}
	return p.threadID
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
	if m.mode == "pager" && m.pager != nil {
		var b strings.Builder
		b.WriteString(m.pager.render(m.styles))
		b.WriteString("\n") // keyhint placeholder row (Task 4 fills it)
		b.WriteString(m.statusLineWith(m.styles, m.ui))
		b.WriteByte('\n')
		return b.String()
	}
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
