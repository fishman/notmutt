package core

import "sync"

type Event any

// Bus fans events out to subscribers. A full subscriber drops the event
// (coalescing): consumers repaint from state, never from events.
type Bus struct {
	mu       sync.Mutex
	subs     []chan Event
	progress map[string]Progress
}

func NewBus() *Bus {
	return &Bus{progress: map[string]Progress{}}
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
	if p, ok := e.(Progress); ok {
		b.progress[p.Job+"\x00"+p.View] = p
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

type QueryBatch struct {
	View  string
	Total int
	Done  bool
}

type WorkerDone struct{ Job string }

type WorkerLockTimeout struct{ Kind string }

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
	TabID       string
	Mode        string
	Account     string
	From        string
	To, Cc      []string
	Subject     string
	Body        string
	Attachments []ComposeAttachment
	Signature   string
	SigContent  string
	MessageID   string
	References  []string
	OriginalID  string
}

// ComposeAttachment is the bus contract's attachment shape (core stays
// dependency-free; compose owns the mapping to its own type).
type ComposeAttachment struct {
	Name, Path string
	Size       int64
}

// SendResult reports the send job's outcome to the dialogue (R4): OK
// closes the tab; a failure keeps it open with Output for review.
type SendResult struct {
	TabID  string
	OK     bool
	Output string
	Err    error
}

// ThreadLoaded carries the open thread's messages (headers + file
// paths) from the worker to the TUI (R13 two-step: content loads on
// open only). Err names a failed worker call; the TUI falls back to
// index mode.
type ThreadLoaded struct {
	ThreadID string
	Msgs     []Message
	Err      error
}

// JobError surfaces a failed background job (R15 error surface); the
// TUI ignores it until the error widget lands.
type JobError struct {
	Job string
	Err error
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
