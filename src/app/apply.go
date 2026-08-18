// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"sort"
	"strings"

	"notmutt/config"
	"notmutt/core"
	"notmutt/filter"
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
// staged during an in-flight apply survive), then moves the message's
// files into the applied folder tag's folder - the tag lands and the
// file follows (the next poll's location-wins resolution would eat an
// applied tag whose file still sits elsewhere). Identities that left
// the view clear their stale entry. Snapshot keys are sorted for a
// deterministic batch.
func applyStaged(view *core.View, groups []core.TagGroup, worker workerAPI, cfg config.Config, root string) error {
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
		if err := moveAfterApply(worker, cfg, root, identity, newTags); err != nil {
			// the tag landed; the move failed - the file sits where the
			// location resolution says it is not, and the next poll
			// reverts the tag. Surface the failure; the user retries.
			if applyErr == nil {
				applyErr = fmt.Errorf("apply %s: %v", identity, err)
			}
		}
		// the view is a materialized mirror of the query output (R13):
		// once the DB op landed, notmuch answers whether the identity
		// still matches the view query - a miss drops the row from the
		// snapshot now (the inbox row disappears on apply, no refresh).
		// A check error keeps the row; the next refresh reconciles.
		if !keptBy(view, worker, identity) {
			view.Remove(identity)
		}
	}
	return applyErr
}

// keptBy asks notmuch whether the identity still matches the view
// query: one limit-1 search, the truth path (R1). The identity query
// is the apply's own (idQuery) - DRY with the op that just landed.
func keptBy(view *core.View, worker workerAPI, identity string) bool {
	if view.Query == "" {
		return true
	}
	keep := false
	_, err := worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: view.Query + " and " + idQuery(identity),
		Limit: 1,
		Emit:  func(msgs []core.Message) bool { keep = len(msgs) > 0; return false },
	})
	if err != nil {
		return true
	}
	return keep
}

// idQuery turns a staged identity into a notmuch query: message ids
// become id:"..." (escaped), thread identities become thread:<id>.
func idQuery(identity string) string {
	if strings.HasPrefix(identity, "t:") {
		return "thread:" + identity[2:]
	}
	return "id:\"" + strings.ReplaceAll(identity, `"`, `""`) + "\""
}

// moveAfterApply physically follows the applied folder tag: the
// exclusive resolution made one member the message's home, and the
// file must be there before the next poll - the location-wins
// resolution (R2) would otherwise eat the tag. Message identities move
// directly; thread identities resolve their messages first (the
// summary-row apply). The folder tag is the resolved member with
// candidates for the message's account; no member, no move. ReadOnly
// accounts skip inside the mover (no targets).
func moveAfterApply(worker workerAPI, cfg config.Config, root string, identity string, newTags []string) error {
	ids := []string{identity}
	if strings.HasPrefix(identity, "t:") {
		ids = ids[:0]
		if rpl, err := worker.Call(notmuch.Action{
			Kind:  notmuch.ActQuery,
			Query: "thread:" + identity[2:],
			Emit: func(chunk []core.Message) bool {
				for i := range chunk {
					ids = append(ids, chunk[i].ID)
				}
				return true
			},
		}); err != nil || rpl.Err != nil {
			return fmt.Errorf("thread resolve: %v %v", err, rpl.Err)
		}
		if len(ids) == 0 {
			return nil
		}
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: ids})
	if err != nil || rpl.Err != nil {
		return fmt.Errorf("snapshot: %v %v", err, rpl.Err)
	}
	entries := make([]filter.Entry, 0, len(rpl.Msgs))
	for _, m := range rpl.Msgs {
		paths := m.Paths
		if root != "" {
			// AccountOf matches the engine's normalized (root-relative)
			// paths; snapshot paths are absolute under the root.
			paths = make([]string, len(m.Paths))
			for i, p := range m.Paths {
				paths[i] = filter.RelPath(root, p)
			}
		}
		acc := filter.AccountOf(cfg, paths)
		if acc == "" {
			continue
		}
		folder := ""
		for _, t := range newTags {
			if len(filter.Candidates(cfg.Accounts[acc], t)) > 0 {
				folder = t
				break
			}
		}
		if folder == "" {
			continue
		}
		entries = append(entries, filter.Entry{ID: m.ID, Account: acc, Folder: folder, Paths: m.Paths})
	}
	if len(entries) == 0 {
		return nil
	}
	_, err = filter.NewMoverLive(worker, cfg, root).Move(&filter.Report{Entries: entries})
	return err
}
