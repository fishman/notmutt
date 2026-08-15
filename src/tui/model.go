package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
)

// chainTimeout expires an armed multi-key prefix: a stray first key
// never mis-sequences a later press. Tests shrink it to 0.
var chainTimeout = time.Second

// frameInterval is the fixed paint cadence: a navigation defers its
// paint (ShouldRender false) and the frame tick lands it one interval
// later, so a hold paints at most once per interval no matter how
// fast the terminal repeats. 8ms (120 paints/sec) - the per-paint
// cost after the SGR precompute is ~1ms, so the cadence stays
// comfortably under the render budget.
var frameInterval = 8 * time.Millisecond

// Actions is the BUILTIN action vocabulary per context (R9): the index
// context carries navigation (including the gg/G edge jumps), open, the
// buffer/apply ops, the compose/reply/forward entry points, and the tab
// keys; the pager context the scroll and back/quit surface plus the tab
// keys; the compose context the dialogue keys (R4); the fuzzy context
// the picker keys. Tag actions are NOT in here - they come from the
// [tag-actions] config map; the app validates every binding value
// against its context's map at startup (unknown action = load error).
var Actions = map[string]map[string]bool{
	"index": {
		"cursor-down": true, "cursor-up": true,
		"cursor-top": true, "cursor-bottom": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"open": true, "preview": true, "quit": true, "undo": true, "apply": true,
		"reply": true, "reply-all": true, "forward": true, "compose": true,
		"tab-prev": true, "tab-next": true,
		"help": true,
	},
	"pager": {
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"back": true, "quit": true,
		"tab-prev": true, "tab-next": true,
		"help": true,
	},
	"compose": {
		"form-down": true, "form-up": true,
		"edit": true, "attach": true, "detach": true,
		"edit-from": true, "edit-to": true, "edit-subject": true,
		"account": true, "signature": true,
		"send": true, "abort": true,
		"tab-prev": true, "tab-next": true,
		"help": true,
	},
	"fuzzy": {
		"fuzzy-down": true, "fuzzy-up": true,
		"fuzzy-select": true, "fuzzy-cancel": true,
	},
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
	// indexOffset is the index window's anchored top row (the
	// read-position model): the window holds
	// still while the cursor moves within it; only when the cursor
	// crosses a page edge does the window jump a full page.
	indexOffset int
	// cursorID mirrors the view's cursor id: the view's CursorRow
	// flattens the whole thread tree per call (the page-key stall at
	// 33k rows), so moves resolve against the cached row list instead.
	cursorID string
	// legend is the debounced status-line icon library (the current
	// message's tag icons): every cursor move clears it and arms the
	// debounce, so it only resolves after the cursor rests - never
	// during movement or inside a render. Resolution itself is cheap
	// (a scan over the cached rows, no view flatten); the debounce is
	// what keeps the row still while the cursor walks. account resolves
	// in the same settle: the account tag of the rested-on message
	// (R2), shown as the status-bar account segment.
	legend        string
	account       string
	legendPending bool
	// legendTickOn gates the debounce to a single tick in flight: a
	// keypress arms one only when none is scheduled, and the tick's
	// own re-arm keeps the chain alive while the cursor keeps moving -
	// holding a key never piles timers up.
	legendTickOn bool
	// keyReleases records whether the terminal answers the
	// ReportEventTypes request with release reporting (the
	// KeyboardEnhancementsMsg). While true, movement never arms the
	// legend tick - the real KeyReleaseMsg resolves the legend, so the
	// hold-time tick churn (80-100 extra render cycles/sec) is pure
	// waste. False until the terminal answers, safe for tests.
	keyReleases bool
	// paint is the ShouldRender gate's state: a navigation defers its
	// paint (false) and the frame tick turns it back on one
	// frameInterval later, so the tea loop skips every intermediate
	// render (one paint per frame window, not one per keypress).
	// Every other message paints immediately.
	paint bool
	// renderDue is a deferred paint waiting on the frame tick.
	renderDue bool
	// frameTickOn gates the frame tick to a single one in flight:
	// repeated navigations inside one interval never pile timers up
	// (the legendTickOn pattern).
	frameTickOn bool
	// legendMoves counts cursor moves: the tick carries the count from
	// when it was armed and resolves only when it matches - a tick that
	// finds newer moves re-arms, so the legend settles one debounce
	// window after the last press (the keyup moment), never mid-hold.
	legendMoves int
	// accountTags is the account tag set (config.AccountTags): the row
	// render skips these tags (the account lives in the status bar, not
	// the mail title) and the account resolution scans against it.
	accountTags map[string]bool
	// vim-style prefixes (R9 data-first): digit keys accumulate into
	// count (a bound digit wins), and an unbound key can arm a
	// multi-key chain (space-joined data keys - "g g", "g r") that
	// expires after chainTimeout. Both engage only when the active
	// context does NOT bind the key.
	count         string
	pendingPrefix string
	pendingAt     time.Time
	// compose tabs: the dialogue stack (R4). tabIdx 0 = the mail
	// surface (index/pager); tabIdx > 0 = tabs[tabIdx-1] attached as
	// the compose dialogue. Stepping off a dialogue parks it - state
	// intact - while the mail surface keeps working; stepping back
	// re-attaches it (spec section 5: the dialogue IS the tab).
	tabs   []compose.State
	tabIdx int
	// formIdx is the compose form cursor slot: 0 = account,
	// 1-4 = From/To/Cc/Subject, 5+i = attachment i (d detaches there).
	formIdx int
	// formView scrolls the compose form (the pager widget): when the
	// rows outgrow the frame, the window follows the cursor. A pointer
	// like the layers - the program holds the model by value, so
	// render-time writes persist only through reference fields.
	formView *viewport
	// fuzzy is the selector popup (account/signature); non-nil
	// renders the popup frame and captures the fuzzy context.
	fuzzy *fuzzy
	// help is the ? overlay (a viewport over the binding rows - the
	// pager widget): the pager scroll keys navigate it, any other
	// keypress closes it (the check runs before dispatch, so the
	// closing key never fires).
	help     bool
	helpView viewport
	// preview is the preview popup (the p key): the thread loads
	// WITHOUT the read-marking, the box overlays the index, and the
	// cursor stays put. previewThread is the load's guard - a stale
	// preview reply (closed or re-targeted meanwhile) drops in
	// onThreadLoaded; previewTitle is the popup title (the cursor
	// row's subject, captured at press time).
	preview       bool
	previewThread string
	previewTitle  string
	// prompt is the compose input row: the attach path prompt and the
	// inline field editors (edit-from/to/subject); non-nil captures
	// the prompt keys and replaces the compose keyhint row.
	prompt *formPrompt
	// opened tracks every attached dialogue's TabID: the bus's
	// ComposeOpened snapshot re-attaches only never-seen IDs, so a
	// closed dialogue can never resurrect on a later keypress.
	opened map[string]bool
	// render caches: the row cache (styled index rows, content-addressed
	// by rowKey) and the region layers (keyhint, status, help). The
	// layers are pointers - the program holds the model by value, so
	// render-time writes persist only through reference fields.
	rowCache    map[rowKey]string
	hintLayer   *layer
	statusLayer *layer
	helpLayer   *layer
	// styleVer bumps when the theme re-resolves: every cache key carries
	// it, so a variant switch invalidates at the next render.
	styleVer int
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
	return Model{view: view, ch: ch, bus: bus, bindings: bindings, tagActions: tagActions, st: st, ui: ui, styles: ResolveStyles(cfg.Theme, cfg.Palette), accountTags: cfg.AccountTags(), opened: map[string]bool{}, mode: "index", rowCache: map[rowKey]string{}, hintLayer: &layer{}, statusLayer: &layer{}, helpLayer: &layer{}, formView: &viewport{}, styleVer: 1}
}

func (m Model) Init() tea.Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// the bus keeps last-value snapshots of the compose events (the
	// LatestProgress pattern, R15): a completion dropped from the
	// channel under backpressure still resolves the dialogue on the
	// next keypress instead of wedging it in PhaseSending
	if m.bus != nil {
		for i := range m.tabs {
			if m.tabs[i].Phase == compose.PhaseSending {
				if e, ok := m.bus.LatestSendResult(m.tabs[i].ID); ok {
					m.onSendResult(e)
				}
			}
		}
		if e, ok := m.bus.LatestComposeOpened(); ok && !m.opened[e.TabID] {
			m.onComposeOpened(e)
		}
	}
	// every message paints except the navigation deferrals below (they
	// set paint false and let the frame tick re-arm it); the frameTick
	// case itself overrides this after the fact
	m.paint = true
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.pager != nil {
			// the keyhint bar (R9) and the status row sit below the
			// pager window (height-2). Re-size and re-style even in
			// index mode: a resize between close and re-open must not
			// leave the window at the old width (the re-open guard
			// skips the re-render). The preview popup sizes its pager
			// to the box's content area instead (pagerSize).
			w, h := m.pagerSize()
			m.pager.setSize(w, h, m.styles)
		}
		if m.help {
			h := m.height - 3
			if h < 1 {
				h = 1
			}
			m.helpView.setSize(m.width, h)
		}
	case tea.KeyPressMsg:
		// the picker outranks the prompt: the attach '?' picker arms the
		// attach prompt (input = "@name"), so both can be live at once
		if m.fuzzy != nil {
			m.fuzzyKey(msg, m.bindings["fuzzy"])
			return m, nil
		}
		if m.prompt != nil {
			return m, m.promptKey(msg)
		}
		if m.help {
			// the help surface borrows the pager keys (neomutt renders
			// the help page in a pager): the scroll keys drive the
			// help's viewport, anything else closes without firing
			switch actionForKey(msg, m.bindings["pager"]) {
			case "scroll-down":
				m.helpView.scrollDown(1)
			case "scroll-up":
				m.helpView.scrollUp(1)
			case "scroll-top":
				m.helpView.scrollTop()
			case "scroll-bottom":
				m.helpView.scrollBottom()
			case "page-down":
				m.helpView.pageDown()
			case "page-up":
				m.helpView.pageUp()
			case "half-page-down":
				m.helpView.halfPageDown()
			case "half-page-up":
				m.helpView.halfPageUp()
			default:
				m.help = false
				m.helpView = viewport{}
			}
			return m, nil
		}
		if m.preview {
			// the popup captures the keys: the pager scroll keys
			// scroll the box, o promotes to a full open, anything else
			// closes
			return m.previewKey(msg)
		}
		km := m.activeBindings()
		// vim-style prefixes (R9 data-first): digits accumulate a
		// count; an unbound key can arm a multi-key chain (space-
		// joined data keys) that expires after chainTimeout. A bound
		// key wins over the prefix - the prefix only engages on keys
		// the context leaves unbound. A counted "g" keeps its jump
		// semantic (12g = row 12) - the chain data never sees it.
		r := msg.Text
		if km[r] == "" && len(r) == 1 && r[0] >= '0' && r[0] <= '9' {
			m.pendingPrefix = ""
			m.count += r
			return m, nil
		}
		if km[r] == "" && r == "g" && m.count != "" {
			// counted g: jump to the numbered row (consumes the
			// count either way - an unusable count is a no-op)
			n, _ := strconv.Atoi(m.count)
			m.count = ""
			m.pendingPrefix = ""
			if m.mode == "index" && n > 0 {
				m.gotoRow(n)
				// a counted jump defers its paint like any movement
				m.paint, m.renderDue = false, true
			}
			return m, m.armFrameTick()
		}
		n := 1
		if m.count != "" {
			n, _ = strconv.Atoi(m.count)
			m.count = ""
		}
		if m.pendingPrefix != "" {
			// a chain is armed: the next key completes, extends, or
			// kills it. An expired prefix dies like a dead key.
			if time.Since(m.pendingAt) >= chainTimeout {
				m.pendingPrefix = ""
				// a chain-starting key re-arms on the expired press
				// (an unbound key must not waste it on a dead
				// dispatch)
				if r != "" && km[r] == "" && chainContinuation(km, r) {
					m.pendingPrefix = r
					m.pendingAt = time.Now()
					return m, nil
				}
			} else {
				cand := m.pendingPrefix + " " + r
				m.pendingPrefix = ""
				switch {
				case r != "" && km[cand] != "":
					return m.dispatchAction(km[cand], n)
				case r != "" && chainContinuation(km, cand):
					m.pendingPrefix = cand
					m.pendingAt = time.Now()
					return m, nil
				}
				// dead key (or a special key): the chain dies and
				// the key dispatches normally below
			}
		} else if r != "" && km[r] == "" && chainContinuation(km, r) {
			m.pendingPrefix = r
			m.pendingAt = time.Now()
			return m, nil
		}
		return m.dispatchAction(actionForKey(msg, km), n)
	case tea.KeyReleaseMsg:
		// the real keyup (kitty keyboard protocol release reporting):
		// the legend resolves at the release, no debounce needed.
		// Terminals without release reporting never send this; the
		// legendTick fallback settles those the same way.
		if m.legendPending {
			m.legend, m.account = m.resolveStatus()
			m.legendPending = false
			m.legendTickOn = false
		}
		// the release paints immediately; the press's deferred paint
		// is settled by it, so the in-flight frame tick must not land
		// a second paint
		m.renderDue = false
		return m, nil
	case tea.KeyboardEnhancementsMsg:
		// the terminal's answer to the ReportEventTypes request (model
		// View): release reporting on means the KeyReleaseMsg handler
		// resolves the legend, so movement must never arm the debounce
		// tick. Terminals that do not answer keep keyReleases false and
		// the tick fallback.
		m.keyReleases = msg.SupportsEventTypes()
		return m, nil
	case editorDoneMsg:
		if msg.err == nil {
			for i := range m.tabs {
				if m.tabs[i].ID == msg.tabID {
					if st, err := applyEditorResult(m.tabs[i], msg.path); err == nil {
						m.tabs[i] = st
					}
					break
				}
			}
		}
		os.Remove(msg.path)
		return m, nil
	case attachCmdDoneMsg:
		if msg.err == nil {
			if data, err := os.ReadFile(msg.path); err == nil {
				for i := range m.tabs {
					if m.tabs[i].ID != msg.tabID {
						continue
					}
					for _, line := range strings.Split(string(data), "\n") {
						if line = strings.TrimSpace(line); line != "" {
							m.tabs[i].AddAttachment(compose.ExpandHome(line))
						}
					}
					break
				}
			}
		} else {
			for i := range m.tabs { // the tab may have closed meanwhile
				if m.tabs[i].ID == msg.tabID {
					m.prompt = &formPrompt{kind: "attach", label: "attach path: ", input: "@" + msg.name}
					break
				}
			}
		}
		os.Remove(msg.path)
		return m, nil
	case frameTick:
		// the deferred paint lands here at the fixed cadence; a tick
		// with nothing deferred (idle model) turns the gate off again
		// and dies - an idle model never renders on a timer
		m.frameTickOn = false
		if m.renderDue {
			m.renderDue = false
			m.paint = true
		} else {
			m.paint = false
		}
		return m, nil
	case legendTick:
		// fallback for terminals without release reporting: the tick
		// carries the move count from when it was armed - newer moves
		// re-arm the single in-flight tick, so the legend resolves one
		// debounce window after the last press
		if !m.legendPending {
			// the release path already resolved: no re-arm, no
			// duplicate work
			m.legendTickOn = false
			return m, nil
		}
		if m.legendMoves != msg.moves {
			return m, legendTickCmd(m.legendMoves)
		}
		m.legend, m.account = m.resolveStatus()
		m.legendPending = false
		m.legendTickOn = false
		return m, nil
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
		case core.ComposeOpened:
			m.onComposeOpened(e)
		case core.SendResult:
			m.onSendResult(e)
		}
		m.refreshProgress()
		m.rows = m.view.Rows()
		if m.legendPending && !m.legendTickOn && !m.keyReleases {
			m.legendTickOn = true
			return m, tea.Batch(EventCmd(m.ch), legendTickCmd(m.legendMoves))
		}
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

// dispatchAction runs a bound action with its count, then the
// legend-tick tail (the fall-through path). Actions with their own
// cmds (quit, edit) return them directly. Multi-key chains resolve
// here too - the chain machinery dispatches the completed chain's
// action, and "?" opens the help overlay.
func (m Model) dispatchAction(action string, n int) (tea.Model, tea.Cmd) {
	// navigation defers its paint to the frame tick: paint=false
	// gates the render (ShouldRender), renderDue lets the tick re-arm
	// it, and the tail arms a single tick
	deferPaint := func() { m.paint, m.renderDue = false, true }
	deferred := false
	switch action {
	case "cursor-down":
		m.moveCursor(n)
		deferPaint()
		deferred = true
	case "cursor-up":
		m.moveCursor(-n)
		deferPaint()
		deferred = true
	case "cursor-top":
		m.cursorTop()
		deferPaint()
		deferred = true
	case "cursor-bottom":
		m.cursorBottom()
		deferPaint()
		deferred = true
	case "open":
		if m.mode == "index" {
			m.openCursorThread()
		}
	case "preview":
		if m.mode == "index" {
			m.previewCursorThread()
		}
	case "quit":
		return m, tea.Quit
	case "undo":
		if m.undo() {
			m.moveCursor(1)
		}
	case "apply":
		onApply()
	case "scroll-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollDown(n)
			deferPaint()
			deferred = true
		}
	case "scroll-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollUp(n)
			deferPaint()
			deferred = true
		}
	case "page-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.pageDown()
		} else if m.mode == "index" {
			m.moveCursor(m.pageRows())
		}
		deferPaint()
		deferred = true
	case "page-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.pageUp()
		} else if m.mode == "index" {
			m.moveCursor(-m.pageRows())
		}
		deferPaint()
		deferred = true
	case "half-page-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.halfPageDown()
		} else if m.mode == "index" {
			m.moveCursor(m.pageRows() / 2)
		}
		deferPaint()
		deferred = true
	case "half-page-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.halfPageUp()
		} else if m.mode == "index" {
			m.moveCursor(-(m.pageRows() / 2))
		}
		deferPaint()
		deferred = true
	case "scroll-top":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollTop()
			deferPaint()
			deferred = true
		}
	case "scroll-bottom":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollBottom()
			deferPaint()
			deferred = true
		}
	case "back":
		if m.mode == "pager" {
			m.mode = "index"
		}
	case "reply":
		m.openReply("reply")
	case "reply-all":
		m.openReply("reply-all")
	case "forward":
		m.openReply("forward")
	case "compose":
		m.openReply("compose")
	case "tab-prev":
		m.tabPrev()
	case "tab-next":
		m.tabNext()
	case "form-down":
		if m.composeTab().Phase == compose.PhaseAborting {
			m.composeTab().Phase = compose.PhaseEditing
		}
		m.formIdx++
		if max := 5 + len(m.composeTab().Attachments); m.formIdx > max {
			m.formIdx = max
		}
		deferPaint()
		deferred = true
	case "form-up":
		if m.formIdx > 0 {
			m.formIdx--
		}
		deferPaint()
		deferred = true
	case "edit":
		// an in-flight edit's result is discarded when the send
		// completes - gate it like attach/detach
		if m.composeTab().Phase != compose.PhaseSending {
			if m.composeTab().Phase == compose.PhaseFailed {
				m.composeTab().Phase = compose.PhaseEditing
			}
			st := *m.composeTab()
			tabID := st.ID
			path, err := writeEditorBuffer(st)
			if err != nil {
				return m, nil
			}
			return m, tea.ExecProcess(editorCmd(path), func(err error) tea.Msg {
				return editorDoneMsg{err: err, path: path, tabID: tabID}
			})
		}
	case "attach":
		if m.composeTab().Phase != compose.PhaseSending {
			m.prompt = &formPrompt{kind: "attach", label: "attach path: "}
		}
	case "detach":
		t := m.composeTab()
		if t.Phase != compose.PhaseSending {
			if i := m.formIdx - 5; i >= 0 && i < len(t.Attachments) {
				t.Attachments = slices.Delete(t.Attachments, i, i+1)
			}
		}
	case "edit-to", "edit-subject", "edit-from":
		// the mutt field editors: t/s/f open an inline prompt
		// prefilled with the field's current value (the compose
		// body stays on e and the $EDITOR buffer)
		if m.composeTab().Phase != compose.PhaseSending {
			if m.composeTab().Phase == compose.PhaseFailed {
				m.composeTab().Phase = compose.PhaseEditing
			}
			st := m.composeTab()
			f := &formPrompt{kind: "field", field: strings.TrimPrefix(action, "edit-")}
			switch f.field {
			case "from":
				f.label, f.input = "From: ", st.From
			case "subject":
				f.label, f.input = "Subject: ", st.Subject
			case "to":
				f.label, f.input = "To: ", strings.Join(st.To, ", ")
			}
			m.prompt = f
		}
	case "account":
		m.openPicker("account")
	case "signature":
		m.openPicker("signature")
	case "send":
		// PhaseSending gates duplicate presses: one job in flight
		// (the detach/attach gates protect the shared Attachments
		// slice while sendJob's Assemble reads it). A retry after a
		// failure re-arms the gate on its first press.
		if m.composeTab().Phase != compose.PhaseSending {
			m.composeTab().Phase = compose.PhaseSending
			if m.bus != nil {
				// drop the old result snapshot: a stale failure
				// must not re-apply while the new job is in flight
				m.bus.ClearSendResult(m.composeTab().ID)
			}
			onSend(*m.composeTab())
		}
	case "abort":
		st := m.composeTab()
		switch st.Phase {
		case compose.PhaseSending:
			// never cancel an in-flight delivery; the tab closes when
			// the send result lands
		case compose.PhaseAborting:
			m.closeComposeTab(m.tabIdx - 1)
		default:
			st.Phase = compose.PhaseAborting
		}
	case "help":
		m.help = true
		h := m.height - 3
		if h < 1 {
			h = 1
		}
		m.helpView.setLines(m.helpRows())
		m.helpView.setSize(m.width, h)
	default:
		// staged tag ops (and undo) advance the cursor one row -
		// the next keypress acts on the next message (mutt's
		// auto-advance). A no-op action (ghost row, unknown action)
		// does not move.
		if m.stage(action) {
			m.moveCursor(1)
		}
	}
	var cmds []tea.Cmd
	if deferred {
		cmds = append(cmds, m.armFrameTick())
	}
	if m.legendPending && !m.legendTickOn && !m.keyReleases {
		m.legendTickOn = true
		cmds = append(cmds, legendTickCmd(m.legendMoves))
	}
	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

// armFrameTick starts the frame tick once; deferrals while one is in
// flight return no cmd (the single-in-flight pattern).
func (m *Model) armFrameTick() tea.Cmd {
	if !m.frameTickOn {
		m.frameTickOn = true
		return frameTickCmd()
	}
	return nil
}

// chainContinuation reports whether any binding key extends the
// prefix: the prefix can still become a complete chain, so the key
// arms instead of dispatching.
func chainContinuation(km map[string]string, prefix string) bool {
	for k := range km {
		if k != prefix && strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
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
		m.styleVer++
		if m.pager != nil {
			// the pager's render is cached - without re-styling here a
			// variant switch keeps the old colors until the next
			// resize or re-open (the preview box sizes the pager to
			// its content area, pagerSize)
			w, h := m.pagerSize()
			m.pager.setSize(w, h, m.styles)
		}
	}
}

// onThreadLoaded attaches the open job's render lines to the pager and
// switches to pager mode. Rendering and the render transforms already
// happened on the async open job - the model only attaches. A failed
// load falls back to index and drops the pager (a stale pager would
// serve old content on a later reload). The thread-id guard makes a
// repeated load of the already-open thread a no-op (idempotent
// handler): content and scroll position survive. A PREVIEW reply only
// fills the box when the model still targets that thread - a stale
// preview (closed or re-targeted meanwhile) drops silently - and the
// index surface stays put (mode is re-forced to index in case a racing
// full-open reply flipped it meanwhile).
func (m *Model) onThreadLoaded(e core.ThreadLoaded) {
	if e.Preview {
		if m.preview && e.ThreadID == m.previewThread {
			m.mode = "index"
			if e.Err != nil {
				m.closePreview()
				return
			}
			if e.ThreadID != pagerThreadID(m.pager) {
				m.pager = newPager(e.ThreadID, e.Lines)
				w, h := m.pagerSize()
				m.pager.setSize(w, h, m.styles)
			}
		}
		return
	}
	if e.Err != nil {
		m.mode, m.pager = "index", nil
		return
	}
	if e.ThreadID != pagerThreadID(m.pager) {
		m.pager = newPager(e.ThreadID, e.Lines)
		// style once at load - width 0 (no WindowSizeMsg yet) pads
		// nothing, the first resize re-styles at the real width
		w, h := m.pagerSize()
		m.pager.setSize(w, h, m.styles)
	}
	m.mode = "pager"
	m.legendPending = true
}

// onSendResult applies a send result to its dialogue: OK closes the
// tab, a failure keeps it open with Output for review. Addressed by
// tab ID, so a closed tab's ID is a no-op (idempotent - the same
// result may arrive via the channel and the bus snapshot).
func (m *Model) onSendResult(e core.SendResult) {
	for i := range m.tabs {
		if m.tabs[i].ID == e.TabID {
			if e.OK {
				m.closeComposeTab(i)
			} else {
				m.tabs[i].Phase = compose.PhaseFailed
				m.tabs[i].Output = e.Output
				if e.Err != nil && m.tabs[i].Output == "" {
					m.tabs[i].Output = e.Err.Error()
				}
			}
			break
		}
	}
}

// onComposeOpened attaches a dialogue tab (R4). The opened set makes
// it idempotent per TabID: the bus snapshot re-attaches a dropped
// open event exactly once, never a closed dialogue.
func (m *Model) onComposeOpened(e core.ComposeOpened) {
	if m.opened[e.TabID] {
		return
	}
	m.opened[e.TabID] = true
	st := compose.FromEvent(e)
	m.tabs = append(m.tabs, *st)
	m.tabIdx = len(m.tabs)
	m.attachTab()
}

// cursorThreadID resolves the cursor row's thread id and subject (the
// preview title source); empty tid means no openable thread. Ghost and
// stub rows carry the thread id in the row itself; the message
// fallback covers rows built before the thread id landed on them.
func (m *Model) cursorThreadID() (tid, subject string) {
	row, ok := m.view.CursorRow()
	if !ok {
		return "", ""
	}
	tid = row.ThreadID
	if tid == "" && row.Msg != nil {
		tid = row.Msg.ThreadID
	}
	if row.Msg != nil {
		subject = row.Msg.Subject
	}
	return tid, subject
}

// openCursorThread hands the cursor row's thread to the open seam (the
// app loads it, marks it read, and publishes ThreadLoaded).
func (m *Model) openCursorThread() {
	tid, _ := m.cursorThreadID()
	if tid != "" {
		onOpen(tid, false)
	}
}

// previewCursorThread opens the cursor thread in the preview popup
// instead: the same seam with preview=true (the app skips the
// read-marking), the popup armed immediately (the empty pager renders
// the title until the load lands), and any stale open pager dropped -
// the box must never show another thread's content. The pre-load pager
// carries an EMPTY threadID: the load's idempotent guard must rebuild,
// not mistake the empty box for the loaded thread.
func (m *Model) previewCursorThread() {
	tid, subject := m.cursorThreadID()
	if tid == "" {
		return
	}
	if subject == "" {
		subject = "thread " + tid
	}
	m.preview = true
	m.previewThread = tid
	m.previewTitle = subject
	m.pager = newPager("", nil)
	w, h := m.pagerSize()
	m.pager.setSize(w, h, m.styles)
	onOpen(tid, true)
}

// previewKey drives the popup: the pager scroll actions scroll the
// box, the index open key promotes to a full open, anything else
// closes. Scrolls defer their paint like pager navigation. The
// promotion keeps the loaded pager (content and scroll position
// survive via the reload guard); an in-flight load rebuilds fresh
// instead.
func (m Model) previewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if actionForKey(msg, m.bindings["index"]) == "open" {
		tid := m.previewThread
		m.preview, m.previewThread, m.previewTitle = false, "", ""
		if len(m.pager.lines) > 0 {
			// the pager leaves the box: re-size it to the full frame
			w, h := m.pagerSize()
			m.pager.setSize(w, h, m.styles)
		} else {
			m.pager = nil
		}
		onOpen(tid, false)
		return m, nil
	}
	switch actionForKey(msg, m.bindings["pager"]) {
	case "scroll-down":
		m.pager.scrollDown(1)
	case "scroll-up":
		m.pager.scrollUp(1)
	case "page-down":
		m.pager.pageDown()
	case "page-up":
		m.pager.pageUp()
	case "half-page-down":
		m.pager.halfPageDown()
	case "half-page-up":
		m.pager.halfPageUp()
	case "scroll-top":
		m.pager.scrollTop()
	case "scroll-bottom":
		m.pager.scrollBottom()
	default:
		m.closePreview()
		return m, nil
	}
	m.paint, m.renderDue = false, true
	return m, m.armFrameTick()
}

// closePreview drops the preview popup and its pager, returning to the
// plain index surface.
func (m *Model) closePreview() {
	m.preview = false
	m.previewThread = ""
	m.previewTitle = ""
	m.pager = nil
}

// pagerSize resolves the pager window: the preview box's content area
// while previewing (the box grows and shrinks with the terminal), the
// full frame otherwise. Every pager resize goes through this so the
// two surfaces never disagree on the window.
func (m Model) pagerSize() (int, int) {
	if m.preview {
		return m.previewContentSize()
	}
	return m.width, m.height - 2
}

// previewContentSize is the popup box's content area: the box spans
// the width minus 4, starts 2 rows down, and its title and hint rows
// take 2 of the box's inner rows.
func (m Model) previewContentSize() (int, int) {
	boxW, boxH := m.width-4, m.height-6
	if boxW < 2 {
		boxW = 2
	}
	if boxH < 4 {
		boxH = 4
	}
	return boxW - 2, boxH - 4
}

// actionForKey resolves the pressed key: runes first (plain keys),
// then BubbleTea's canonical name ("ctrl+n", "alt+v", ...) so control
// keys are bindable.
func actionForKey(msg tea.KeyPressMsg, km map[string]string) string {
	if a, ok := km[msg.Text]; ok {
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

func progressTickCmd() tea.Cmd {
	return tea.Tick(progressTickInterval, func(time.Time) tea.Msg { return progressTick{} })
}

type legendTick struct{ moves int }

func legendTickCmd(moves int) tea.Cmd {
	return tea.Tick(legendDebounce, func(time.Time) tea.Msg { return legendTick{moves} })
}

// frameTick lands one frameInterval after a navigation defers its
// paint; the handler re-arms the ShouldRender gate for that one
// update, so the paint lands at the fixed cadence.
type frameTick struct{}

func frameTickCmd() tea.Cmd {
	return tea.Tick(frameInterval, func(time.Time) tea.Msg { return frameTick{} })
}

// ShouldRender is the tea loop's optional paint gate (the vendored
// loop consults it when the model implements it): false skips the
// render after an update, so a deferred paint lands on the frame tick
// instead of on every keypress of a hold.
func (m Model) ShouldRender() bool { return m.paint }

// resolveStatus computes the status-line legend and account for the
// cursor's message: the icon library over the row's tags, and the
// account tag among them (R2). In pager mode both fall back to the open
// thread's first real message.
func (m Model) resolveStatus() (legend, account string) {
	tags := m.cursorTags()
	return iconLegend(tags, m.ui.Tags, m.accountTags), accountTag(tags, m.accountTags)
}

// cursorTags resolves the cursor message's tag list. Read-only over the
// cached row list: the mirror id scan, or the view's stored cursor
// index (O(1)) when the mirror is empty (stub cursor) or stale (the
// view re-anchored after a merge) - never the view's CursorRow, which
// flattens the whole thread tree. In pager mode the fallback is the
// open thread's first real message.
func (m Model) cursorTags() []string {
	rows := m.rows
	if len(rows) == 0 {
		return nil
	}
	var tags []string
	if m.cursorID != "" {
		for _, r := range rows {
			if r.Msg != nil && r.Msg.ID == m.cursorID {
				tags = r.Msg.Tags
				break
			}
		}
	}
	if tags == nil {
		if idx := m.view.CursorRowIndex(); idx >= 0 && idx < len(rows) {
			if msg := rows[idx].Msg; msg != nil {
				tags = msg.Tags
			}
		}
	}
	if len(tags) == 0 && m.mode == "pager" && m.pager != nil {
		for _, r := range rows {
			if r.Msg != nil && r.ThreadID == m.pager.threadID {
				tags = r.Msg.Tags
				break
			}
		}
	}
	return tags
}

// moveCursor moves the index cursor n rows (a counted move loops
// single steps so edge crossings page). The window holds still while
// the cursor moves within the page; only at a page edge does it jump
// a full page. All stepping is index-local against the cached row
// list: the view's CursorRow flattens the whole thread tree per call
// (the page-key stall at 33k rows), so the cursor id is mirrored on
// the model and looked up by scanning m.rows - one scan per move, no
// flatten.
func (m *Model) moveCursor(delta int) {
	rows := m.view.Rows()
	m.rows = rows
	if len(rows) == 0 {
		return
	}
	m.clampIndexOffset()
	idx := m.CursorIndex()
	n := delta
	step := 1
	if n < 0 {
		n = -n
		step = -1
	}
	for i := 0; i < n; i++ {
		idx = cursorStepAt(rows, idx, step)
		m.setCursorAt(rows, idx)
		idx = m.pageAtEdgeAt(rows, idx)
	}
}

// cursorStepAt moves idx one row in dir. Ghost rows are pass-through:
// a step onto a ghost walks in the move direction to the nearest real
// message; at a boundary, the step does not move (returns start).
func cursorStepAt(rows []core.Row, idx, dir int) int {
	start := idx
	idx += dir
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	if rows[idx].Msg == nil {
		for {
			idx += dir
			if idx < 0 || idx >= len(rows) {
				return start
			}
			if rows[idx].Msg != nil {
				break
			}
		}
	}
	return idx
}

// pageAtEdgeAt jumps the window a full page when the cursor crossed a
// page edge (the read-position model): crossing
// the bottom lands the cursor on the new page's first line, crossing
// the top on its last line. Returns the possibly re-anchored cursor
// index. A single step crosses exactly one edge.
func (m *Model) pageAtEdgeAt(rows []core.Row, idx int) int {
	if len(rows) == 0 {
		return idx
	}
	h := m.listHeight()
	if idx > m.indexOffset+h-1 {
		m.indexOffset += h
		if m.indexOffset > len(rows)-h {
			m.indexOffset = len(rows) - h
		}
		return cursorLandAt(rows, m.indexOffset, 1)
	}
	if idx < m.indexOffset {
		m.indexOffset -= h
		if m.indexOffset < 0 {
			m.indexOffset = 0
		}
		return cursorLandAt(rows, m.indexOffset+h-1, -1)
	}
	return idx
}

// cursorLandAt anchors on the nearest real row from idx, walking dir
// first (a page landing can hit a ghost at a page boundary; the cursor
// never rests on a ghost). Stubs anchor by index.
func cursorLandAt(rows []core.Row, idx, dir int) int {
	for i := idx; i >= 0 && i < len(rows); i += dir {
		if rows[i].Msg != nil {
			return i
		}
	}
	for i := idx; i >= 0 && i < len(rows); i -= dir {
		if rows[i].Msg != nil {
			return i
		}
	}
	return idx
}

// setCursorAt anchors the view cursor on row idx and mirrors the id:
// stub rows (no message id) anchor by index - the viewport hydrate
// replaces the stub with the real message and re-anchors by id. A
// ghost row leaves the cursor untouched (cursorLandAt's all-ghost
// fallback).
func (m *Model) setCursorAt(rows []core.Row, idx int) {
	// any move blanks the legend and account and arms the debounce -
	// the status row only shows what the cursor rested on
	m.legend = ""
	m.account = ""
	m.legendPending = true
	m.legendMoves++
	if rows[idx].Msg == nil {
		return
	}
	if id := rows[idx].Msg.ID; id != "" {
		m.view.SetCursor(id)
		m.cursorID = id
	} else {
		m.view.SetCursorIndex(idx)
		m.cursorID = ""
	}
}

// listHeight is the index window's row count: the bottom two rows are
// the keyhint bar (R9) and the status line (R15).
func (m *Model) listHeight() int {
	h := m.height - 2
	if h < 1 {
		h = 1
	}
	return h
}

// pageRows is the page-down/up step size: one index window.
func (m *Model) pageRows() int {
	return m.listHeight()
}

// clampIndexOffset keeps the anchored window inside the current rows
// after resizes and refreshes shrank the view.
func (m *Model) clampIndexOffset() {
	rows := m.rows
	if len(rows) == 0 {
		m.indexOffset = 0
		return
	}
	if max := len(rows) - m.listHeight(); m.indexOffset > max {
		m.indexOffset = max
	}
	if m.indexOffset < 0 {
		m.indexOffset = 0
	}
}

// cursorTop/cursorBottom jump the index cursor to the first/last real
// row (gg / G) and pin the window to the matching edge. moveCursor's
// boundary walk cannot reach backward past a leading ghost row, so the
// edge walk is direction-aware: ghosts and stubs are skipped in the
// jump direction.
func (m *Model) cursorTop() {
	m.cursorEdge(1)
	m.indexOffset = 0
}

func (m *Model) cursorBottom() {
	m.cursorEdge(-1)
	m.indexOffset = len(m.rows) - m.listHeight()
	m.clampIndexOffset()
}

// gotoRow jumps the cursor to row n (1-based): the delta from the
// current row walks through moveCursor, so window anchoring, ghost
// skipping, and edge clamping behave exactly like a counted move.
func (m *Model) gotoRow(n int) {
	m.moveCursor(n - 1 - m.CursorIndex())
}

func (m *Model) cursorEdge(dir int) {
	rows := m.view.Rows()
	m.rows = rows
	if len(rows) == 0 {
		return
	}
	i := 0
	if dir < 0 {
		i = len(rows) - 1
	}
	for i >= 0 && i < len(rows) {
		if rows[i].Msg != nil {
			m.setCursorAt(rows, i)
			return
		}
		i += dir
	}
}

// stage runs a tag action on the cursor row (R14) and reports whether
// it staged anything - the caller advances the cursor only on an
// effect. A tag in any tag group is a folder tag and stages +tag -
// exclusive-group resolution dedups at render/apply; a tag in no group
// is soft (unread is canonical) and toggles from the applied state.
// Ghost rows are guarded like the M1 cursor keys. The staged identity
// is the row's message id, or the thread identity for summary rows
// (search summaries carry no message id): a tag op on a summary is a
// thread-level op - apply emits thread:<id>, notmuch's natural unit.
func (m *Model) stage(action string) bool {
	tag, ok := m.tagActions[action]
	if !ok {
		return false
	}
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return false
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
	return true
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
// drop, no DB traffic. Reports whether anything was staged, so a
// no-op undo does not advance the cursor. Ghost rows are guarded like
// the M1 cursor keys.
func (m *Model) undo() bool {
	row, ok := m.view.CursorRow()
	if !ok || row.Msg == nil {
		return false
	}
	identity := row.Msg.ID
	if identity == "" {
		identity = "t:" + row.ThreadID
	}
	if !m.view.IsStaged(identity) {
		return false
	}
	m.view.Undo(identity)
	m.rows = m.view.Rows()
	return true
}

// CursorIndex resolves the cursor's row index against the cached row
// list - one scan, never a view flatten (CursorRow rebuilds the whole
// row model; at 33k rows that is the movement stall). A stale mirror
// (cursor set on the view directly) falls back to the view's own
// resolution, which flattens once - the model-set cursor path never
// does. Stub rows carry no message id: the view's last row index is
// the anchor.
func (m Model) CursorIndex() int {
	if m.cursorID != "" {
		for i, r := range m.rows {
			if r.Msg != nil && r.Msg.ID == m.cursorID {
				return i
			}
		}
	}
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
	idx := m.view.CursorRowIndex()
	if len(m.rows) == 0 {
		return 0
	}
	if idx < 0 || idx >= len(m.rows) {
		idx = len(m.rows) - 1
	}
	return idx
}

// View wraps the rendered frame in the v2 view struct: the alt-screen
// flag and the keyboard-enhancement request (kitty protocol release
// reporting) are declarative View fields in v2 - no program options.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	// ReportEventTypes asks the terminal to report key repeat and
	// release events; when supported, the program receives
	// KeyReleaseMsg and the legend resolves on the real keyup
	v.KeyboardEnhancements.ReportEventTypes = true
	return v
}

// render builds the full frame. The frame must NOT end with a newline: the
// vendored renderer splits the frame on "\n" and a trailing empty element
// makes the split longer than the window height, which drops the first row
// and shifts every line - the diff then matches nothing and the whole page
// repaints on every keypress.
func (m Model) render() string {
	if m.help {
		return m.renderHelp()
	}
	if m.mode == "compose" {
		return m.renderCompose()
	}
	if m.mode == "pager" && m.pager != nil {
		var b strings.Builder
		b.WriteString(m.pager.render())
		b.WriteString("\n")
		b.WriteString(m.keyhint())
		b.WriteString("\n")
		b.WriteString(m.statusLineWith(m.styles, m.ui))
		return b.String()
	}
	if m.rows == nil {
		m.rows = m.view.Rows()
	}
	st := m.styles
	rows := m.rows
	if len(rows) == 0 {
		// an empty view renders like a filled one: blank rows fill the
		// list area (the indicator sits on the first, cursor-style), and
		// the keyhint bar and status row always render - "empty" is a
		// data state, never a surface state
		listHeight := m.height - 2
		if listHeight < 1 {
			listHeight = 1
		}
		var b strings.Builder
		for i := 0; i < listHeight; i++ {
			outer := st.Normal
			if i == 0 {
				outer = st.Indicator
			}
			if m.width > 0 {
				b.WriteString(padRow("", m.width, outer))
			}
			b.WriteByte('\n')
		}
		b.WriteString(m.keyhint())
		b.WriteByte('\n')
		b.WriteString(m.statusLineWith(st, m.ui))
		return m.overlayPreview(b.String())
	}
	cur := m.CursorIndex()
	// the window is ANCHORED at indexOffset (the read-position model):
	// the cursor moves within the window, and
	// only a page-edge crossing moves it. The clamp handles resizes and
	// refreshes that shrank the rows; the write-back keeps the movement
	// math in sync. The bottom two rows are the keyhint bar (R9) and
	// the status line (R15); the list window is height-2.
	listHeight := m.listHeight()
	top := m.indexOffset
	bottom := top + listHeight
	if bottom > len(rows) {
		bottom = len(rows)
		top = bottom - listHeight
		if top < 0 {
			top = 0
		}
		m.indexOffset = top
	}
	// the number slot grows with the largest row number (the width is
	// per-render and shared by every row - alignment never shifts)
	numWidth := len(strconv.Itoa(len(rows)))
	// the tag slot is sized the same way, per page: the widest tag run
	// among the visible rows sets the width every row pads to, so the
	// subject column aligns within the page (the next page re-aligns)
	tagWidth := 0
	for _, r := range rows[top:bottom] {
		if w := tagRunWidth(rowTagList(r), m.ui.Tags.Max, m.ui.Tags, m.accountTags); w > tagWidth {
			tagWidth = w
		}
	}
	sg := st.sgr
	var b strings.Builder
	for i := top; i < bottom; i++ {
		// the row cache: a cursor move restyles only the two rows whose
		// selected flag flips; the rest concatenate from the cache. The
		// key carries the row address (reflattens churn it - auto-miss)
		// plus every style-affecting parameter; the outer row style is
		// a function of the row's own fields and selected, so the
		// rendered line is fully keyed.
		key := rowKey{row: &rows[i], numWidth: numWidth, tagWidth: tagWidth, width: m.width, styles: m.styleVer, selected: i == cur}
		if rows[i].Msg != nil {
			key.atts = len(rows[i].Msg.Atts) > 0
		}
		line, ok := m.rowCache[key]
		if !ok {
			if len(m.rowCache) > rowCacheMax {
				m.rowCache = make(map[rowKey]string, 512)
			}
			line = renderRow(i+1, rows[i], st, m.ui, numWidth, tagWidth, i == cur, m.accountTags)
			outer := sg.normal
			if i == cur {
				outer = sg.indicator
			} else if rows[i].Ghost {
				outer = sg.ghost
			}
			if rows[i].Staged {
				// staged rows keep the row style and gain the staged look
				// ([index.staged] default: bold + muted fg); the slot
				// styles only override fg, so bold carries through
				switch {
				case i == cur:
					outer = sg.stagedIndicator
				case rows[i].Ghost:
					outer = sg.stagedGhost
				default:
					outer = sg.stagedNormal
				}
			}
			if m.width > 0 {
				// bubbletea's first View() runs before WindowSizeMsg:
				// width 0 must not blank the rows (padRow would truncate
				// them away)
				line = padRowSGR(line, m.width, outer)
			}
			m.rowCache[key] = line
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(m.keyhint())
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(st, m.ui))
	return m.overlayPreview(b.String())
}

// overlayPreview splices the preview popup over the index frame: a
// lipgloss-bordered box (title, pager content, hint) whose rows
// replace WHOLE frame lines, so the splice never cuts an SGR
// sequence. The pager is sized to the box's content area (pagerSize),
// so its lines fit the box exactly; before the load lands the empty
// pager renders blank content rows. The border glyphs are config data
// (R11); the border colors derive from the theme - the indicator's fg
// on the frame background, the border style the sgr set precomputes.
// A terminal too small for the box (height < 10) leaves the frame
// untouched.
func (m Model) overlayPreview(frame string) string {
	if !m.preview {
		return frame
	}
	lines := strings.Split(frame, "\n")
	boxW, boxH := m.width-4, m.height-6
	if boxW < 2 {
		boxW = 2
	}
	if boxH < 4 {
		return frame
	}
	top := 2
	// index mode renders short lists shorter than the window (only the
	// empty view pads to height); the popup must splice a full-height
	// frame - pad the list section before the keyhint/status tail
	pad := m.height - len(lines)
	if pad > 0 {
		tail := append([]string{}, lines[len(lines)-2:]...)
		lines = append(lines[:len(lines)-2], make([]string, pad)...)
		lines = append(lines, tail...)
	}
	if top+boxH > len(lines) {
		boxH = len(lines) - top
	}
	if boxH < 4 {
		return frame
	}
	sg := m.styles.sgr
	g := m.ui.Glyphs
	ch := boxH - 4
	content := make([]string, ch)
	if m.pager != nil {
		copy(content, strings.Split(m.pager.render(), "\n"))
	}
	// the box is one lipgloss style: config border glyphs, the
	// indicator's fg on the frame background, the box's content width.
	// The title and hint are interior lines, pre-styled (the box's own
	// styling never touches the pager lines - they carry their SGR
	// already); both truncate to the inner width, so no line exceeds it
	// and the box never word-wraps to a different height.
	inner := boxW - 2
	body := sg.border.render(truncCells(m.previewTitle, inner)) + "\n" +
		strings.Join(content, "\n") + "\n" +
		sg.normal.render(truncCells(m.previewHint(), inner))
	box := m.styles.Normal.
		Border(lipgloss.Border{
			TopLeft: g.BorderTL, Top: g.BorderH, TopRight: g.BorderTR,
			Left: g.BorderV, Right: g.BorderV,
			BottomLeft: g.BorderBL, Bottom: g.BorderH, BottomRight: g.BorderBR,
		}).
		BorderForeground(m.styles.Indicator.GetBackground()).
		BorderBackground(m.styles.Normal.GetBackground()).
		Width(inner).
		Render(body)
	rows := make([]string, 0, boxH)
	for _, line := range strings.Split(box, "\n") {
		rows = append(rows, padRowSGR(line, m.width, sg.normal))
	}
	copy(lines[top:top+boxH], rows)
	return strings.Join(lines, "\n")
}

// statusLineWith builds the status data from the model's view and
// progress state and renders the row at the window width. The layer
// cache rebuilds the row only when its inputs change - a cursor move
// repaints the status from the cache, not from a re-render.
func (m Model) statusLineWith(st Styles, ui config.UI) string {
	d := m.statusData()
	sig := m.mode + "|" + d.view + "|" + strconv.Itoa(d.visible) + "|" + strconv.FormatBool(d.on)
	if d.prog != nil {
		sig += "|" + d.prog.Job + "|" + d.prog.View + "|" + strconv.Itoa(d.prog.Done) + "|" + strconv.Itoa(d.prog.Total)
	}
	sig += "|" + d.legend + "|" + d.account + "|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.styleVer)
	return m.statusLayer.get(sig, func() string { return statusLineWidth(st, ui, d, m.width) })
}

// statusData builds the status row's input: the cursor row's tags feed
// the icon library (in pager mode the open thread's first message -
// the index cursor is hidden). The cursor resolution is the cached-row
// scan, never a view flatten.
func (m Model) statusData() statusData {
	if m.mode == "compose" {
		st := m.tabs[m.tabIdx-1]
		return statusData{view: "compose", visible: len(m.tabs), account: st.Account}
	}
	d := statusData{view: m.view.Name, visible: len(m.rows), on: m.progressOn}
	if m.progressOn {
		p := m.progress
		d.prog = &p
	}
	// the legend and account are pre-resolved by the debounced
	// legendTick - the render path never touches the view's cursor
	// resolution (the flattening CursorRow at 33k rows)
	d.legend = m.legend
	d.account = m.account
	return d
}

// formPrompt is one compose input row: the attach path prompt (kind
// "attach") and the inline field editors (kind "field", field names
// the dialogue field: from/to/subject). The label is the rendered
// prefix; the input the current text.
type formPrompt struct {
	kind  string // "attach" | "field"
	field string // field prompt: from/to/subject
	label string // rendered prefix ("attach path: ", "From: ", ...)
	input string
}

// promptKey captures the prompt keys: printable text appends,
// backspace pops, enter resolves (attach: invalid paths keep the
// prompt open; field: the value replaces the dialogue field), esc
// cancels. The prompt only exists while a dialogue is attached (the
// compose-context actions), so the direct index is safe.
// promptKey drives the prompt row; it returns the tea.Cmd to run when
// the enter key arms one (an attach command exec - a path enter has no
// side command). Update forwards the cmd, so the prompt can hand back
// a subprocess run without escaping the message loop.
func (m *Model) promptKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.prompt
	switch {
	case msg.String() == "enter":
		input := strings.TrimSpace(p.input)
		if p.kind == "attach" {
			if strings.HasPrefix(input, "@") {
				return m.runAttachCommand(strings.TrimPrefix(input, "@"))
			}
			path := compose.ExpandHome(input)
			if st := &m.tabs[m.tabIdx-1]; st.AddAttachment(path) == nil {
				m.prompt = nil
			}
			return nil
		}
		st := &m.tabs[m.tabIdx-1]
		switch p.field {
		case "from":
			st.From = input
		case "subject":
			st.Subject = input
		case "to":
			st.To = compose.SplitAddrs(input)
		}
		m.prompt = nil
	case msg.String() == "esc":
		m.prompt = nil
	case msg.String() == "backspace":
		if p.input != "" {
			p.input = p.input[:len(p.input)-1]
		}
	case msg.Text == "?" && p.kind == "attach" && p.input == "":
		// a path can legally contain '?' - the list key is only the
		// empty-prompt '?'; anything else appends
		if names := attachCommandNames(); len(names) > 0 {
			m.fuzzy = newFuzzy("attachcmd", "attach command:", names)
		}
	case msg.Text != "":
		p.input += msg.Text
	}
	return nil
}

// runAttachCommand arms the command exec (the $EDITOR pattern): the
// chooser temp file is appended to the command's argv (F4 - argv only,
// never a shell string), the command runs as a foreground TUI
// subprocess, the result handler reads the selected paths back. An
// unknown command keeps the prompt open (no exec, no error).
func (m *Model) runAttachCommand(name string) tea.Cmd {
	if m.composeTab().Phase == compose.PhaseSending {
		return nil
	}
	argv, ok := attachCommands()[name]
	if !ok {
		return nil
	}
	f, err := os.CreateTemp("", "notmutt-chooser-*")
	if err != nil {
		return nil
	}
	f.Close() // the subprocess writes it
	st := m.composeTab()
	cmd := exec.Command(argv[0], append(argv[1:], f.Name())...)
	m.prompt = nil
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return attachCmdDoneMsg{err: err, path: f.Name(), tabID: st.ID, name: name}
	})
}

// attachCommandNames lists the registered attach commands, sorted (the
// picker entry list contract).
func attachCommandNames() []string {
	names := make([]string, 0, len(attachCommands()))
	for name := range attachCommands() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// fuzzyKey captures the fuzzy context: bound actions dispatch,
// unbound printable keys filter the query, backspace trims it.
func (m *Model) fuzzyKey(msg tea.KeyPressMsg, km map[string]string) bool {
	if a := actionForKey(msg, km); a != "" {
		switch a {
		case "fuzzy-down":
			m.fuzzy.move(1)
		case "fuzzy-up":
			m.fuzzy.move(-1)
		case "fuzzy-select":
			m.fuzzySelect()
		case "fuzzy-cancel":
			m.fuzzy = nil
		}
		return true
	}
	switch {
	case msg.String() == "backspace":
		if m.fuzzy.query != "" {
			m.fuzzy.query = m.fuzzy.query[:len(m.fuzzy.query)-1]
		}
	case msg.Text != "":
		m.fuzzy.query += msg.Text
	}
	m.fuzzy.sel = 0
	return true
}

// fuzzySelect applies the picker's selection to the dialogue: an
// account switch sets Account and From; a signature switch loads the
// file and attaches it. The picker only exists while a dialogue is
// attached (the account/signature actions are compose-context).
func (m *Model) fuzzySelect() {
	entry, ok := m.fuzzy.selected()
	if !ok {
		m.fuzzy = nil
		return
	}
	if m.fuzzy.kind == "attachcmd" {
		// the selection arms the attach prompt: enter runs it
		if m.prompt != nil && m.prompt.kind == "attach" {
			m.prompt.input = "@" + entry
		}
		m.fuzzy = nil
		return
	}
	st := &m.tabs[m.tabIdx-1]
	if m.fuzzy.kind == "account" {
		a := m.st.Config().Accounts[entry]
		st.Account, st.From = entry, a.From
	} else {
		if data, err := os.ReadFile(filepath.Join(sigDir, st.Account, entry)); err == nil {
			st.SetSignature(entry, strings.TrimSuffix(string(data), "\n"))
		}
	}
	m.fuzzy = nil
}

// openPicker opens the account or signature selector: the entries are
// the configured accounts, or the account's signature files.
func (m *Model) openPicker(kind string) {
	st := &m.tabs[m.tabIdx-1]
	if kind == "account" {
		names := make([]string, 0, len(m.st.Config().Accounts))
		for n := range m.st.Config().Accounts {
			names = append(names, n)
		}
		m.fuzzy = newFuzzy("account", "account:", names)
		return
	}
	var names []string
	if sigDir != "" {
		if entries, err := os.ReadDir(filepath.Join(sigDir, st.Account)); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					names = append(names, e.Name())
				}
			}
		}
	}
	m.fuzzy = newFuzzy("signature", "signature:", names)
}

// openReply hands the reply context to the app seam: the cursor row's
// message in the index, the open thread's first message in the
// pager, nil for a blank compose.
func (m *Model) openReply(mode string) {
	var msg *core.Message
	if m.mode == "index" {
		if row, ok := m.view.CursorRow(); ok {
			msg = row.Msg
		}
	} else if m.mode == "pager" && m.pager != nil {
		for _, r := range m.rows {
			if r.Msg != nil && r.ThreadID == m.pager.threadID {
				msg = r.Msg
				break
			}
		}
	}
	if mode == "reply" || mode == "reply-all" || mode == "forward" {
		if msg == nil {
			return
		}
	}
	onReply(msg, mode)
}

// tabNext/tabPrev cycle the tab list: the mail surface (index 0) and
// every open dialogue. Stepping off a dialogue parks it; stepping
// back re-attaches it. The pager state survives in m.pager - the mail
// surface restores to "pager" when a thread was open.
func (m *Model) tabNext() {
	if len(m.tabs) == 0 {
		return
	}
	m.tabIdx++
	if m.tabIdx > len(m.tabs) {
		m.tabIdx = 0
	}
	m.attachTab()
}

func (m *Model) tabPrev() {
	if len(m.tabs) == 0 {
		return
	}
	m.tabIdx--
	if m.tabIdx < 0 {
		m.tabIdx = len(m.tabs)
	}
	m.attachTab()
}

// composeTab is the attached dialogue the compose context acts on
// (tabIdx > 0 is guaranteed whenever mode == "compose" - attachTab
// sets both together).
func (m *Model) composeTab() *compose.State {
	return &m.tabs[m.tabIdx-1]
}

func (m *Model) attachTab() {
	m.fuzzy, m.prompt = nil, nil
	if m.tabIdx > 0 {
		m.mode = "compose"
		return
	}
	if m.pager != nil {
		m.mode = "pager"
		return
	}
	m.mode = "index"
}

// closeComposeTab removes the dialogue and lands on the previous
// tab (or the mail surface when none remain).
func (m *Model) closeComposeTab(i int) {
	m.tabs = append(m.tabs[:i], m.tabs[i+1:]...)
	if m.tabIdx > i {
		m.tabIdx--
	}
	if m.tabIdx > len(m.tabs) {
		m.tabIdx = len(m.tabs)
	}
	m.attachTab()
}

// editorDoneMsg reports the $EDITOR run: the buffer path is read back
// (applyEditorResult) and removed. The result is addressed by tab ID
// (not position): a tab closed or replaced while the editor runs must
// never receive a stale buffer.
type editorDoneMsg struct {
	err   error
	path  string
	tabID string
}

// attachCmdDoneMsg reports an attach command run: the chooser file
// holds one selected path per line; err is the exec failure. name
// prefills the retry prompt.
type attachCmdDoneMsg struct {
	err   error
	path  string // the chooser file
	tabID string
	name  string // command name, for the failure retry
}
