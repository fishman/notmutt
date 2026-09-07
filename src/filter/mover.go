// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"notmutt/config"
	"notmutt/lib/xdg"
	"notmutt/notmuch"
)

// Mover is the per-account mail mover (the afew MailMover as native
// logic, R2): hard-tagged mail physically moves into the folder of
// its move tag, within the account's folder space. The reference's
// stale-handle workaround (MailMover.py:214-220) is absent: the mover
// updates the database through the client's own notmuch layer.
type Mover struct {
	worker Worker
	cfg    config.Config
	root   string
	dryRun bool
	// lockStrict marks the apply mover (NewMoverLive): it waits for a
	// contended flock up to the timeout and then errors. The poll mover
	// (NewMover) is best-effort: contended, it skips the batch.
	lockStrict bool
	// Progress, when set, reports each processed entry (R15 batch boundary: the per-message loop).
	Progress func(done, total int)
}

func newMover(w Worker, cfg config.Config, root string, dryRun bool) *Mover {
	return &Mover{worker: w, cfg: cfg, root: root, dryRun: dryRun}
}

func NewMover(w Worker, cfg config.Config, root string) *Mover {
	return newMover(w, cfg, root, cfg.Filter.DryRun)
}

// NewMoverLive is the apply path's mover: the staged apply is explicit
// intent, the filter dry-run gates the poll, not the $ key. It waits
// for a contended flock instead of skipping (lockStrict).
func NewMoverLive(w Worker, cfg config.Config, root string) *Mover {
	m := newMover(w, cfg, root, false)
	m.lockStrict = true
	return m
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
// entries ARE the review surface (what-would-move per file). Skip, when
// set, is a batch-level reason no files moved (the poll mover's
// contended-flock skip).
type MoveReport struct {
	Moves []MoveEntry
	Skip  string
}

// moverLockPath is the cross-process mover lock, beside the poll stamp
// (the same cache dir the app's classify floor uses): one file serializes
// every client's live mover, apply and poll alike.
func moverLockPath() string {
	if base := xdg.CacheHome(); base != "" {
		return filepath.Join(base, "notmutt", "mover.lock")
	}
	return "mover.lock"
}

const moverLockTick = 100 * time.Millisecond

// moverLockTimeout caps the apply mover's wait for a contended flock -
// the UI rule that a tag op never hangs behind a lock (notmuch's
// lock_timeout / the app's lockBudget). Overridden in tests.
var moverLockTimeout = 10 * time.Second

// lockLive takes the cross-process flock for a live Move. flock is
// chosen over a pidfile or create/delete lockfile because the kernel
// releases it the instant the holder's fd closes - on crash, SIGKILL,
// or panic - so a dead mover never leaves a stale lock and a blocked
// waiter wakes automatically. The lock file is opened fresh per call:
// separate fds contend even inside one process, so one file serializes
// the apply mover against the poll mover too. A fresh fd opened on the
// same file by this process is not reentrant, but a Move never nests
// inside a Move. Dry runs write nothing and skip the lock entirely.
func (m *Mover) lockLive(out *MoveReport) (unlock func(), err error) {
	if m.dryRun {
		return func() {}, nil
	}
	p := moverLockPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("mover: lock dir: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mover: lock: %w", err)
	}
	deadline := time.Now().Add(moverLockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { f.Close() }, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case !errors.Is(err, syscall.EWOULDBLOCK):
			f.Close()
			return nil, fmt.Errorf("mover: lock: %w", err)
		}
		if !m.lockStrict {
			// best effort (the poll mover): contended -> skip the batch;
			// the reconciling poll re-runs and whoever holds the lock is
			// classifying the same account - eventual.
			out.Skip = "mover lock held by another process"
			f.Close()
			return func() {}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("mover: lock held by another process (waited %s)", moverLockTimeout)
		}
		time.Sleep(moverLockTick)
	}
}

// Move moves each report entry's files into the folder of its move
// tag. Copy-then-delete: every copy lands before any source goes, so
// the database sees AddPaths before RemovePaths and the message keeps
// its tags (re-add of a duplicate id is success - the mover's exact
// add-first case). Live moves run under the cross-process flock
// (lockLive); a contended poll mover skips the batch, a contended apply
// mover waits up to the timeout and errors.
func (m *Mover) Move(rep *Report) (*MoveReport, error) {
	out := &MoveReport{}
	unlock, err := m.lockLive(out)
	if err != nil {
		return out, err
	}
	defer unlock()
	if out.Skip != "" {
		return out, nil
	}
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
		// home is a message-level rule: a file already in the target
		// tree means no copy moves. Moving the mbsync-owned delivered
		// copy breaks its UID bookkeeping (the next sync re-downloads it).
		home := false
		for _, g := range e.Paths {
			if sameTree(filepath.Dir(filepath.Dir(RelPath(m.root, g))), target) {
				home = true
				break
			}
		}
		for _, f := range e.Paths {
			me := MoveEntry{ID: e.ID, From: f}
			rel := RelPath(m.root, f)
			dst := filepath.Join(target, filepath.Base(filepath.Dir(rel)), stripUID(filepath.Base(rel)))
			if _, err := os.Stat(absPath(m.root, f)); err != nil {
				me.Skip = "source gone"
			} else {
				srcMaildir := filepath.Dir(filepath.Dir(rel))
				switch {
				case !managedTree(managed[e.Account], srcMaildir):
					me.Skip = "not managed"
				case home:
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
		// copy-then-delete: the sources go only after every copy
		// landed. RemovePaths drops only the index link - the file
		// itself is the mover's.
		for _, p := range toRemove {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("mover: remove %s: %w", p, err)
			}
		}
	}
	if !m.dryRun && len(toAdd) > 0 {
		// add-first keeps the tags: the new filename lands before the
		// sources go. A backend without path ops (the cli) no-ops
		// silently; its `notmuch new` reconciles the move next poll.
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
// folder sets once per run (never cached: folder state changes
// between polls). Managed = the inbox tree plus every resolved target
// tree - mail in organizational folders is left alone (reference
// gmail/* wildcard expansion). The inbox tree resolves through
// Candidates (R2), with bare INBOX as the no-candidates fallback.
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
		if !ok || a.ReadOnly {
			// readonly accounts: no targets here is the defense - the
			// engine drops them first, but nothing may move regardless.
			continue
		}
		fs := a.Tag(e.Account)
		ts := map[string]string{}
		var trees []string
		if cs := Candidates(a, "inbox"); len(cs) > 0 {
			trees = append(trees, ResolveFolder(m.root, fs, cs))
		} else {
			trees = []string{filepath.Join(fs, "INBOX")}
		}
		for tag, cs := range candidateTags(a) {
			ts[tag] = ResolveFolder(m.root, fs, cs)
			trees = append(trees, ts[tag])
		}
		targets[e.Account] = ts
		managed[e.Account] = trees
	}
	return targets, managed
}

// candidateTags is the account's hard tags with folder candidates:
// the union of moves, preset, and detected-folder keys (the moves >
// preset > folders precedence lives in Candidates()).
func candidateTags(a config.Account) map[string][]string {
	out := map[string][]string{}
	seen := map[string]bool{}
	add := func(tag string) {
		if seen[tag] {
			return
		}
		seen[tag] = true
		if cs := Candidates(a, tag); len(cs) > 0 {
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

// stripUID removes an mbsync UID marker (",U=NNN") from a maildir
// basename: moving a file with the UID intact collides with the
// destination's UID tracking ("duplicate UID 1234"). Detection is the
// marker's presence, never a config option (afew rename=auto,
// MailMover.py:43-46).
func stripUID(name string) string {
	i := strings.Index(name, ",U=")
	if i < 0 {
		return name
	}
	end := i + 3
	for end < len(name) && name[end] >= '0' && name[end] <= '9' {
		end++
	}
	return name[:i] + name[end:]
}

// ResolveFolder is the reference _resolve_account_folder: the first
// candidate that exists under root+folderSpace wins ('*' are globs);
// none existing falls back to the first - the sync tool creates the
// folder. Returns the account-relative path; the caller joins its root.
func ResolveFolder(root, folderSpace string, cs []string) string {
	base := filepath.Join(root, folderSpace)
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

// managedTree reports whether the maildir is managed for the account:
// INBOX or a resolved target tree (or a subfolder of either).
func managedTree(trees []string, maildir string) bool {
	for _, t := range trees {
		if maildir == t || strings.HasPrefix(maildir, t+"/") {
			return true
		}
	}
	return false
}

// sameTree is the reference _same_maildir_tree: the source already
// lives in the destination tree (folder or subfolder) - keeps prefix
// queries from pulling mail out of organizational subfolders.
func sameTree(srcMaildir, dstMaildir string) bool {
	return srcMaildir == dstMaildir || strings.HasPrefix(srcMaildir, dstMaildir+"/")
}

// copyFile is shutil.copy2: content, mode, and mtime. The mtime is
// kept because the untag-reversal delivery gate compares file times;
// destination dirs are created so a first move never fails on a
// missing folder. The copy lands in a dot-prefixed temp in the
// destination directory and renames over the final name: a concurrent
// notmuch new must never index a half-published destination (headers
// copy first - a truncated file still parses a Message-ID), and rename
// is atomic within a directory while notmuch ignores dotfiles.
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
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, fi.ModTime(), fi.ModTime()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
