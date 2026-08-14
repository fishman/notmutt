package app

import (
	"fmt"
	"sort"
	"strings"

	"notmutt/core"
	"notmutt/notmuch"
)

// applyStaged flushes the staged buffer: one ActTag per staged message
// carrying the resolved op set (R14). Success writes the resolved tags
// as the applied baseline and clears the entry (generation-guarded, so
// ops staged during an in-flight apply survive); failure keeps the
// entry staged for retry or undo. Messages that left the view clear
// their stale entry. Snapshot keys are sorted for a deterministic batch.
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
	for _, msgID := range ids {
		tags := view.MsgTags(msgID)
		if tags == nil {
			view.ClearStaged(msgID, gen)
			continue
		}
		newTags, resolved := core.ResolveOps(tags, snapshot[msgID], groups)
		if len(resolved) == 0 {
			view.ClearStaged(msgID, gen)
			continue
		}
		rpl, err := worker.Call(notmuch.Action{
			Kind:   notmuch.ActTag,
			Query:  "id:\"" + strings.ReplaceAll(msgID, `"`, `""`) + `"`,
			TagOps: resolved,
		})
		if err != nil || rpl.Err != nil {
			return fmt.Errorf("apply %s: %v %v", msgID, err, rpl.Err)
		}
		view.SetTags(msgID, newTags)
		view.ClearStaged(msgID, gen)
	}
	return nil
}
