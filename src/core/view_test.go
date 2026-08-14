package core

import (
	"slices"
	"testing"
)

func msg(id string, ts int64, refs ...string) *Message {
	return &Message{ID: id, Timestamp: ts, References: refs}
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
	// a reply arrives, sorts before its parent in the message list under
	// reverse-date; the tree still renders the parent above the child
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
	if len(rows) != 4 {
		t.Fatalf("want ghost + 3 rows, got %d", len(rows))
	}
	if !rows[0].Ghost {
		t.Fatalf("moved thread has two roots, must render ghost first: %+v", rows[0])
	}
	if rows[1].Msg.ID != "c" {
		t.Fatalf("moved thread should be first, got %+v", rows[1])
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
	if len(rows) != 3 {
		t.Fatalf("thread merge lost rows: %d", len(rows))
	}
	if !rows[0].Ghost {
		t.Fatalf("merged thread must render ghost row: %+v", rows[0])
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
	t1 := NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})
	v.MergeThreads([]*Thread{t1})
	rows := v.Rows()
	if len(rows) != 3 {
		t.Fatalf("want ghost + 2 rows, got %d", len(rows))
	}
	if !rows[0].Ghost || rows[0].Msg != nil || rows[0].Depth != 0 || rows[0].Count != 2 {
		t.Fatalf("first row must be the ghost marker: %+v", rows[0])
	}
	if rows[1].Msg.ID != "b" || rows[1].Depth != 1 || rows[1].Root {
		t.Fatalf("second row wrong: %+v", rows[1])
	}
	if rows[2].Msg.ID != "a" || rows[2].Depth != 1 || rows[2].Root {
		t.Fatalf("third row wrong: %+v", rows[2])
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
	// no references: ghost root + two rows
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{
		{ID: "a", Timestamp: 200},
		{ID: "b", Timestamp: 100},
	})})
	v.Stage("ghost", TagOp{Tag: "archive", Add: true}) // unknown id: no-op
	rows := v.Rows()
	for _, r := range rows {
		if r.Ghost && r.Staged {
			t.Fatal("ghost rows must never be staged")
		}
	}
}
