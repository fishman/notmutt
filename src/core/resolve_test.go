package core

import (
	"slices"
	"testing"
)

var folderGroup = TagGroup{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}

func TestResolveOpsApplies(t *testing.T) {
	got, ops := ResolveOps([]string{"inbox", "unread"}, []TagOp{{Tag: "unread", Add: false}}, nil)
	if !slices.Equal(got, []string{"inbox"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "unread", Add: false}}) {
		t.Fatalf("resolved = %v", ops)
	}
}

func TestResolveMoveRemovesOthers(t *testing.T) {
	// +archive on [inbox, unread]: archive wins, inbox is a member and
	// goes, unread stays; the batch is the symmetric difference, sorted
	got, ops := ResolveOps([]string{"inbox", "unread"}, []TagOp{{Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive", "unread"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "archive", Add: true}, {Tag: "inbox", Add: false}}) {
		t.Fatalf("ops = %v", ops)
	}
}

func TestResolveCombinedStage(t *testing.T) {
	// r then a staged together: -unread +archive on [inbox, unread]
	// emits -unread -inbox +archive in one batch (spec section 4)
	got, ops := ResolveOps([]string{"inbox", "unread"}, []TagOp{{Tag: "unread", Add: false}, {Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "archive", Add: true}, {Tag: "inbox", Add: false}, {Tag: "unread", Add: false}}) {
		t.Fatalf("ops = %v", ops)
	}
}

func TestResolveMovesAreSymmetric(t *testing.T) {
	// +inbox on [archive] moves back; +deleted on [archive] moves to
	// trash; +archive on [spam] unspams - no priority, no sticky
	got, _ := ResolveOps([]string{"archive"}, []TagOp{{Tag: "inbox", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"inbox"}) {
		t.Fatalf("tags = %v", got)
	}
	got, _ = ResolveOps([]string{"archive"}, []TagOp{{Tag: "deleted", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"deleted"}) {
		t.Fatalf("tags = %v", got)
	}
	got, _ = ResolveOps([]string{"spam"}, []TagOp{{Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive"}) {
		t.Fatalf("tags = %v", got)
	}
}

func TestResolveUntouchedGroupsStay(t *testing.T) {
	// pending mail keeps its inbox view until moved; soft tags are never
	// removed by folder moves
	got, ops := ResolveOps([]string{"pending", "inbox", "unread"}, []TagOp{{Tag: "unread", Add: false}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"pending", "inbox"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "unread", Add: false}}) {
		t.Fatalf("ops = %v", ops)
	}
	got, ops = ResolveOps([]string{"inbox", "work"}, []TagOp{{Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive", "work"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "archive", Add: true}, {Tag: "inbox", Add: false}}) {
		t.Fatalf("ops = %v", ops)
	}
}

func TestResolveNetNoOpEmpty(t *testing.T) {
	// staging +archive on an already-archived message: nothing to do
	got, ops := ResolveOps([]string{"archive"}, []TagOp{{Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive"}) {
		t.Fatalf("tags = %v", got)
	}
	if len(ops) != 0 {
		t.Fatalf("resolved must be empty, got %v", ops)
	}
}

func TestResolveLegacyTiebreak(t *testing.T) {
	// legacy mail carrying two folder tags normalizes on a
	// group-touching removal: the first present in list order wins
	got, ops := ResolveOps([]string{"archive", "deleted"}, []TagOp{{Tag: "sent", Add: false}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"archive"}) {
		t.Fatalf("tags = %v", got)
	}
	if !slices.Equal(ops, []TagOp{{Tag: "deleted", Add: false}}) {
		t.Fatalf("ops = %v", ops)
	}
}

func TestResolveAddThenRemoveNets(t *testing.T) {
	// a member added then removed again must not delete the surviving
	// member (the group falls back to the list scan)
	got, ops := ResolveOps([]string{"inbox"}, []TagOp{{Tag: "archive", Add: true}, {Tag: "archive", Add: false}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"inbox"}) {
		t.Fatalf("tags = %v", got)
	}
	if len(ops) != 0 {
		t.Fatalf("resolved must be empty, got %v", ops)
	}
}

func TestResolveOrderPreserved(t *testing.T) {
	// input order preserved, additions appended in op order
	got, _ := ResolveOps([]string{"unread", "inbox"}, []TagOp{{Tag: "archive", Add: true}}, []TagGroup{folderGroup})
	if !slices.Equal(got, []string{"unread", "archive"}) {
		t.Fatalf("tags = %v", got)
	}
}
