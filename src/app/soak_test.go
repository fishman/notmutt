package app

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"notmutt/config"
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
	worker := notmuch.NewWorker(core.NewBus(), notmuch.New(), 10*time.Second)
	go worker.Start(ctx)
	defer worker.Call(notmuch.Action{Kind: notmuch.ActClose})
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		t.Fatalf("open: %v %v", err, rpl.Err)
	}

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

// Run: NOTMUCH_SOAK=1 go test ./app/ -run TestSoakStagedApply -v. Stages
// and applies a folder move on one real message, verifies the DB state at
// each step, then restores it exactly. Prints ids and tag names only.
func TestSoakStagedApply(t *testing.T) {
	if os.Getenv("NOTMUCH_SOAK") == "" {
		t.Skip("soak runs against the real DB; set NOTMUCH_SOAK=1")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := notmuch.NewWorker(core.NewBus(), notmuch.New(), 10*time.Second)
	go worker.Start(ctx)
	defer worker.Call(notmuch.Action{Kind: notmuch.ActClose})
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		t.Fatalf("open: %v %v", err, rpl.Err)
	}

	groups := config.Default().TagGroupList()
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(groups)

	// ActQuery fills the reply only via the emit callback (CLI backend)
	var seedMsgs []core.Message
	seed, err := worker.Call(notmuch.Action{
		Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 1,
		Emit: func(msgs []core.Message) bool {
			seedMsgs = append(seedMsgs, msgs...)
			return true
		},
	})
	if err != nil || seed.Err != nil || len(seedMsgs) == 0 {
		t.Skip("no inbox mail")
	}
	thr, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: seedMsgs[0].ThreadID})
	if err != nil || thr.Err != nil || len(thr.Msgs) == 0 {
		t.Skip("no message with resolvable id")
	}
	target := thr.Msgs[0].ID
	before := append([]string(nil), thr.Msgs[0].Tags...)
	if hasTag(before, "archive") {
		t.Skip("target already archived; the staged move would be a no-op")
	}
	ptrs := make([]*core.Message, len(thr.Msgs))
	for i := range thr.Msgs {
		ptrs[i] = &thr.Msgs[i]
	}
	view.MergeThreads([]*core.Thread{core.NewThread(thr.Msgs[0].ThreadID, ptrs)})

	staged := []core.TagOp{{Tag: "archive", Add: true}, {Tag: "unread", Add: false}}
	view.Stage(target, staged[0])
	view.Stage(target, staged[1])

	// staging must not touch the DB
	got := fetchTags(worker, thr.Msgs[0].ThreadID, target)
	if !sameTags(before, got) {
		t.Fatalf("staging changed the DB: %v -> %v", before, got)
	}

	expected, _ := core.ResolveOps(before, staged, groups)
	byID := "id:\"" + strings.ReplaceAll(target, `"`, `""`) + `"`
	// defer the restore from before apply, so every failure path leaves
	// the DB clean; on an untouched DB the inverse ops are harmless no-ops
	defer worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: byID, TagOps: inverseOps(expected, before)})

	if err := applyStaged(view, groups, worker, config.Default(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got = fetchTags(worker, thr.Msgs[0].ThreadID, target)
	if !sameTags(expected, got) {
		t.Fatalf("apply landed %v, want %v", got, expected)
	}
	if view.IsStaged(target) {
		t.Fatal("buffer must clear after apply")
	}
	t.Logf("soak apply ok: %s -> %v", target, got)

	// restore now, so the end state is verified; the defer above stays as
	// the failure-path backstop and is a no-op once the DB is back at
	// `before` (the inverse ops only touch the diff)
	worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: byID, TagOps: inverseOps(expected, before)})
	got = fetchTags(worker, thr.Msgs[0].ThreadID, target)
	if !sameTags(before, got) {
		t.Fatalf("restore left %v, want %v", got, before)
	}
}

func fetchTags(worker workerAPI, threadID, msgID string) []string {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
	if err != nil || rpl.Err != nil {
		return nil
	}
	for _, m := range rpl.Msgs {
		if m.ID == msgID {
			return append([]string(nil), m.Tags...)
		}
	}
	return nil
}

func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := map[string]bool{}
	for _, t := range a {
		as[t] = true
	}
	for _, t := range b {
		if !as[t] {
			return false
		}
	}
	return true
}

// inverseOps returns the ops restoring `to` from `from` (the inverse of
// the symmetric difference), sorted for a deterministic batch.
func inverseOps(from, to []string) []notmuch.TagOp {
	have := map[string]bool{}
	for _, t := range from {
		have[t] = true
	}
	want := map[string]bool{}
	for _, t := range to {
		want[t] = true
	}
	var ops []notmuch.TagOp
	for t := range have {
		if !want[t] {
			ops = append(ops, notmuch.TagOp{Tag: t, Add: false})
		}
	}
	for t := range want {
		if !have[t] {
			ops = append(ops, notmuch.TagOp{Tag: t, Add: true})
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Tag < ops[j].Tag })
	return ops
}
