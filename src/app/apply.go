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

// applyEnv is the plumbing every tag write shares: the worker, the live
// views, the bus that carries the repaint, and the config a folder-tag
// move resolves against.
type applyEnv struct {
	worker workerAPI
	bus    *core.Bus
	views  map[string]*core.View
	cfg    config.Config
	root   string
	groups []core.TagGroup
	// flagSyncOff: the store's flag writes do not rename files
	// (maildir.synchronize_flags off, or a backend that keeps flags
	// elsewhere) - a flag op changes no path, so rows skip the repoint.
	// Inverted so the zero value repoints: skipping one that was needed
	// leaves a row naming a deleted file, repointing a no-op does not.
	flagSyncOff bool
}

// applyStaged flushes the staged buffer: one tag write per staged identity
// (R14). Identities are message ids or thread identities (t:<thread>, the
// whole thread). Every entry is attempted; a failed entry stays staged for
// retry/undo while the batch proceeds, the first failure surfaces. Success
// writes the tags as the applied baseline (generation-guarded, so ops
// staged during an in-flight apply survive) and reconciles the rows - the
// same seam (tagWrite) a direct write takes. A folder-tag ADD must resolve
// its move BEFORE the tag lands: an unresolvable move is a config error,
// not a half-applied state.
func applyStaged(view *core.View, env applyEnv) error {
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
		_, resolved := core.ResolveOps(tags, snapshot[identity], env.groups)
		if len(resolved) == 0 {
			view.ClearStaged(identity, gen)
			continue
		}
		if err := env.execApply(identity, resolved); err != nil {
			if applyErr == nil {
				applyErr = fmt.Errorf("apply %s: %v", identity, err)
			}
			continue
		}
		view.ClearStaged(identity, gen)
		// the view mirrors the query output (R13): once the DB op landed,
		// a miss drops the row now (no refresh); a check error keeps it,
		// the next refresh reconciles.
		if !env.keptBy(view, identity) {
			view.Remove(identity)
		}
	}
	return applyErr
}

// execApply executes a resolved op set on one identity (a message id or
// a t:thread) - the apply arm shared by the UI staged flush (applyStaged)
// and the MCP ops path. The folder guard runs first: a group ADD resolves
// and executes its physical move BEFORE the tag lands - a tag whose file
// cannot follow becomes the tag-without-folder state the next poll's
// location-wins resolution eats. The error names the config fix. Moving
// first keeps the DB honest at every instant: a file already in its target
// folder carries the tag the folder rule would give it anyway, so even a
// classify racing the gap agrees. Where a folder move fails, the tag never
// lands - tagging first would need a poll to revert it, and the apply's own
// ActTag advances the revision out of band, so no poll ever reclassifies
// the entry on cgo. The write itself is tagWrite: one seam, whatever
// issued the op.
func (e applyEnv) execApply(identity string, resolved []core.TagOp) error {
	moved := false
	if folderTags := groupAdds(resolved, e.groups); len(folderTags) > 0 {
		move, err := moveEntries(e.worker, e.cfg, e.root, identity, folderTags)
		if err != nil {
			return err
		}
		if len(move) > 0 {
			mr, err := filter.NewMoverLive(e.worker, e.cfg, e.root).Move(&filter.Report{Entries: move})
			if err != nil {
				return err
			}
			reportMoveDiag("apply", mr, 0)
			moved = true
		}
	}
	return e.tagWrite(identity, resolved, moved)
}

// keptBy asks notmuch whether the identity still matches the view
// query: one limit-1 search, the truth path (R1), reusing the apply's
// own query (idQuery).
func (e applyEnv) keptBy(view *core.View, identity string) bool {
	q := view.ViewQuery() // the refresher may switch the view on its goroutine
	if q == "" {
		return true
	}
	keep := false
	_, err := e.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: q + " and " + idQuery(identity),
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

// groupAdds returns the ops' last ADD per exclusive group (the
// lastAdd-wins winner, the folder tag that becomes the message's
// home). Empty = no group touched: no move, no guard.
func groupAdds(ops []core.TagOp, groups []core.TagGroup) []string {
	var out []string
	for _, g := range groups {
		last := ""
		for _, op := range ops {
			if op.Add && groupMember(g, op.Tag) {
				last = op.Tag
			}
		}
		if last != "" {
			out = append(out, last)
		}
	}
	return out
}

func groupMember(g core.TagGroup, tag string) bool {
	for _, t := range g.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// moveEntries resolves a folder tag's physical move BEFORE the tag
// lands: the account from the message's paths, the winner tag's
// candidates (moves > preset > detected), and the readonly gate. Every
// resolution failure is an error naming the config fix, so a folder
// tag never applies without a resolvable move. Thread identities
// resolve their messages first (the summary-row apply).
func moveEntries(worker workerAPI, cfg config.Config, root string, identity string, folderTags []string) ([]filter.Entry, error) {
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
			return nil, fmt.Errorf("thread resolve: %v %v", err, rpl.Err)
		}
		if len(ids) == 0 {
			return nil, nil
		}
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: ids})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("snapshot: %v %v", err, rpl.Err)
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
			path := "(no files)"
			if len(paths) > 0 {
				path = paths[0]
			}
			return nil, fmt.Errorf("no account folder space matches %s (check the [accounts] folder prefixes)", path)
		}
		a := cfg.Accounts[acc]
		if a.ReadOnly {
			return nil, fmt.Errorf("account %s is readonly: folder tags never move", acc)
		}
		folder := ""
		for _, t := range folderTags {
			if len(filter.Candidates(a, t)) > 0 {
				folder = t
				break
			}
		}
		if folder == "" {
			return nil, fmt.Errorf("folder tag %s: no move candidates in account %s ([accounts.%s] folders/moves/preset)", strings.Join(folderTags, "/"), acc, acc)
		}
		entries = append(entries, filter.Entry{ID: m.ID, Account: acc, Folder: folder, Paths: m.Paths})
	}
	return entries, nil
}

// tagWrite is the one tag-write seam: it lands ops on one identity (a
// message id or a t:thread), then reconciles every view row holding it -
// net-apply the ops with that view's own tag groups (the staged render's
// rule), write the row, notify, and repoint its paths when the op moved
// the file: a flag tag with maildir flag sync on renames in place, a
// folder move relocates whatever the flag-sync setting (moved). The write
// lands first: a failed write reports and never reflects. A folder op
// carries a move - the caller resolves that before this call (execApply).
// views nil (the MCP ops path) = a pure DB write, and then bus is never
// touched. Tags were snapshotted before the write; the setter overwrites
// whatever a concurrent merge reconciled in between, and the next refresh
// re-reconciles, so the window self-heals.
func (e applyEnv) tagWrite(identity string, ops []core.TagOp, moved bool) error {
	rpl, err := e.worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: idQuery(identity), TagOps: ops})
	if err != nil {
		return err
	}
	if rpl.Err != nil {
		return rpl.Err
	}
	thread := strings.HasPrefix(identity, "t:")
	changed := false
	for name, v := range e.views {
		old := v.Tags(identity)
		if old == nil {
			continue
		}
		nw, resolved := core.ResolveOps(old, ops, v.Groups())
		if len(resolved) == 0 {
			continue // no row change: the op is a no-op here, no path change either
		}
		changed = true
		if thread {
			v.SetThreadTags(identity[2:], nw)
		} else {
			v.SetTags(identity, nw)
		}
		e.bus.Publish(core.ViewDiff{View: name})
	}
	// a hydrated thread defers: its messages self-heal on the next fetch
	if changed && !thread && (moved || !e.flagSyncOff) {
		refreshPaths(e.worker, e.views, identity)
	}
	return nil
}

// refreshPaths repoints live rows for msgID at its current paths (R1): a
// tag op that renamed or moved the file deleted the old path, and a row
// caching it would open a deleted file. Call at tag seams out-of-band of a
// refresh; views not holding the message no-op.
func refreshPaths(worker workerAPI, views map[string]*core.View, msgID string) {
	paths := currentPaths(worker, msgID)
	if len(paths) == 0 {
		return
	}
	for _, v := range views {
		v.SetPaths(msgID, paths)
	}
}

// currentPaths asks notmuch for the message's present file paths: one
// limit-free snapshot by id, the same query the mover resolves against.
func currentPaths(worker workerAPI, msgID string) []string {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: []string{msgID}})
	if err != nil || rpl.Err != nil || len(rpl.Msgs) == 0 {
		return nil
	}
	return rpl.Msgs[0].Paths
}
