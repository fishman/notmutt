// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
	// display filter (F): narrows the flattened rows at materialization.
	// filterRows/filterIdx are parallel (filtered row -> full index);
	// filterMap maps full index -> filtered index for the cursor. The
	// filter is display-only: the thread set and the query are
	// untouched, the maps re-derive on every dirty rebuild (R1 - the
	// view stays a notmuch query).
	filter     string
	filterRows []Row
	filterIdx  []int
	filterMap  map[int]int
	// mergeDepth/mergeDirty gate the dirty-mark during refresh fills
	// (BeginMerge/EndMerge): merges inside an open batch never mark
	// dirty individually, so the flatten stays stable across the
	// intermediate keypresses and rebuilds once per batch end.
	mergeDepth int
	mergeDirty bool
	// gen is the view generation: bumped on Reset, so the hydrator can
	// scope its dedupe, cursor, and merges to the view's current query
	// (thread ids span folders - a wave started for one view must never
	// land in another's rows).
	gen uint64
	// the tree window budget ([index.thread]): winRows rows per thread.
	// Zero winRows = no window.
	winRows int
	// me is the identity set for the thread-tail marks (SetMe, the
	// account from fields): the sent-tag or address "me" detection in
	// ClassifyRows. Zero = sent-tag identity only.
	me []string
}

// SetMe sets the identity set for the thread-tail marks (the account
// from fields; MyAddrs). The app applies it at startup like the other
// view configuration.
func (v *View) SetMe(me []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.me = me
	v.dirty = true
}

// SetWindowBudget bounds the tree window (the [index.thread] config):
// each thread renders at most winRows rows starting at its WinStart.
// Zero winRows disables the window.
func (v *View) SetWindowBudget(winRows int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.winRows = winRows
}

func NewView(name, query string) *View {
	return &View{Name: name, Query: query, staged: map[string][]TagOp{}}
}

// ViewName and ViewQuery are the locked identity reads: the refresher's
// config switch rewrites the fields in place on its own goroutine
// (SetIdentity), while the jobs read them for event labels and the
// apply path builds identity queries - cross-goroutine by design.
func (v *View) ViewName() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.Name
}

func (v *View) ViewQuery() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.Query
}

// SetIdentity is the single write path for the view's name and query
// (the refresher's onConfig switch).
func (v *View) SetIdentity(name, query string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Name = name
	v.Query = query
}

// Reset drops the view's rows for a full re-load: a view switch renders
// empty until the new query's first chunk lands, never the previous
// view's rows. Staged ops survive - the buffer is keyed by message
// identity, not position or view (R14).
func (v *View) Reset() {
	v.mu.Lock()
	v.Threads = nil
	v.rows = nil
	v.dirty = true
	v.cursorID = ""
	v.lastRow = 0
	v.gen++
	v.mu.Unlock()
}

// Gen is the view generation: the hydrator scopes its wave to it, so
// a view switch neither suppresses nor misdirects in-flight fetches.
func (v *View) Gen() uint64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gen
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
		full := flattenThread(t, t.Collapsed)
		// the thread-tail marks: only a windowed thread marks - one with
		// rows hidden above or below (the "+N more" ghosts). The marks
		// classify the FULL tree and ride the rows through the window,
		// so the recent-5 of a long thread tints wherever it sits in the
		// window and a thread that fits renders unmarked. Computed in
		// the flatten so the marks are memoized with the rows - the
		// index tints without any open, and a rebuild re-derives them at
		// the same cost.
		if v.winRows > 0 && len(full) > v.winRows {
			marks := ClassifyRows(full, v.me)
			for j := range full {
				full[j].Mark = marks[j]
			}
		}
		out := window(full, t.WinStart, v.winRows)
		// the tree-window overflow: a thread with rows hidden below the
		// window marks its last emitted row with the count (the page
		// move continues the thread there) and emits a ghost "+N more"
		// indicator row in the free space under the thread
		if v.winRows > 0 && len(full) > v.winRows {
			start := max(0, min(t.WinStart, len(full)-v.winRows))
			if n := len(full) - (start + v.winRows); n > 0 {
				out[len(out)-1].More = n
				out = append(out, Row{Ghost: true, ThreadID: t.ID, More: n})
			}
			// the top-side mirror: a window that starts mid-thread emits
			// a leading ghost with the hidden-above count, so the rows
			// over the window stay visible (the walk-up slides into them)
			if start > 0 {
				out = append([]Row{{Ghost: true, ThreadID: t.ID, MoreTop: start}}, out...)
			}
		}
		rows = append(rows, out...)
	}
	// re-anchor the cursor index by id at materialization (once per
	// merge, never per paint): the render's CursorIndex read is O(1)
	if v.cursorID != "" {
		for i := range rows {
			if rows[i].Msg != nil && rows[i].Msg.ID == v.cursorID {
				v.lastRow = i
				break
			}
		}
	}
	for i := range rows {
		msg := rows[i].Msg
		if msg == nil {
			continue
		}
		// the staged key is the row's identity: the message id, or the
		// thread identity for summary rows (no message id - the index
		// is search data). A message whose id carries no ops falls back
		// to its thread's ops: an op staged on the stub row survives
		// hydration (R14 - the staged state never vanishes mid-session).
		identity := msg.ID
		if identity == "" {
			identity = "t:" + rows[i].ThreadID
		} else if _, ok := v.staged[identity]; !ok && rows[i].ThreadID != "" {
			identity = "t:" + rows[i].ThreadID
		}
		if ops, ok := v.staged[identity]; ok {
			rows[i].Staged = true
			rows[i].StagedTags, _ = ResolveOps(msg.Tags, ops, v.groups)
		}
	}
	if v.filter == "" {
		v.filterRows, v.filterIdx, v.filterMap = nil, nil, nil
		return rows
	}
	// the display filter: rows that match stay, the rest drop. The
	// cursor stays on its message when the filter hides it - the
	// mapping falls back to the first visible row.
	full := rows
	v.filterRows = v.filterRows[:0]
	v.filterIdx = v.filterIdx[:0]
	v.filterMap = make(map[int]int, len(full))
	for i, r := range full {
		if !rowMatches(r, v.filter) {
			continue
		}
		v.filterMap[i] = len(v.filterRows)
		v.filterRows = append(v.filterRows, r)
		v.filterIdx = append(v.filterIdx, i)
	}
	return v.filterRows
}

// rowMatches is the filter predicate: a case-insensitive substring
// over the row's index data - author, subject, and tag names (the F
// filter's "text or other data that exists in the index view").
func rowMatches(r Row, f string) bool {
	msg := r.Msg
	if msg == nil {
		return false // ghost rows carry no index data
	}
	if strings.Contains(strings.ToLower(msg.Author), f) ||
		strings.Contains(strings.ToLower(msg.Subject), f) {
		return true
	}
	for _, tag := range msg.Tags {
		if strings.Contains(tag, f) {
			return true
		}
	}
	return false
}

// SetFilter narrows the displayed rows to the ones matching the text
// (case-insensitive substring over author, subject, and tags - the
// live F filter). Display-only (R1): the query and the thread set are
// untouched; merges keep feeding the view and re-derive the filter at
// the next materialization.
func (v *View) SetFilter(f string) {
	v.mu.Lock()
	v.filter = strings.ToLower(f)
	v.dirty = true
	v.mu.Unlock()
}

// Filter is the active display filter text (empty = no filter).
func (v *View) Filter() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.filter
}

// collapseMsg is the message a collapsed thread row shows: the newest
// unread message in the thread, or the newest message when nothing is
// unread (the row keeps the root's tree position; the subject tells
// what still needs reading). Nil for a thread without messages - the
// ghost root renders the stub.
func collapseMsg(root *Node) *Message {
	var last, unread *Message
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Msg != nil {
			if last == nil || n.Msg.Timestamp > last.Timestamp {
				last = n.Msg
			}
			if slices.Contains(n.Msg.Tags, "unread") && (unread == nil || n.Msg.Timestamp > unread.Timestamp) {
				unread = n.Msg
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if unread != nil {
		return unread
	}
	return last
}

func flattenThread(t *Thread, collapsed bool) []Row {
	var rows []Row
	if t.Root == nil {
		return rows
	}
	count := t.Count()
	if collapsed {
		rows = append(rows, Row{Msg: collapseMsg(t.Root), ThreadID: t.ID, Count: count, Collapsed: true})
		return rows
	}
	child := func(siblings []bool, last bool) []bool {
		s := make([]bool, 0, len(siblings)+1)
		s = append(s, !last)
		return append(s, siblings...)
	}
	var walk func(*Node, int, []bool)
	walk = func(node *Node, depth int, siblings []bool) {
		if node.Msg == nil {
			if node.Forest {
				for _, c := range node.Children {
					walk(c, depth, nil)
				}
				return
			}
			rows = append(rows, Row{Ghost: true, ThreadID: t.ID, Depth: depth, Siblings: siblings, Count: count})
			for i, c := range node.Children {
				walk(c, depth+1, child(siblings, i == len(node.Children)-1))
			}
			return
		}
		rows = append(rows, Row{Msg: node.Msg, ThreadID: t.ID, Depth: depth, Root: depth == 0, Siblings: siblings, Count: count})
		for i, c := range node.Children {
			walk(c, depth+1, child(siblings, i == len(node.Children)-1))
		}
	}
	walk(t.Root, 0, nil)
	return rows
}

// window bounds one thread's flattened rows to the tree window: the
// rows [start, start+winRows). Zero winRows passes the thread through
// untouched; start is clamped to the valid range (merges can shrink
// the flatten between slides). A window that starts mid-thread cuts
// the leading tree columns: every row's Depth shifts by the first
// row's depth (the marker of the first visible row lands at column 0),
// so a deep thread's window still shows the subject lines. The shift
// is display-only - the Siblings chains keep their true indexing, and
// column c then shows the ancestor at true depth cut+c (the visible
// row above), never a cut one.
func window(full []Row, start, winRows int) []Row {
	if winRows <= 0 {
		return full
	}
	if len(full) <= winRows {
		return full
	}
	start = max(0, min(start, len(full)-winRows))
	out := full[start : start+winRows]
	if cut := out[0].Depth; cut > 0 {
		for i := range out {
			out[i].Depth = max(1, out[i].Depth-cut+1)
		}
	}
	return out
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
		if stubThread(in) && hasRealMsg(cur) {
			cur.LastDate = in.LastDate // summary ordering data is still fresh
			continue                   // a stub snapshot must not delete a hydrated tree
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
	if v.mergeDepth > 0 {
		v.mergeDirty = true
	} else {
		v.dirty = true
	}
}

// BeginMerge opens a merge-batching window: MergeThreads calls inside
// it do not mark the view dirty, so Rows() keeps returning the last
// flatten across the intermediate keypresses of a refresh fill.
// EndMerge marks dirty once if any merge ran inside, so the flatten
// rebuilds once per batch end. No-op when no batch is open: single
// merges (tests, tag ops) keep their immediate dirty-mark. The depth
// counter makes nested windows safe; an EndMerge without a matching
// BeginMerge is a no-op.
// Dirty reports whether the memoized rows need rebuilding: the batch
// discipline defers the mark to EndMerge, so the batch owner publishes
// its completion diff only when a merge actually landed inside - a
// no-op wave must not self-trigger.
func (v *View) Dirty() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.dirty
}

func (v *View) BeginMerge() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mergeDepth++
}

func (v *View) EndMerge() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.mergeDepth == 0 {
		return
	}
	v.mergeDepth--
	if v.mergeDepth == 0 && v.mergeDirty {
		v.dirty = true
		v.mergeDirty = false
	}
}

// stubThread reports a thread with only summary rows (no message has an
// id - the refresh feed shape). A stub snapshot must never delete a
// hydrated tree (the MergeThreads guard).
func stubThread(t *Thread) bool { return !hasRealMsg(t) }

func hasRealMsg(t *Thread) bool {
	for _, m := range t.msgs {
		if m.ID != "" {
			return true
		}
	}
	return false
}

// MergeThread replaces one thread's messages with a fetched snapshot
// (the hydrator path): the thread keeps its collapse and window state
// and its sorted position; other threads are untouched. No-op when the
// thread left the view mid-fetch (a view switch raced the reply).
func (v *View) MergeThread(in *Thread) {
	v.mu.Lock()
	defer v.mu.Unlock()
	cur := findThread(v.Threads, in.ID)
	if cur == nil {
		return
	}
	cur.msgs = append([]*Message(nil), in.msgs...)
	sort.Slice(cur.msgs, func(i, j int) bool { return MsgLess(cur.msgs[i], cur.msgs[j]) })
	cur.LastDate = in.LastDate
	cur.Root = buildTree(cur.msgs)
	sort.Slice(v.Threads, func(i, j int) bool { return ThreadLess(v.Threads[i], v.Threads[j]) })
	// the batch discipline of MergeThreads: the hydrator merges a
	// whole wave under one BeginMerge, so the flatten rebuilds once
	// per wave, not once per thread.
	if v.mergeDepth > 0 {
		v.mergeDirty = true
	} else {
		v.dirty = true
	}
}

// Hydrated reports whether the thread holds real messages (the stub
// guard's positive side): the hydrator skips hydrated threads and the
// refresher re-fetches changed ones.
func (v *View) Hydrated(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			return hasRealMsg(t)
		}
	}
	return false
}

// ThreadMsgs returns the thread's full message set - the walk's rows
// carry headers and paths, the open path's rows-first resolution (an
// open must not queue behind the full walk that owns the worker).
// nil when the thread is not in the view.
func (v *View) ThreadMsgs(id string) []*Message {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			return t.msgs
		}
	}
	return nil
}

// SlideWindow advances the thread's tree window by step rows and
// reports whether it moved. False at the edges: the walk-through steps
// (+-1) refuse when nothing hides in that direction or the thread fits
// the budget, so the caller steps on normally. A larger step (the page
// move) clamps to the tail instead of refusing - the hidden rows
// exist, the tail window is the last chunk.
func (v *View) SlideWindow(threadID string, step int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.winRows <= 0 {
		return false
	}
	for _, t := range v.Threads {
		if t.ID != threadID {
			continue
		}
		full := flattenThread(t, t.Collapsed)
		if len(full) <= v.winRows {
			return false
		}
		next := t.WinStart + step
		if next < 0 {
			return false
		}
		if next > len(full)-v.winRows {
			// the overshoot clamps to the tail; at the tail itself the
			// clamp lands on the current start and the slide refuses
			next = len(full) - v.winRows
			if next == t.WinStart {
				return false
			}
		}
		t.WinStart = next
		v.dirty = true
		return true
	}
	return false
}

// WindowRows is the per-thread tree window budget ([index.thread]
// max-rows); the page move advances a truncated thread by one chunk.
func (v *View) WindowRows() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.winRows
}

// WindowStart is the thread's window start (the scroll snap's
// chunk-boundary arithmetic).
func (v *View) WindowStart(threadID string) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == threadID {
			return t.WinStart
		}
	}
	return 0
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

// SetCursorIndex records the cursor's row index - the O(1) read the
// paint path uses (moves write it, merges re-anchor it at
// materialization). The id anchor survives: SetCursor(id) + SetCursorIndex
// together leave both fields consistent, and the stub case (no id)
// clears the anchor via SetCursor("").
func (v *View) SetCursorIndex(idx int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	// while a filter is active the caller steps in filtered space:
	// store the full-space index
	if v.filter != "" && v.filterIdx != nil && idx >= 0 && idx < len(v.filterIdx) {
		idx = v.filterIdx[idx]
	}
	v.lastRow = idx
}

// CursorRowIndex returns the last known cursor row index - the
// fallback CursorRow/CursorIndex use when the cursor id is empty or
// gone (stub rows and post-merge drift). While a filter is active the
// index is the filtered-space one (the row list the caller renders);
// a cursor whose message the filter hides reports the first visible
// row.
func (v *View) CursorRowIndex() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.filter != "" && v.filterMap != nil {
		if fi, ok := v.filterMap[v.lastRow]; ok {
			return fi
		}
		return 0
	}
	return v.lastRow
}

// CursorRow returns the row the cursor points at, or the row at the
// previous index (clamped to the last row) when the id is gone; ok is
// false only when the view is empty.
func (v *View) CursorRow() (Row, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	// cached rows when clean: the unconditional flatten here was the
	// per-paint O(all rows) stall at 33k (every render resolved the
	// cursor through CursorRow)
	if v.dirty || v.rows == nil {
		v.rows = v.rowsLocked()
		v.dirty = false
	}
	rows := v.rows
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
	v.lastRow = min(v.lastRow, len(rows)-1)
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
			if !collapsed {
				t.WinStart = 0 // expand shows the thread from the top
			}
			v.dirty = true
			return nil
		}
	}
	return fmt.Errorf("view: unknown thread %q", id)
}

// Collapsed reports the thread's collapse state (the C/ctrl+v keys).
func (v *View) Collapsed(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			return t.Collapsed
		}
	}
	return false
}

// ToggleCollapsed flips the thread's collapse state (the C key).
// Collapsing re-anchors the cursor to the thread's root row - the
// child rows vanish at the next materialization, so the anchor must
// name a row that survives.
func (v *View) ToggleCollapsed(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			t.Collapsed = !t.Collapsed
			if t.Collapsed {
				// the anchor must name the row that survives the collapse:
				// the summary shows collapseMsg, not the root
				if m := collapseMsg(t.Root); m != nil {
					v.cursorID = m.ID
				}
			} else {
				t.WinStart = 0
			}
			v.dirty = true
			return nil
		}
	}
	return fmt.Errorf("view: unknown thread %q", id)
}

// ToggleCollapseAll flips the whole index between the flat layout
// (every thread one row) and the tree (all expanded): collapse-all
// when any thread is expanded, expand-all when every thread is
// collapsed. The cursor's thread survives by its root anchor.
func (v *View) ToggleCollapseAll() {
	v.mu.Lock()
	defer v.mu.Unlock()
	all := true
	for _, t := range v.Threads {
		if !t.Collapsed {
			all = false
			break
		}
	}
	for _, t := range v.Threads {
		t.Collapsed = !all
		if all {
			t.WinStart = 0
		}
	}
	v.dirty = true
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

// Remove drops an identity from the snapshot (R13 materialized-view
// discipline): a message leaves its thread's tree (the thread stays
// when other messages remain), a thread identity removes the whole
// thread. The apply path calls it when notmuch reports the identity
// no longer matches the view query; a later refresh reconciles truth.
func (v *View) Remove(identity string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if strings.HasPrefix(identity, "t:") {
		for i, t := range v.Threads {
			if t.ID == identity[2:] {
				v.Threads = append(v.Threads[:i], v.Threads[i+1:]...)
				v.dirty = true
				return
			}
		}
		return
	}
	var empty string
	for _, t := range v.Threads {
		msgs := t.msgs[:0]
		for _, m := range t.msgs {
			if m.ID != identity {
				msgs = append(msgs, m)
			}
		}
		if len(msgs) != len(t.msgs) {
			t.msgs = msgs
			t.Root = buildTree(t.msgs)
			if len(msgs) == 0 {
				empty = t.ID
			}
			v.dirty = true
			break
		}
	}
	if empty != "" {
		for i, t := range v.Threads {
			if t.ID == empty {
				v.Threads = append(v.Threads[:i], v.Threads[i+1:]...)
				break
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
	// every message a root: the walk's refs fallback ships no chains
	// (docs/refs-from-terms.md), so a structure-less thread renders as
	// a flat forest without the [...] marker - a genuine multi-root
	// has at least one attached child and keeps the marker
	return &Node{Children: roots, Forest: len(roots) == len(msgs)}
}

// parentOf scans references in reverse so the nearest present ancestor
// wins over distant ones.
func parentOf(m *Message, nodes map[string]*Node) *Node {
	for _, ref := range slices.Backward(m.References) {
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
