package core

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// View is a forest of thread trees ordered by last date. All state
// access goes through the locked methods (Rows, MergeThreads,
// SetCursor, CursorRow, SetCollapsed, SetAtts, SetTags, Tags,
// SetGroups, Stage, StagedOps, Undo, ClearStaged, HasStaged,
// IsStaged); touching Threads, message fields, or Collapsed from
// another goroutine is a data race. The cache job's Atts writes are
// T12 wiring and must also go through the view under its lock; UI tag
// toggles go through SetTags the same way.
type View struct {
	Name      string
	Query     string
	Threads   []*Thread // sorted by ThreadLess
	mu        sync.Mutex
	cursorID  string
	lastRow   int
	groups    []TagGroup
	staged    map[string][]TagOp
	stagedGen uint64
	rows      []Row // memoized flatten; rebuilt only when dirty
	dirty     bool
}

func NewView(name, query string) *View {
	return &View{Name: name, Query: query, staged: map[string][]TagOp{}}
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
// only their root row. The flatten is memoized: only structure or
// staged-state changes (MergeThreads, Stage/Undo, SetTags, SetGroups,
// SetCollapsed) mark it dirty - message CONTENT updates (SetAtts) are
// visible through the shared Msg pointers, so a cache scan or a
// progress tick never rebuilds the full row model (the 129k-row case).
func (v *View) Rows() []Row {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.dirty && v.rows != nil {
		return v.rows
	}
	v.rows = v.rowsLocked()
	v.dirty = false
	return v.rows
}

func (v *View) rowsLocked() []Row {
	var rows []Row
	for _, t := range v.Threads {
		rows = append(rows, flattenThread(t, t.Collapsed)...)
	}
	for i := range rows {
		msg := rows[i].Msg
		if msg == nil {
			continue
		}
		// the staged key is the row's identity: the message id, or the
		// thread identity for summary rows (no message id - the index
		// is search data)
		identity := msg.ID
		if identity == "" {
			identity = "t:" + rows[i].ThreadID
		}
		if ops, ok := v.staged[identity]; ok {
			rows[i].Staged = true
			rows[i].StagedTags, _ = ResolveOps(msg.Tags, ops, v.groups)
		}
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
	// per-thread findThread scans are quadratic in the snapshot size (the
	// refresher re-merges the whole snapshot per chunk)
	byID := make(map[string]*Thread, len(v.Threads))
	for _, t := range v.Threads {
		byID[t.ID] = t
	}
	for _, in := range sorted {
		cur := byID[in.ID]
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
	v.dirty = true
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

// SetCursorIndex anchors the cursor by row index instead of message
// id - the stub-row case: search summaries carry no message id, so
// id-anchored tracking is impossible until the viewport hydrate
// replaces the stub. A later SetCursor(id) re-anchors by id.
func (v *View) SetCursorIndex(idx int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.cursorID = ""
	v.lastRow = idx
}

// CursorRowIndex returns the last known cursor row index - the
// fallback CursorRow/CursorIndex use when the cursor id is empty or
// gone (stub rows and post-merge drift).
func (v *View) CursorRowIndex() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lastRow
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
			v.dirty = true
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
	if m := v.findMsgLocked(msgID); m != nil {
		m.Atts = atts
	}
}

// SetTags replaces a message's tags under the view lock. UI tag toggles
// go through here, never by writing Msg.Tags directly: the message
// pointers are shared with the refresher's merge path.
func (v *View) SetTags(msgID string, tags []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if m := v.findMsgLocked(msgID); m != nil {
		m.Tags = tags
		v.dirty = true
	}
}

// Tags returns an identity's applied tags under the view lock; the
// slice is shared, so callers copy before mutating it (SetTags /
// SetThreadTags are the write paths). A message identity resolves its
// message; a thread identity (t:threadID) resolves the thread's
// summary stub, or the first message once the stub is gone (hydrated
// rows). Unknown identities return nil.
func (v *View) Tags(identity string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if strings.HasPrefix(identity, "t:") {
		if t := findThread(v.Threads, identity[2:]); t != nil {
			for _, m := range t.msgs {
				if m.ID == "" {
					return m.Tags
				}
			}
			if len(t.msgs) > 0 {
				return t.msgs[0].Tags
			}
		}
		return nil
	}
	if m := v.findMsgLocked(identity); m != nil {
		return m.Tags
	}
	return nil
}

// identityExistsLocked reports whether the identity names something in
// the view: a message id, or a thread for thread identities.
func (v *View) identityExistsLocked(identity string) bool {
	if strings.HasPrefix(identity, "t:") {
		return findThread(v.Threads, identity[2:]) != nil
	}
	return v.findMsgLocked(identity) != nil
}

// SetThreadTags replaces a thread summary's tags under the view lock -
// the apply-path baseline write for thread identities (the stub's tags
// are the thread's; a hydrated thread self-heals on the next refresh).
func (v *View) SetThreadTags(threadID string, tags []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if t := findThread(v.Threads, threadID); t != nil {
		for _, m := range t.msgs {
			if m.ID == "" {
				m.Tags = tags
				v.dirty = true
				return
			}
		}
	}
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

// SetGroups sets the exclusive tag groups the staged render resolves
// against (R14). The app supplies them from the config store; the view
// never knows the member list itself.
func (v *View) SetGroups(groups []TagGroup) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.groups = groups
	v.dirty = true
}

// Groups returns a copy of the exclusive tag groups under the view lock;
// callers may not mutate the view's slice.
func (v *View) Groups() []TagGroup {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.groups)
}

// Stage appends a pending tag op for an identity - a message id, or a
// thread identity ("t:" + threadID) for summary rows: the index is
// search data without message ids, and a tag op on a summary row is a
// thread-level op (the apply path emits thread:<id>, notmuch's natural
// unit - moving a thread moves all its messages). Staging an op
// identical to one already staged cancels it (toggle semantics: r
// twice is a no-op, r then a keeps both). Unknown identities are a
// no-op: the message left the view. Staging bumps the generation: an
// in-flight apply snapshot taken before it can no longer clear the
// entry (ClearStaged is generation-guarded).
func (v *View) Stage(identity string, op TagOp) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.identityExistsLocked(identity) {
		return
	}
	ops := v.staged[identity]
	for i, o := range ops {
		if o == op {
			ops = append(ops[:i], ops[i+1:]...)
			if len(ops) == 0 {
				delete(v.staged, identity)
			} else {
				v.staged[identity] = ops
			}
			v.stagedGen++
			v.dirty = true
			return
		}
	}
	v.staged[identity] = append(ops, op)
	v.stagedGen++
	v.dirty = true
}

// StagedOps snapshots the buffer for the apply path, with the buffer
// generation at snapshot time; ClearStaged(msgID, gen) no-ops unless
// gen is still current, so ops staged during an in-flight apply cannot
// be cleared by it.
func (v *View) StagedOps() (map[string][]TagOp, uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make(map[string][]TagOp, len(v.staged))
	for id, ops := range v.staged {
		out[id] = append([]TagOp(nil), ops...)
	}
	return out, v.stagedGen
}

// Undo discards all staged ops for a message (pure buffer drop, no DB
// traffic). Unknown ids are a no-op.
func (v *View) Undo(msgID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, ok := v.staged[msgID]; !ok {
		return
	}
	delete(v.staged, msgID)
	v.stagedGen++
	v.dirty = true
}

// ClearStaged removes the entry if the generation still matches, i.e.
// nothing was staged or undone since the caller's snapshot. The guard
// over-blocks: it no-ops when ANY message staged or undid since the
// snapshot, not just this entry, leaving an already-applied entry
// staged until the next apply or undo. Benign: notmuch tag ops are
// idempotent.
func (v *View) ClearStaged(msgID string, gen uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.stagedGen != gen {
		return
	}
	delete(v.staged, msgID)
	v.dirty = true
}

// HasStaged reports whether any message has pending staged ops.
func (v *View) HasStaged() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.staged) > 0
}

// IsStaged reports whether the message has pending staged ops.
func (v *View) IsStaged(msgID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.staged[msgID]
	return ok
}

// findMsgLocked returns the message with the given id, or nil when it
// left the view.
func (v *View) findMsgLocked(msgID string) *Message {
	for _, t := range v.Threads {
		for _, m := range t.msgs {
			if m.ID == msgID {
				return m
			}
		}
	}
	return nil
}
