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
	// baseline written, buffer cleared
	if tags := view.MsgTags("m1"); !slices.Equal(tags, []string{"archive"}) {
		t.Fatalf("m1 baseline = %v", tags)
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
	if hasTag(view.MsgTags("m1"), "archive") {
		t.Fatal("baseline must not be written on failure")
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

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
