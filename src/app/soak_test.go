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

	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 50})
	if err != nil || rpl.Err != nil || len(rpl.Msgs) == 0 {
		t.Skipf("no inbox mail: %v %v", err, rpl.Err)
	}
	before := len(rpl.Msgs)

	// CLI Query stubs carry empty IDs; resolve one via Thread.
	target := ""
	seed, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 1})
	if err == nil && seed.Err == nil && len(seed.Msgs) > 0 {
		thr, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: seed.Msgs[0].ThreadID})
		if err == nil && thr.Err == nil && len(thr.Msgs) > 0 {
			target = thr.Msgs[0].ID
		}
	}
	if target == "" {
		t.Skip("no message with resolvable id")
	}

	// id query quoting follows app.go's SetTagOpHandler escape
	byID := "id:\"" + strings.ReplaceAll(target, `"`, `""`) + `"`

	rpl, err = worker.Call(notmuch.Action{
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
	rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:" + scratch, Limit: 10})
	if err != nil || rpl.Err != nil || len(rpl.Msgs) == 0 {
		t.Fatalf("scratch tag not visible: %v %v %d msgs", err, rpl.Err, len(rpl.Msgs))
	}

	rpl, err = worker.Call(notmuch.Action{
		Kind: notmuch.ActTag, Query: byID,
		TagOps: []notmuch.TagOp{{Tag: scratch, Add: false}},
	})
	if err != nil || rpl.Err != nil {
		t.Fatalf("remove scratch tag: %v %v", err, rpl.Err)
	}
	rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 50})
	if err != nil || rpl.Err != nil {
		t.Fatalf("final query: %v %v", err, rpl.Err)
	}
	if len(rpl.Msgs) != before {
		t.Fatalf("inbox count changed: before %d after %d", before, len(rpl.Msgs))
	}
	t.Logf("soak ok: %d inbox msgs, scratch tag applied and removed on %s", before, target)
}
