package core

import (
	"fmt"
	"sort"
	"sync"
)

// View is a forest of thread trees ordered by last date. All state
// access goes through the locked methods (Rows, MergeThreads,
// SetCursor, CursorRow, SetCollapsed); touching Threads, message
// fields, or Collapsed from another goroutine is a data race. The
// cache job's Atts writes are T12 wiring and must also go through the
// view under its lock; UI tag toggles go through SetTags the same way.
type View struct {
	Name     string
	Query    string
	Threads  []*Thread // sorted by ThreadLess
	mu       sync.Mutex
	cursorID string
	lastRow  int
}

func NewView(name, query string) *View {
	return &View{Name: name, Query: query}
}

// NewThread builds a thread with a reference tree. msgs are sorted by
// MsgLess and copied.
func NewThread(id string, msgs []*Message) *Thread {
	sorted := append([]*Message(nil), msgs...)
	sort.Slice(sorted, func(i, j int) bool { return MsgLess(sorted[i], sorted[j]) })
	t := &Thread{ID: id, msgs: sorted}
	var last int64
	for _, m := range sorted {
		if m.Timestamp > last {
			last = m.Timestamp
		}
	}
	t.LastDate = last
	t.Root = buildTree(sorted)
	return t
}

func (t *Thread) Count() int {
	if t.Root == nil {
		return 0
	}
	n := 0
	var walk func(*Node)
	walk = func(node *Node) {
		if node.Msg != nil {
			n++
		}
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(t.Root)
	return n
}

// Rows flattens the thread forest depth-first. Collapsed threads render
// only their root row.
func (v *View) Rows() []Row {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.rowsLocked()
}

func (v *View) rowsLocked() []Row {
	var rows []Row
	for _, t := range v.Threads {
		rows = append(rows, flattenThread(t, t.Collapsed)...)
	}
	return rows
}

func flattenThread(t *Thread, collapsed bool) []Row {
	var rows []Row
	if t.Root == nil {
		return rows
	}
	count := t.Count()
	var walk func(*Node, int)
	walk = func(node *Node, depth int) {
		if node.Msg == nil {
			rows = append(rows, Row{Ghost: true, ThreadID: t.ID, Depth: depth, Count: count})
			if collapsed {
				return
			}
			for _, c := range node.Children {
				walk(c, depth+1)
			}
			return
		}
		rows = append(rows, Row{Msg: node.Msg, ThreadID: t.ID, Depth: depth, Root: depth == 0, Count: count})
		if collapsed {
			return
		}
		for _, c := range node.Children {
			walk(c, depth+1)
		}
	}
	walk(t.Root, 0)
	return rows
}

// MergeThreads diffs the incoming threads into the view: thread-level
// diff plus per-thread message diffs for threads present on both sides.
// Input is sorted defensively (the caller's slice is not modified).
// Matched messages keep their identity; the snapshot reconciles their
// fields (reconcile-then-replay is the ordering the optimistic layer
// of T11/T12 builds on, per plan section 8). The cursor survives by
// id.
func (v *View) MergeThreads(threads []*Thread) {
	v.mu.Lock()
	defer v.mu.Unlock()
	sorted := append([]*Thread(nil), threads...)
	sort.Slice(sorted, func(i, j int) bool { return ThreadLess(sorted[i], sorted[j]) })
	ops := DiffSorted(v.Threads, sorted, ThreadLess, func(t *Thread) string { return t.ID })
	v.Threads = Apply(v.Threads, ops)
	for _, in := range sorted {
		cur := findThread(v.Threads, in.ID)
		if cur == nil {
			continue // pure insert: already carries its tree
		}
		mops := DiffSorted(cur.msgs, in.msgs, MsgLess, func(m *Message) string { return m.ID })
		cur.msgs = Apply(cur.msgs, mops)
		// Matched keys keep old elements, so snapshot fields must be
		// reconciled onto them. Reconcile-then-replay is the ordering
		// the optimistic layer (T11/T12) builds on: apply snapshot
		// truth first, then replay pending local ops.
		for i, j := 0, 0; i < len(cur.msgs) && j < len(in.msgs); {
			c, f := cur.msgs[i], in.msgs[j]
			if c.ID == f.ID {
				reconcileMsg(c, f)
				i++
				j++
			} else if MsgLess(c, f) {
				i++
			} else {
				j++
			}
		}
		cur.LastDate = in.LastDate
		cur.Root = buildTree(cur.msgs)
	}
	// Retained threads can change LastDate (snapshots are filtered by
	// the view query), so restore the sorted invariant the next diff
	// depends on.
	sort.Slice(v.Threads, func(i, j int) bool { return ThreadLess(v.Threads[i], v.Threads[j]) })
}

// reconcileMsg copies snapshot fields from the fresh message onto the
// retained one. Atts are never copied (the cache job owns them and
// snapshots carry empty lists) and Paths are never copied (the
// thread-fetch path returns none).
func reconcileMsg(cur, fresh *Message) {
	cur.Timestamp = fresh.Timestamp
	cur.Author = fresh.Author
	cur.Subject = fresh.Subject
	cur.Tags = fresh.Tags
	cur.References = fresh.References
	cur.ThreadID = fresh.ThreadID
}

func findThread(threads []*Thread, id string) *Thread {
	for _, t := range threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (v *View) SetCursor(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cursorID = id
}

// CursorRow returns the row the cursor points at, or the row at the
// previous index (clamped to the last row) when the id is gone; ok is
// false only when the view is empty.
func (v *View) CursorRow() (Row, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	rows := v.rowsLocked()
	if len(rows) == 0 {
		return Row{}, false
	}
	if v.cursorID != "" {
		for i, r := range rows {
			if r.Msg != nil && r.Msg.ID == v.cursorID {
				v.lastRow = i
				return r, true
			}
		}
	}
	if v.lastRow >= len(rows) {
		v.lastRow = len(rows) - 1
	}
	return rows[v.lastRow], true
}

// SetCollapsed toggles a thread's collapsed state under the view lock.
// It errors on unknown thread ids; collapse toggles go through the view
// from now on, never by writing Threads[i].Collapsed directly.
func (v *View) SetCollapsed(id string, collapsed bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			t.Collapsed = collapsed
			return nil
		}
	}
	return fmt.Errorf("view: unknown thread %q", id)
}

// SetAtts records the cache job's attachment list under the view lock
// (see the View doc comment). Unknown ids are a no-op: the message left
// the view between the scan and the write.
func (v *View) SetAtts(msgID string, atts []Attachment) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		for _, m := range t.msgs {
			if m.ID == msgID {
				m.Atts = atts
				return
			}
		}
	}
}

// SetTags replaces a message's tags under the view lock. UI tag toggles
// go through here, never by writing Msg.Tags directly: the message
// pointers are shared with the refresher's merge path.
func (v *View) SetTags(msgID string, tags []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		for _, m := range t.msgs {
			if m.ID == msgID {
				m.Tags = tags
				return
			}
		}
	}
}

// MsgTags returns a message's tags under the view lock; the slice is
// shared, so callers copy before mutating it (SetTags is the write
// path). Unknown ids return nil.
func (v *View) MsgTags(msgID string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		for _, m := range t.msgs {
			if m.ID == msgID {
				return m.Tags
			}
		}
	}
	return nil
}

// buildTree attaches each message under the nearest present reference;
// messages without a present parent become roots. Multiple roots get a
// synthetic ghost root (mutt "[...]" row).
func buildTree(msgs []*Message) *Node {
	nodes := make(map[string]*Node, len(msgs))
	for _, m := range msgs {
		nodes[m.ID] = &Node{Msg: m}
	}
	var roots []*Node
	for _, m := range msgs {
		n := nodes[m.ID]
		p := parentOf(m, nodes)
		if p == nil {
			roots = append(roots, n)
		} else {
			p.Children = append(p.Children, n)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	if len(roots) == 1 {
		return roots[0]
	}
	return &Node{Children: roots}
}

// parentOf scans references in reverse so the nearest present ancestor
// wins over distant ones.
func parentOf(m *Message, nodes map[string]*Node) *Node {
	for i := len(m.References) - 1; i >= 0; i-- {
		ref := m.References[i]
		if ref == m.ID {
			continue
		}
		if p, ok := nodes[ref]; ok {
			return p
		}
	}
	return nil
}
