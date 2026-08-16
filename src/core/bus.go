package core

import "sync"

type Event any

// Bus fans events out to subscribers. A full subscriber drops the event
// (coalescing): consumers repaint from state, never from events. The
// compose completion events (SendResult, ComposeOpened) keep last-value
// snapshots so a drop never wedges a dialogue.
type Bus struct {
	mu       sync.Mutex
	subs     []chan Event
	progress map[string]Progress
	sendLast map[string]SendResult
	openLast []ComposeOpened
	addrLast *AddressIndex
}

func NewBus() *Bus {
	return &Bus{progress: map[string]Progress{}, sendLast: map[string]SendResult{}}
}

func (b *Bus) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch e := e.(type) {
	case Progress:
		b.progress[e.Job+"\x00"+e.View] = e
	case SendResult:
		b.sendLast[e.TabID] = e
	case ComposeOpened:
		b.openLast = append(b.openLast, e)
	case AddressIndex:
		b.addrLast = &e
	}
	for _, s := range b.subs {
		select {
		case s <- e:
		default:
		}
	}
}

// LatestProgress returns the last published Progress for a job and
// virtual folder. The map write never drops, so the completion event
// survives subscriber backpressure - the TUI renders this snapshot,
// events only wake it.
func (b *Bus) LatestProgress(job, view string) (Progress, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.progress[job+"\x00"+view]
	return p, ok
}

// LatestSendResult returns the last published SendResult for a tab.
// The map write never drops, so a completion dropped from the channel
// under backpressure still resolves the dialogue on the next keypress
// instead of wedging it in PhaseSending.
func (b *Bus) LatestSendResult(tabID string) (SendResult, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sendLast[tabID]
	return s, ok
}

// ClearSendResult forgets a tab's last result: a retry re-arms the
// snapshot, so a stale failure cannot re-apply while the new job is
// in flight (which would reopen the send gates).
func (b *Bus) ClearSendResult(tabID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sendLast, tabID)
}

// LatestComposeOpened returns the most recent ComposeOpened event
// (insertion order), so a dropped open event still attaches the
// dialogue on the next keypress.
func (b *Bus) LatestComposeOpened() (ComposeOpened, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.openLast) == 0 {
		return ComposeOpened{}, false
	}
	return b.openLast[len(b.openLast)-1], true
}

// LatestAddressIndex returns the last published sender corpus. The
// map write never drops, so a harvest result survives subscriber
// backpressure - a dropped event would otherwise leave the lazy
// trigger pending forever.
func (b *Bus) LatestAddressIndex() (AddressIndex, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.addrLast == nil {
		return AddressIndex{}, false
	}
	return *b.addrLast, true
}

// AddressEntry is one deduplicated sender address (the go.notmuch
// harvest shape, Name empty for bare addresses). The compose Tab
// completion matches against these.
type AddressEntry struct {
	Addr string
	Name string
}

// AddressRequest is the compose completion's lazy trigger: the TUI
// publishes it (through the app seam) when Tab meets the length gate
// and no corpus is loaded yet.
type AddressRequest struct{}

// AddressIndex carries the harvested sender corpus from the app to
// the TUI; the TUI caches it for the session.
type AddressIndex struct {
	Addrs []AddressEntry
}

type QueryBatch struct {
	View  string
	Total int
	Done  bool
}

type WorkerDone struct{ Job string }

type WorkerLockTimeout struct{ Kind string }

// RefreshRequested is the manual poll trigger (the refresh key): the
// refresher runs the same poll body as its ticker.
type RefreshRequested struct{}

type CacheResult struct {
	MsgID string
	Atts  []Attachment
	Err   error
}

type ConfigChanged struct{ Section string }

type ViewDiff struct{ View string }

// ComposeOpened opens a compose dialogue tab (R4): the app builds the
// prefill (account detection, quoting, default signature) and the TUI
// attaches the dialogue. Mode is one of "compose" | "reply" |
// "reply-all" | "forward".
type ComposeOpened struct {
	TabID        string
	Mode         string
	Account      string
	From         string
	To, Cc       []string
	Bcc, ReplyTo []string
	Subject      string
	Body         string
	Fcc          string
	Security     string
	Attachments  []ComposeAttachment
	Signature    string
	SigContent   string
	MessageID    string
	References   []string
	OriginalID   string
}

// ComposeAttachment is the bus contract's attachment shape (core stays
// dependency-free; compose owns the mapping to its own type).
type ComposeAttachment struct {
	Name, Path string
	Size       int64
	MimeType   string
}

// SendResult reports the send job's outcome to the dialogue (R4): OK
// closes the tab; a failure keeps it open with Output for review.
type SendResult struct {
	TabID  string
	OK     bool
	Output string
	Err    error
}

// ThreadLoaded carries the open thread's rendered lines from the open
// job to the TUI (R13 two-step: content loads on open only). The app
// renders the worker's messages and runs the registered render
// transforms (decision record 20 - hooks run on the async core with a
// deadline, never inline) before publishing; the TUI only attaches the
// lines. Err names a failed worker call or render; the TUI falls back
// to index mode. Preview marks the preview fetch (the p key): the load
// did NOT mark the thread read, and the TUI shows the popup instead of
// switching to the pager - a stale preview reply (closed or
// re-targeted meanwhile) drops in onThreadLoaded.
type ThreadLoaded struct {
	ThreadID string
	Preview  bool
	Lines    []Line
	Err      error
}

// JobError surfaces a failed background job (R15 error surface); the
// TUI ignores it until the error widget lands.
type JobError struct {
	Job string
	Err error
}

// FilterDone reports a filter run's outcome (R2): the classification
// entry count and the mover's moves and skips, dry-run or applied. The
// per-file detail lines live in diag; this is the summary surface (the
// TUI's status line, R15's async channel).
type FilterDone struct {
	DryRun   bool
	Entries  int
	Moves    int
	Skips    int
	Priority []string // subjects of entries with a [notify] priority tag, capped (F6: subjects only)
}

// LuaResult reports a :lua command or a Lua plugin action run (R8):
// the collected print output plus the error, or nil on success. The
// TUI shows it as a transient status notice. Output is plugin/user
// data, never mail content (F6 - errors never carry message text).
type LuaResult struct {
	Output string
	Err    error
}

// Progress reports a background job's batch progress (R15). Jobs report
// their own totals; the worker action loop is not a progress source.
// View names the virtual folder the job serves - progress is scoped per
// view (inbox, unread, sent, drafts, per-account folders), and the TUI
// shows only the current view's bar.
type Progress struct {
	Job   string
	View  string
	Done  int
	Total int
}
