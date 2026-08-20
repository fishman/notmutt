// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
	aiLast   map[string]AiResult
}

func NewBus() *Bus {
	return &Bus{progress: map[string]Progress{}, sendLast: map[string]SendResult{}, aiLast: map[string]AiResult{}}
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
	case AiResult:
		b.aiLast[e.JobID] = e
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

// LatestAiResult returns the last published AiResult for a summary
// job. The map write never drops, so a stream completion dropped from
// the channel under backpressure still resolves the summary view on
// the next keypress.
func (b *Bus) LatestAiResult(jobID string) (AiResult, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.aiLast[jobID]
	return r, ok
}

// ClearAiResult forgets a summary job's result when its view closes.
func (b *Bus) ClearAiResult(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.aiLast, jobID)
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
// RenderMode selects the pager's view of a message: the plain parts,
// the rendered html part, or the html part's raw source (the ctrl+u
// key). The view falls back to what the message actually carries - the
// html view of a plain-only message and the plain view of an html-only
// message render the parts that exist; the Mime label of the reply
// says what actually rendered.
type RenderMode int

const (
	RenderPlain RenderMode = iota
	RenderHTML
	RenderSource
	// RenderAuto is the open key's default: the app resolves it per
	// sender domain ([pager] default-views) before publishing - the
	// sentinel never rides the bus, the reply always carries the
	// resolved view.
	RenderAuto
)

type ThreadLoaded struct {
	ThreadID string
	// MsgID is the opened message: the pager renders that message's
	// content only, never the whole thread (the thread-wide text stays
	// queryable via the lua layer, not as the default view).
	MsgID   string
	Preview bool
	// RenderMode names the view the lines were rendered with (the
	// toggle-render and source keys): the TUI compares it against its
	// own mode so a same-thread reload with another view replaces the
	// pager content instead of being dropped as a duplicate.
	RenderMode RenderMode
	// Headers echoes the open's header toggle (the h key): the full
	// header block renders at the top of the plain view.
	Headers bool
	// LinkLabels marks a label-render (the pager F key): every link
	// in the html view carries its "[N]" label inline. The TUI
	// compares it against its own link mode so a same-thread reload
	// without labels replaces the labeled content.
	LinkLabels bool
	// Links is the label-render's target list (label N opens
	// Links[N-1], document order), empty in an unlabeled render.
	Links []string
	// Mime is the rendered content's mime label (text/plain or
	// text/html) for the status bar - what is on screen, resolved
	// against the message's actual parts, never the requested view.
	Mime  string
	Lines []Line
	// Mark is the opened message's thread-position marker, computed
	// against the full thread fetch (the pager shows one message, the
	// mark keeps its place in the conversation - the recent-5 tint or
	// the prominent other-side one).
	Mark MsgMark
	Err  error
}

// AttachmentLoaded carries the attachment view (the v dialog's
// enter): the chosen attachment rendered to pager lines, or the
// error - the TUI swaps the pager content and back re-opens the
// message to restore. Ordinal echoes the request (the save key's
// re-extraction index).
type AttachmentLoaded struct {
	ThreadID string
	// MsgID is the message the attachment came from (the pager's
	// identity is message-scoped: back re-opens the same message).
	MsgID   string
	Ordinal int
	Name    string
	Lines   []Line
	Err     error
}

// AttachmentSaved carries the attachment save result (the s key in an
// attachment view): the write target, or the error - the TUI surfaces
// the outcome on the status line.
type AttachmentSaved struct {
	Path string
	Err  error
}

// ImageFetched carries one remote image fetch (the render-images
// remote mode): the bytes for the URL, or the error - the TUI attaches
// them to the image lines. Data is an image blob, never message text
// (F6).
type ImageFetched struct {
	URL  string
	Data []byte
	Err  error
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
	Priority []NotifyHeadline // summary rows: priority entries first, the batch filling the cap (F6: no ids, no bodies)
}

// NotifyHeadline is one notification row: sender, subject, and
// timestamp - the 3 display parts of an email, never ids or bodies
// (F6). The notify side effect renders these.
type NotifyHeadline struct {
	Sender    string
	Subject   string
	Timestamp int64
}

// LuaResult reports a :lua command or a Lua plugin action run (R8):
// the collected print output plus the error, or nil on success. The
// TUI shows it as a transient status notice. Output is plugin/user
// data, never mail content (F6 - errors never carry message text).
type LuaResult struct {
	Output string
	Err    error
}

// AiStarted opens the AI summary view (R8): the app publishes it when
// an ai_chat plugin call begins streaming; the TUI saves the pager's
// current lines and swaps in a placeholder. MsgID is the message the
// summary displaced (the TUI fills it from the pager when the app
// cannot know it) - back restores that message's render.
type AiStarted struct {
	JobID    string
	ThreadID string
	MsgID    string
}

// AiChunk carries one streamed text delta from the AI provider; the
// TUI appends it to the summary pager (append-as-it-arrives, the R3
// diff discipline).
type AiChunk struct {
	JobID string
	Text  string
}

// AiResult reports a summary stream's completion: Err names the
// failure, and the summary view shows an error banner with the mail
// restored on the back key. The last value is snapshotted so a drop
// never wedges the view.
type AiResult struct {
	JobID string
	Err   error
}

// PickerRequest asks the TUI to run an external picker (R8): the Lua
// action's picker_* call queues it, the TUI runs the argv (by name from
// the attach-command registry, or the inline Argv - F4, argv only) with
// the chooser-file temp file (the attach-command exec path), then
// publishes PickerResult back and the app resumes the blocked VM.
type PickerRequest struct {
	ID   string
	Name string   // registered attach command name ("" when Argv is set)
	Argv []string // inline argv (picker_argv), appended with the chooser file
}

// PickerResult returns the picker's selection to the app: the paths
// from the chooser file, one per line, or the error.
type PickerResult struct {
	ID    string
	Paths []string
	Err   error
}

// PromptRequest asks the TUI to open a native prompt dialogue (R8):
// the Lua prompt() call queues it, the user's answer (or the cancel)
// publishes PromptResult back and the app resumes the blocked VM.
type PromptRequest struct {
	ID      string
	Label   string // the prompt label, e.g. "Language:"
	Prefill string // initial input ("" = empty)
}

// PromptResult returns the prompt's outcome: Text is the committed
// input, Canceled marks the esc cancel (Text empty).
type PromptResult struct {
	ID       string
	Text     string
	Canceled bool
}

// AttachFiles attaches files to the active compose tab (R8): the Lua
// action's attach_add calls drain here after the action returns.
type AttachFiles struct {
	Paths []string
}

// TagStaged carries the Lua action's staged tag ops to the TUI (R8,
// the AI-classification flow): staging is the ONLY tag surface a
// script gets - the ops land in the current folder's staged buffer
// exactly like a UI keypress, and the APPLY key flushes them (R14).
// Lua never writes notmuch directly; ThreadID names the cursor
// message the script classified.
type TagStaged struct {
	ThreadID string
	Ops      []TagOp
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
