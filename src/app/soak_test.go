package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

// Run: NOTMUCH_SOAK=1 go test ./app/ -run TestSoak -v; Mutates the real DB
// with a scratch tag, fully reversed. Prints counts and ids only - never
// subjects or headers (privacy rule).
func TestSoak(t *testing.T) {
	if os.Getenv("NOTMUCH_SOAK") == "" {
		t.Skip("soak runs against the real DB; set NOTMUCH_SOAK=1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := notmuch.NewWorker(core.NewBus(), notmuch.NewCLI(), 10*time.Second)
	go worker.Start(ctx)
	defer worker.Call(notmuch.Action{Kind: notmuch.ActClose})

	const scratch = "notmutt-soak"
	// failure-path cleanup: remove the scratch tag from anything carrying it
	defer worker.Call(notmuch.Action{
		Kind:   notmuch.ActTag,
		Query:  "tag:" + scratch,
		TagOps: []notmuch.TagOp{{Tag: scratch, Add: false}},
	})

	query := func(q string, limit int) []core.Message {
		var got []core.Message
		worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: q, Limit: limit, Emit: func(msgs []core.Message) bool {
			got = append(got, msgs...)
			return true
		}})
		return got
	}
	inbox := query("tag:inbox", 50)
	if len(inbox) == 0 {
		t.Skip("no inbox mail")
	}
	before := len(inbox)

	// CLI Query stubs carry empty IDs; resolve one via Thread.
	target := ""
	seed := query("tag:inbox", 1)
	if len(seed) > 0 {
		thr, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: seed[0].ThreadID})
		if err == nil && thr.Err == nil && len(thr.Msgs) > 0 {
			target = thr.Msgs[0].ID
		}
	}
	if target == "" {
		t.Skip("no message with resolvable id")
	}

	// id query quoting follows applyStaged's escape (app/apply.go)
	byID := "id:\"" + strings.ReplaceAll(target, `"`, `""`) + `"`

	rpl, err := worker.Call(notmuch.Action{
		Kind: notmuch.ActTag, Query: byID,
		TagOps: []notmuch.TagOp{{Tag: scratch, Add: true}},
	})
	if err != nil || rpl.Err != nil {
		t.Fatalf("apply scratch tag: %v %v", err, rpl.Err)
	}
	rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		t.Fatalf("revision: %v %v", err, rpl.Err)
	}
	if rpl.Rev == 0 {
		t.Fatal("revision must be nonzero after a tag change")
	}
	if got := query("tag:"+scratch, 10); len(got) == 0 {
		t.Fatal("scratch tag not visible after apply")
	}

	rpl, err = worker.Call(notmuch.Action{
		Kind: notmuch.ActTag, Query: byID,
		TagOps: []notmuch.TagOp{{Tag: scratch, Add: false}},
	})
	if err != nil || rpl.Err != nil {
		t.Fatalf("remove scratch tag: %v %v", err, rpl.Err)
	}
	if got := query("tag:"+scratch, 10); len(got) != 0 {
		t.Fatalf("scratch tag still present after removal: %d msgs", len(got))
	}
	t.Logf("soak ok: %d inbox msgs, scratch tag applied and removed on %s", before, target)
}
