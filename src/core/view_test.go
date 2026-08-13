package core

import "testing"

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
	if rows[0].Msg.ID != "c" {
		t.Fatalf("moved thread should be first, got %+v", rows[0])
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
	v.Threads[0].Collapsed = true
	rows := v.Rows()
	if len(rows) != 1 {
		t.Fatalf("collapsed thread must render 1 row, got %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Fatalf("collapsed row must still count the thread, got %d", rows[0].Count)
	}
}
