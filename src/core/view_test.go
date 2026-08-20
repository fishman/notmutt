// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"fmt"
	"slices"
	"strconv"
	"testing"
)

func msg(id string, ts int64, refs ...string) *Message {
	return &Message{ID: id, Timestamp: ts, References: refs}
}

// TestFlattenSkipsDeletedLeaves pins the threaded-view rule: a
// deleted message vanishes only when it has no response (a deleted
// leaf); a deleted message with children keeps its row so the
// subtree stays attached. The flat mode skips nothing.
func TestFlattenSkipsDeletedLeaves(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	root := msg("a", 1)
	delLeaf := msg("b", 2)
	delLeaf.Tags = []string{"deleted"}
	delParent := msg("c", 3, "a")
	delParent.Tags = []string{"deleted"}
	reply := msg("d", 4, "c")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{root, delLeaf, delParent, reply})})
	rowIDs := func() []string {
		var ids []string
		for _, r := range v.Rows() {
			if r.Msg != nil {
				ids = append(ids, r.Msg.ID)
			}
		}
		return ids
	}
	if got := rowIDs(); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("threaded rows = %v (want a c d: del leaf hidden, del parent with reply kept)", got)
	}
	v.SetThreaded(false)
	if got := rowIDs(); !slices.Equal(sorted(got), []string{"a", "b", "c", "d"}) {
		t.Fatalf("flat rows = %v (want all four)", got)
	}
}

func sorted(ids []string) []string {
	out := append([]string(nil), ids...)
	slices.Sort(out)
	return out
}

func TestRowsFlattenThreadTree(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})
	t2 := NewThread("t2", []*Message{msg("other", 300)})
	v.MergeThreads([]*Thread{t2, t1})
	rows := v.Rows()
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Msg.ID != "other" || !rows[0].Root {
		t.Fatalf("first row wrong: %+v", rows[0])
	}
	if rows[1].Msg.ID != "root" || !rows[1].Root {
		t.Fatalf("second row wrong: %+v", rows[1])
	}
	if rows[2].Msg.ID != "kid" || rows[2].Depth != 1 {
		t.Fatalf("third row wrong: %+v", rows[2])
	}
}

func TestMergeInsertsIntoExistingThread(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100)})})
	// a reply arrives, sorts after its parent in the message list under
	// date-asc; the tree still renders the parent above the child
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100), msg("reply", 200, "root")})})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows after insert, got %d", len(rows))
	}
	if rows[0].Msg.ID != "root" || !rows[0].Root {
		t.Fatalf("root must stay first row: %+v", rows[0])
	}
	if rows[1].Msg.ID != "reply" || rows[1].Depth != 1 {
		t.Fatalf("reply not inserted under parent: %+v", rows[1])
	}
}

func TestMergeThreadMoves(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	old := NewThread("old", []*Message{msg("a", 100)})
	newer := NewThread("newer", []*Message{msg("b", 300)})
	v.MergeThreads([]*Thread{newer, old})
	// old thread gets a new message, jumps to the top
	old2 := NewThread("old", []*Message{msg("a", 100), msg("c", 500)})
	v.MergeThreads([]*Thread{old2, newer})
	rows := v.Rows()
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Msg.ID != "a" || rows[0].Ghost {
		t.Fatalf("flat moved thread first without ghost: %+v", rows[0])
	}
	if rows[1].Msg.ID != "c" {
		t.Fatalf("sibling order must be chronological, got %+v", rows[1])
	}
}

func TestMergeThreadMerge(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{msg("a", 100)})
	t2 := NewThread("t2", []*Message{msg("b", 200)})
	v.MergeThreads([]*Thread{t1, t2})
	merged := NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})
	v.MergeThreads([]*Thread{merged})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("thread merge lost rows: %d", len(rows))
	}
	if rows[0].Ghost {
		t.Fatalf("merged structure-less thread must render flat: %+v", rows[0])
	}
}

func TestCursorSurvivesMerge(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100)})})
	v.SetCursor("a")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})})
	if _, ok := v.CursorRow(); !ok {
		t.Fatal("cursor lost after merge")
	}
}

func TestCollapseHidesChildren(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	th := NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})
	v.MergeThreads([]*Thread{th})
	if err := v.SetCollapsed("t1", true); err != nil {
		t.Fatal(err)
	}
	rows := v.Rows()
	if len(rows) != 1 {
		t.Fatalf("collapsed thread must render 1 row, got %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Fatalf("collapsed row must still count the thread, got %d", rows[0].Count)
	}
}

// TestCollapseShowsLastUnread pins the summary row: a collapsed thread
// shows the newest unread message, falling back to the newest message
// when nothing is unread (the row still counts the whole thread).
func TestCollapseShowsLastUnread(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	root := msg("root", 100)
	kid := msg("kid", 200, "root")
	kid.Tags = []string{"unread"}
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{root, kid})})
	if err := v.SetCollapsed("t1", true); err != nil {
		t.Fatal(err)
	}
	rows := v.Rows()
	if len(rows) != 1 || rows[0].Msg != kid || rows[0].Count != 2 {
		t.Fatalf("collapsed row must show the last unread: %+v", rows[0])
	}
	// all read: the fallback is the newest message
	kid.Tags = nil
	v.SetCollapsed("t1", false)
	if err := v.SetCollapsed("t1", true); err != nil {
		t.Fatal(err)
	}
	rows = v.Rows()
	if rows[0].Msg != kid {
		t.Fatalf("collapsed row must fall back to the newest message: %+v", rows[0].Msg)
	}
	// toggling re-anchors the cursor to the message the row shows
	if err := v.ToggleCollapsed("t1"); err != nil {
		t.Fatal(err)
	}
	if err := v.ToggleCollapsed("t1"); err != nil {
		t.Fatal(err)
	}
	r, ok := v.CursorRow()
	if !ok || r.Msg != kid {
		t.Fatalf("collapse must anchor the cursor to the summary message: %+v ok=%v", r.Msg, ok)
	}
}

func TestMergeTagConvergence(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	m := msg("a", 100)
	m.Tags = []string{"inbox", "unread"}
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{m})})
	fresh := msg("a", 100)
	fresh.Tags = []string{"inbox", "work"}
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{fresh})})
	rows := v.Rows()
	if rows[0].Msg != m {
		t.Fatalf("matched message must be retained, not replaced: %+v", rows[0].Msg)
	}
	if len(rows[0].Msg.Tags) != 2 || rows[0].Msg.Tags[0] != "inbox" || rows[0].Msg.Tags[1] != "work" {
		t.Fatalf("snapshot tags must win, got %v", rows[0].Msg.Tags)
	}
}

func TestDepth3Chain(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{
		msg("root", 100),
		msg("mid", 200, "root"),
		msg("leaf", 300, "root", "mid"),
	})
	v.MergeThreads([]*Thread{t1})
	rows := v.Rows()
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	for i, want := range []struct {
		id    string
		depth int
	}{{"root", 0}, {"mid", 1}, {"leaf", 2}} {
		if rows[i].Msg.ID != want.id || rows[i].Depth != want.depth {
			t.Fatalf("row %d wrong: %+v", i, rows[i])
		}
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{
		NewThread("t1", []*Message{{ID: "m1", ThreadID: "t1", Author: "Ann", Subject: "meeting notes", Tags: []string{"inbox", "work"}}}),
		NewThread("t2", []*Message{{ID: "m2", ThreadID: "t2", Author: "Bob", Subject: "lunch", Tags: []string{"inbox"}}}),
		NewThread("t3", []*Message{{ID: "m3", ThreadID: "t3", Author: "Ann", Subject: "receipt", Tags: []string{"inbox", "receipt"}}}),
	})
	v.SetFilter("meeting")
	if rows := v.Rows(); len(rows) != 1 || rows[0].Msg.ID != "m1" {
		t.Fatalf("subject filter: %d rows", len(rows))
	}
	v.SetFilter("ann") // author match, case-insensitive
	if rows := v.Rows(); len(rows) != 2 {
		t.Fatalf("author filter: %d rows", len(rows))
	}
	v.SetFilter("receipt") // tag match
	if rows := v.Rows(); len(rows) != 1 {
		t.Fatalf("tag filter: %d rows", len(rows))
	}
	v.SetFilter("")
	if rows := v.Rows(); len(rows) != 3 {
		t.Fatalf("clear must restore all rows: %d rows", len(rows))
	}
}

func TestFilterCursorMapping(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{
		NewThread("t1", []*Message{{ID: "m1", ThreadID: "t1", Author: "Ann", Subject: "meeting", Tags: []string{"inbox"}}}),
		NewThread("t2", []*Message{{ID: "m2", ThreadID: "t2", Author: "Bob", Subject: "lunch", Tags: []string{"inbox"}}}),
		NewThread("t3", []*Message{{ID: "m3", ThreadID: "t3", Author: "Ann", Subject: "receipt", Tags: []string{"inbox"}}}),
	})
	v.SetCursor("m2")  // full-space row 1
	v.SetFilter("ann") // m1 and m3 remain
	v.Rows()
	if idx := v.CursorRowIndex(); idx != 0 {
		t.Fatalf("a hidden cursor must map to the first visible row, got %d", idx)
	}
	// the UI move pattern: re-anchor the id, step in filtered space
	v.SetCursor("m3")
	v.SetCursorIndex(1) // filtered-space row 1 = m3 (full row 2)
	v.SetFilter("")
	if rows := v.Rows(); rows[v.CursorRowIndex()].Msg.ID != "m3" {
		t.Fatalf("cursor must survive the filter clear at its message: %q", rows[v.CursorRowIndex()].Msg.ID)
	}
}

func TestCursorClamps(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{msg("m1", 100)})
	t2 := NewThread("t2", []*Message{msg("m2", 200)})
	t3 := NewThread("t3", []*Message{msg("m3", 300)})
	v.MergeThreads([]*Thread{t3, t2, t1})
	v.SetCursor("m2")
	if r, ok := v.CursorRow(); !ok || r.Msg.ID != "m2" {
		t.Fatalf("cursor should sit on m2, got %+v ok=%v", r, ok)
	}
	// t2 leaves the view; the cursor must stay at index 1, not jump to 0
	v.MergeThreads([]*Thread{t3, t1})
	r, ok := v.CursorRow()
	if !ok || r.Msg.ID != "m1" {
		t.Fatalf("cursor must clamp to previous index (m1), got %+v ok=%v", r, ok)
	}
}

func TestGhostRootRow(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	// genuine multi-root: a and b attach nothing, c attaches a - the
	// [...] marker stays; a structure-less thread renders flat instead
	t1 := NewThread("t1", []*Message{msg("a", 100), msg("b", 200), msg("c", 300, "a")})
	v.MergeThreads([]*Thread{t1})
	rows := v.Rows()
	if len(rows) != 4 {
		t.Fatalf("want ghost + 3 rows, got %d", len(rows))
	}
	if !rows[0].Ghost || rows[0].Msg != nil || rows[0].Depth != 0 || rows[0].Count != 3 {
		t.Fatalf("first row must be the ghost marker: %+v", rows[0])
	}
	if rows[1].Msg.ID != "a" || rows[1].Depth != 1 || rows[1].Root {
		t.Fatalf("second row wrong: %+v", rows[1])
	}
	if rows[2].Msg.ID != "c" || rows[2].Depth != 2 {
		t.Fatalf("attached row wrong: %+v", rows[2])
	}
	if rows[3].Msg.ID != "b" || rows[3].Depth != 1 || rows[3].Root {
		t.Fatalf("fourth row wrong: %+v", rows[3])
	}
}

func TestFlatThreadNoGhostRows(t *testing.T) {
	// the refs-fallback walk ships no chains: a structure-less thread
	// renders as a flat forest - depth-0 rows, no [...] marker
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 flat rows, got %d", len(rows))
	}
	for i, r := range rows {
		if r.Ghost || r.Depth != 0 || !r.Root || len(r.Siblings) != 0 {
			t.Fatalf("flat row %d wrong: %+v", i, r)
		}
	}
}

func TestCollapseSurvivesMerge(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100)})})
	if err := v.SetCollapsed("t1", true); err != nil {
		t.Fatal(err)
	}
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})})
	rows := v.Rows()
	if len(rows) != 1 {
		t.Fatalf("collapse lost after merge: %d rows", len(rows))
	}
	if rows[0].Count != 2 {
		t.Fatalf("count must reflect the merged thread, got %d", rows[0].Count)
	}
}

func TestSetCollapsedUnknownErrors(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	if err := v.SetCollapsed("nope", true); err == nil {
		t.Fatal("SetCollapsed must error on unknown thread")
	}
}

func TestConcurrentMergeAndRows(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200, "a")})})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200, "a")})})
		}
	}()
	for i := 0; i < 200; i++ {
		if rows := v.Rows(); len(rows) != 2 {
			t.Fatalf("want 2 rows, got %d", len(rows))
		}
	}
	<-done
}

func TestViewSetAtts(t *testing.T) {
	atts := []Attachment{{Name: "f.pdf", MimeType: "application/pdf", Size: 10}}
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("m1", 100)})})
	v.SetAtts("m1", atts)
	rows := v.Rows()
	if len(rows) != 1 || len(rows[0].Msg.Atts) != 1 || rows[0].Msg.Atts[0].Name != "f.pdf" {
		t.Fatalf("Atts not recorded: %+v", rows)
	}
	// unknown id: no-op, the message stays untouched
	v.SetAtts("gone", atts)
	rows = v.Rows()
	if len(rows[0].Msg.Atts) != 1 || rows[0].Msg.Atts[0].Name != "f.pdf" {
		t.Fatalf("unknown id must not change Atts: %+v", rows)
	}
	// SetAtts before the message exists: no panic, no-op
	v2 := NewView("inbox", "tag:inbox")
	v2.SetAtts("m1", atts)
	if rows := v2.Rows(); len(rows) != 0 {
		t.Fatalf("empty view must stay empty: %d rows", len(rows))
	}
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func stagedView(t *testing.T) *View {
	t.Helper()
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("m1", 100)})})
	return v
}

func TestStageCancelUndo(t *testing.T) {
	v := stagedView(t)
	v.Stage("m1", TagOp{Tag: "unread", Add: false})
	if !v.IsStaged("m1") || !v.HasStaged() {
		t.Fatal("not staged after Stage")
	}
	v.Stage("m1", TagOp{Tag: "unread", Add: false}) // identical: cancels
	if v.IsStaged("m1") || v.HasStaged() {
		t.Fatal("identical op must cancel")
	}
	v.Stage("m1", TagOp{Tag: "unread", Add: false})
	v.Stage("m1", TagOp{Tag: "archive", Add: true})
	v.Undo("m1")
	if v.HasStaged() {
		t.Fatal("Undo must clear the entry")
	}
}

func TestStageUnknownIDNoop(t *testing.T) {
	v := stagedView(t)
	v.Stage("ghost", TagOp{Tag: "archive", Add: true})
	if v.HasStaged() {
		t.Fatal("unknown id must not stage")
	}
	v.Undo("ghost") // must not panic
}

func TestStagedOpsSnapshotCopies(t *testing.T) {
	v := stagedView(t)
	v.Stage("m1", TagOp{Tag: "archive", Add: true})
	snap, _ := v.StagedOps()
	snap["m1"][0].Tag = "mutated"
	ops, _ := v.StagedOps()
	if ops["m1"][0].Tag != "archive" {
		t.Fatal("snapshot must be a copy")
	}
}

func TestClearStagedGeneration(t *testing.T) {
	v := stagedView(t)
	v.Stage("m1", TagOp{Tag: "archive", Add: true})
	_, gen := v.StagedOps()
	v.Stage("m1", TagOp{Tag: "deleted", Add: true}) // bumps gen
	v.ClearStaged("m1", gen)                        // stale: must no-op
	if !v.IsStaged("m1") {
		t.Fatal("stale clear must no-op")
	}
	_, gen2 := v.StagedOps()
	v.ClearStaged("m1", gen2)
	if v.IsStaged("m1") {
		t.Fatal("current clear must remove")
	}
}

func TestStagedRowShowsResolved(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.SetGroups([]TagGroup{folderGroup})
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{{
		ID: "m1", Timestamp: 100, Tags: []string{"inbox", "unread"},
	}})})
	v.Stage("m1", TagOp{Tag: "archive", Add: true})
	rows := v.Rows()
	found := false
	for _, r := range rows {
		if r.Msg == nil || r.Msg.ID != "m1" {
			continue
		}
		found = true
		if !r.Staged {
			t.Fatal("row must be staged")
		}
		if !slices.Equal(r.StagedTags, []string{"archive", "unread"}) {
			t.Fatalf("StagedTags = %v", r.StagedTags)
		}
		if hasTag(r.Msg.Tags, "archive") {
			t.Fatalf("applied Msg.Tags must be untouched: %v", r.Msg.Tags)
		}
	}
	if !found {
		t.Fatal("m1 row missing")
	}
}

func TestStagedGhostRowsNeverStaged(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.SetGroups([]TagGroup{folderGroup})
	// genuine multi-root (c attaches a): ghost root + three rows
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{
		{ID: "a", Timestamp: 200},
		{ID: "b", Timestamp: 100},
		{ID: "c", Timestamp: 300, References: []string{"a"}},
	})})
	v.Stage("a", TagOp{Tag: "archive", Add: true})
	rows := v.Rows()
	found := false
	for _, r := range rows {
		if r.Ghost {
			if r.Staged {
				t.Fatal("ghost rows must never be staged")
			}
			continue
		}
		if r.Msg != nil && r.Msg.ID == "a" {
			found = true
			if !r.Staged {
				t.Fatal("staged message row must be staged")
			}
		}
	}
	if !found {
		t.Fatal("a row missing")
	}
}

// TestRowsMemoized pins the damage tracking: content-only updates (SetAtts)
// return the cached row slice unchanged - the cache scan and progress ticks
// never rebuild the flatten - while structure or staged-state changes
// (MergeThreads, Stage, SetTags) rebuild it.
func TestMergeManyThreadsInBatches(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	// the refresher's full-snapshot merge: every chunk re-merges the
	// whole accumulated snapshot (MergeThreads is replace semantics)
	var snapshot []*Thread
	want := 0
	for b := 1; b <= 3; b++ {
		batch := make([]*Thread, 0, 3000)
		for i := 1; i <= 3000; i++ {
			id := fmt.Sprintf("t%d", b*3000+i)
			batch = append(batch, NewThread(id, []*Message{msg("m"+id, int64(b*3000+i))}))
		}
		snapshot = append(snapshot, batch...)
		want += len(batch)
		v.MergeThreads(snapshot)
		if got := len(v.Threads); got != want {
			t.Fatalf("batch %d: want %d threads, got %d", b, want, got)
		}
	}
	if len(v.Rows()) != want {
		t.Fatalf("rows: want %d, got %d", want, len(v.Rows()))
	}
	sorted := slices.IsSortedFunc(v.Threads, func(a, b *Thread) int {
		switch {
		case ThreadLess(a, b):
			return -1
		case ThreadLess(b, a):
			return 1
		}
		return 0
	})
	if !sorted {
		t.Fatal("threads must stay sorted after batched merges")
	}
	// a full-snapshot re-merge must be a no-op, not a duplication
	v.MergeThreads(v.Threads)
	if len(v.Threads) != want {
		t.Fatalf("re-merge duplicated threads: %d", len(v.Threads))
	}
}

func TestRowsMemoized(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("m1", 100)}), NewThread("t2", []*Message{msg("m2", 200)})})
	r1 := v.Rows()
	if len(r1) != 2 {
		t.Fatalf("want 2 rows, got %d", len(r1))
	}
	// content-only: same backing array, no rebuild
	v.SetAtts("m1", []Attachment{{Name: "f.pdf"}})
	r2 := v.Rows()
	if &r1[0] != &r2[0] {
		t.Fatal("SetAtts must not rebuild the row model")
	}
	if len(r2[1].Msg.Atts) != 1 {
		t.Fatalf("Atts must still be visible through the cached rows: %+v", r2[1])
	}
	// structure change: new backing array
	v.SetTags("m1", []string{"unread"})
	r3 := v.Rows()
	if len(r3) != 2 || &r1[0] == &r3[0] {
		t.Fatal("SetTags must rebuild the row model")
	}
	// staged change: rebuild
	v.Stage("m1", TagOp{Tag: "archive", Add: true})
	r4 := v.Rows()
	if len(r4) != 2 || &r1[0] == &r4[0] {
		t.Fatal("Stage must rebuild the row model")
	}
	if !r4[1].Staged {
		t.Fatalf("staged flag must render after the rebuild: %+v", r4[1])
	}
	// clean reads stay cached
	if &r4[0] != &v.Rows()[0] {
		t.Fatal("clean reads must hit the cache")
	}
}

// TestMergeBatchDefersDirty pins the FIX3 batching contract: merges
// inside an open BeginMerge window do not mark the view dirty, so the
// row model stays stable across the intermediate keypresses of a
// refresh fill; EndMerge marks dirty once, so the flatten rebuilds
// exactly once per batch end with the merged content.
func TestMergeBatchDefersDirty(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100)})})
	r1 := v.Rows()
	if len(r1) != 1 {
		t.Fatalf("want 1 row, got %d", len(r1))
	}
	v.BeginMerge()
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200, "a")})})
	// the batch is still open: Rows must return the cached flatten, not
	// rebuild the merged state
	r2 := v.Rows()
	if len(r2) != 1 || &r1[0] != &r2[0] {
		t.Fatal("merges inside a batch must not rebuild the row model")
	}
	v.EndMerge()
	r3 := v.Rows()
	if &r1[0] == &r3[0] {
		t.Fatal("EndMerge must mark dirty once after a batched merge")
	}
	if len(r3) != 2 {
		t.Fatalf("EndMerge must rebuild with the merged content, got %d rows", len(r3))
	}
}

// TestEndMergeWithoutMergesNotDirty pins the batching edge: a batch
// that never merged must not mark the view dirty.
func TestEndMergeWithoutMergesNotDirty(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100)})})
	r1 := v.Rows()
	v.BeginMerge()
	v.EndMerge()
	if r2 := v.Rows(); &r1[0] != &r2[0] {
		t.Fatal("EndMerge without merges must not mark dirty")
	}
}

// TestMergeBatchNestedAndUnbalanced pins the depth-counter edges: a
// nested window defers the dirty-mark to the outer close, and an
// EndMerge without an open batch is a no-op that cannot corrupt the
// flag.
func TestMergeBatchNestedAndUnbalanced(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100)})})
	r1 := v.Rows()
	v.EndMerge() // no open batch: must be a no-op
	v.BeginMerge()
	v.BeginMerge() // nested window
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})})
	v.EndMerge() // inner close: the outer batch is still open
	if r2 := v.Rows(); &r1[0] != &r2[0] {
		t.Fatal("inner EndMerge must not mark dirty while the outer batch is open")
	}
	v.EndMerge() // outer close: dirty lands once
	r3 := v.Rows()
	if &r1[0] == &r3[0] {
		t.Fatal("outer EndMerge must mark dirty")
	}
	if len(r3) != 2 { // flat forest, a + b (no references)
		t.Fatalf("rows must show the merged content, got %d", len(r3))
	}
	// an unbalanced extra EndMerge after the close is a no-op
	v.EndMerge()
	r4 := v.Rows()
	if &r3[0] != &r4[0] {
		t.Fatal("EndMerge without an open batch must not mark dirty")
	}
}

// TestRemoveMessageRebuildsTree pins the apply-path eviction (R13):
// removing one message rebuilds its thread's tree (the thread stays
// with the rest), removing the last message or a thread identity drops
// the whole thread.
func TestRemoveMessageRebuildsTree(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{
		NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")}),
		NewThread("t2", []*Message{msg("other", 300)}),
	})
	v.Remove("kid")
	rows := v.Rows()
	if len(rows) != 2 || rows[0].Msg.ID != "other" || rows[1].Msg.ID != "root" {
		t.Fatalf("removal must keep the thread with its tree: %+v", rows)
	}
	v.Remove("root")
	rows = v.Rows()
	if len(rows) != 1 || rows[0].Msg.ID != "other" {
		t.Fatalf("emptied thread must leave the view: %+v", rows)
	}
	v.Remove("t:t2")
	if len(v.Rows()) != 0 {
		t.Fatalf("thread identity removes the whole thread: %+v", v.Rows())
	}
}

func TestRemoveUnknownIdentity(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("m1", 100)})})
	v.Remove("nope")
	v.Remove("t:nope")
	if len(v.Rows()) != 1 {
		t.Fatalf("unknown identities must be no-ops: %+v", v.Rows())
	}
}

// TestMergeThreadReplacesStub pins the hydrator path: a stub thread in
// the view (no message id - the refresh feed shape) gets replaced by the
// fetched content WITHOUT losing the collapse state (MergeThread keeps
// the thread object; SetCollapsed survives).
func TestMergeThreadReplacesStub(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{{ID: "", Timestamp: 100}})})
	if err := v.SetCollapsed("t1", true); err != nil {
		t.Fatal(err)
	}
	// the hydrator's merge: the fetched tree replaces the stub
	v.MergeThread(NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")}))
	rows := v.Rows()
	if len(rows) != 1 {
		t.Fatalf("collapsed thread must stay one row: %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Fatalf("collapsed row must count the fetched tree: %d", rows[0].Count)
	}
	if err := v.ToggleCollapsed("t1"); err != nil {
		t.Fatal(err)
	}
	rows = v.Rows()
	if len(rows) != 2 {
		t.Fatalf("expand must show the fetched tree: %d rows", len(rows))
	}
	if rows[1].Msg.ID != "kid" || rows[1].Depth != 1 {
		t.Fatalf("kid not at depth 1: %+v", rows[1])
	}
}

// TestStubMergeKeepsHydratedTree pins the stub guard: a hydrated thread
// receiving a stub snapshot (the refresh carry-over) must keep its tree -
// the stub's summary data updates, the tree rows stay.
func TestStubMergeKeepsHydratedTree(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})})
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{{ID: "", Timestamp: 200}})})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("the stub must not delete the tree: %d rows", len(rows))
	}
	if rows[0].Msg.ID != "root" || rows[1].Msg.ID != "kid" {
		t.Fatalf("tree rows wrong: %+v", rows)
	}
}

// TestThreadWindow pins the bounded tree window: a deep thread renders
// at most winRows rows with Depth clamped at winDepth, and SlideWindow
// advances the window until the tail. A thread that fits the budget is
// untouched.
func TestThreadWindow(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	chain := make([]*Message, 40)
	for i := range chain {
		chain[i] = msg("m"+strconv.Itoa(i+1), int64(i+1), "m"+strconv.Itoa(i))
	}
	v.MergeThreads([]*Thread{NewThread("t1", chain)})
	v.SetWindowBudget(10)
	rows := v.Rows()
	if len(rows) != 11 {
		t.Fatalf("window must emit maxRows rows plus the overflow indicator: %d", len(rows))
	}
	if rows[0].Depth != 0 || rows[0].Msg.ID != "m1" {
		t.Fatalf("window must start at the thread root: %+v", rows[0])
	}
	if rows[9].Depth != 9 {
		t.Fatalf("depth must stay true, never clamped: %+v", rows[9])
	}
	if len(rows[9].Siblings) != 9 {
		t.Fatalf("a deep row keeps its full sibling chain: %+v", rows[9])
	}
	for i, s := range rows[9].Siblings {
		if s {
			t.Fatalf("the single-child chain has no next siblings, got %d: %+v", i, rows[9])
		}
	}
	// the overflow indicator: the window's last row counts the hidden
	// tail and a ghost row renders it in the free space under the thread
	if rows[9].More != 30 {
		t.Fatalf("the window's last row must count the hidden rows: %d", rows[9].More)
	}
	if !rows[10].Ghost || rows[10].More != 30 || rows[10].ThreadID != "t1" {
		t.Fatalf("the overflow indicator row wrong: %+v", rows[10])
	}
	// slide down to the tail: 30 moves, then the edge refuses
	for i := 0; i < 30; i++ {
		if !v.SlideWindow("t1", 1) {
			t.Fatalf("slide %d must move", i)
		}
	}
	if v.SlideWindow("t1", 1) {
		t.Fatal("the tail edge must refuse the slide")
	}
	if rows := v.Rows(); len(rows) != 11 || rows[0].MoreTop != 30 || rows[1].Msg.ID != "m31" {
		t.Fatalf("the tail window must lead with the hidden-above indicator: %+v", rows[0])
	}
	// slide back up to the root
	for i := 0; i < 30; i++ {
		if !v.SlideWindow("t1", -1) {
			t.Fatalf("slide-up %d must move", i)
		}
	}
	if v.SlideWindow("t1", -1) {
		t.Fatal("the root edge must refuse the slide")
	}
	if rows := v.Rows(); rows[0].Msg.ID != "m1" {
		t.Fatalf("the root window wrong: %+v", rows[0])
	}
	// a page-chunk slide (the page move) advances one window: the
	// continuation chunk starts the window and the tail count shrinks
	if !v.SlideWindow("t1", 10) {
		t.Fatal("the page slide must move one chunk")
	}
	if rows := v.Rows(); rows[0].MoreTop != 10 || rows[1].Msg.ID != "m11" || rows[10].More != 20 || !rows[11].Ghost {
		t.Fatalf("the page-chunk window wrong: %+v", rows[0])
	}
	if !v.SlideWindow("t1", 10) {
		t.Fatal("the second page slide must move one chunk")
	}
	if !v.SlideWindow("t1", 10) {
		t.Fatal("the third page slide must move one chunk")
	}
	// the chunk near the tail clamps to the last window: the tail row
	// count drops to zero and the indicator disappears
	if rows := v.Rows(); rows[0].MoreTop != 30 || rows[1].Msg.ID != "m31" || len(rows) != 11 {
		t.Fatalf("the tail clamp wrong: %+v", rows[0])
	}
	// a mid-thread window cuts the front tree columns: the first visible
	// row's marker lands at column 0 (Depth 1), and every other row
	// shifts with it, so the subject lines stay visible
	if rows := v.Rows(); rows[1].Depth != 1 || rows[10].Depth != 10 {
		t.Fatalf("the mid-thread cut must shift depths to start at 1: %+v", rows[1])
	}
	if v.SlideWindow("t1", 10) {
		t.Fatal("the tail edge must refuse the page slide")
	}
	// a thread that fits the budget never slides
	v.SetWindowBudget(10)
	v.MergeThreads([]*Thread{NewThread("t2", []*Message{msg("solo", 1)})})
	if v.SlideWindow("t2", 1) || v.SlideWindow("t2", -1) {
		t.Fatal("a fitting thread must refuse every slide")
	}
}
