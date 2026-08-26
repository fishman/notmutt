// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "sync"

type Event any

// Bus fans events out to subscribers; a full subscriber drops the event
// (coalescing) - consumers repaint from state. Completion events keep
// last-value snapshots so a drop never wedges a dialogue.
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
// view. The map write never drops under backpressure - the TUI renders
// this snapshot, events only wake it.
func (b *Bus) LatestProgress(job, view string) (Progress, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.progress[job+"\x00"+view]
	return p, ok
}

// LatestSendResult returns the last published SendResult for a tab:
// the map write never drops, so a completion dropped under
// backpressure still resolves the dialogue instead of wedging it.
func (b *Bus) LatestSendResult(tabID string) (SendResult, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.sendLast[tabID]
	return s, ok
}

// ClearSendResult forgets a tab's last result so a retry re-arms the
// snapshot - a stale failure cannot re-apply while the new job is in
// flight.
func (b *Bus) ClearSendResult(tabID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sendLast, tabID)
}

// LatestComposeOpened returns the most recent ComposeOpened (insertion
// order) so a dropped open event still attaches the dialogue.
func (b *Bus) LatestComposeOpened() (ComposeOpened, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.openLast) == 0 {
		return ComposeOpened{}, false
	}
	return b.openLast[len(b.openLast)-1], true
}

// LatestAiResult returns the last published AiResult for a summary
// job: the map write never drops, so a stream completion dropped under
// backpressure still resolves the summary view.
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

// LatestAddressIndex returns the last published sender corpus: the map
// write never drops, so a dropped harvest result cannot leave the lazy
// trigger pending forever.
func (b *Bus) LatestAddressIndex() (AddressIndex, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.addrLast == nil {
		return AddressIndex{}, false
	}
	return *b.addrLast, true
}

// AddressEntry is one deduplicated sender address (go.notmuch harvest
// shape; Name empty for bare addresses), matched by the compose Tab.
type AddressEntry struct {
	Addr string
	Name string
}

// AddressRequest is the compose completion's lazy trigger: the TUI
// publishes it when Tab meets the length gate and no corpus is loaded.
type AddressRequest struct{}

// AddressIndex carries the harvested sender corpus to the TUI, which
// caches it for the session.
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

// RefreshRequested is the manual poll trigger (the refresh key); the
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
// attaches the dialogue. Mode: "compose" | "reply" | "reply-all" |
// "forward".
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
// dependency-free; compose owns the mapping).
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

// ScheduledResult reports one scheduled mail's fate (app -> TUI): the
// schedule prompt and the due-delivery both surface on the status
// line. At is the human time the mail was scheduled for; the payload
// never carries mail content (F6).
type ScheduledResult struct {
	ID  string
	OK  bool
	At  string
	Err error
}

// ThreadLoaded carries a thread's rendered lines to the TUI (R13
// two-step: content loads on open only). The app renders the worker's
// messages and runs the render transforms (decision record 20 - hooks
// run on the async core with a deadline, never inline) before
// publishing. Err names a failed worker call or render - the TUI
// falls back to index mode. Preview is the p key fetch: the load did
// NOT mark the thread read, the TUI shows the popup instead of the
// pager, and a stale preview reply (closed or re-targeted) drops.
// RenderMode selects the pager's view: plain parts, rendered html, or
// the html raw source (ctrl+u), falling back to the parts the message
// actually carries; Mime reports what actually rendered.
type RenderMode int

const (
	RenderPlain RenderMode = iota
	RenderHTML
	RenderSource
	// RenderAuto is the open key's default: the app resolves it per sender
	// domain ([pager] default-views) before publishing - the sentinel
	// never rides the bus, the reply always carries the resolved view.
	RenderAuto
)

type ThreadLoaded struct {
	ThreadID string
	// MsgID is the opened message: the pager renders that message only,
	// never the whole thread (thread-wide text stays queryable via lua).
	MsgID   string
	Preview bool
	// RenderMode names the view the lines were rendered with (the
	// toggle-render and source keys): a same-thread reload with another
	// view replaces the pager content instead of being dropped.
	RenderMode RenderMode
	// Headers echoes the open's header toggle (the h key): the full
	// header block renders atop the plain view.
	Headers bool
	// LinkLabels marks a label-render (the pager F key): every html link
	// carries its "[N]" label inline; a same-thread reload without labels
	// replaces the labeled content.
	LinkLabels bool
	// Links is the label-render's target list (label N opens Links[N-1],
	// document order), empty unlabeled.
	Links []string
	// Mime is the rendered content's mime label (text/plain or text/html)
	// for the status bar - what is on screen, never the requested view.
	Mime  string
	Lines []Line
	// SMIME is the opened message's S/MIME verdict (R10), nil when unsigned
	// or no verifier is configured.
	SMIME *SMIMEStatus
	Err   error
}

// SMIMEStatus is the read-path S/MIME verdict. Crypto validity and signer
// identity are separate (R10): a valid signature from an unexpected cert
// must render as a warning, never green. Err is set when verification could
// not run (no roots, unparseable CMS) - distinct from a failed signature.
type SMIMEStatus struct {
	Present bool
	Valid   bool
	Signer  string
	Revoked bool
	Checked bool
	Err     string
}

// AttachmentLoaded carries the attachment view (the v dialog's enter):
// the chosen attachment rendered to pager lines, or the error - the
// TUI swaps the pager content, back re-opens the message. Ordinal
// echoes the request (the save key's re-extraction index).
type AttachmentLoaded struct {
	ThreadID string
	// MsgID is the message the attachment came from (pager identity is
	// message-scoped).
	MsgID   string
	Ordinal int
	Name    string
	Lines   []Line
	Err     error
}

// AttachmentSaved carries the attachment save result (the s key in an
// attachment view): the write target, or the error the TUI surfaces.
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

// FilterDone reports a filter run's outcome (R2): the entry count and
// the mover's moves and skips, dry-run or applied. Per-file detail
// lines live in diag; this is the summary surface (R15's async channel).
type FilterDone struct {
	DryRun   bool
	Entries  int
	Notify   int // entries that carry every [notify] tags entry (default: unread inbox)
	Moves    int
	Skips    int
	Priority []NotifyHeadline // summary rows: priority entries first, the batch filling the cap (F6: no ids, no bodies)
}

// NotifyHeadline is one notification row: sender, subject, timestamp -
// the 3 display parts of an email, never ids or bodies (F6).
type NotifyHeadline struct {
	Sender    string
	Subject   string
	Timestamp int64
}

// LuaResult reports a :lua command or Lua plugin action run (R8): the
// print output plus the error, or nil on success - a transient status
// notice. Output is plugin/user data, never mail content (F6).
type LuaResult struct {
	Output string
	Err    error
}

// CategorizeResult reports the categorize hotkey pass (the index
// categorize action): the save/skip lines plus the tallies, or the
// error. Lines are per-attachment targets, never message content.
type CategorizeResult struct {
	ThreadID string
	Lines    []string
	Saved    int
	Skipped  int
	Err      error
}

// AiStarted opens the AI summary view (R8): the app publishes it when
// an ai_chat plugin call begins streaming; the TUI saves the pager's
// current lines and swaps in a placeholder. MsgID is the displaced
// message (the TUI fills it when the app cannot know it) - back
// restores that message's render.
type AiStarted struct {
	JobID    string
	ThreadID string
	MsgID    string
}

// AiChunk carries one streamed text delta; the TUI appends it to the
// summary pager (append-as-it-arrives, the R3 diff discipline).
type AiChunk struct {
	JobID string
	Text  string
}

// AiResult reports a summary stream's completion: Err names the
// failure - the view shows an error banner and back restores the mail.
// The last value is snapshotted so a drop never wedges the view.
type AiResult struct {
	JobID string
	Err   error
}

// PickerRequest asks the TUI to run an external picker (R8): the Lua
// action's picker_* call queues it; the TUI runs the argv (registry
// name, or the inline Argv - F4, argv only) with the chooser-file
// temp file, then publishes PickerResult back and the app resumes.
type PickerRequest struct {
	ID   string
	Name string   // registered attach command name ("" when Argv is set)
	Argv []string // inline argv (picker_argv), appended with the chooser file
}

// PickerResult returns the picker's selection to the app: the chooser
// file's paths, one per line, or the error.
type PickerResult struct {
	ID    string
	Paths []string
	Err   error
}

// PromptRequest asks the TUI to open a native prompt dialogue (R8):
// the Lua prompt() call queues it; the answer (or cancel) publishes
// PromptResult back and the app resumes the blocked VM.
type PromptRequest struct {
	ID      string
	Label   string // the prompt label, e.g. "Language:"
	Prefill string // initial input ("" = empty)
}

// PromptResult returns the prompt's outcome: Text is the committed
// input; Canceled marks the esc cancel (Text empty).
type PromptResult struct {
	ID       string
	Text     string
	Canceled bool
}

// AttachFiles attaches files to the active compose tab (R8): the Lua
// attach_add calls drain here after the action returns.
type AttachFiles struct {
	Paths []string
}

// TagStaged carries the Lua action's staged tag ops to the TUI (R8,
// the AI-classification flow): staging is the ONLY tag surface a
// script gets - the ops land in the staged buffer like a UI keypress
// and the APPLY key flushes them (R14). Lua never writes notmuch
// directly; ThreadID names the cursor message the script classified.
type TagStaged struct {
	ThreadID string
	Ops      []TagOp
}

// Progress reports a background job's batch progress (R15); jobs
// report their own totals - the worker action loop is not a progress
// source. View scopes progress per virtual folder; the TUI shows only
// the current view's bar.
type Progress struct {
	Job   string
	View  string
	Done  int
	Total int
}
