package app

import (
	"fmt"
	"sort"
	"strings"

	"notmutt/core"
	"notmutt/notmuch"
)

// applyStaged flushes the staged buffer: one ActTag per staged identity
// carrying the resolved op set (R14). Identities are message ids
// (id:"..." query) or thread identities (t:<thread>, thread:<id> query
// - the whole thread, what a summary row stands for; notmuch's natural
// unit). Every entry is attempted; a failed entry stays staged for retry
// or undo while the rest of the batch proceeds, and the first failure
// surfaces as the returned error. Success writes the resolved tags as
// the applied baseline and clears the entry (generation-guarded, so ops
// staged during an in-flight apply survive). Identities that left the
// view clear their stale entry. Snapshot keys are sorted for a
// deterministic batch.
func applyStaged(view *core.View, groups []core.TagGroup, worker workerAPI) error {
	snapshot, gen := view.StagedOps()
	if len(snapshot) == 0 {
		return nil
	}
	ids := make([]string, 0, len(snapshot))
	for id := range snapshot {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var applyErr error
	for _, identity := range ids {
		tags := view.Tags(identity)
		if tags == nil {
			view.ClearStaged(identity, gen)
			continue
		}
		newTags, resolved := core.ResolveOps(tags, snapshot[identity], groups)
		if len(resolved) == 0 {
			view.ClearStaged(identity, gen)
			continue
		}
		rpl, err := worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  idQuery(identity),
			TagOps: resolved,
		})
		if err != nil || rpl.Err != nil {
			if applyErr == nil {
				applyErr = fmt.Errorf("apply %s: %v %v", identity, err, rpl.Err)
			}
			continue
		}
		// Tags was snapshotted at apply start; the setter overwrites
		// whatever a concurrent merge reconciled in between. The next
		// refresh re-reconciles snapshot truth, so the window self-heals.
		if strings.HasPrefix(identity, "t:") {
			view.SetThreadTags(identity[2:], newTags)
		} else {
			view.SetTags(identity, newTags)
		}
		view.ClearStaged(identity, gen)
	}
	return applyErr
}

// idQuery turns a staged identity into a notmuch query: message ids
// become id:"..." (escaped), thread identities become thread:<id>.
func idQuery(identity string) string {
	if strings.HasPrefix(identity, "t:") {
		return "thread:" + identity[2:]
	}
	return "id:\"" + strings.ReplaceAll(identity, `"`, `""`) + "\""
}
