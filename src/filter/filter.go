// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package filter is the classification engine (R2): it turns the
// lastmod delta (the changed messages between two revisions) into tag
// ops through the declarative rule set.
package filter

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// Worker is the notmuch action channel the engine reads and writes
// through (the app's worker; the engine never touches the backend).
type Worker interface {
	Call(a notmuch.Action) (notmuch.Reply, error)
}

// Engine classifies the delta. New resolves the account rules once;
// Run evaluates one revision bracket per poll.
type Engine struct {
	worker Worker
	cfg    config.Config
	groups []core.TagGroup
	// emissions is the per-group tag emission order: the folder group
	// follows folderEmission (priority ascending, last add wins the
	// exclusive resolution); other groups keep their config order.
	emissions [][]string
	accs      []accountRule
	root      string // the mail root, for file stats (paths are relative to it)
}

// folderEmission is the folder-group emission order, lowest priority
// first (reference "Priority: archive > deleted > sent > draft >
// pending > spam", inbox removed by any other member). The last
// member-add wins the exclusive resolution (sent beats inbox, archive
// beats sent); custom folder tags emit first, in group order.
var folderEmission = []string{"inbox", "spam", "pending", "draft", "sent", "deleted", "archive"}

type accountRule struct {
	name   string
	acc    config.Account
	folder string // the account tag: the folder space
	rules  map[string][]string
	inbox  []string
}

// New builds the engine from the config: one rule set per account
// (folder space, folder-rule candidates, inbox candidates).
func New(w Worker, cfg config.Config, root string) *Engine {
	e := &Engine{worker: w, cfg: cfg, root: root, groups: cfg.TagGroupList()}
	for _, g := range e.groups {
		order := make([]string, 0, len(g.Tags))
		for _, t := range g.Tags {
			if !slices.Contains(folderEmission, t) {
				order = append(order, t)
			}
		}
		for _, t := range folderEmission {
			if slices.Contains(g.Tags, t) {
				order = append(order, t)
			}
		}
		e.emissions = append(e.emissions, order)
	}
	for name, a := range cfg.Accounts {
		ar := accountRule{name: name, acc: a, folder: a.Tag(name), rules: map[string][]string{}}
		for _, g := range e.groups {
			for _, tag := range g.Tags {
				if tag == "inbox" {
					ar.inbox = Candidates(a, tag)
					continue
				}
				if cs := Candidates(a, tag); len(cs) > 0 {
					ar.rules[tag] = cs
				}
			}
		}
		e.accs = append(e.accs, ar)
	}
	return e
}

// Candidates resolves a hard tag's folder candidates for an account:
// moves override, else preset, else the detected folders map (afew
// folder_priorities as data, R2).
func Candidates(a config.Account, tag string) []string {
	if cs, ok := a.Moves[tag]; ok {
		return cs
	}
	if a.Preset != "" {
		if cs, ok := config.Presets[a.Preset][tag]; ok {
			return cs
		}
	}
	if f, ok := a.Folders[tag]; ok && f != "" {
		return []string{f}
	}
	return nil
}

// Entry is one message's classification outcome.
type Entry struct {
	ID        string
	Sender    string // the From display name (the notify headline)
	Subject   string
	Timestamp int64
	Priority  bool // carries a [notify] priority tag after classification
	Notify    bool // carries every [notify] tags entry after classification (default: unread inbox)
	Account   string
	Folder    string       // the resolved folder-group winner with move candidates; empty = no move
	Paths     []string     // the message's files, as notmuch reported them
	Ops       []core.TagOp // the fully resolved op set (adds + exclusive-group removals)
}

// Report is the run's outcome: dry-run writes nothing and the entries
// ARE the review surface; a live run applies them and reports the same.
type Report struct {
	DryRun  bool
	Entries []Entry
}

// Run classifies the (pre, cur] lastmod delta into per-message ops
// (one ActTag per message id): account tags, folder rules, header
// rules, and the delivery-gated untag-reversal. Dry-run ([filter]
// dry-run) computes the report and writes nothing.
func (e *Engine) Run(pre, cur uint64) (*Report, error) {
	ids, err := e.delta(pre, cur)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return &Report{}, nil
	}
	rpl, err := e.worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: ids})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("filter: snapshots: %v %v", err, rpl.Err)
	}
	hits, err := e.headerMatches(ids)
	if err != nil {
		return nil, err
	}
	rep := &Report{DryRun: e.cfg.Filter.DryRun}
	for i := range rpl.Msgs {
		entry := e.classify(rpl.Msgs[i], hits)
		if len(entry.Ops) == 0 && entry.Folder == "" {
			continue
		}
		rep.Entries = append(rep.Entries, entry)
	}
	if rep.DryRun || len(rep.Entries) == 0 {
		return rep, nil
	}
	for _, entry := range rep.Entries {
		if len(entry.Ops) == 0 {
			continue // a move-only entry: nothing to tag, the mover owns it
		}
		rpl, err := e.worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: "id:" + entry.ID, TagOps: entry.Ops})
		if err != nil || rpl.Err != nil {
			return nil, fmt.Errorf("filter: apply %s: %v %v", entry.ID, err, rpl.Err)
		}
	}
	return rep, nil
}

// delta is the lastmod bracket as bare message ids.
func (e *Engine) delta(pre, cur uint64) ([]string, error) {
	var ids []string
	rpl, err := e.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQueryMsgs,
		Query: fmt.Sprintf("lastmod:%d..%d", pre, cur),
		Emit: func(chunk []core.Message) bool {
			for i := range chunk {
				ids = append(ids, chunk[i].ID)
			}
			return true
		},
	})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("filter: delta: %v %v", err, rpl.Err)
	}
	return ids, nil
}

// idChunk bounds one header-rule OR query: notmuch chokes on
// unbounded queries (a mass change can touch thousands of messages).
const idChunk = 1000

// headerMatches evaluates the header rules against the delta: each
// rule's query scoped to a chunk of ids. The guard is the snapshot
// check in classify - rule files carry no NOT guards of their own.
func (e *Engine) headerMatches(ids []string) ([]map[string]bool, error) {
	hits := make([]map[string]bool, len(e.cfg.Filter.HeaderRules))
	for i, r := range e.cfg.Filter.HeaderRules {
		hit := map[string]bool{}
		for lo := 0; lo < len(ids); lo += idChunk {
			hi := min(lo+idChunk, len(ids))
			var q strings.Builder
			q.WriteByte('(')
			q.WriteString(r.Query)
			q.WriteString(") and (")
			for j := lo; j < hi; j++ {
				if j > lo {
					q.WriteString(" or ")
				}
				q.WriteString("id:")
				q.WriteString(ids[j])
			}
			q.WriteByte(')')
			rpl, err := e.worker.Call(notmuch.Action{
				Kind:  notmuch.ActQueryMsgs,
				Query: q.String(),
				Emit: func(chunk []core.Message) bool {
					for k := range chunk {
						hit[chunk[k].ID] = true
					}
					return true
				},
			})
			if err != nil || rpl.Err != nil {
				return nil, fmt.Errorf("filter: header rule %d: %v %v", i, err, rpl.Err)
			}
		}
		hits[i] = hit
	}
	return hits, nil
}

// classify runs the rule set over one message snapshot: account tag
// (path prefix), folder rules (location IS the home - a member emits
// when the file sits in its folders), header rules, then the
// exclusive-group resolution (R2: applying a member removes the
// others present, inbox included).
func (e *Engine) classify(m core.Message, hits []map[string]bool) Entry {
	var ops []core.TagOp
	paths := e.norm(m.Paths)
	ar := e.accountOf(paths)
	if ar != nil && ar.acc.ReadOnly {
		// readonly accounts (dead accounts like toptal) are never
		// classified: the client writes nothing to their mail.
		return Entry{ID: m.ID}
	}
	if ar != nil {
		if !hasTag(m.Tags, ar.folder) {
			ops = append(ops, core.TagOp{Tag: ar.folder, Add: true})
		}
		// folder rules, emitted in folderEmission order: the last
		// member-add wins the exclusive resolution. A member without
		// folder candidates never emits: it cannot be a home here.
		for gi := range e.groups {
			for _, tag := range e.emissions[gi] {
				cs := ar.rules[tag]
				if len(cs) == 0 {
					if tag != "inbox" {
						continue
					}
					cs = ar.inbox
					if len(cs) == 0 {
						continue
					}
				}
				if inFolder(paths, ar.folder, cs) {
					ops = append(ops, core.TagOp{Tag: tag, Add: true})
				}
			}
		}
	}
	for i, r := range e.cfg.Filter.HeaderRules {
		if !hits[i][m.ID] {
			continue
		}
		for _, t := range r.Add {
			if !hasTag(m.Tags, t) {
				ops = append(ops, core.TagOp{Tag: t, Add: true})
			}
		}
	}
	// one op per tag: the report and apply carry the resolved set, not
	// the raw rule emissions.
	seen := map[string]bool{}
	uniq := ops[:0]
	for _, op := range ops {
		if seen[op.Tag] {
			continue
		}
		seen[op.Tag] = true
		uniq = append(uniq, op)
	}
	ops = uniq
	final, resolved := core.ResolveOps(m.Tags, ops, e.groups)
	prio := false
	if len(ops) > 0 && len(e.cfg.Notify.Priority) > 0 {
		for _, t := range final {
			for _, p := range e.cfg.Notify.Priority {
				if t == p {
					prio = true
					break
				}
			}
			if prio {
				break
			}
		}
	}
	notif := len(e.cfg.Notify.Tags) == 0
	if !notif {
		notif = true
		for _, t := range e.cfg.Notify.Tags {
			if !slices.Contains(final, t) {
				notif = false
				break
			}
		}
	}
	acc := ""
	folder := ""
	if ar != nil {
		acc = ar.name
		// the move tag: the resolved winner among folder tags with
		// candidates (inbox has none and never moves).
		for _, op := range resolved {
			if op.Add {
				if _, ok := ar.rules[op.Tag]; ok {
					folder = op.Tag
					break
				}
			}
		}
		if folder == "" {
			// already-tagged mail moves too: a hard tag the message
			// carries must physically follow its folder. The inFolder
			// guard keeps already-home rows out of the report.
			for _, t := range final {
				if cs := ar.rules[t]; len(cs) > 0 && !inFolder(paths, ar.folder, cs) {
					folder = t
					break
				}
			}
		}
	}
	if len(ops) == 0 && folder == "" {
		return Entry{}
	}
	return Entry{ID: m.ID, Sender: m.Author, Subject: m.Subject, Timestamp: m.Timestamp, Priority: prio, Notify: notif, Account: acc, Folder: folder, Paths: m.Paths, Ops: resolved}
}

// RelPath strips the mail root prefix for the path rules: the root
// join leaves a leading slash that would break the folder prefix
// match.
func RelPath(root, p string) string {
	if root != "" && strings.HasPrefix(p, root) {
		return strings.Trim(strings.TrimPrefix(p, root), "/")
	}
	return p
}

// absPath joins the root for a file stat; absolute paths pass through.
func absPath(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// norm strips the mail root prefix once per message.
func (e *Engine) norm(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = RelPath(e.root, p)
	}
	return out
}

// accountOf resolves the message's account by path: the first path
// under an account's folder space wins (reference folder:/^<acc>\//).
func (e *Engine) accountOf(paths []string) *accountRule {
	for _, p := range paths {
		for i := range e.accs {
			if strings.HasPrefix(p, e.accs[i].folder+"/") {
				return &e.accs[i]
			}
		}
	}
	return nil
}

// AccountOf is accountOf as a standalone for the apply path: resolves
// an applied message's account the same way the engine does.
func AccountOf(cfg config.Config, paths []string) string {
	for _, p := range paths {
		for name, a := range cfg.Accounts {
			if strings.HasPrefix(p, a.Tag(name)+"/") {
				return name
			}
		}
	}
	return ""
}

// abs joins the root for a file stat; absolute paths pass through.
func (e *Engine) abs(p string) string {
	return absPath(e.root, p)
}

// inFolder reports whether the path sits under the account's folder
// space in one of the candidates (a candidate, its subfolders, or a
// glob on the first segment - afew '*' globs, R2).
func inFolder(paths []string, folder string, candidates []string) bool {
	for _, p := range paths {
		rel, ok := strings.CutPrefix(p, folder+"/")
		if !ok {
			continue
		}
		for _, c := range candidates {
			if rel == c || strings.HasPrefix(rel, c+"/") {
				return true
			}
			if strings.ContainsAny(c, "*?[") {
				if ok, _ := path.Match(c, strings.SplitN(rel, "/", 2)[0]); ok {
					return true
				}
			}
		}
	}
	return false
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}
