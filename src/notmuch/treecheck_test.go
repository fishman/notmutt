package notmuch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/lib/testutil"
)

func fetch(t *testing.T, b *CGOBackend, id string) *core.Thread {
	t.Helper()
	msgs, err := b.Thread(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	p := make([]*core.Message, len(msgs))
	for i := range msgs {
		p[i] = &msgs[i]
	}
	return core.NewThread(id, p)
}

func dump(t *testing.T, label string, v *core.View) {
	t.Helper()
	fmt.Println("--", label)
	for i, r := range v.Rows() {
		if r.Msg == nil {
			fmt.Printf("%2d ghost d%d %v\n", i, r.Depth, r.Siblings)
			continue
		}
		ts := time.Unix(r.Msg.Timestamp, 0).Format("01-02 15:04")
		id := r.Msg.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Printf("%2d d%d %v id=%-12s %s %s\n", i, r.Depth, r.Siblings, id, ts, r.Msg.Author)
	}
}

// TestDebugTreeCheck walks one 10-deep synthetic thread (ThreadTree)
// through the real refresh's merge sequence: stub load, hydration,
// refresh merges, window slide. Row dumps only, fabricated data.
func TestDebugTreeCheck(t *testing.T) {
	e := testutil.Setup(t)
	testutil.ThreadTree(t, e.Maildir, 1, 10)
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	var tid string
	if err := b.Query(context.Background(), "tag:inbox", 1, false, func(chunk []core.Message) bool {
		if len(chunk) > 0 {
			tid = chunk[0].ThreadID
		}
		return false
	}); err != nil {
		t.Fatal(err)
	}
	if tid == "" {
		t.Fatal("seed query found nothing")
	}
	v := core.NewView("inbox", "tag:inbox")
	real := fetch(t, b, tid)

	// 1. the fullReload feed: a summary stub for the thread
	stub := core.NewThread(tid, []*core.Message{{ThreadID: tid, Author: "alpha", Subject: "lorem ipsum"}})
	v.MergeThreads([]*core.Thread{stub})
	dump(t, "after stub load", v)

	// 2. the hydrator: MergeThread with the real fetch
	v.MergeThread(real)
	dump(t, "after hydration", v)

	// 3. refresh cycle: re-fetch + MergeThreads
	ref := fetch(t, b, tid)
	v.MergeThreads([]*core.Thread{ref})
	dump(t, "after refresh merge", v)

	// 4. second refresh (carry-over shape)
	v.MergeThreads([]*core.Thread{stub, fetch(t, b, tid)})
	dump(t, "after second refresh", v)

	// 5. the window: budget + slide to WinStart 4
	v.SetWindowBudget(10)
	v.SlideWindow(tid, 4)
	dump(t, "after slide 4", v)
}
