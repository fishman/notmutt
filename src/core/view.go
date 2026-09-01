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
// another goroutine is a data race. The cache job's Atts writes (T12
// wiring) and UI tag toggles go through the view under its lock.
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
	// filterRows/filterIdx are parallel (filtered -> full index);
	// filterMap maps full -> filtered index for the cursor. Display-only:
	// the thread set and query are untouched, the maps re-derive on
	// every dirty rebuild (R1 - the view stays a notmuch query).
	filter     string
	filterRows []Row
	filterIdx  []int
	filterMap  map[int]int
	// mergeDepth/mergeDirty gate the dirty-mark during refresh fills
	// (BeginMerge/EndMerge): merges in an open batch never mark dirty
	// individually, so the flatten stays stable and rebuilds once per
	// batch end.
	mergeDepth int
	mergeDirty bool
	// gen is the view generation: bumped on Reset, so the hydrator
	// scopes its dedupe, cursor, and merges to the current query
	// (thread ids span folders - a wave for one view must never land
	// in another's rows).
	gen uint64
	// the tree window budget ([index.thread]): winRows rows per thread;
	// zero = no window.
	winRows int
	// msgDesc flips the flatten's per-thread row order (SetMsgDesc, the
	// [index.thread] sort config): desc reads the thread newest-first.
	msgDesc bool
	// me is the identity set for the thread-tail marks (SetMe, the
	// account from fields): the sent-tag or address "me" detection in
	// ClassifyRows. Zero = sent-tag identity only.
	me []string
	// threaded is the view's thread mode (the [view] threads config):
	// threaded views (inbox, archive) render trees and hide deleted
	// leaves; flat views (unread, deleted, search) are plain
	// chronological lists - one row per message, no tree. NewView
	// defaults to threaded.
	threaded bool
}

// SetMe sets the identity set for the thread-tail marks (the account
// from fields; MyAddrs), applied at startup like the other config.
func (v *View) SetMe(me []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.me = me
	v.dirty = true
}

// SetWindowBudget bounds the tree window ([index.thread]): each thread
// renders at most winRows rows from its WinStart; zero disables it.
func (v *View) SetWindowBudget(winRows int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.winRows = winRows
}

// SetMsgDesc sets the flatten's message order inside a thread
// ([index.thread] sort config): desc reads the thread newest-first
// like the index.
func (v *View) SetMsgDesc(desc bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.msgDesc = desc
	v.dirty = true
}

func NewView(name, query string) *View {
	return &View{Name: name, Query: query, staged: map[string][]TagOp{}, threaded: true}
}

// SetThreaded sets the view's thread mode ([view] threads config):
// threaded views render trees and hide deleted leaves; flat views are
// plain message lists.
func (v *View) SetThreaded(on bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.threaded = on
	v.dirty = true
}

// ViewFlat reports the flat mode: the refresher picks the
// message-level walk, prune, and refresh merge on it.
func (v *View) ViewFlat() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return !v.threaded
}

// ViewName and ViewQuery are the locked identity reads: the refresher's
// config switch rewrites the fields in place on its own goroutine
// (SetIdentity) while jobs read them for event labels - cross-goroutine
// by design.
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

// SetIdentity is the single write path for the view's name and query (the
// refresher's onConfig switch).
func (v *View) SetIdentity(name, query string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.Name = name
	v.Query = query
}

// Reset drops the view's rows for a full re-load: a view switch renders
// empty until the new query's first chunk lands, never the previous
// view's rows. Staged ops survive - keyed by message identity, not
// position or view (R14).
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

// Gen is the view generation: the hydrator scopes its wave to it, so a
// view switch neither suppresses nor misdirects in-flight fetches.
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

// Rows flattens the thread forest depth-first; collapsed threads render
// only their root row. Memoized: only structure or staged-state changes
// (MergeThreads, Stage/Undo, SetTags, SetGroups, SetCollapsed) mark it
// dirty - content updates (SetAtts) are visible through the shared Msg
// pointers, so a cache scan never rebuilds the row model (129k-row case).
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
		full := flattenThread(t, t.Collapsed, v.threaded, v.msgDesc)
		// the thread-tail marks: only a windowed thread marks (rows
		// hidden above or below). The marks classify the FULL tree and
		// ride the rows through the window - a long thread tints
		// wherever it sits, a thread that fits renders unmarked.
		// Computed in the flatten so the marks are memoized with the
		// rows; the index tints without any open.
		if v.winRows > 0 && len(full) > v.winRows {
			marks := ClassifyRows(full, v.me)
			for j := range full {
				full[j].Mark = marks[j]
			}
		}
		out := window(full, t.WinStart, v.winRows)
		// the tree-window overflow: hidden tail marks the last emitted
		// row with the count (the page move continues there) and emits
		// a ghost "+N more" row under the thread
		if v.winRows > 0 && len(full) > v.winRows {
			start := max(0, min(t.WinStart, len(full)-v.winRows))
			if n := len(full) - (start + v.winRows); n > 0 {
				out[len(out)-1].More = n
				out = append(out, Row{Ghost: true, ThreadID: t.ID, More: n})
			}
			// the top-side mirror: a mid-thread start emits a leading
			// ghost with the hidden-above count, so the rows over the
			// window stay visible (the walk-up slides into them)
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
		// hydration (R14).
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
	// the display filter: rows that match stay, the rest drop. A cursor
	// the filter hides falls back to the first visible row.
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
// over author, subject, and tag names (the F filter).
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

// SetFilter narrows the displayed rows to those matching the text
// (case-insensitive substring over author, subject, and tags - the
// live F filter). Display-only (R1): the query and thread set are
// untouched; merges re-derive the filter at the next materialization.
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
// unread message, or the newest when nothing is unread (the row keeps
// the root's tree position). Nil when the thread has no messages - the
// ghost root renders the stub. skipDeleted excludes deleted messages
// (the threaded views' rule - the flat views never collapse).
func collapseMsg(root *Node, skipDeleted bool) *Message {
	var last, unread *Message
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Msg != nil && (!skipDeleted || !slices.Contains(n.Msg.Tags, "deleted")) {
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

// flattenThread flattens one thread's tree into index rows. The
// skipDeleted rule applies AFTER the tree is built over the full
// message set (fetch everything first, filter after): a deleted
// message with children keeps its row (removing it would break the
// hierarchy); only a deleted LEAF vanishes from the threaded view.
// desc reverses the flattened rows - the thread reads newest-first,
// while the tree itself is never reordered, so the diff's MsgLess
// ordering stays untouched.
func flattenThread(t *Thread, collapsed, skipDeleted, desc bool) []Row {
	var rows []Row
	if t.Root == nil {
		return rows
	}
	count := t.Count()
	if collapsed {
		rows = append(rows, Row{Msg: collapseMsg(t.Root, skipDeleted), ThreadID: t.ID, Count: count, Collapsed: true})
		return rows
	}
	// child marks whether this child has a sibling below it in the DISPLAY
	// order. Asc draws the tree top-down, so a child has one below iff it is
	// not the last at its level. Desc flips the flatten (newest-first), so a
	// child has one below iff it is not the first - the reversed rows must
	// keep the correct branch/leaf glyphs, not mirror the asc connectors.
	child := func(siblings []bool, i, n int) []bool {
		hasBelow := i != n-1
		if desc {
			hasBelow = i != 0
		}
		s := make([]bool, 0, len(siblings)+1)
		s = append(s, hasBelow)
		return append(s, siblings...)
	}
	var walk func(*Node, int, []bool)
	walk = func(node *Node, depth int, siblings []bool) {
		if node.Msg == nil {
			if node.Forest {
				for _, c := range orderedChildren(node, desc) {
					walk(c, depth, nil)
				}
				return
			}
			rows = append(rows, Row{Ghost: true, ThreadID: t.ID, Depth: depth, Siblings: siblings, Count: count, Desc: desc})
			for i, c := range orderedChildren(node, desc) {
				walk(c, depth+1, child(siblings, i, len(node.Children)))
			}
			return
		}
		if skipDeleted && slices.Contains(node.Msg.Tags, "deleted") && len(node.Children) == 0 {
			return
		}
		rows = append(rows, Row{Msg: node.Msg, ThreadID: t.ID, Depth: depth, Root: depth == 0, Siblings: siblings, Count: count, Desc: desc})
		for i, c := range orderedChildren(node, desc) {
			walk(c, depth+1, child(siblings, i, len(node.Children)))
		}
	}
	walk(t.Root, 0, nil)
	if desc {
		slices.Reverse(rows)
	}
	return rows
}

// orderedChildren returns the node's children for the flatten walk. Desc
// reads newest-first, so children are ordered by their subtree's newest
// timestamp ascending - the newest message is visited last and, after the
// row reversal, lands at the top. Asc keeps the stored chronological order.
func orderedChildren(n *Node, desc bool) []*Node {
	if !desc || len(n.Children) <= 1 {
		return n.Children
	}
	kids := append([]*Node(nil), n.Children...)
	sort.SliceStable(kids, func(i, j int) bool { return subtreeMax(kids[i]) < subtreeMax(kids[j]) })
	return kids
}

// subtreeMax is the newest timestamp anywhere in the subtree (the
// [index.thread] desc ordering key): a node's own message or the newest of
// its descendants.
func subtreeMax(n *Node) int64 {
	var m int64
	if n.Msg != nil {
		m = n.Msg.Timestamp
	}
	for _, c := range n.Children {
		if v := subtreeMax(c); v > m {
			m = v
		}
	}
	return m
}

// window bounds one thread's flattened rows to [start, start+winRows).
// Zero winRows passes the thread through; start is clamped (merges can
// shrink the flatten between slides). A mid-thread start cuts the
// leading tree columns: every Depth shifts by the first row's depth
// (the first visible row's marker lands at column 0). Display-only -
// the Siblings chains keep their true indexing, so column c shows the
// ancestor at true depth cut+c, never a cut one.
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
// diff plus per-thread message diffs for threads on both sides. Input
// is sorted defensively (the caller's slice is not modified). Matched
// messages keep their identity; the snapshot reconciles their fields
// (reconcile-then-replay, the ordering the optimistic layer of
// T11/T12 builds on, per plan section 8). The cursor survives by id.
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
		// Matched keys keep old elements, so reconcile snapshot fields
		// onto them (reconcile-then-replay, the T11/T12 ordering).
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
// flatten across a refresh fill's intermediate keypresses. EndMerge
// marks dirty once if any merge ran inside. No-op when no batch is
// open (single merges keep their immediate dirty-mark); the depth
// counter makes nested windows safe.
// Dirty reports whether the memoized rows need rebuilding: the batch
// discipline defers the mark to EndMerge, so a no-op wave must not
// self-trigger the completion diff.
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

// stubThread reports a thread with only summary rows (no message id -
// the refresh feed shape). A stub snapshot must never delete a
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
// (the hydrator path): the thread keeps its collapse, window state,
// and sorted position; others are untouched. No-op when the thread
// left the view mid-fetch (a view switch raced the reply).
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
	// the MergeThreads batch discipline: the hydrator merges a whole
	// wave under one BeginMerge, so the flatten rebuilds once per wave.
	if v.mergeDepth > 0 {
		v.mergeDirty = true
	} else {
		v.dirty = true
	}
}

// Hydrated reports whether the thread holds real messages (the stub
// guard's positive side): the hydrator skips these; the refresher
// re-fetches changed ones.
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
// open must not queue behind the full walk that owns the worker). nil
// when the thread is not in the view.
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
// reports whether it moved. The walk-through steps (+-1) refuse at the
// edges - nothing hides that way or the thread fits the budget; the
// page move clamps to the tail instead of refusing.
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
		full := flattenThread(t, t.Collapsed, v.threaded, v.msgDesc)
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
// max-rows).
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
// retained one. Atts are never copied (the cache job owns them) and
// Paths are never copied (the thread-fetch path returns none).
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
// materialization). The id anchor survives: SetCursor(id) +
// SetCursorIndex leave both consistent; the stub case (no id) clears
// the anchor via SetCursor("").
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
// gone (stub rows and post-merge drift). Under an active filter the
// index is the filtered-space one; a cursor the filter hides reports
// the first visible row.
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
	// per-paint O(all rows) stall at 33k
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

// SetCollapsed toggles a thread's collapsed state under the view lock;
// errors on unknown ids. Collapse toggles go through the view, never
// by writing Threads[i].Collapsed directly.
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
// Collapsing re-anchors the cursor to the summary row - the child rows
// vanish at the next materialization, so the anchor must survive.
func (v *View) ToggleCollapsed(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, t := range v.Threads {
		if t.ID == id {
			t.Collapsed = !t.Collapsed
			if t.Collapsed {
				// anchor the cursor on the summary row (collapseMsg), not
				// the root: it is the row that survives the collapse
				if m := collapseMsg(t.Root, v.threaded); m != nil {
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

// ToggleCollapseAll flips the whole index between flat (one row per
// thread) and tree: collapse-all when any thread is expanded,
// expand-all when every thread is collapsed.
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

// SetTags replaces a message's tags under the view lock. UI toggles go
// through here, never by writing Msg.Tags directly: the pointers are
// shared with the refresher's merge path.
func (v *View) SetTags(msgID string, tags []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if m := v.findMsgLocked(msgID); m != nil {
		m.Tags = tags
		v.dirty = true
	}
}

// Tags returns an identity's applied tags under the view lock; the
// slice is shared, so callers copy before mutating (SetTags /
// SetThreadTags are the write paths). A message identity resolves its
// message; a thread identity (t:threadID) resolves the summary stub,
// or the first message once the stub is gone. Unknown ids return nil.
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

// identityExistsLocked reports whether the identity names a message
// id, or a thread for thread identities.
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
// when other messages remain); a thread identity removes the whole
// thread. Called by the apply path when notmuch reports the identity
// no longer matches the query; a later refresh reconciles truth.
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
	// has an attached child and keeps the marker
	return &Node{Children: roots, Forest: len(roots) == len(msgs)}
}

// parentOf scans references in reverse so the nearest present ancestor
// wins.
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
// against (R14); the app supplies them from the config store.
func (v *View) SetGroups(groups []TagGroup) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.groups = groups
	v.dirty = true
}

// Groups returns a copy of the exclusive tag groups under the view
// lock; callers may not mutate the view's slice.
func (v *View) Groups() []TagGroup {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.groups)
}

// Stage appends a pending tag op for an identity - a message id, or a
// thread identity ("t:" + threadID) for summary rows: the index is
// search data without message ids, so a summary-row op is thread-level
// (the apply path emits thread:<id> - moving a thread moves all its
// messages). An identical op already staged cancels it (toggle
// semantics: r twice is a no-op, r then a keeps both). Unknown
// identities are a no-op. Staging bumps the generation: an in-flight
// apply snapshot can no longer clear the entry (generation-guarded).
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
// gen is current, so in-flight apply cannot clear newer ops.
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
// traffic); unknown ids are a no-op.
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
// nothing was staged or undone since the snapshot. The guard
// over-blocks: it no-ops when ANY message staged or undid, not just
// this entry, leaving an applied entry staged until the next apply or
// undo. Benign: notmuch tag ops are idempotent.
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
