package app

import (
	"errors"
	"slices"
	"testing"

	"notmutt/core"
)

var applyGroups = []core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}}

func TestApplyStaged(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox", "unread"}},
		{ID: "m2", ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "unread", Add: false})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	view.Stage("m2", core.TagOp{Tag: "deleted", Add: true})

	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	calls := fw.tagCallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("one ActTag per staged message, got %d", len(calls))
	}
	// m1: symmetric difference of [inbox, unread] -> [archive], sorted
	if calls[0].query != "id:\"m1\"" || !slices.Equal(calls[0].tagOps,
		[]core.TagOp{{Tag: "archive", Add: true}, {Tag: "inbox", Add: false}, {Tag: "unread", Add: false}}) {
		t.Fatalf("m1 call wrong: %+v", calls[0])
	}
	if calls[1].query != "id:\"m2\"" || !slices.Equal(calls[1].tagOps,
		[]core.TagOp{{Tag: "deleted", Add: true}, {Tag: "inbox", Add: false}}) {
		t.Fatalf("m2 call wrong: %+v", calls[1])
	}
	// one membership check per applied identity, against the view query
	if n := fw.queries.Load(); n != 2 {
		t.Fatalf("membership checks = %d, want 2", n)
	}
	if q, _ := fw.lastQuery.Load().(string); q != "tag:inbox and id:\"m2\"" {
		t.Fatalf("membership query = %q", q)
	}
	// both messages left the query: the snapshot drops them (no refresh)
	if len(view.Rows()) != 0 {
		t.Fatalf("evicted rows must leave the view: %+v", view.Rows())
	}
	if view.IsStaged("m1") || view.IsStaged("m2") {
		t.Fatal("buffer must clear after apply")
	}
}

func TestApplyNetNoOpClearsEntry(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"archive"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true}) // net no-op
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	if calls := fw.tagCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("no ActTag expected, got %d", len(calls))
	}
	if view.IsStaged("m1") {
		t.Fatal("net no-op must clear the entry")
	}
}

func TestApplyFailureKeepsEntry(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setTagErr(errors.New("lock timeout"))
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	if err := applyStaged(view, applyGroups, fw); err == nil {
		t.Fatal("apply must surface the worker error")
	}
	if !view.IsStaged("m1") {
		t.Fatal("entry must stay staged on failure")
	}
	if hasTag(view.Tags("m1"), "archive") {
		t.Fatal("baseline must not be written on failure")
	}
}

func TestApplyReplyErrKeepsEntry(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setReplyErr(errors.New("lock timeout"))
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	if err := applyStaged(view, applyGroups, fw); err == nil {
		t.Fatal("apply must surface the worker reply error")
	}
	if !view.IsStaged("m1") {
		t.Fatal("entry must stay staged on reply error")
	}
	if hasTag(view.Tags("m1"), "archive") {
		t.Fatal("baseline must not be written on reply error")
	}
}

func TestApplyContinuesPastFailure(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setFailQuery("id:\"m1\"")
	fw.setTagErr(errors.New("lock timeout"))
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}},
		{ID: "m2", ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	view.Stage("m2", core.TagOp{Tag: "deleted", Add: true})
	if err := applyStaged(view, applyGroups, fw); err == nil {
		t.Fatal("apply must surface the failed entry's error")
	}
	if !view.IsStaged("m1") {
		t.Fatal("failed entry must stay staged")
	}
	if hasTag(view.Tags("m1"), "archive") {
		t.Fatal("failed entry's baseline must not be written")
	}
	if view.IsStaged("m2") {
		t.Fatal("succeeding entry must clear")
	}
	// m2's op removed its inbox tag: the row was evicted, m1 stays
	rows := view.Rows()
	if len(rows) != 1 || rows[0].Msg.ID != "m1" {
		t.Fatalf("only the failed entry's row stays: %+v", rows)
	}
	calls := fw.tagCallsSnapshot()
	if len(calls) != 1 || calls[0].query != "id:\"m2\"" {
		t.Fatalf("only m2 must reach the worker, got %+v", calls)
	}
}

// TestApplyThreadIdentity pins the summary-row apply: a thread identity
// (t:<id>) resolves against the stub's tags and emits thread:<id> - the
// whole thread, notmuch's natural unit. The baseline write goes to the
// thread (SetThreadTags), so the render flips without waiting for a
// refresh.
func TestApplyThreadIdentity(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("t:t1", core.TagOp{Tag: "archive", Add: true})
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	calls := fw.tagCallsSnapshot()
	if len(calls) != 1 || calls[0].query != "thread:t1" {
		t.Fatalf("thread identity must apply via thread:<id>, got %+v", calls)
	}
	if !slices.Equal(calls[0].tagOps,
		[]core.TagOp{{Tag: "archive", Add: true}, {Tag: "inbox", Add: false}}) {
		t.Fatalf("resolved ops wrong: %+v", calls[0].tagOps)
	}
	// the thread left the inbox query: the whole thread drops
	if len(view.Rows()) != 0 {
		t.Fatalf("evicted thread must leave the view: %+v", view.Rows())
	}
	if view.IsStaged("t:t1") {
		t.Fatal("buffer must clear after apply")
	}
}

// TestApplyStaleThreadClears pins the thread-identity stale path: a
// thread that left the view clears its entry without an ActTag.
func TestApplyStaleThreadClears(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("t:t1", core.TagOp{Tag: "archive", Add: true})
	view.MergeThreads(nil) // the thread left the view
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	if calls := fw.tagCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("no ActTag for a stale thread, got %d", len(calls))
	}
	if view.IsStaged("t:t1") {
		t.Fatal("stale entry must clear")
	}
}

func TestApplyStaleMessageSkipped(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	view.MergeThreads(nil) // the message left the view
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	if calls := fw.tagCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("no ActTag for a stale message, got %d", len(calls))
	}
	if view.IsStaged("m1") {
		t.Fatal("stale entry must clear")
	}
}

// TestApplyKeepsMatchingRow pins the other half of the eviction check:
// a soft-tag apply (unread toggle, no group) leaves the message in the
// view query - the row stays and the baseline renders.
func TestApplyKeepsMatchingRow(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}}})
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox", "unread"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "unread", Add: false})
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	if len(view.Rows()) != 1 {
		t.Fatalf("a soft-tag apply must keep the row: %+v", view.Rows())
	}
	if tags := view.Tags("m1"); !slices.Equal(tags, []string{"inbox"}) {
		t.Fatalf("baseline = %v", tags)
	}
	if view.IsStaged("m1") {
		t.Fatal("buffer must clear")
	}
}

// TestApplyEvictsMessageKeepsThread pins the tree-rebuild path: one
// message of a multi-message thread leaves the query, the thread stays
// with its remaining messages.
func TestApplyEvictsMessageKeepsThread(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t1", Tags: []string{"inbox", "unread"}}})
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups(applyGroups)
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}},
		{ID: "m2", ThreadID: "t1", Tags: []string{"inbox", "unread"}},
	})})
	view.Stage("m1", core.TagOp{Tag: "archive", Add: true})
	if err := applyStaged(view, applyGroups, fw); err != nil {
		t.Fatal(err)
	}
	rows := view.Rows()
	if len(rows) != 1 || rows[0].Msg.ID != "m2" {
		t.Fatalf("only the evicted message leaves the thread: %+v", rows)
	}
	if view.IsStaged("m1") {
		t.Fatal("buffer must clear")
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
