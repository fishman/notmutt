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
