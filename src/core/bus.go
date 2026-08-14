package core

import "sync"

type Event any

// Bus fans events out to subscribers. A full subscriber drops the event
// (coalescing): consumers repaint from state, never from events.
type Bus struct {
	mu   sync.Mutex
	subs []chan Event
}

func NewBus() *Bus { return &Bus{} }

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
	for _, s := range b.subs {
		select {
		case s <- e:
		default:
		}
	}
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

// JobError surfaces a failed background job (R15 error surface); the
// TUI ignores it until the error widget lands.
type JobError struct {
	Job string
	Err error
}

// Progress reports a background job's batch progress (R15). Jobs report
// their own totals; the worker action loop is not a progress source.
type Progress struct {
	Job   string
	Done  int
	Total int
}
