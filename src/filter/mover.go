package filter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"notmutt/config"
	"notmutt/notmuch"
)

// Mover is the per-account mail mover (the afew MailMover as native
// logic, R2): hard-tagged mail physically moves into the folder of its
// move tag, resolved only within the account's folder space. The
// reference's stale-handle workaround (MailMover.py:214-220) does not
// exist here - the mover updates the database through the client's own
// notmuch layer, and the revision bump reaches the next refresh cycle.
type Mover struct {
	worker Worker
	cfg    config.Config
	root   string
	dryRun bool
	// Progress, when set, reports each processed entry (R15 batch
	// boundary: the mover's per-message loop).
	Progress func(done, total int)
}

func NewMover(w Worker, cfg config.Config, root string) *Mover {
	return &Mover{worker: w, cfg: cfg, root: root, dryRun: cfg.Filter.DryRun}
}

// MoveEntry is one file's move outcome: To empty means the file stays
// and Skip names why.
type MoveEntry struct {
	ID   string // the message id
	From string
	To   string
	Skip string
}

// MoveReport is the run's outcome; dry-run writes nothing and the
// entries ARE the review surface (what-would-move per file).
type MoveReport struct {
	Moves []MoveEntry
}

// Move moves each report entry's files into the folder of its move tag
// (the report entries with a Folder set, computed by the engine).
// Copy-then-delete: every copy lands before any source goes; the
// database sees AddPaths before RemovePaths, so the message keeps its
// tags through the move (the binding's duplicate-id status on re-add is
// success - the mover's exact add-first case).
func (m *Mover) Move(rep *Report) (*MoveReport, error) {
	out := &MoveReport{}
	targets, managed := m.resolveAccounts(rep)
	var toAdd, toRemove []string
	for i, e := range rep.Entries {
		if m.Progress != nil {
			m.Progress(i+1, len(rep.Entries))
		}
		if e.Folder == "" {
			continue
		}
		target := targets[e.Account][e.Folder]
		if target == "" {
			continue
		}
		for _, f := range e.Paths {
			me := MoveEntry{ID: e.ID, From: f}
			rel := relPath(m.root, f)
			dst := filepath.Join(target, filepath.Base(filepath.Dir(rel)), filepath.Base(rel))
			if _, err := os.Stat(absPath(m.root, f)); err != nil {
				me.Skip = "source gone"
			} else {
				srcMaildir := filepath.Dir(filepath.Dir(rel))
				switch {
				case !managedTree(managed[e.Account], srcMaildir):
					me.Skip = "not managed"
				case sameTree(srcMaildir, filepath.Dir(filepath.Dir(dst))):
					me.Skip = "already home"
				case exists(absPath(m.root, dst)):
					me.Skip = "dest exists"
				default:
					me.To = dst
				}
			}
			out.Moves = append(out.Moves, me)
			if me.To != "" && !m.dryRun {
				if err := copyFile(absPath(m.root, f), absPath(m.root, dst)); err != nil {
					return nil, fmt.Errorf("mover: copy %s: %w", f, err)
				}
				toAdd = append(toAdd, absPath(m.root, dst))
				toRemove = append(toRemove, absPath(m.root, f))
			}
		}
	}
	if !m.dryRun && len(toRemove) > 0 {
		// copy-then-delete: every copy landed (the loop either completed
		// or returned on an error); only now do the sources go. The
		// backend's RemovePaths only drops the index link - the file
		// itself is the mover's.
		for _, p := range toRemove {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("mover: remove %s: %w", p, err)
			}
		}
	}
	if !m.dryRun && len(toAdd) > 0 {
		// add-first keeps the tags: the new filename lands before the
		// sources go. A backend without path ops (the cli) is a silent
		// no-op - its own `notmuch new` reconciles the move one poll
		// later.
		if rpl, err := m.worker.Call(notmuch.Action{Kind: notmuch.ActAddPaths, Paths: toAdd}); err != nil || rpl.Err != nil {
			if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
				return nil, fmt.Errorf("mover: add: %v %v", err, rpl.Err)
			}
			return out, nil
		}
		if rpl, err := m.worker.Call(notmuch.Action{Kind: notmuch.ActRemovePaths, Paths: toRemove}); err != nil || rpl.Err != nil {
			if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
				return nil, fmt.Errorf("mover: remove: %v %v", err, rpl.Err)
			}
		}
	}
	return out, nil
}

// resolveAccounts computes the per-account move targets and managed
// folder sets once per run: the folder state changes between polls
// (mbsync creates folders), so the resolution never caches across runs.
// The managed set is the inbox tree plus every resolved target tree -
// mail the user filed in organizational folders is left where it is
// (the reference gmail/* wildcard expansion). The inbox tree resolves
// through the same candidate machinery as the untag gate (candidates,
// R2); the bare INBOX convention is the fallback where an account has
// no inbox candidates at all.
func (m *Mover) resolveAccounts(rep *Report) (map[string]map[string]string, map[string][]string) {
	targets := map[string]map[string]string{}
	managed := map[string][]string{}
	for _, e := range rep.Entries {
		if e.Folder == "" {
			continue
		}
		if _, ok := targets[e.Account]; ok {
			continue
		}
		a, ok := m.cfg.Accounts[e.Account]
		if !ok {
			continue
		}
		fs := a.Tag(e.Account)
		ts := map[string]string{}
		var trees []string
		if cs := candidates(a, "inbox"); len(cs) > 0 {
			trees = append(trees, m.resolveTarget(fs, cs))
		} else {
			trees = []string{filepath.Join(fs, "INBOX")}
		}
		for tag, cs := range candidateTags(a) {
			ts[tag] = m.resolveTarget(fs, cs)
			trees = append(trees, ts[tag])
		}
		targets[e.Account] = ts
		managed[e.Account] = trees
	}
	return targets, managed
}

// candidateTags is the account's hard tags with folder candidates:
// the union of the moves, preset, and detected-folder keys, each
// resolved through candidates() - the moves > preset > folders
// precedence lives there once.
func candidateTags(a config.Account) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(tag string) {
		if seen[tag] {
			return
		}
		seen[tag] = true
		if cs := candidates(a, tag); len(cs) > 0 {
			out[tag] = cs
		}
	}
	for tag := range a.Moves {
		add(tag)
	}
	if a.Preset != "" {
		for tag := range config.Presets[a.Preset] {
			add(tag)
		}
	}
	for tag, f := range a.Folders {
		if f != "" {
			add(tag)
		}
	}
	return out
}

// resolveTarget is the reference _resolve_account_folder: the first
// candidate that exists on disk wins ('*' candidates are globs); none
// existing falls back to the first candidate - the sync tool creates
// the folder.
func (m *Mover) resolveTarget(folderSpace string, cs []string) string {
	base := filepath.Join(m.root, folderSpace)
	for _, c := range cs {
		if strings.ContainsAny(c, "*?[") {
			matches, err := filepath.Glob(filepath.Join(base, c))
			if err == nil {
				sort.Strings(matches)
				for _, match := range matches {
					if isDir(match) {
						return filepath.Join(folderSpace, filepath.Base(match))
					}
				}
			}
			continue
		}
		if isDir(filepath.Join(base, c)) {
			return filepath.Join(folderSpace, c)
		}
	}
	return filepath.Join(folderSpace, cs[0])
}

// managedTree reports whether the source maildir is one the mover
// manages for the account: INBOX or a resolved target tree, or a
// subfolder of either.
func managedTree(trees []string, maildir string) bool {
	for _, t := range trees {
		if maildir == t || strings.HasPrefix(maildir, t+"/") {
			return true
		}
	}
	return false
}

// sameTree is the reference _same_maildir_tree: the source already
// lives in the destination tree (the folder or a subfolder) - this
// keeps prefix folder queries from pulling mail out of organizational
// subfolders into the folder root.
func sameTree(srcMaildir, dstMaildir string) bool {
	return srcMaildir == dstMaildir || strings.HasPrefix(srcMaildir, dstMaildir+"/")
}

// copyFile is shutil.copy2: content, mode, and mtime. The moved file
// keeps its mtime - the delivery-gate checks (untag-reversal) compare
// file times, so a move must not age a file. The destination dirs are
// created (the reference leaves folder creation to the sync tool; the
// mover is the client's own engine and a first move must not fail on a
// missing folder).
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
