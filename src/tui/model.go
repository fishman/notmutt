// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	sfuzzy "github.com/sahilm/fuzzy"

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
		"open": true, "open-headers": true, "preview": true, "quit": true, "undo": true, "apply": true, "refresh": true,
		"filter": true, "search": true, "search-next": true,
		"collapse-thread": true, "collapse-all": true,
		"reply": true, "reply-all": true, "forward": true, "compose": true,
		"tab-prev": true, "tab-next": true,
		"help": true, "log": true, "command": true,
	},
	"pager": {
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"back": true, "quit": true, "load-remote-images": true,
		"toggle-render": true, "show-source": true, "open-links": true,
		"open-headers": true, "open": true,
		"attachments": true, "save-attachment": true,
		"tab-prev": true, "tab-next": true,
		"help": true, "log": true, "command": true,
	},
	"compose": {
		"form-down": true, "form-up": true,
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"half-page-down": true, "half-page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"edit": true, "attach": true, "detach": true,
		"edit-from": true, "edit-to": true, "edit-subject": true,
		"edit-cc": true, "edit-bcc": true, "edit-replyto": true,
		"security": true,
		"account":  true, "signature": true,
		"send": true, "abort": true,
		"tab-prev": true, "tab-next": true,
		"help": true, "log": true, "command": true,
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
	// renderMode is the pager's requested view (the toggle-render and
	// source keys): the plain parts, the rendered html part, or the raw
	// html source. renderMime is the last reply's mime label for the
	// status bar - what actually rendered, resolved against the
	// message's parts. showHeaders is the h key: the full header block
	// renders at the top of the plain view. linkMode is the F key: the
	// html view carries the "[N]" link labels and linkList holds the
	// targets (label N opens linkList[N-1]). The mode/headers/links
	// mirrors the last ThreadLoaded - a same-thread reload with another
	// view replaces the pager content.
	renderMode core.RenderMode
	renderMime string
	linkMode   bool
	linkList   []string
	// linkInput is the easyjump number under entry: digits extend it,
	// backspace drops it (no prompt - the selection is the live
	// highlight). A complete number opens the link on the spot.
	linkInput   string
	showHeaders bool
	// searchQuery is the index search pattern (the / key): rows whose
	// author or subject contains it render the match highlighted, and
	// the n key jumps to the next match. Empty means no active search.
	searchQuery string
	// imgProto is the terminal's image protocol ("" = unsupported:
	// images stay collapsed); imgCache holds the decoded+scaled window
	// images; painted the rects the terminal currently holds (the
	// paint-diff source). imgMode is the render-images cycle (0 off,
	// 1 local - cid:/data: bytes only, 2 remote - http(s) srcs fetch
	// on the key); imgFetching single-flights the in-flight fetches.
	imgProto    string
	imgCache    map[*core.Image]image.Image
	painted     map[*core.Image]cellRect
	imgMode     int
	imgFetching map[string]bool
	// attView is the pager's active attachment view (the v dialog's
	// enter): the message's lines are replaced by the attachment's
	// render, and back re-opens the message to restore. The viewed
	// attachment's identity rides here for the s key (the save prompt
	// prefills its name). nil = the pager shows the message.
	attView    *attView
	job        string
	progress   core.Progress
	progressOn bool
	// statusMsg is the status line's last log entry (the R4 send
	// results, the R8 lua results, job errors, lock timeouts):
	// logEntry is the single write path, an entry survives until the
	// next one replaces it. msgErr styles it with the error style.
	statusMsg    string
	statusMsgErr bool
	// log is the session log ring (logEntry appends, logCap caps);
	// logOpen is the ~ overlay flag, logView its viewport (the pager
	// widget, same as the help overlay).
	log     []logLine
	logOpen bool
	logView viewport
	// spin is the send dialogue spinner's frame index (sendTick
	// advances it while a send is in flight).
	spin int
	// sendTickOn gates the spinner tick to a single one in flight (the
	// legendTickOn pattern): armed when a send starts, dies when the
	// last send completes.
	sendTickOn bool
	// addrs is the harvested sender corpus for the compose Tab address
	// completion (lazy, debounced harvest - loaded once per session,
	// never at startup).
	addrs []core.AddressEntry
	// addrPending marks a harvest request in flight (single-flight):
	// cleared when the AddressIndex result lands.
	addrPending bool
	// addrSeen dedupes the bus snapshot rescue: the corpus applies
	// once, never re-opens the picker on every keypress.
	addrSeen bool
	// addrReqAt is the last Tab trigger time; the debounce settle
	// guard fires the harvest only when no trigger arrived since the
	// tick was armed (the legendDebounce pattern).
	addrReqAt time.Time
	// indexOffset is the index window's anchored top row (the
	// read-position model): the window holds
	// still while the cursor moves within it; only when the cursor
	// crosses a page edge does the window jump a full page.
	indexOffset int
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
	// frameInterval later, so the loop skips every intermediate render
	// (one paint per frame window, not one per keypress). Every other
	// message paints immediately.
	paint bool
	// renderDue is a deferred paint waiting on the frame tick.
	renderDue bool
	// frameTickOn gates the frame tick to a single one in flight:
	// repeated navigations inside one interval never pile timers up
	// (the legendTickOn pattern).
	frameTickOn bool
	// frameCache is the last painted frame (View): View's value
	// receiver copies the model, so the cache lives behind a pointer
	// - a deferred View returns it instead of rebuilding.
	frameCache *frameCache
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
	// formIdx is the compose form cursor slot: 8 the message-text row,
	// 9+i attachment i. The
	// settings rows are never focused - every field edits by hotkey.
	formIdx int
	// formView scrolls the compose form (the pager widget): when the
	// rows outgrow the frame, the window follows the cursor. A pointer
	// like the layers - the program holds the model by value, so
	// render-time writes persist only through reference fields.
	formView *viewport
	// previewPager is the compose preview pane (the pager widget, the
	// same component as the mail pager); previewContent is its rendered
	// input cache - syncPreviewPager rebuilds only when the content
	// changes, so scroll position survives edits and tab switches.
	previewPager   *pager
	previewContent string
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
	// dialogue is the modal prompt box (R4); non-nil captures the
	// dialogue keys in every mode. The concrete type decides the keys
	// and the frame (a text/confirm/error box, a list/file chooser).
	dialogue dialogue
	// fileDir is the built-in chooser's current directory (the file
	// dialogue descends into directories and comes back up on esc)
	fileDir string
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
	logLayer    *layer
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
	return Model{view: view, ch: ch, bus: bus, bindings: bindings, tagActions: tagActions, st: st, ui: ui, styles: ResolveStyles(cfg.Theme, cfg.Palette), accountTags: cfg.AccountTags(), opened: map[string]bool{}, mode: "index", rowCache: map[rowKey]string{}, hintLayer: &layer{}, statusLayer: &layer{}, helpLayer: &layer{}, logLayer: &layer{}, formView: &viewport{}, previewPager: newPager("", nil), frameCache: &frameCache{}, styleVer: 1, imgProto: detectImageProtocol(cfg.Pager), imgCache: map[*core.Image]image.Image{}, painted: map[*core.Image]cellRect{}, imgFetching: map[string]bool{}}
}

func (m Model) Init() Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg any) (Model, Cmd) {
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
		if e, ok := m.bus.LatestAddressIndex(); ok && !m.addrSeen {
			m.onAddressIndex(e)
		}
	}
	// every message paints except the navigation deferrals below (they
	// set paint false and let the frame tick re-arm it); the frameTick
	// case itself overrides this after the fact
	m.paint = true
	switch msg := msg.(type) {
	case WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resetImages() // the cell math changed: dims and rects are stale
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
			h := m.height - 4
			if h < 1 {
				h = 1
			}
			m.helpView.setSize(m.width, h)
		}
		if m.logOpen {
			h := m.height - 4
			if h < 1 {
				h = 1
			}
			m.logView.setSize(m.width, h)
		}
	case KeyPressMsg:
		// the F key's label mode owns the keys (no dialogue - the
		// selection is the live highlight): digits extend the number,
		// backspace drops it, enter opens the highlighted link, esc/F
		// exit, and the pager scroll keys stay live (the labels below
		// the fold are reachable)
		if m.linkMode && m.mode == "pager" && m.pager != nil {
			m.linkKey(msg)
			return m, nil
		}
		// the active dialogue owns the keys (R4): it can close (nil),
		// swap itself out (a chooser over its prompt), or hand back a
		// Cmd (an attach command exec, the addr harvest tick)
		if m.dialogue != nil {
			d, cmd := m.dialogue.handle(&m, msg)
			m.dialogue = d
			return m, cmd
		}
		if m.logOpen {
			// the log overlay borrows the pager keys like the help: the
			// scroll keys drive the viewport, anything else closes
			// without firing
			switch actionForKey(msg, m.bindings["pager"]) {
			case "scroll-down":
				m.logView.scrollDown(1)
			case "scroll-up":
				m.logView.scrollUp(1)
			case "scroll-top":
				m.logView.scrollTop()
			case "scroll-bottom":
				m.logView.scrollBottom()
			case "page-down":
				m.logView.pageDown()
			case "page-up":
				m.logView.pageUp()
			case "half-page-down":
				m.logView.halfPageDown()
			case "half-page-up":
				m.logView.halfPageUp()
			default:
				m.logOpen = false
				m.logView = viewport{}
			}
			return m, nil
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
		cand := m.pendingPrefix + " " + r
		if km[r] == "" && len(r) == 1 && r[0] >= '0' && r[0] <= '9' &&
			!(km[cand] != "" || chainContinuation(km, cand)) {
			// an armed chain gets its digit (g 1 is a chain key);
			// otherwise digits accumulate a count
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
					return m, chainTickCmd()
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
					return m, chainTickCmd()
				}
				// dead key (or a special key): the chain dies and
				// the key dispatches normally below
			}
		} else if r != "" && km[r] == "" && chainContinuation(km, r) {
			m.pendingPrefix = r
			m.pendingAt = time.Now()
			return m, chainTickCmd()
		}
		return m.dispatchAction(actionForKey(msg, km), n)
	case KeyReleaseMsg:
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
	case KeyboardEnhancementsMsg:
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
		// the buffer file lives on: it IS the tab's edit surface
		// (BodyPath), removed when the tab closes
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
			// the chooser failed: the error box offers a retry (y
			// re-runs the command - the tab may have closed meanwhile)
			for i := range m.tabs {
				if m.tabs[i].ID == msg.tabID {
					m.dialogue = &errorDialogue{label: "attach failed: " + msg.name, output: msg.err.Error(), name: msg.name}
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
	case chainTick:
		// the armed prefix expires on its timer (an extended chain's
		// stale tick no-ops on the age check); the continuation view
		// resets to the base bindings
		if m.pendingPrefix != "" && time.Since(m.pendingAt) >= chainTimeout {
			m.pendingPrefix = ""
			m.paint = true
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
		case core.AttachmentLoaded:
			m.onAttachmentLoaded(e)
		case core.AttachmentSaved:
			// the s key's write result (the app extracted + wrote the
			// attachment): the path or the failure surfaces on the
			// status line
			if e.Err != nil {
				m.logEntry("save failed: "+e.Err.Error(), true)
			} else {
				m.logEntry("saved attachment to "+e.Path, false)
			}
		case core.ImageFetched:
			// a fetch reply for the remote images mode; stale replies
			// (the mode cycled away meanwhile) drop - network data
			// never feeds the decode outside the remote mode
			if m.imgMode == 1 {
				m.attachFetched(e)
			}
		case core.ComposeOpened:
			m.onComposeOpened(e)
		case core.SendResult:
			m.onSendResult(e)
		case core.AddressIndex:
			m.onAddressIndex(e)
		case core.LuaResult:
			// the :lua command or plugin action result: the output or
			// the error goes into the session log and surfaces as the
			// status line's last entry (R8 - never mail content)
			if e.Err != nil {
				m.logEntry("lua: "+e.Err.Error(), true)
			} else {
				m.logEntry(e.Output, false)
			}
		case core.FilterDone:
			// the filter run's summary on the status line (R2); the
			// per-file detail lives in diag
			verb := "applied"
			if e.DryRun {
				verb = "dry-run"
			}
			m.logEntry(fmt.Sprintf("filter: %d entries, %d moved, %d skipped (%s)", e.Entries, e.Moves, e.Skips, verb), false)
		case core.JobError:
			// a failed background job logs with its kind (R15)
			m.logEntry(e.Job+": "+e.Err.Error(), true)
		case core.WorkerLockTimeout:
			m.logEntry("lock timeout: "+e.Kind, true)
		}
		m.refreshProgress()
		m.rows = m.view.Rows()
		if m.legendPending && !m.legendTickOn && !m.keyReleases {
			m.legendTickOn = true
			return m, batch(EventCmd(m.ch), legendTickCmd(m.legendMoves))
		}
		if m.progressOn {
			return m, batch(EventCmd(m.ch), progressTickCmd())
		}
		return m, EventCmd(m.ch)
	case progressTick:
		m.refreshProgress()
		if m.progressOn {
			return m, progressTickCmd()
		}
		return m, nil
	case sendTick:
		// the send spinner's frame cadence: the tick re-arms itself
		// and the event channel while a send is in flight, so the
		// result lands even between keypresses (the event channel is
		// otherwise only re-armed by events and the progress tick)
		if m.anySending() {
			m.spin++
			m.sendTickOn = true
			return m, batch(EventCmd(m.ch), sendTickCmd())
		}
		m.sendTickOn = false
		return m, nil
	case addrReqTick:
		// the debounce settle guard (the legendDebounce pattern): the
		// tick fires the harvest only when no trigger arrived since it
		// was armed; a too-young tick re-arms itself. addrPending
		// stays true until the corpus lands - that is the single
		// flight.
		if time.Since(m.addrReqAt) < addrDebounce {
			return m, addrReqTickCmd()
		}
		onAddrRequest()
		return m, nil
	}
	return m, nil
}

// anySending reports a send in flight across the dialogue tabs - the
// spinner tick's liveness.
func (m *Model) anySending() bool {
	for i := range m.tabs {
		if m.tabs[i].Phase == compose.PhaseSending {
			return true
		}
	}
	return false
}

// dispatchAction runs a bound action with its count, then the
// legend-tick tail (the fall-through path). Actions with their own
// cmds (quit, edit) return them directly. Multi-key chains resolve
// here too - the chain machinery dispatches the completed chain's
// action, and "?" opens the help overlay.
func (m Model) dispatchAction(action string, n int) (Model, Cmd) {
	// a view switch (goto-<view>, R9 data-first): the store is the
	// single write path (R8); the refresher re-reads the active view
	// and re-fetches. Unknown views are a no-op - load validation
	// already rejected them at startup.
	if strings.HasPrefix(action, "goto-") {
		if m.st != nil {
			m.st.SetActiveView(strings.TrimPrefix(action, "goto-"))
		}
		return m, nil
	}
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
	case "collapse-thread":
		// the C key: the cursor thread collapses to its root row
		// (re-anchored in the view) or expands back to its tree
		if m.mode == "index" {
			rows := m.view.Rows()
			m.rows = rows
			idx := m.CursorIndex()
			if idx >= 0 && idx < len(rows) && rows[idx].Msg != nil {
				m.view.ToggleCollapsed(rows[idx].ThreadID)
				m.rows = m.view.Rows()
				m.clampIndexOffset()
				deferPaint()
				deferred = true
			}
		}
	case "collapse-all":
		// the ctrl+v key: the whole index flattens to one row per
		// thread or expands back to the full tree
		if m.mode == "index" {
			m.view.ToggleCollapseAll()
			m.rows = m.view.Rows()
			m.clampIndexOffset()
			deferPaint()
			deferred = true
		}
	case "open":
		if m.mode == "index" {
			m.openCursorThread()
		} else if m.mode == "pager" && m.pager != nil {
			// enter in the pager: the next mail (mutt's next-message).
			// The index cursor advances; a same-thread press at the
			// last row is a no-op. The reload guard replaces the
			// pager content on arrival.
			m.moveCursor(1)
			if tid, _ := m.cursorThreadID(); tid != "" && tid != pagerThreadID(m.pager) {
				onOpen(tid, false, m.showHeaders, m.width)
				deferPaint()
				deferred = true
			}
		}
	case "open-headers":
		// the h key: the index flips the flag and opens; in the pager
		// the open thread re-renders with the header block toggled
		// (the same seam as v - the reply flips the state, the
		// onThreadLoaded guard decides the replace).
		if m.mode == "index" {
			m.showHeaders = !m.showHeaders
			m.openCursorThread()
		} else if m.mode == "pager" && m.pager != nil {
			onToggleRender(pagerThreadID(m.pager), m.renderMode, !m.showHeaders, m.width, false)
			deferPaint()
			deferred = true
		}
	case "preview":
		if m.mode == "index" {
			m.previewCursorThread()
		}
	case "quit":
		// staged ops are session-local: quitting discards them, so a
		// pending buffer asks first - the confirm re-dispatches
		// quit-confirmed, which bypasses this gate
		if m.mode == "index" && m.view.HasStaged() {
			m.dialogue = &confirmDialogue{label: "Discard staged changes and quit?", action: "quit-confirmed"}
			return m, nil
		}
		return m, quitCmd()
	case "quit-confirmed":
		return m, quitCmd()
	case "undo":
		if m.undo() {
			m.moveCursor(1)
		}
	case "apply":
		onApply()
	case "refresh":
		// the manual poll trigger: the app-side refresher runs the same
		// poll body as its ticker (notmuch new + view cycle)
		if m.bus != nil {
			m.bus.Publish(core.RefreshRequested{})
		}
	case "scroll-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollDown(n)
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.scrollDown(n)
		} else {
			break
		}
		deferPaint()
		deferred = true
	case "scroll-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollUp(n)
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.scrollUp(n)
		} else {
			break
		}
		deferPaint()
		deferred = true
	case "page-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.pageDown()
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.pageDown()
		} else if m.mode == "index" {
			m.moveCursor(m.pageRows())
		}
		deferPaint()
		deferred = true
	case "page-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.pageUp()
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.pageUp()
		} else if m.mode == "index" {
			m.moveCursor(-m.pageRows())
		}
		deferPaint()
		deferred = true
	case "half-page-down":
		if m.mode == "pager" && m.pager != nil {
			m.pager.halfPageDown()
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.halfPageDown()
		} else if m.mode == "index" {
			m.moveCursor(m.pageRows() / 2)
		}
		deferPaint()
		deferred = true
	case "half-page-up":
		if m.mode == "pager" && m.pager != nil {
			m.pager.halfPageUp()
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.halfPageUp()
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
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.scrollTop()
			deferPaint()
			deferred = true
		}
	case "scroll-bottom":
		if m.mode == "pager" && m.pager != nil {
			m.pager.scrollBottom()
			deferPaint()
			deferred = true
		} else if m.mode == "compose" && m.previewPager != nil {
			m.previewPager.scrollBottom()
			deferPaint()
			deferred = true
		}
	case "back":
		if m.mode == "pager" {
			if m.attView != nil {
				// the q key in an attachment view returns to the
				// message: the re-open reply replaces the pager (the
				// onThreadLoaded attView guard) and clears the view
				onToggleRender(m.attView.threadID, m.renderMode, m.showHeaders, m.width, false)
				deferPaint()
				deferred = true
				break
			}
			m.clearImageRects() // before the frame: the index renders over the pager area
			m.mode = "index"
		}
	case "attachments":
		// the v key: the attachment picker (mutt's v dialog) lists the
		// message's attachments from the pager's attachment lines;
		// enter views the chosen one. A linkless attachment-less
		// message reports instead of arming a dead picker.
		if m.mode == "pager" && m.pager != nil {
			if entries := attachmentEntries(m.pager.lines); len(entries) > 0 {
				m.dialogue = &listDialogue{f: newFuzzy("attachments", "attachments:", entries)}
			} else {
				m.logEntry("no attachments in this message", true)
			}
			deferPaint()
			deferred = true
		}
	case "save-attachment":
		// the s key in an attachment view: the save prompt prefills
		// the viewed attachment's name, enter writes it via the app
		if m.mode == "pager" && m.attView != nil {
			m.dialogue = &textDialogue{field: "saveatt", label: "save attachment to: ", input: m.attView.name}
			deferPaint()
			deferred = true
		}
	case "load-remote-images":
		// the privacy gate: images render ONLY on this key (no
		// protocol - no unsupported terminal - keeps the Alt row). The
		// alt+i toggle: off -> remote (embedded bytes render, http(s)
		// srcs fetch, gated by this key) -> off.
		if m.mode == "pager" && m.pager != nil && m.imgProto != "" {
			m.setImgMode((m.imgMode + 1) % 2)
			deferPaint()
			deferred = true
		}
	case "toggle-render":
		// the html/txt toggle: the app re-opens the thread with the
		// other html-part view and publishes a fresh ThreadLoaded
		// (the render always runs on the async open job, R13). The
		// flag flips only when the reply lands - the reply's mode
		// must differ from the CURRENT content's to replace it. The
		// source view (ctrl+u) is not in the cycle - v leaves it.
		if m.mode == "pager" && m.pager != nil {
			mode := core.RenderHTML
			if m.renderMode == core.RenderHTML {
				mode = core.RenderPlain
			}
			onToggleRender(pagerThreadID(m.pager), mode, m.showHeaders, m.width, false)
			deferPaint()
			deferred = true
		}
	case "show-source":
		// the raw html source view (ctrl+u): re-opens the thread in
		// the source view unless it is already showing it.
		if m.mode == "pager" && m.pager != nil && m.renderMode != core.RenderSource {
			onToggleRender(pagerThreadID(m.pager), core.RenderSource, m.showHeaders, m.width, false)
			deferPaint()
			deferred = true
		}
	case "open-links":
		// the F key (easyjump-style): the html view re-renders with the
		// inline "[N]" link labels, and the key loop owns the digits
		// (no prompt - the selection is the live highlight, linkKey
		// handles the numbers and the scroll keys); a linkless mail
		// reports instead of arming a dead loop (labels exist only at
		// link sites). The plain/source views list the visible links in
		// the picker - no labels exist there. F again (or esc) exits the
		// label mode. The linkMode flag adopts only when the render
		// reply lands (the onThreadLoaded guard) - a request never
		// claims the state before the reply, or the reply would match
		// the request and skip the replace.
		if m.mode == "pager" && m.pager != nil {
			if m.renderMode == core.RenderHTML {
				onToggleRender(pagerThreadID(m.pager), m.renderMode, m.showHeaders, m.width, true)
			} else if links := linksOfLines(m.pager.lines, m.renderMode == core.RenderSource); len(links) > 0 {
				m.dialogue = &listDialogue{f: newFuzzy("openlink", "open link:", numberedLinks(links))}
			} else {
				m.logEntry("no links in this message", true)
			}
			deferPaint()
			deferred = true
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
		// navigation lives in the message-text row and the attachment
		// list only: the settings rows are edited by hotkey, never
		// focused
		if m.formIdx < 8+len(m.composeTab().Attachments) {
			m.formIdx++
		}
		deferPaint()
		deferred = true
	case "form-up":
		if m.formIdx > 8 {
			m.formIdx--
		}
		deferPaint()
		deferred = true
	case "edit":
		// the body editor is unconditional: every field edits by its
		// own hotkey (t/s/f/x/b/r), the account by c, the security by S
		if m.composeTab().Phase == compose.PhaseSending {
			break
		}
		if m.composeTab().Phase == compose.PhaseFailed {
			m.composeTab().Phase = compose.PhaseEditing
		}
		t := m.composeTab()
		path, err := writeEditorBuffer(*t, t.BodyPath)
		if err != nil {
			return m, nil
		}
		t.BodyPath = path
		tabID := t.ID
		return m, execCmd(editorCmd(path), func(err error) any {
			return editorDoneMsg{err: err, path: path, tabID: tabID}
		})
	case "attach":
		if m.composeTab().Phase != compose.PhaseSending {
			m.dialogue = &textDialogue{field: "attach", label: "attach path: "}
		}
	case "detach":
		t := m.composeTab()
		if t.Phase != compose.PhaseSending {
			// slot 8 is the message-text row, never an attachment
			if i := m.formIdx - 9; i >= 0 && i < len(t.Attachments) {
				t.Attachments = slices.Delete(t.Attachments, i, i+1)
				if n := len(t.Attachments); m.formIdx > 8+n {
					m.formIdx = 8 + n
				}
			}
		}
	case "edit-to", "edit-subject", "edit-from", "edit-cc", "edit-bcc", "edit-replyto":
		// the mutt field editors: t/s/f/x/b/r open an inline prompt
		// prefilled with the field's current value (the compose
		// body stays on e and the $EDITOR buffer)
		if m.composeTab().Phase != compose.PhaseSending {
			if m.composeTab().Phase == compose.PhaseFailed {
				m.composeTab().Phase = compose.PhaseEditing
			}
			st := m.composeTab()
			d := &textDialogue{field: strings.TrimPrefix(action, "edit-")}
			switch d.field {
			case "from":
				d.label, d.input = "From: ", st.From
			case "subject":
				d.label, d.input = "Subject: ", st.Subject
			case "to":
				d.label, d.input = "To: ", strings.Join(st.To, ", ")
			case "cc":
				d.label, d.input = "Cc: ", strings.Join(st.Cc, ", ")
			case "bcc":
				d.label, d.input = "Bcc: ", strings.Join(st.Bcc, ", ")
			case "replyto":
				d.label, d.input = "Reply-To: ", strings.Join(st.ReplyTo, ", ")
			}
			d.cur = len(d.input)
			m.dialogue = d
		}
	case "account":
		m.openPicker("account")
	case "signature":
		m.openPicker("signature")
	case "security":
		if m.composeTab().Phase != compose.PhaseSending {
			m.composeTab().Security = m.composeTab().Security.Next()
		}
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
			m.dialogue = &confirmDialogue{label: "Abort composition?", action: "abort", draft: true}
		}
	case "help":
		m.help = true
		m.logOpen = false
		h := m.height - 4
		if h < 1 {
			h = 1
		}
		m.helpView.setLines(m.helpRows())
		m.helpView.setSize(m.width, h)
	case "log":
		m.logOpen = true
		m.help = false
		h := m.height - 4
		if h < 1 {
			h = 1
		}
		m.logView.setLines(m.logRows())
		m.logView.setSize(m.width, h)
		m.logView.scrollBottom()
	case "command":
		// the command line: ": lua <code>" runs a Lua chunk, plugin
		// action names dispatch to their registered functions (R8)
		m.dialogue = &textDialogue{field: "command", label: ": "}
	case "filter":
		// the live display filter (F): the prompt edits the view's
		// filter on every key, esc restores the pre-open text
		if m.mode == "index" {
			d := &textDialogue{field: "filter",
				label: "filter: ", input: m.view.Filter(), saved: m.view.Filter()}
			d.cur = len(d.input)
			m.dialogue = d
		}
	case "search":
		// the mutt search prompt (/): enter commits the pattern and
		// closes the prompt - the n key repeats the search from the
		// cursor. The pattern preloads so a repeat-search can edit it.
		if m.mode == "index" {
			d := &textDialogue{field: "search", label: "/",
				input: m.searchQuery, saved: m.searchQuery}
			d.cur = len(d.input)
			m.dialogue = d
		}
	case "search-next":
		// n repeats the last search from the cursor (wrapping)
		if m.mode == "index" && m.searchQuery != "" {
			m.searchNext()
			deferPaint()
			deferred = true
		}
	default:
		// a plugin-registered action (R8): the app runs it in the
		// plugin's VM with the cursor thread as msg() context
		if pluginActions()[action] {
			tid, _ := m.cursorThreadID()
			onLuaAction(action, tid)
			break
		}
		// staged tag ops (and undo) advance the cursor one row -
		// the next keypress acts on the next message (mutt's
		// auto-advance). A no-op action (ghost row, unknown action)
		// does not move.
		if m.stage(action) {
			m.moveCursor(1)
		}
	}
	var cmds []Cmd
	if deferred {
		cmds = append(cmds, m.armFrameTick())
	}
	if m.legendPending && !m.legendTickOn && !m.keyReleases {
		m.legendTickOn = true
		cmds = append(cmds, legendTickCmd(m.legendMoves))
	}
	// a send press (or a retry) arms the spinner tick while the job is
	// in flight - the single-in-flight gate keeps the 100ms re-arms
	// from piling up across dispatches
	if m.anySending() && !m.sendTickOn {
		m.sendTickOn = true
		cmds = append(cmds, sendTickCmd())
	}
	if len(cmds) > 0 {
		return m, batch(cmds...)
	}
	return m, nil
}

// armFrameTick starts the frame tick once; deferrals while one is in
// flight return no cmd (the single-in-flight pattern).
func (m *Model) armFrameTick() Cmd {
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
// exitLinkMode closes the F key's label mode: the selection clears and
// the thread re-renders without the "[N]" labels. The linkMode flag
// follows the unlabeled reply (exitLinkMode never claims it - the
// onThreadLoaded guard decides the replace).
func (m *Model) exitLinkMode() {
	m.linkInput = ""
	if m.pager != nil {
		m.pager.setLinkSel("")
		onToggleRender(pagerThreadID(m.pager), m.renderMode, m.showHeaders, m.width, false)
	}
}

// linkKey is the easyjump key loop (no prompt - the selection IS the
// feedback): digits extend the number, backspace drops it, enter opens
// the highlighted link, esc/F leave the label mode, and the pager
// scroll keys stay live - the labels below the fold are reachable.
func (m *Model) linkKey(msg KeyPressMsg) {
	if t := msg.Text; len(t) == 1 && t[0] >= '0' && t[0] <= '9' {
		m.linkDigit(t)
		return
	}
	switch msg.String() {
	case "backspace":
		if len(m.linkInput) > 0 {
			m.linkInput = m.linkInput[:len(m.linkInput)-1]
		}
		m.syncLinkSel()
	case "enter":
		m.openLinkSel()
		m.exitLinkMode()
	case "esc", "ctrl+g":
		m.exitLinkMode()
	default:
		switch actionForKey(msg, m.bindings["pager"]) {
		case "open-links":
			m.exitLinkMode()
		case "scroll-down":
			m.pager.scrollDown(1)
		case "scroll-up":
			m.pager.scrollUp(1)
		case "scroll-top":
			m.pager.scrollTop()
		case "scroll-bottom":
			m.pager.scrollBottom()
		case "page-down":
			m.pager.pageDown()
		case "page-up":
			m.pager.pageUp()
		case "half-page-down":
			m.pager.halfPageDown()
		case "half-page-up":
			m.pager.halfPageUp()
		}
	}
}

// linkDigit extends the easyjump number: a digit that would overshoot
// the label count is ignored (labels are 1..N - a number above N is a
// prefix of nothing, a dead entry). The highlight follows the digits;
// a complete number - one no further label extends (n*10 > N) - opens
// the link immediately.
func (m *Model) linkDigit(d string) {
	n, err := strconv.Atoi(m.linkInput + d)
	if err != nil || n < 1 || n > len(m.linkList) {
		return
	}
	m.linkInput += d
	m.syncLinkSel()
	if n*10 > len(m.linkList) {
		openLink(m.linkList[n-1])
		m.exitLinkMode()
	}
}

// openLinkSel opens the link the current digits select (the enter key;
// linkDigit's auto-open path bypasses it). An empty or out-of-range
// number is a no-op - enter only closes the mode.
func (m *Model) openLinkSel() {
	n, err := strconv.Atoi(m.linkInput)
	if err != nil || n < 1 || n > len(m.linkList) {
		return
	}
	openLink(m.linkList[n-1])
}

// syncLinkSel points the pager at the selected label's marker (the F
// key's live highlight: the "[N]" of the number under entry renders
// reversed). No digits = no highlight.
func (m *Model) syncLinkSel() {
	sel := ""
	if n, err := strconv.Atoi(m.linkInput); err == nil && n >= 1 && n <= len(m.linkList) {
		sel = fmt.Sprintf("[%d]", n)
	}
	if m.pager != nil {
		m.pager.setLinkSel(sel)
	}
}

// linksOfLines extracts the visible links of a non-html render (the
// F key fallback): the joined line texts scanned for URLs and
// addresses. isHTML marks the source view - the raw html, where the
// angle brackets delimit the links.
func linksOfLines(lines []core.Line, isHTML bool) []string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return core.Links(b.String(), isHTML)
}

// numberedLinks prefixes each link with its 1-based number (the
// plain-view picker's "enter the number" entries).
func numberedLinks(links []string) []string {
	out := make([]string, len(links))
	for i, l := range links {
		out[i] = fmt.Sprintf("%d. %s", i+1, l)
	}
	return out
}

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
		m.mode, m.pager, m.attView = "index", nil, nil
		return
	}
	// the attView term: any message render while an attachment view is
	// active replaces it (the back key's restore) - the reply carries
	// the message's own mode/headers, which alone never differ
	if e.ThreadID != pagerThreadID(m.pager) || e.RenderMode != m.renderMode || e.Headers != m.showHeaders || e.LinkLabels != m.linkMode || m.attView != nil {
		m.renderMode, m.showHeaders, m.linkMode, m.linkList = e.RenderMode, e.Headers, e.LinkLabels, e.Links
		m.attView = nil // the attachment view ends with the restore
		m.pager = newPager(e.ThreadID, e.Lines)
		// style once at load - width 0 (no WindowSizeMsg yet) pads
		// nothing, the first resize re-styles at the real width
		w, h := m.pagerSize()
		m.pager.setSize(w, h, m.styles)
		// the F key's label mode arms with the labeled reply: the digits
		// start empty (the selection is the live highlight) and links
		// exist or the mode reports - never a silent dead entry
		if e.LinkLabels {
			m.linkInput = ""
			m.syncLinkSel()
			if len(e.Links) == 0 {
				m.logEntry("no links in this message", true)
			}
		}
	}
	m.renderMime = e.Mime
	m.mode = "pager"
	// the render-images mode survives an open: a fresh pager re-arms
	// the expansion and re-triggers the visible remote fetches (the
	// decode runs per frame in renderBase)
	if m.imgMode != 0 {
		m.pager.setImages(true)
		if m.imgMode == 1 {
			m.fetchVisibleImages()
		}
	}
	m.legendPending = true
}

// onAttachmentLoaded swaps the pager into the attachment view (the v
// dialog's enter): the message's lines are replaced by the chosen
// attachment's render and the identity rides attView - back re-opens
// the message to restore, s saves through the app. Stale replies (the
// pager moved on) drop.
func (m *Model) onAttachmentLoaded(e core.AttachmentLoaded) {
	if e.Err != nil {
		m.logEntry("attachment: "+e.Err.Error(), true)
		return
	}
	if m.mode != "pager" || e.ThreadID != pagerThreadID(m.pager) {
		return
	}
	m.attView = &attView{threadID: e.ThreadID, ordinal: e.Ordinal, name: e.Name}
	m.pager = newPager(e.ThreadID, e.Lines)
	w, h := m.pagerSize()
	m.pager.setSize(w, h, m.styles)
}

// attView is the pager's attachment view state: the viewed
// attachment's thread + ordinal (the save seam's re-extraction key)
// and name (the save prompt's prefill).
type attView struct {
	threadID string
	ordinal  int
	name     string
}

// attachmentEntries maps the pager's attachment lines to the v
// dialog's entries: "N. name (size)" - the leading number is the
// attachment's ordinal (1-based), the display name drops the size
// suffix the line carries. Line order equals parse order (renderMessage
// emits one line per parsed attachment in order), so the picker's
// numbering is the extraction index.
func attachmentEntries(lines []core.Line) []string {
	var entries []string
	n := 0
	for _, l := range lines {
		if l.Kind != core.LineAttachment {
			continue
		}
		n++
		name := strings.TrimPrefix(l.Text, "attachment: ")
		if i := strings.Index(name, " ("); i > 0 {
			name = name[:i]
		}
		entries = append(entries, fmt.Sprintf("%d. %s", n, name))
	}
	return entries
}

// onSendResult applies a send result to its dialogue: OK closes the
// tab with the "sent to ..." log entry on the status line (the fcc
// note rides along); a failure keeps the tab open, opens the error
// dialogue with Output for review, and logs "send failed". Addressed
// by tab ID, so a closed tab's ID is a no-op (idempotent - the same
// result may arrive via the channel and the bus snapshot).
func (m *Model) onSendResult(e core.SendResult) {
	for i := range m.tabs {
		if m.tabs[i].ID == e.TabID {
			if e.OK {
				msg := "sent"
				if len(m.tabs[i].To) > 0 {
					msg = "sent to " + strings.Join(m.tabs[i].To, ", ")
				}
				if e.Output != "" {
					msg += " (" + e.Output + ")"
				}
				m.logEntry(core.SanitizeControls(msg), false)
				m.closeComposeTab(i)
			} else {
				m.tabs[i].Phase = compose.PhaseFailed
				m.tabs[i].Output = e.Output
				if e.Err != nil && m.tabs[i].Output == "" {
					m.tabs[i].Output = e.Err.Error()
				}
				m.logEntry("send failed", true)
				m.dialogue = &errorDialogue{
					label: "send failed", output: m.tabs[i].Output, tabID: e.TabID,
				}
			}
			break
		}
	}
}

// activateTab attaches the dialogue with the given ID - the error
// dialogue's retry/edit land on the failed tab, wherever the send
// result found the user.
func (m *Model) activateTab(id string) {
	for i := range m.tabs {
		if m.tabs[i].ID == id {
			m.tabIdx = i + 1
			m.attachTab()
			return
		}
	}
}

// addrLookup resolves a Tab completion trigger: a loaded corpus swaps
// the completion picker over the prompt; otherwise the lazy harvest
// fires after the debounce (single-flight - repeated triggers never
// pile up requests). The prompt stays when the picker is gated.
func (m *Model) addrLookup() (dialogue, Cmd) {
	if len(m.addrs) > 0 {
		if p := m.addrPicker(); p != nil {
			return p, nil
		}
		return m.dialogue, nil
	}
	if m.addrPending {
		return m.dialogue, nil
	}
	m.addrPending = true
	m.addrReqAt = time.Now()
	return m.dialogue, addrReqTickCmd()
}

// onAddressIndex stores the harvested sender corpus and resolves a
// pending trigger: if an address field is still open, the picker swaps
// over it now with the corpus (the lazy trigger's pickup; addrPicker
// owns the completion gate).
func (m *Model) onAddressIndex(e core.AddressIndex) {
	m.addrs = e.Addrs
	m.addrPending = false
	m.addrSeen = true
	if p := m.addrPicker(); p != nil {
		m.dialogue = p
	}
}

// addrSection splits an address field's input at the edit cursor into
// the fixed head, the section under completion, and the fixed tail:
// the section is the text between the surrounding commas (the field
// holds several senders, one per comma - SplitAddrs; the picker
// completes only the section the cursor is in, never the whole line).
// head keeps the leading "..., " prefix, tail the trailing ", ..."
// suffix; both empty when absent.
func addrSection(input string, cur int) (head, section, tail string) {
	if cur > len(input) {
		cur = len(input)
	}
	prev := strings.LastIndex(input[:cur], ",")
	next := strings.Index(input[cur:], ",")
	end := len(input)
	if next >= 0 {
		end = cur + next
	}
	section = strings.TrimSpace(input[prev+1 : end])
	if prev >= 0 {
		head = strings.TrimSpace(input[:prev+1])
	}
	if next >= 0 {
		tail = strings.TrimSpace(input[cur+next+1:])
	}
	return head, section, tail
}

// addrPicker builds the address completion picker over the current
// text dialogue (its back reference), or nil when gated: the active
// dialogue must be an address field, and the section under completion
// at least 4 characters. The corpus entries are pre-filtered by the
// section (one pass - the picker then narrows as the user keeps
// typing), and the picker query starts at the section so the filter
// bar reads it back.
func (m *Model) addrPicker() dialogue {
	d, ok := m.dialogue.(*textDialogue)
	if !ok || !isAddrField(d.field) {
		return nil
	}
	_, q, _ := addrSection(d.input, d.cur)
	if len(q) < 4 {
		return nil
	}
	var disp []string
	for _, a := range m.addrs {
		addr := a.Addr
		if a.Name != "" {
			addr = a.Name + " <" + a.Addr + ">"
		}
		disp = append(disp, addr)
	}
	entries := make([]string, 0, len(disp))
	for _, mt := range sfuzzy.Find(q, disp) {
		entries = append(entries, disp[mt.Index])
	}
	f := newFuzzy("address", "address:", entries)
	f.query = q
	return &listDialogue{f: f, back: d}
}

// isAddrField reports whether the dialogue field carries addresses -
// the Tab completion surface.
func isAddrField(f string) bool {
	switch f {
	case "to", "cc", "bcc", "replyto":
		return true
	}
	return false
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
	m.formIdx = 8 // the message-text row; with no attachments the cursor rests there
	// the body is backed by a buffer file for the tab's lifetime (mutt's
	// msgbody) - the message-text row shows its path, e reuses it
	tab := &m.tabs[len(m.tabs)-1]
	if path, err := writeEditorBuffer(*tab, ""); err == nil {
		tab.BodyPath = path
	}
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
// app loads it, marks it read, and publishes ThreadLoaded). The
// headers flag is the h toggle: the open renders the full header
// block.
func (m *Model) openCursorThread() {
	tid, _ := m.cursorThreadID()
	if tid != "" {
		onOpen(tid, false, m.showHeaders, m.width)
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
	onOpen(tid, true, m.showHeaders, m.width)
}

// previewKey drives the popup: the pager scroll actions scroll the
// box, the index open key promotes to a full open, anything else
// closes. Scrolls defer their paint like pager navigation. The
// promotion keeps the loaded pager (content and scroll position
// survive via the reload guard); an in-flight load rebuilds fresh
// instead.
func (m Model) previewKey(msg KeyPressMsg) (Model, Cmd) {
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
		onOpen(tid, false, m.showHeaders, m.width)
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
	return m.width, m.height - 3
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
func actionForKey(msg KeyPressMsg, km map[string]string) string {
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

// WindowSizeMsg is the terminal size report: the loop's resize events
// and its initial size query.
type WindowSizeMsg struct{ Width, Height int }

// KeyboardEnhancementsMsg mirrors the tea v2 shape (the release path
// stays wired and tested): SupportsEventTypes reports release
// reporting on. tcell delivers no such message - no kitty keyboard
// protocol (verified at implementation time, record 23) - the
// legendTick fallback covers terminals without it.
type KeyboardEnhancementsMsg struct {
	Flags uint32
}

func (m KeyboardEnhancementsMsg) SupportsEventTypes() bool {
	return m.Flags&kittyReportEventTypes != 0
}

// kitty enhancement flags the message carries; only the release
// reporting bit gates the release path.
const (
	kittyReportEventTypes uint32 = 1 << iota
	kittyDisambiguateEscapeCodes
)

type progressTick struct{}

func progressTickCmd() Cmd {
	return tickCmd(progressTickInterval, func(time.Time) any { return progressTick{} })
}

type sendTick struct{}

func sendTickCmd() Cmd {
	return tickCmd(sendTickInterval, func(time.Time) any { return sendTick{} })
}

// addrReqTick is the address harvest trigger's debounce tick (the
// legendDebounce settle guard): it fires the harvest request once the
// Tab triggers settle.
type addrReqTick struct{}

func addrReqTickCmd() Cmd {
	return tickCmd(addrDebounce, func(time.Time) any { return addrReqTick{} })
}

type legendTick struct{ moves int }

func legendTickCmd(moves int) Cmd {
	return tickCmd(legendDebounce, func(time.Time) any { return legendTick{moves} })
}

// frameTick lands one frameInterval after a navigation defers its
// paint; the handler re-arms the ShouldRender gate for that one
// update, so the paint lands at the fixed cadence.
type frameTick struct{}

func frameTickCmd() Cmd {
	return tickCmd(frameInterval, func(time.Time) any { return frameTick{} })
}

// chainTick lands chainTimeout after a chain is armed; the handler
// expires the prefix on the timer, not only on the next keypress, so
// the keyhint's continuation view resets to the base bindings when
// the chain times out.
type chainTick struct{}

func chainTickCmd() Cmd {
	return tickCmd(chainTimeout, func(time.Time) any { return chainTick{} })
}

// ShouldRender is the loop's paint gate: false skips the render after
// an update, so a deferred paint lands on the frame tick instead of on
// every keypress of a hold.
func (m Model) ShouldRender() bool { return m.paint }

// resolveStatus computes the status-line legend and account for the
// cursor's message: the icon library over the row's tags, and the
// account tag among them (R2). In pager mode both fall back to the open
// thread's first real message.
func (m Model) resolveStatus() (legend, account string) {
	tags := m.cursorTags()
	return iconLegend(tags, m.ui.Tags, m.accountTags), accountTag(tags, m.accountTags)
}

// cursorTags resolves the cursor message's tag list - an O(1) read of
// the row at the view's stored cursor index (moves write it, merges
// re-anchor it). In pager mode the fallback is the open thread's first
// real message.
func (m Model) cursorTags() []string {
	rows := m.rows
	if len(rows) == 0 {
		return nil
	}
	var tags []string
	if idx := m.view.CursorRowIndex(); idx >= 0 && idx < len(rows) {
		if msg := rows[idx].Msg; msg != nil {
			tags = msg.Tags
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
// list; the view records the cursor index on every move (O(1) paint
// reads, no flatten, no scan).
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

// searchNext jumps the cursor to the next search match at or after
// the current row (the / prompt's enter and the n key). The scan
// wraps; a miss logs the mutt "Pattern not found" notice and leaves
// the cursor. The cursor move goes through moveCursor so the window
// pages like any counted move.
func (m *Model) searchNext() {
	rows := m.view.Rows()
	m.rows = rows
	if len(rows) == 0 {
		return
	}
	idx := nextMatch(rows, m.CursorIndex()+1, m.searchQuery)
	if idx < 0 {
		m.logEntry("search: no match", false)
		return
	}
	m.moveCursor(idx - m.CursorIndex())
}

// nextMatch finds the first real row at or after start (wrapping)
// whose author or subject contains query, case-insensitive; -1 when
// nothing matches. start may equal len(rows) - the wrap covers the
// first rows.
func nextMatch(rows []core.Row, start int, query string) int {
	if query == "" || len(rows) == 0 {
		return -1
	}
	q := strings.ToLower(query)
	for i := 0; i < len(rows); i++ {
		r := rows[(start+i)%len(rows)]
		if r.Msg == nil {
			continue
		}
		if strings.Contains(strings.ToLower(r.Msg.Author), q) ||
			strings.Contains(strings.ToLower(r.Msg.Subject), q) {
			return (start + i) % len(rows)
		}
	}
	return -1
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
	m.view.SetCursor(rows[idx].Msg.ID)
	m.view.SetCursorIndex(idx)
}

// listHeight is the index window's row count: the top row is the tab
// bar, the bottom two rows are the keyhint bar (R9) and the status
// line (R15).
func (m *Model) listHeight() int {
	h := m.height - 3
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
// CursorIndex is the cursor's row index - an O(1) read of the view's
// stored index. Moves write it (setCursorAt), merges re-anchor it by
// id at materialization (rowsLocked); the paint path never scans or
// flattens the row list.
func (m Model) CursorIndex() int {
	if len(m.rows) == 0 {
		return 0
	}
	idx := m.view.CursorRowIndex()
	if idx < 0 || idx >= len(m.rows) {
		idx = len(m.rows) - 1
	}
	return idx
}

// frameCache holds the last painted frame. The vendored tea loop
// renders after every update and never consults ShouldRender - the
// coalescing gate lives here instead (a vendor patch was lost once to
// a re-vendor; the model owns it now). The loop calls View on a copy
// of the model, so the cache must sit behind a pointer to survive the
// copy.
type frameCache struct{ s string }

// View returns the rendered frame. The loop owns the screen (tcell,
// record 23): the alt-screen flag and the keyboard-enhancement request
// were declarative View fields in tea v2 - tcell covers the alt screen
// at init, and has no kitty keyboard protocol (verified at
// implementation time), so the release path's types survive only for
// the legendTick fallback's tests. A deferred paint (navigation
// between frame ticks) returns the last painted frame: one build per
// paint, not per keypress.
func (m Model) View() string {
	if m.paint || m.frameCache.s == "" {
		m.frameCache.s = m.render()
	}
	return m.frameCache.s
}

// textCursor reports the live text-input cell (x, y) when the active
// dialogue has an editable text row: the list chooser's matcher row
// (the query is the input while the picker is open), else the prompt's
// input row. The v2 renderer shows the terminal cursor only when the
// view declares it; the prompt box splices 3 rows above the keyhint
// bar (the input row is the box's content row) and the matcher row is
// the second frame line.
func (m Model) textCursor() (int, int, bool) {
	if m.dialogue == nil {
		return 0, 0, false
	}
	return m.dialogue.cursor(&m)
}

// render builds the full frame. The frame must NOT end with a newline: the
// vendored renderer splits the frame on "\n" and a trailing empty element
// makes the split longer than the window height, which drops the first row
// and shifts every line - the diff then matches nothing and the whole page
// repaints on every keypress.
func (m Model) render() string {
	if m.dialogue != nil {
		// the dialogue renders its own frame: a box spliced over the
		// base frame, or the full-frame list chooser
		return m.dialogue.render(&m)
	}
	return m.renderBase()
}

func (m Model) renderBase() string {
	if m.help {
		return m.renderHelp()
	}
	if m.logOpen {
		return m.renderLog()
	}
	if m.mode == "compose" {
		return m.renderCompose()
	}
	if m.mode == "pager" && m.pager != nil {
		m.prepareImages() // decode-gained expansion lands in THIS frame
		var b strings.Builder
		b.WriteString(m.tabBar())
		b.WriteByte('\n')
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
		// list area (the cursor marker sits on the first, cursor-style),
		// and the keyhint bar and status row always render - "empty" is
		// a data state, never a surface state. The list area is the same
		// height as the filled path: tabBar + list + keyhint + status
		// must equal the frame height, one line over and the renderer
		// writes out of bounds.
		listHeight := m.listHeight()
		var b strings.Builder
		b.WriteString(m.tabBar())
		b.WriteByte('\n')
		for i := 0; i < listHeight; i++ {
			line := st.sgr.normal.render(" ")
			if i == 0 {
				line = st.sgr.indicator.render(m.ui.Glyphs.Cursor)
			}
			if m.width > 0 {
				b.WriteString(padRow(line, m.width, st.Normal))
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
	// the status line (R15); the list window is height-3.
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
	b.WriteString(m.tabBar())
	b.WriteByte('\n')
	for i := top; i < bottom; i++ {
		// the row cache: a cursor move restyles only the two rows whose
		// selected flag flips; the rest concatenate from the cache. The
		// key carries the row address (reflattens churn it - auto-miss)
		// plus every style-affecting parameter; the outer row style is
		// a function of the row's own fields and selected, so the
		// rendered line is fully keyed.
		key := rowKey{row: &rows[i], numWidth: numWidth, tagWidth: tagWidth, width: m.width, styles: m.styleVer, selected: i == cur, query: m.searchQuery}
		if rows[i].Msg != nil {
			key.atts = len(rows[i].Msg.Atts) > 0
		}
		line, ok := m.rowCache[key]
		if !ok {
			if len(m.rowCache) > rowCacheMax {
				m.rowCache = make(map[rowKey]string, 512)
			}
			line = renderRow(i+1, rows[i], st, m.ui, numWidth, tagWidth, i == cur, m.accountTags, m.searchQuery)
			outer := sg.normal
			if rows[i].Ghost {
				outer = sg.ghost
			}
			if rows[i].Staged {
				// staged rows keep the row style and gain the staged look
				// ([index.staged] default: bold + muted fg); the slot
				// styles only override fg, so bold carries through
				if rows[i].Ghost {
					outer = sg.stagedGhost
				} else {
					outer = sg.stagedNormal
				}
			}
			if m.width > 0 {
				// the loop's first View() runs before the resize lands:
				// width 0 must not blank the rows (padRow would truncate
				// them away)
				line = padRowSGR(line, m.width, outer)
			}
			m.rowCache[key] = line
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	// pad a short list to the full window: the renderer diffs cells and
	// never erases past the last frame line, so a frame shorter than the
	// screen leaves the previous paint's rows on screen and the next
	// diff misaligns. The list area is always listHeight rows - the
	// empty view pads the same way.
	for i := bottom; i < top+listHeight; i++ {
		if m.width > 0 {
			b.WriteString(padRow("", m.width, st.Normal))
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.keyhint())
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(st, m.ui))
	return m.overlayPreview(b.String())
}

// listFrame renders the list-choose dialogue (the fuzzy picker
// surface): the matcher row on top (title + filter input - the title
// doubles as the prompt, no standalone title line), then the ranked
// matches, then the fuzzy keyhint and status rows. Exactly m.height
// lines - the frame replaces the underlying frame (a clean diff,
// never an overlay). The matcher row always renders - the user's
// filter input stays visible mid-type - and the match list clips to
// fill the frame (large lists scroll later).
func (m Model) listFrame(f *fuzzy) string {
	rows := m.height - 3
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteByte('\n')
	lines := []string{padRow(f.title+" "+f.query, m.width, m.styles.Indicator)}
	matchRows := rows - 1
	if matchRows < 0 {
		matchRows = 0
	}
	matches := f.filtered()
	if matchRows > len(matches) {
		matchRows = len(matches)
	}
	for i := 0; i < matchRows; i++ {
		outer := m.styles.Normal
		if i == f.sel {
			outer = m.styles.Indicator
		}
		lines = append(lines, padRow(f.entries[matches[i]], m.width, outer))
	}
	for len(lines) < rows {
		lines = append(lines, padRow("", m.width, m.styles.Normal))
	}
	for _, l := range lines[:rows] {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteString(keyhintRow(m.bindings["fuzzy"], m.width))
	b.WriteByte('\n')
	b.WriteString(m.statusLineWith(m.styles, m.ui))
	return b.String()
}

// dialogueBox splices the prompt dialogue box over the base frame: a
// lipgloss-bordered box (border, content rows, border) whose rows
// replace whole frame lines above the keyhint bar, so the splice never
// cuts an SGR sequence and the hotkey row survives the overlay. The
// derivation is the preview popup's: config border glyphs (R11), the
// indicator's background as the border color, the content rows
// indicator-styled. The content is the dialogue type's own frame. A
// terminal too small (height < 5, width < 3) leaves the frame
// untouched.
func (m Model) dialogueBox(content []string) string {
	lines := strings.Split(m.renderBase(), "\n")
	if m.height < 5 || m.width < 3 || len(lines) < 4 {
		return strings.Join(lines, "\n")
	}
	lines = padFrameTail(lines, m.height)
	return strings.Join(spliceBox(lines, m.width, m.ui, m.styles, content), "\n")
}

// spliceBox replaces whole frame rows above the keyhint bar with the
// lipgloss-bordered box (border + content rows), so the splice never
// cuts an SGR sequence and the hotkey row stays visible. Config
// border glyphs (R11), the indicator's background as the border
// color, the content rows on the normal background.
func spliceBox(lines []string, width int, ui config.UI, st Styles, content []string) []string {
	g := ui.Glyphs
	inner := width - 2
	if inner < 1 {
		inner = 1
	}
	box := st.Normal.
		Border(boxBorder(g)).
		BorderForeground(st.Indicator.GetBackground()).
		BorderBackground(st.Normal.GetBackground()).
		Width(inner).
		Render(strings.Join(content, "\n"))
	rows := make([]string, 0, len(content)+2)
	for i, line := range strings.Split(box, "\n") {
		if i >= len(content)+2 {
			break
		}
		rows = append(rows, padRowSGR(line, width, st.sgr.normal))
	}
	top := len(lines) - len(rows) - 2 // anchored above the keyhint bar
	if top < 0 {
		top = 0
	}
	copy(lines[top:top+len(rows)], rows)
	return lines
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
	top := 3 // below the tab bar and the first two list rows
	// index mode renders short lists shorter than the window (only the
	// empty view pads to height); the popup must splice a full-height
	// frame - pad the list section before the keyhint/status tail
	lines = padFrameTail(lines, m.height)
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
		Border(boxBorder(g)).
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

// padFrameTail pads a short frame (an empty index list) to the window
// height before the keyhint/status tail, so an overlay can splice
// full-height rows without cutting the tail.
func padFrameTail(lines []string, height int) []string {
	if pad := height - len(lines); pad > 0 {
		tail := append([]string{}, lines[len(lines)-2:]...)
		lines = append(lines[:len(lines)-2], make([]string, pad)...)
		lines = append(lines, tail...)
	}
	return lines
}

// boxBorder builds the popup border from the config glyphs (R11).
func boxBorder(g config.Glyphs) lipgloss.Border {
	return lipgloss.Border{
		TopLeft: g.BorderTL, Top: g.BorderH, TopRight: g.BorderTR,
		Left: g.BorderV, Right: g.BorderV,
		BottomLeft: g.BorderBL, Bottom: g.BorderH, BottomRight: g.BorderBR,
	}
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
	sig += "|" + d.legend + "|" + d.account + "|" + d.mime + "|" + d.msg + "|" + strconv.FormatBool(d.msgErr) +
		"|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.styleVer)
	return m.statusLayer.get(sig, func() string { return statusLineWidth(st, ui, d, m.width) })
}

// statusData builds the status row's input: the cursor row's tags feed
// the icon library (in pager mode the open thread's first message -
// the index cursor is hidden). The cursor resolution is the cached-row
// scan, never a view flatten.
func (m Model) statusData() statusData {
	if m.mode == "compose" {
		st := m.tabs[m.tabIdx-1]
		return statusData{view: "compose", visible: len(m.tabs), account: st.Account,
			msg: m.statusMsg, msgErr: m.statusMsgErr}
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
	if m.mode == "pager" {
		d.mime = m.renderMime
	}
	d.msg = m.statusMsg
	d.msgErr = m.statusMsgErr
	return d
}

// dialogue is a modal compose dialogue (R4): a concrete type owns its
// state and handles keys until it closes (handle returns nil) or
// swaps itself out (the attach text prompt opens the file chooser on
// Tab; a chooser selection or cancel swaps back to the prompt it came
// from). The Model routes keys through the active dialogue and asks
// it for its frame and cursor; a new dialogue type implements the
// interface and the Model switches no further.
type dialogue interface {
	// handle processes one key press; the returned dialogue replaces
	// the current one (nil closes), the Cmd runs after the update.
	handle(m *Model, msg KeyPressMsg) (dialogue, Cmd)
	// render draws the full frame with the dialogue on top: a box
	// spliced over the underlying frame (text/confirm/error) or a
	// full-frame replacement (the list/file choosers).
	render(m *Model) string
	// cursor reports the text cursor cell (x, y) or ok = false when
	// the dialogue has no editable text.
	cursor(m *Model) (x, y int, ok bool)
}

// textDialogue is the form-entry prompt (R4): a label, an editable
// line with mutt-style cursor editing, and a field-determined commit.
// The editor keys live here: left/right move, c-w kills the word
// before the cursor, c-u clears the line, alt-f/alt-b jump by words,
// backspace deletes before the cursor, typing inserts at it. enter
// resolves per field (attach: a path or @command; a field commit: the
// value replaces the dialogue field; filter: closes - applied live;
// command: the Lua layer), esc and ctrl+g cancel (the filter restores
// its pre-open text). tab opens the field's chooser (the address
// completion picker, the attach command/file chooser).
type textDialogue struct {
	field string
	label string
	input string
	// cur is the byte offset of the edit cursor into input
	cur   int
	saved string // the state the prompt opened with (the filter text): esc restores it
}

// cancelDialogue resets the abort gate (q opened the confirm with the
// phase Aborting - any cancel returns to Editing) and closes the box.
func (m *Model) cancelDialogue() {
	m.dialogue = nil
	if m.mode == "compose" && m.composeTab().Phase == compose.PhaseAborting {
		m.composeTab().Phase = compose.PhaseEditing
	}
}

func (d *textDialogue) handle(m *Model, msg KeyPressMsg) (dialogue, Cmd) {
	switch msg.String() {
	case "enter":
		input := strings.TrimSpace(d.input)
		switch d.field {
		case "attach":
			if strings.HasPrefix(input, "@") {
				// the exec takes over; the chooser result re-opens the
				// error box on failure. An unknown command arms no exec
				// - the prompt keeps the text for correction.
				cmd := m.runAttachCommand(strings.TrimPrefix(input, "@"))
				if cmd == nil {
					return d, nil
				}
				return nil, cmd
			}
			path := compose.ExpandHome(input)
			if st := &m.tabs[m.tabIdx-1]; st.AddAttachment(path) == nil {
				return nil, nil
			}
			return d, nil
		case "command":
			if input != "" {
				tid, _ := m.cursorThreadID()
				onLuaCommand(input, tid)
			}
			return nil, nil
		case "filter":
			// the filter applied live per key - enter only closes
			return nil, nil
		case "search":
			// enter commits the pattern and closes the prompt - the
			// saved state drives the n key from the new cursor. The
			// update's default paint stays: the box must vanish
			// immediately, not on the next armed tick.
			if input != "" {
				m.searchQuery = core.SanitizeControls(input)
				m.searchNext()
			}
			return nil, nil
		case "saveatt":
			// the s key in an attachment view: the app writes the
			// viewed attachment to the path (0600) and the result
			// surfaces on the status line
			if input != "" && m.attView != nil {
				onAttachmentSave(m.attView.threadID, m.attView.ordinal, compose.ExpandHome(input))
			}
			return nil, nil
		}
		st := &m.tabs[m.tabIdx-1]
		switch d.field {
		case "from":
			st.From = input
		case "subject":
			st.Subject = input
		case "to":
			st.To = compose.SplitAddrs(input)
		case "cc":
			st.Cc = compose.SplitAddrs(input)
		case "bcc":
			st.Bcc = compose.SplitAddrs(input)
		case "replyto":
			st.ReplyTo = compose.SplitAddrs(input)
		}
		return nil, nil
	case "esc", "ctrl+g":
		// ctrl+g cancels like esc (the readline convention)
		if d.field == "filter" {
			m.view.SetFilter(d.saved)
			m.rows = m.view.Rows()
		}
		m.cancelDialogue()
		return nil, nil
	case "tab":
		if isAddrField(d.field) {
			// address completion: the picker opens over the harvested
			// sender corpus for the section under completion (gated on
			// its length); the corpus loads lazily on the first trigger
			return m.addrLookup()
		}
		if d.field == "attach" {
			// Tab runs the default chooser when one is registered
			// (yazi, ranger, else any command - attach commands are all
			// file choosers), and the built-in directory chooser
			// otherwise
			if name := defaultChooser(attachCommands()); name != "" {
				cmd := m.runAttachCommand(name)
				if cmd == nil {
					return d, nil // the send gate is armed: no exec
				}
				return nil, cmd
			}
			return m.filePicker(d), nil
		}
	case "?":
		// a path can legally contain '?' - the command list is only the
		// empty-prompt '?'; anything else appends
		if d.field == "attach" && d.input == "" {
			if names := attachCommandNames(); len(names) > 0 {
				return &listDialogue{f: newFuzzy("attachcmd", "attach command:", names), back: d}, nil
			}
		}
	case "left":
		if d.cur > 0 {
			d.cur--
		}
	case "right":
		if d.cur < len(d.input) {
			d.cur++
		}
	case "backspace":
		if d.cur > 0 {
			d.input = d.input[:d.cur-1] + d.input[d.cur:]
			d.cur--
			d.changed(m)
		}
	case "ctrl+w": // delete the word before the cursor
		d.killWord()
		d.changed(m)
	case "ctrl+u": // clear the line (the mutt prompt convention)
		d.input = ""
		d.cur = 0
		d.changed(m)
	case "alt+f": // forward a word
		d.forwardWord()
	case "alt+b": // back a word
		d.backWord()
	default:
		if msg.Text != "" {
			d.input = d.input[:d.cur] + msg.Text + d.input[d.cur:]
			d.cur += len(msg.Text)
			d.changed(m)
		}
	}
	return d, nil
}

// changed applies an edit's live side effects: the filter prompt
// narrows the view per key.
func (d *textDialogue) changed(m *Model) {
	if d.field == "filter" {
		m.view.SetFilter(d.input)
		m.rows = m.view.Rows()
	}
}

// killWord deletes the word before the cursor (c-w): whitespace then
// word characters, so "a b|c" leaves "a |".
func (d *textDialogue) killWord() {
	i := d.cur
	for i > 0 && d.input[i-1] == ' ' {
		i--
	}
	for i > 0 && d.input[i-1] != ' ' {
		i--
	}
	d.input = d.input[:i] + d.input[d.cur:]
	d.cur = i
}

// backWord moves the cursor to the start of the previous word (alt-b).
func (d *textDialogue) backWord() {
	i := d.cur
	for i > 0 && d.input[i-1] == ' ' {
		i--
	}
	for i > 0 && d.input[i-1] != ' ' {
		i--
	}
	d.cur = i
}

// forwardWord moves the cursor to the end of the next word (alt-f).
func (d *textDialogue) forwardWord() {
	n := len(d.input)
	i := d.cur
	for i < n && d.input[i] == ' ' {
		i++
	}
	for i < n && d.input[i] != ' ' {
		i++
	}
	d.cur = i
}

// listDialogue is the list-choose dialogue (R4): a fuzzy-filtered
// entry list (account, signature, the address completion, the attach
// command picker). back is the dialogue to return to on select or
// cancel (the address/attach-command pickers swap back to the prompt
// they came from; the account/signature pickers open from the form,
// back is nil and a cancel closes). kind decides the select behavior.
type listDialogue struct {
	f    *fuzzy
	back dialogue
}

func (d *listDialogue) handle(m *Model, msg KeyPressMsg) (dialogue, Cmd) {
	if a := actionForKey(msg, m.bindings["fuzzy"]); a != "" {
		switch a {
		case "fuzzy-down":
			d.f.move(1)
		case "fuzzy-up":
			d.f.move(-1)
		case "fuzzy-select":
			return d.selectEntry(m)
		case "fuzzy-cancel":
			m.cancelDialogue()
			return d.back, nil
		}
		return d, nil
	}
	d.typeKey(msg)
	return d, nil
}

// typeKey narrows the filter: printable text appends, backspace trims
// it; the selection resets to the top.
func (d *listDialogue) typeKey(msg KeyPressMsg) bool {
	switch {
	case msg.String() == "backspace":
		if d.f.query != "" {
			d.f.query = d.f.query[:len(d.f.query)-1]
		}
	case msg.Text != "":
		d.f.query += msg.Text
	default:
		return false
	}
	d.f.sel = 0
	return true
}

// selectEntry applies the selection (kind decides): an account switch
// sets Account and From; a signature switch loads the file; the
// address picker merges the selection into the prompt it came from;
// the attach-command picker arms the prompt with "@name".
func (d *listDialogue) selectEntry(m *Model) (dialogue, Cmd) {
	entry, ok := d.f.selected()
	if !ok {
		return d.back, nil
	}
	switch d.f.kind {
	case "openlink":
		// the plain/source-view fallback (the F key): the entry is
		// "N. url" - strip the number, open the link
		if i := strings.Index(entry, ". "); i > 0 {
			openLink(entry[i+2:])
		}
		return nil, nil
	case "attachcmd":
		back, ok := d.back.(*textDialogue)
		if !ok {
			return nil, nil
		}
		back.input = "@" + entry
		back.cur = len(back.input)
		return back, nil
	case "address":
		back, ok := d.back.(*textDialogue)
		if !ok {
			return nil, nil
		}
		head, _, tail := addrSection(back.input, back.cur)
		back.input = entry
		if head != "" {
			back.input = head + " " + entry
		}
		if tail != "" {
			back.input += ", " + tail
		}
		back.cur = len(back.input)
		return back, nil
	case "attachments":
		// the v dialog's enter: view the chosen attachment - the app
		// re-opens the message, extracts + renders the part, and the
		// reply swaps the pager (back restores). The entry's leading
		// number is the attachment's ordinal.
		if i := strings.Index(entry, ". "); i > 0 {
			if n, err := strconv.Atoi(entry[:i]); err == nil && n > 0 && m.mode == "pager" && m.pager != nil {
				onAttachmentView(pagerThreadID(m.pager), n-1)
			}
		}
		m.cancelDialogue()
		return nil, nil
	}
	st := &m.tabs[m.tabIdx-1]
	if d.f.kind == "account" {
		a := m.st.Config().Accounts[entry]
		st.Account, st.From = entry, a.From
	} else {
		if data, err := os.ReadFile(filepath.Join(sigDir, st.Account, entry)); err == nil {
			st.SetSignature(entry, strings.TrimSuffix(string(data), "\n"))
		}
	}
	m.cancelDialogue()
	return nil, nil
}

// fileDialogue is the built-in file-choose dialogue (the chooser
// fallback when no attach command is registered): a fuzzy listing of
// the current directory - a directory entry descends, a file attaches
// and closes; esc walks back up and closes at the root. back is the
// attach prompt the dialogue returns to.
type fileDialogue struct {
	listDialogue
}

func (d *fileDialogue) handle(m *Model, msg KeyPressMsg) (dialogue, Cmd) {
	if a := actionForKey(msg, m.bindings["fuzzy"]); a != "" {
		switch a {
		case "fuzzy-down":
			d.f.move(1)
		case "fuzzy-up":
			d.f.move(-1)
		case "fuzzy-select":
			return d.selectEntry(m)
		case "fuzzy-cancel":
			m.cancelDialogue()
			if up := filepath.Dir(m.fileDir); up != m.fileDir {
				m.fileDir = up
				return m.filePicker(d.back), nil
			}
			return d.back, nil
		}
		return d, nil
	}
	d.typeKey(msg)
	return d, nil
}

func (d *fileDialogue) selectEntry(m *Model) (dialogue, Cmd) {
	entry, ok := d.f.selected()
	if !ok {
		return d.back, nil
	}
	path := filepath.Join(m.fileDir, entry)
	if strings.HasSuffix(entry, "/") {
		m.fileDir = path
		return m.filePicker(d.back), nil
	}
	if st := &m.tabs[m.tabIdx-1]; st.AddAttachment(path) != nil {
		return d.back, nil
	}
	return nil, nil
}

// confirmDialogue asks yes/no (R4): enter dispatches the action, esc
// cancels. The abort confirmation is the current user.
type confirmDialogue struct {
	label  string
	action string
	draft  bool // the abort confirm: d saves the composition and quits
}

func (d *confirmDialogue) handle(m *Model, msg KeyPressMsg) (dialogue, Cmd) {
	switch msg.String() {
	case "enter":
		// the abort phase must survive to dispatchAction: it was armed
		// by the q that opened the box, and the confirm lands it
		m.dialogue = nil
		mm, cmd := m.dispatchAction(d.action, 1)
		*m = mm
		return nil, cmd
	case "esc":
		m.cancelDialogue()
		return nil, nil
	case "d":
		if !d.draft {
			break
		}
		// save as draft and quit: the write is local (no transport),
		// it runs inline - an error keeps the composition open, the
		// tab's phase resets so the user can fix or abort again
		if err := onDraft(*m.composeTab()); err != nil {
			m.composeTab().Phase = compose.PhaseEditing
			return &errorDialogue{label: "draft failed", output: err.Error()}, nil
		}
		m.dialogue = nil
		m.closeComposeTab(m.tabIdx - 1)
		return nil, nil
	}
	return d, nil
}

// errorDialogue shows a failure with a retry (R4): the send failure's
// output (y re-enters the send gate, e edits the body) or an attach
// command's exec error (y re-runs the command). name is the attach
// command to re-run; empty means the send job (tabID the failed tab).
type errorDialogue struct {
	label  string
	output string
	tabID  string
	name   string
}

func (d *errorDialogue) handle(m *Model, msg KeyPressMsg) (dialogue, Cmd) {
	switch msg.String() {
	case "enter", "esc":
		// close the box; the failed tab keeps the failure note in its
		// preview (y retries, e edits from there)
		return nil, nil
	case "y":
		if d.name != "" {
			return nil, m.runAttachCommand(d.name)
		}
		m.cancelDialogue()
		m.activateTab(d.tabID)
		mm, cmd := m.dispatchAction("send", 1)
		*m = mm
		return nil, cmd
	case "e":
		if d.name == "" {
			m.cancelDialogue()
			m.activateTab(d.tabID)
			mm, cmd := m.dispatchAction("edit", 1)
			*m = mm
			return nil, cmd
		}
	}
	return d, nil
}

// The render surface: a boxed dialogue splices its content over the
// base frame (dialogueBox), the list choosers replace the frame with
// the matcher + matches frame (listFrame). Sanitize runs on the label
// and entry here - dialogue text is user-typed, not the pre-sanitized
// mail path (F1).
func (d *textDialogue) render(m *Model) string {
	inner := m.width - 2
	label := core.SanitizeControls(d.label)
	entry := core.SanitizeControls(d.input)
	// labels are ASCII constants, so byte length is cell width; the
	// entry truncates to the remaining inner width - the line never
	// exceeds it and the box never word-wraps to a different height
	budget := inner - len(label)
	if budget < 0 {
		budget = 0
	}
	return m.dialogueBox([]string{m.styles.ComposeLabel.Render(label) +
		m.styles.Normal.Render(truncCells(entry, budget))})
}

func (d *confirmDialogue) render(m *Model) string {
	hint := "enter = confirm, esc = cancel"
	if d.draft {
		hint = "enter = quit, esc = cancel, d = save draft"
	}
	return m.dialogueBox([]string{core.SanitizeControls(d.label) + " (" + hint + ")"})
}

func (d *errorDialogue) render(m *Model) string {
	inner := m.width - 2
	// the box grows upward: the output is capped only by the frame
	// rows above the keyhint/status lines, never a fixed count (the
	// failed tab's preview keeps the full text)
	outRows := m.height - 7 // label + hint + the two border rows
	if outRows < 1 {
		outRows = 1
	}
	out := strings.Split(d.output, "\n")
	for i := range out {
		out[i] = core.SanitizeControls(out[i])
	}
	if len(out) > outRows {
		out = append(out[:outRows-1], "...")
	}
	content := []string{m.styles.ComposeLabel.Render(core.SanitizeControls(d.label))}
	for _, l := range out {
		content = append(content, truncCells(l, inner))
	}
	hint := "(enter/esc = close"
	if d.tabID != "" || d.name != "" {
		hint += ", y = retry"
	}
	if d.tabID != "" {
		hint += ", e = edit"
	}
	content = append(content, hint+")")
	return m.dialogueBox(content)
}

func (d *listDialogue) render(m *Model) string {
	return m.listFrame(d.f)
}

// The cursor surface: the text prompt's input row (the box splices 3
// rows above the keyhint bar), the list chooser's matcher row.
func (d *textDialogue) cursor(m *Model) (int, int, bool) {
	if m.height < 5 || m.width < 3 {
		return 0, 0, false
	}
	inner := m.width - 2
	budget := inner - len(d.label)
	if budget < 0 {
		budget = 0
	}
	cur := d.cur
	if cur > len(d.input) {
		cur = len(d.input)
	}
	x := 1 + len(d.label) + min(runewidth.StringWidth(d.input[:cur]), budget)
	return x, m.height - 4, true
}

func (d *listDialogue) cursor(m *Model) (int, int, bool) {
	x := len(d.f.title) + 1 + len(d.f.query)
	if x > m.width-1 {
		x = m.width - 1
	}
	return x, 1, true
}

func (d *confirmDialogue) cursor(m *Model) (int, int, bool) { return 0, 0, false }
func (d *errorDialogue) cursor(m *Model) (int, int, bool)   { return 0, 0, false }

// runAttachCommand arms the command exec (the $EDITOR pattern): the
// chooser temp file is appended to the command's argv (F4 - argv only,
// never a shell string), the command runs as a foreground TUI
// subprocess, the result handler reads the selected paths back. An
// unknown command keeps the prompt open (no exec, no error).
func (m *Model) runAttachCommand(name string) Cmd {
	if m.composeTab().Phase == compose.PhaseSending {
		return nil
	}
	var argv []string
	for _, c := range attachCommands() {
		if c.Name == name {
			argv = c.Argv
			break
		}
	}
	if argv == nil {
		return nil
	}
	f, err := os.CreateTemp("", "notmutt-chooser-*")
	if err != nil {
		return nil
	}
	f.Close() // the subprocess writes it
	st := m.composeTab()
	cmd := exec.Command(argv[0], append(argv[1:], f.Name())...)
	m.dialogue = nil
	return execCmd(cmd, func(err error) any {
		return attachCmdDoneMsg{err: err, path: f.Name(), tabID: st.ID, name: name}
	})
}

// attachCommandNames lists the registered attach commands, sorted (the
// picker entry list contract).
func attachCommandNames() []string {
	cmds := attachCommands()
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}

// defaultChooser is the attach command Tab runs: the first registered
// command. Registration order is the preference - the Lua plugin
// script that registers the choosers controls Tab by call order (the
// script is data, never compiled). Empty when none are registered:
// Tab falls back to the built-in directory picker.
func defaultChooser(cmds []AttachCommand) string {
	if len(cmds) == 0 {
		return ""
	}
	return cmds[0].Name
}

// filePicker builds the built-in fallback chooser: a fuzzy picker
// over the directory listing (directories carry a trailing "/" and
// descend on select, esc walks up; at the root it closes and returns
// to back). Empty dir resumes the last position, else the working
// directory. The attach prompt is the back reference the chooser
// returns to.
func (m *Model) filePicker(back dialogue) dialogue {
	dir := m.fileDir
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &errorDialogue{label: "cannot read " + dir, output: err.Error()}
	}
	m.fileDir = dir
	disp := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n := e.Name()
		if e.IsDir() {
			n += "/"
		}
		disp = append(disp, n)
	}
	return &fileDialogue{listDialogue{f: newFuzzy("file", dir+":", disp), back: back}}
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
		m.dialogue = &listDialogue{f: newFuzzy("account", "account:", names)}
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
	m.dialogue = &listDialogue{f: newFuzzy("signature", "signature:", names)}
}

// openReply hands the reply context to the app seam: the cursor row's
// message in the index, the open thread's first message in the
// pager, nil for a blank compose.
func (m *Model) openReply(mode string) {
	var msg *core.Message
	if m.mode == "index" {
		if row, ok := m.view.CursorRow(); ok {
			msg = row.Msg
			// ghost rows (multi-root threads) carry the thread id only -
			// the app's thread-fetch fallback rehydrates the original
			if msg == nil && row.ThreadID != "" {
				msg = &core.Message{ThreadID: row.ThreadID}
			}
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
	if m.mode == "pager" {
		m.clearImageRects() // the compose frame covers the pager area
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
	m.dialogue = nil
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
// tab (or the mail surface when none remain). The tab's buffer file
// dies with it.
func (m *Model) closeComposeTab(i int) {
	os.Remove(m.tabs[i].BodyPath)
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
