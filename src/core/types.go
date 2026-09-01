// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

type Message struct {
	ID         string
	ThreadID   string
	Timestamp  int64
	Author     string
	Subject    string
	Tags       []string
	References []string
	Paths      []string
	Atts       []Attachment
}

type Attachment struct {
	Name     string
	MimeType string
	Size     int64
}

type Thread struct {
	ID        string
	LastDate  int64
	Collapsed bool
	Root      *Node
	msgs      []*Message
	// WinStart is the tree window's first emitted row (the [index.thread]
	// budget); index into the full flatten, clamped at materialization.
	WinStart int
}

type Node struct {
	Msg      *Message
	Children []*Node
	Forest   bool // flat-thread synthetic root: no message attaches another, renders without the [...] marker
}

type Row struct {
	Msg        *Message
	ThreadID   string
	Depth      int
	Root       bool
	Siblings   []bool // root-ward chain: Siblings[0] own has-next-sibling, Siblings[k] ancestor k levels up (the conditional tree indent)
	Count      int
	Ghost      bool     // synthetic multi-root marker row; has no Msg
	More       int      // thread rows hidden below the tree window: set on the window's last row and the trailing "+N more" row
	MoreTop    int      // thread rows hidden above the tree window: set on the leading "+N more" indicator row
	Staged     bool     // pending ops staged for this row (R14)
	StagedTags []string // display tags with staged ops resolved
	Collapsed  bool     // the C-collapsed thread's summary row: renders the collapse marker
	Mark       MsgMark  // the thread-position mark (ClassifyRows, set by the flatten)
	Desc       bool     // the row renders in a desc (bottom-up) thread: the leaf corner mirrors upward
}

// TagOp is a pending tag change: add or remove Tag. Same shape as the
// worker's ActTag (notmuch aliases it); intent is recorded verbatim,
// group resolution is a separate step.
type TagOp struct {
	Tag string
	Add bool
}

// TagGroup is an exclusive tag group: at most one member applies.
// Membership IS the hard-tag declaration - a member is a physical
// folder mapped to a notmuch tag. Tags outside groups are soft
// (work, ...): unlimited, coexisting, applied by header rules, never
// touched by group resolution.
type TagGroup struct {
	Tags []string
}
