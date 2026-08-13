package core

import "sort"

type View struct {
	Name     string
	Query    string
	Threads  []*Thread // sorted by ThreadLess
	cursorID string
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
			for _, c := range node.Children {
				walk(c, depth)
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
// Input must be sorted by ThreadLess. The cursor survives by id.
func (v *View) MergeThreads(threads []*Thread) {
	ops := DiffSorted(v.Threads, threads, ThreadLess, func(t *Thread) string { return t.ID })
	v.Threads = Apply(v.Threads, ops)
	for _, in := range threads {
		cur := findThread(v.Threads, in.ID)
		if cur == nil {
			continue // pure insert: already carries its tree
		}
		mops := DiffSorted(cur.msgs, in.msgs, MsgLess, func(m *Message) string { return m.ID })
		cur.msgs = Apply(cur.msgs, mops)
		cur.LastDate = in.LastDate
		cur.Root = buildTree(cur.msgs)
	}
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
	v.cursorID = id
}

// CursorRow returns the row the cursor points at, or the first row when
// the id is gone; ok is false only when the view is empty.
func (v *View) CursorRow() (Row, bool) {
	rows := v.Rows()
	if len(rows) == 0 {
		return Row{}, false
	}
	if v.cursorID != "" {
		for _, r := range rows {
			if r.Msg.ID == v.cursorID {
				return r, true
			}
		}
	}
	return rows[0], true
}

// buildTree attaches each message under the first reference present in
// the set; messages without a present parent become roots. Multiple
// roots get a synthetic ghost root (mutt "[...]" row).
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

func parentOf(m *Message, nodes map[string]*Node) *Node {
	for _, ref := range m.References {
		if ref == m.ID {
			continue
		}
		if p, ok := nodes[ref]; ok {
			return p
		}
	}
	return nil
}
