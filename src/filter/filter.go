// Package filter is the classification engine (R2): it turns the
// lastmod delta (the changed messages between two revisions) into tag
// ops through the declarative rule set. The muttrc post-new hook and
// afew are reference shapes, not backends - the engine is the pipeline.
package filter

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
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

// dropped are the folder tags a fresh INBOX delivery sheds (the
// muttrc/bin/untag-reversal DROPPED set): an external client moved the
// mail into INBOX, the tags of its old folder would move it right back.
var dropped = []string{"spam", "deleted", "archive", "pending"}

// Engine classifies the delta. New resolves the account rules once;
// Run evaluates one revision bracket per poll.
type Engine struct {
	worker Worker
	cfg    config.Config
	groups []core.TagGroup
	accs   []accountRule
	root   string // the mail root, for file stats (paths are relative to it)
}

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
	for name, a := range cfg.Accounts {
		ar := accountRule{name: name, acc: a, folder: a.Tag(name), rules: map[string][]string{}}
		for _, g := range e.groups {
			for _, tag := range g.Tags {
				if tag == "inbox" {
					ar.inbox = candidates(a, tag)
					continue
				}
				if cs := candidates(a, tag); len(cs) > 0 {
					ar.rules[tag] = cs
				}
			}
		}
		e.accs = append(e.accs, ar)
	}
	return e
}

// candidates resolves a hard tag's folder candidates for an account:
// the per-account moves override, else the preset, else the detected
// folders map (afew folder_priorities as data, R2).
func candidates(a config.Account, tag string) []string {
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
	ID       string
	Subject  string
	Priority bool // carries a [notify] priority tag after classification
	Account  string
	Folder   string       // the resolved folder-group winner with move candidates; empty = no move
	Paths    []string     // the message's files, as notmuch reported them
	Ops      []core.TagOp // the fully resolved op set (adds + exclusive-group removals)
}

// Report is the run's outcome: dry-run writes nothing and the entries
// ARE the review surface; a live run applies them and reports the same.
type Report struct {
	DryRun  bool
	Entries []Entry
}

// Run classifies the (pre, cur] lastmod delta: the changed message ids
// (QueryMsgs), their snapshots (tags + paths), and the rule set -
// account tags, folder rules, header rules, and the delivery-gated
// untag-reversal - into per-message resolved ops, one ActTag per
// message (id:<id>). Dry-run (the config [filter] dry-run) computes the
// report and writes nothing.
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
	mark := markerMtime()
	rep := &Report{DryRun: e.cfg.Filter.DryRun}
	for i := range rpl.Msgs {
		entry := e.classify(rpl.Msgs[i], hits, mark)
		if len(entry.Ops) == 0 {
			continue
		}
		rep.Entries = append(rep.Entries, entry)
	}
	if rep.DryRun || len(rep.Entries) == 0 {
		return rep, nil
	}
	for _, entry := range rep.Entries {
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

// idChunk bounds one header-rule OR query: the delta ids join an OR
// query, and notmuch chokes on unbounded queries (a mass change can
// touch thousands of messages at once).
const idChunk = 1000

// headerMatches evaluates the header rules against the delta: each
// rule's query scoped to a chunk of ids, the matched set per rule. The
// engine-enforced guard is the snapshot check in classify - the rule
// files carry no NOT guards of their own.
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
// (path prefix), folder rules (path match, guard = the tag is absent),
// header rules (delta scope, guard = absent), the delivery-gated
// untag-reversal, then the exclusive-group resolution (R2: applying any
// member removes the other members present, inbox included).
func (e *Engine) classify(m core.Message, hits []map[string]bool, mark int64) Entry {
	var ops []core.TagOp
	paths := e.norm(m.Paths)
	ar := e.accountOf(paths)
	if ar != nil {
		if !hasTag(m.Tags, ar.folder) {
			ops = append(ops, core.TagOp{Tag: ar.folder, Add: true})
		}
		// folder rules in reverse group order: the last member-add wins,
		// so a message with files in several folders resolves to the
		// reference priority (archive > deleted > sent > draft > pending
		// > spam).
		for gi := len(e.groups) - 1; gi >= 0; gi-- {
			for ti := len(e.groups[gi].Tags) - 1; ti >= 0; ti-- {
				tag := e.groups[gi].Tags[ti]
				if tag == "inbox" || hasTag(m.Tags, tag) {
					continue
				}
				if cs := ar.rules[tag]; len(cs) > 0 && inFolder(paths, ar.folder, cs) {
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
	if ar != nil && mark != 0 && inFolder(paths, ar.folder, ar.inbox) && freshInbox(paths, ar.folder, ar.inbox, e.abs, mark) {
		var drops []core.TagOp
		for _, t := range dropped {
			if hasTag(m.Tags, t) {
				drops = append(drops, core.TagOp{Tag: t})
			}
		}
		if len(drops) > 0 {
			ops = append(ops, drops...)
			ops = append(ops, core.TagOp{Tag: "inbox", Add: true})
		}
	}
	if len(ops) == 0 {
		return Entry{}
	}
	final, resolved := core.ResolveOps(m.Tags, ops, e.groups)
	prio := false
	if len(e.cfg.Notify.Priority) > 0 {
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
	acc := ""
	folder := ""
	if ar != nil {
		acc = ar.name
		// the move tag: the resolved winner among the folder tags with
		// candidates (inbox has no rule and never moves - the untag
		// delivers it, the mover would no-op).
		for _, op := range resolved {
			if op.Add {
				if _, ok := ar.rules[op.Tag]; ok {
					folder = op.Tag
					break
				}
			}
		}
	}
	return Entry{ID: m.ID, Subject: m.Subject, Priority: prio, Account: acc, Folder: folder, Paths: m.Paths, Ops: resolved}
}

// relPath strips the mail root prefix: the path rules match relative
// paths (notmuch reports them relative to the database path; absolute
// only for files outside it). The root join leaves a leading slash
// that would break the folder prefix match.
func relPath(root, p string) string {
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
		out[i] = relPath(e.root, p)
	}
	return out
}

// accountOf resolves the message's account by path: the first path
// under an account's folder space wins (the reference folder:/^<acc>\//
// pattern).
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

// abs joins the root for a file stat; absolute paths pass through.
func (e *Engine) abs(p string) string {
	return absPath(e.root, p)
}

// inFolder reports whether the path sits under the account's folder
// space in one of the candidates: the candidate matches a subfolder of
// itself, and a glob candidate matches the first folder segment (afew
// '*' globs, R2).
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

// freshInbox gates the untag-reversal on delivery: the reference
// touches only files mbsync just delivered (mtime >= the sync marker),
// so mutt-tagged old mail keeps its pending d/y/a/p tags.
func freshInbox(paths []string, folder string, inbox []string, abs func(string) string, mark int64) bool {
	for _, p := range paths {
		if !inFolder([]string{p}, folder, inbox) {
			continue
		}
		fi, err := os.Stat(abs(p))
		if err == nil && fi.ModTime().Unix() >= mark {
			return true
		}
	}
	return false
}

// markerMtime is the sync marker's mtime; 0 = no marker (the untag is
// off - the reference returns early without one).
func markerMtime() int64 {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	fi, err := os.Stat(filepath.Join(home, ".cache", "mail-sync-mark"))
	if err != nil {
		return 0
	}
	return fi.ModTime().Unix()
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}
