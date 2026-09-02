// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
	"notmutt/notmuch"
)

// attachmentFolder resolves the [attachments] folder, expanding a
// leading ~ at use (a config edit or HOME change applies on the next
// pass).
func attachmentFolder(cfg config.Config) string {
	f := cfg.Attachments.Folder
	if f == "" {
		f = config.DefaultAttachFolder
	}
	if strings.HasPrefix(f, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, f[2:])
		}
	}
	return f
}

// AttachMeta is the message projection the categorize hooks see: the
// sender (raw From header and its lowercase address domain), subject,
// and unix date. Never paths, ids, or content (the plugin boundary's
// data policy).
type AttachMeta struct {
	From    string
	Subject string
	Date    int64
	Domain  string
}

// CategorizeHook decides one message's attachment destinations: the
// mail handle (the message's parsed attachment list), the metadata
// projection, and a map of 1-based attachment ordinal to a relative
// path - a bare category (slotted into the config layout) or a full
// folder/filename path (used verbatim). Attachments without an entry
// are skipped. The Lua layer registers its adapter here (the R2 filter
// interface shape).
type CategorizeHook func(handle string, m AttachMeta) (map[int]string, error)

var categorizeHooks []CategorizeHook

// RegisterCategorizeHook registers an attachment-category hook.
func RegisterCategorizeHook(fn CategorizeHook) {
	categorizeHooks = append(categorizeHooks, fn)
}

// The handle registry: the save pass registers each message's parsed
// attachment list under an opaque handle before invoking the hooks and
// unregisters after. The sandboxed plugin cannot open files - the list
// is what the client parsed (get_attachments reads this table).
var (
	attListMu sync.Mutex
	attLists  = map[string][]mail.Attachment{}
	attSeq    atomic.Uint64
)

func registerAttachments(atts []mail.Attachment) string {
	h := fmt.Sprintf("att-%d", attSeq.Add(1))
	attListMu.Lock()
	attLists[h] = atts
	attListMu.Unlock()
	return h
}

// attachmentsForHandle returns the handle's attachment list; ok is
// false for an unknown (or already-unregistered) handle.
func attachmentsForHandle(handle string) ([]mail.Attachment, bool) {
	attListMu.Lock()
	defer attListMu.Unlock()
	a, ok := attLists[handle]
	return a, ok
}

func unregisterAttachments(handle string) {
	attListMu.Lock()
	delete(attLists, handle)
	attListMu.Unlock()
}

// runCategorize asks the hooks in registration order; the first hook
// with a non-empty category map wins. A hook error falls through, and
// when nothing categorized the last error surfaces - an undecidable
// message is a review-surface entry, never a silent skip.
func runCategorize(handle string, m AttachMeta) (map[int]string, error) {
	var lastErr error
	for _, h := range categorizeHooks {
		cats, err := h(handle, m)
		if err != nil {
			lastErr = err
			continue
		}
		if len(cats) > 0 {
			return cats, nil
		}
	}
	return nil, lastErr
}

// sanitizeSegment makes a mail- or plugin-derived name safe as a
// single path segment: separators become underscores, control runes
// dropped (the SanitizeControls rule), and a name collapsing to "",
// "." or ".." is rejected.
func sanitizeSegment(s string) string {
	s = core.SanitizeControls(strings.ReplaceAll(strings.ReplaceAll(s, "/", "_"), "\\", "_"))
	if s == "" || s == "." || s == ".." {
		return ""
	}
	return s
}

// attachmentTarget resolves one save's target from the hook's relative
// path. A multi-segment value (travel/flights/london.pdf) is the full
// destination below the [attachments] folder - the plugin owns the
// structure, the date layout is bypassed. A single segment (travel) is
// a legacy category slotted into the config layout
// (<YYYY-MM>/<category>/<filename>). Every segment passes the
// sanitizer; an empty or collapsed path means nothing to save.
func attachmentTarget(folder, layout string, meta AttachMeta, rel, name string) string {
	var segs []string
	for _, p := range strings.Split(rel, "/") {
		if p = sanitizeSegment(p); p != "" {
			segs = append(segs, p)
		}
	}
	if len(segs) == 0 {
		return ""
	}
	if len(segs) == 1 {
		file := sanitizeSegment(name)
		if file == "" {
			return ""
		}
		segs = append(layoutDir(layout, meta.Date), segs[0], file)
	}
	return filepath.Join(append([]string{folder}, segs...)...)
}

// dateLayout translates a YYYY/MM/DD token pattern into a Go time
// layout (the user-facing pattern, not Go's reference layout). Shared
// by the [attachments] config and the date_str Lua binding.
func dateLayout(pattern string) string {
	return strings.NewReplacer("YYYY", "2006", "MM", "01", "DD", "02").Replace(pattern)
}

// layoutDir renders the [attachments] layout date pattern into path
// segments: YYYY/MM/DD map to the year, month, and day, everything else
// literal. Empty layout = no date directory.
func layoutDir(layout string, ts int64) []string {
	if layout == "" {
		return nil
	}
	return strings.Split(time.Unix(ts, 0).UTC().Format(dateLayout(layout)), "/")
}

// AttachSave is one attachment's pass outcome - the review surface of
// the diag lines and the command output.
type AttachSave struct {
	Category string
	Name     string
	Target   string
	Exists   bool // target already on disk; skipped (idempotency)
	Err      error
}

// saveMessageAttachments runs the categorize hooks over one message
// file and saves each categorized attachment (0600 files, 0700 dirs,
// F5). Idempotent: an existing target is skipped, never overwritten -
// the skip check precedes the extract, so re-runs never re-read
// already-saved attachments. Dry-run reports the plan without writing.
// Failures are recorded per attachment, never aborting the message or
// the pass.
func saveMessageAttachments(file string, meta AttachMeta, folder, layout string, dryRun bool) []AttachSave {
	msg, err := mail.ParseMessage(file)
	if err != nil {
		return []AttachSave{{Name: file, Err: err}}
	}
	handle := registerAttachments(msg.Attachments)
	defer unregisterAttachments(handle)
	cats, herr := runCategorize(handle, meta)
	if herr != nil {
		return []AttachSave{{Name: file, Err: herr}}
	}
	ordinals := make([]int, 0, len(cats))
	for o := range cats {
		ordinals = append(ordinals, o)
	}
	sort.Ints(ordinals)
	var saves []AttachSave
	for _, o := range ordinals {
		rel := cats[o]
		if o < 1 || o > len(msg.Attachments) {
			saves = append(saves, AttachSave{Name: fmt.Sprintf("attachment %d", o), Category: rel,
				Err: fmt.Errorf("ordinal %d out of range 1..%d", o, len(msg.Attachments))})
			continue
		}
		att := msg.Attachments[o-1]
		target := attachmentTarget(folder, layout, meta, rel, att.Name)
		if target == "" {
			saves = append(saves, AttachSave{Category: rel, Name: att.Name, Err: fmt.Errorf("unsafe name %q", att.Name)})
			continue
		}
		s := AttachSave{Category: rel, Name: att.Name, Target: target}
		if _, err := os.Stat(target); err == nil {
			s.Exists = true
			saves = append(saves, s)
			continue
		}
		if dryRun {
			saves = append(saves, s)
			continue
		}
		_, _, data, err := mail.ExtractAttachment(file, o-1)
		if err != nil {
			s.Err = err
			saves = append(saves, s)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			s.Err = err
			saves = append(saves, s)
			continue
		}
		if err := os.WriteFile(target, data, 0o600); err != nil {
			s.Err = err
			saves = append(saves, s)
			continue
		}
		saves = append(saves, s)
	}
	return saves
}

// parseAttachmentsSpec reads the attachments flags: --dry-run (report
// the plan, write nothing) and one optional query (default "*").
func parseAttachmentsSpec(args []string) (dryRun bool, query string, err error) {
	fs := flag.NewFlagSet("attachments", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&dryRun, "dry-run", false, "report the plan, write nothing")
	if err := fs.Parse(args); err != nil {
		return false, "", err
	}
	if fs.NArg() > 1 {
		return false, "", errors.New("attachments: at most one query")
	}
	query = "*"
	if fs.NArg() == 1 {
		query = fs.Arg(0)
	}
	return dryRun, query, nil
}

// attachmentsOnce is the headless backfill (`notmutt attachments
// [--dry-run] [query]`): categorize and download the query's message
// attachments into the [attachments] folder. The Lua plugins must load
// for the hooks to exist - a build without a categorize plugin saves
// nothing and says so.
func attachmentsOnce() error {
	dryRun, query, err := parseAttachmentsSpec(os.Args[2:])
	if err != nil {
		return err
	}
	cfg, err := config.Load(configDir())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	loadLuaPlugins(filepath.Join(configDir(), "lua"), cfg.Lua.Network)
	if len(categorizeHooks) == 0 {
		return errors.New("attachments: no categorize hook - install a Lua plugin declaring a categorize function")
	}
	bus := core.NewBus()
	setLuaLogBus(bus)
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	root, err := mailRoot()
	if err != nil {
		return fmt.Errorf("attachments: mail root: %w", err)
	}
	saved, skipped, err := runAttachmentBackfill(worker, root, attachmentFolder(cfg), cfg.Attachments.Layout, query, dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("attachments: %d saved, %d skipped (dry-run=%v)\n", saved, skipped, dryRun)
	return nil
}

// absMailPath resolves a message path against the mail root; absolute
// paths pass through (snapshot paths are root-relative as notmuch
// reports them).
func absMailPath(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// attachmentPass is the shared pass body (the headless command and the
// hotkey action): the query's message ids, their snapshots (paths), and
// saveMessageAttachments per message. Returns one save/skip line per
// attachment plus the tallies; the callers decide the sink.
func attachmentPass(worker workerAPI, root, folder, layout, query string, dryRun bool) (lines []string, saved, skipped int, err error) {
	var ids []string
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQueryMsgs, Query: query,
		Emit: func(chunk []core.Message) bool {
			for i := range chunk {
				ids = append(ids, chunk[i].ID)
			}
			return true
		}}); err != nil || rpl.Err != nil {
		return nil, 0, 0, fmt.Errorf("attachments: query: %v %v", err, rpl.Err)
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: ids})
	if err != nil || rpl.Err != nil {
		return nil, 0, 0, fmt.Errorf("attachments: snapshots: %v %v", err, rpl.Err)
	}
	for i := range rpl.Msgs {
		m := rpl.Msgs[i]
		meta := AttachMeta{From: m.Author, Subject: m.Subject, Date: m.Timestamp, Domain: senderDomain(m.Author)}
		for _, p := range m.Paths {
			for _, s := range saveMessageAttachments(absMailPath(root, p), meta, folder, layout, dryRun) {
				if s.Err != nil {
					// a stale path (an external maildir rename between notmuch
					// new runs) must not abort the pass - the line is the
					// review surface
					lines = append(lines, fmt.Sprintf("skip %s (%v)", s.Name, s.Err))
					continue
				}
				if s.Exists {
					skipped++
					lines = append(lines, fmt.Sprintf("skip %s (exists)", s.Target))
				} else {
					saved++
					lines = append(lines, fmt.Sprintf("save %s", s.Target))
				}
			}
		}
	}
	return lines, saved, skipped, nil
}

// runAttachmentBackfill is the headless command body: attachmentPass
// with the lines printed (the command's review surface).
func runAttachmentBackfill(worker workerAPI, root, folder, layout, query string, dryRun bool) (saved, skipped int, err error) {
	lines, saved, skipped, err := attachmentPass(worker, root, folder, layout, query, dryRun)
	for _, l := range lines {
		fmt.Println(l)
	}
	return saved, skipped, err
}

// categorizeThread is the hotkey pass (the index categorize action):
// the cursor thread's messages, attachmentPass over the thread, the
// lines and tallies published as CategorizeResult for the session log.
// The cursor may carry a message id (a flat search tab): thread:<tid>
// matches nothing for it, so the id branch resolves it - the threaded
// case is unchanged (the id term adds nothing).
func categorizeThread(worker workerAPI, bus *core.Bus, threadID string, cfg *config.Config) {
	res := core.CategorizeResult{ThreadID: threadID}
	q := fmt.Sprintf("(thread:%q) or (id:%q)", threadID, threadID)
	lines, saved, skipped, err := attachmentPass(worker, "", attachmentFolder(*cfg), cfg.Attachments.Layout, q, false)
	if err != nil {
		res.Err = err
		bus.Publish(res)
		return
	}
	res.Lines, res.Saved, res.Skipped = lines, saved, skipped
	bus.Publish(res)
}
