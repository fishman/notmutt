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
	"strings"
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
// sender, subject, and unix date. Never paths, ids, or content (the
// plugin boundary's data policy).
type AttachMeta struct {
	From    string
	Subject string
	Date    int64
}

// CategorizeHook decides one attachment's category: message metadata
// plus the attachment's name/mime/size in, a category string or ""
// (skip) out. The Lua layer registers its adapter here (the R2 filter
// interface shape).
type CategorizeHook func(m AttachMeta, a core.Attachment) (string, error)

var categorizeHooks []CategorizeHook

// RegisterCategorizeHook registers an attachment-category hook.
func RegisterCategorizeHook(fn CategorizeHook) {
	categorizeHooks = append(categorizeHooks, fn)
}

// runCategorize asks the hooks in registration order; the first
// non-empty category wins. A hook error falls through to the next
// hook, and when nothing categorized, the last error surfaces - an
// undecidable attachment is a review-surface entry, never a silent
// skip.
func runCategorize(m AttachMeta, a core.Attachment) (string, error) {
	var lastErr error
	for _, h := range categorizeHooks {
		cat, err := h(m, a)
		if err != nil {
			lastErr = err
			continue
		}
		if cat != "" {
			return cat, nil
		}
	}
	return "", lastErr
}

// sanitizeSegment makes a mail- or plugin-derived name safe as a
// single path segment: separators become underscores, control runes
// are dropped (the SanitizeControls rule), and a name that collapses
// to "", "." or ".." is rejected. Shared by the filename and the
// category - after the separator replacement a segment cannot traverse.
func sanitizeSegment(s string) string {
	s = core.SanitizeControls(strings.ReplaceAll(strings.ReplaceAll(s, "/", "_"), "\\", "_"))
	if s == "" || s == "." || s == ".." {
		return ""
	}
	return s
}

// attachmentTarget resolves one save's target:
// <folder>/<YYYY-MM>/<category>/<filename> (the month from the
// message date, local time). Empty when the sanitizer dropped a
// segment (nothing to save).
func attachmentTarget(folder string, meta AttachMeta, category, name string) string {
	cat := sanitizeSegment(category)
	file := sanitizeSegment(name)
	if cat == "" || file == "" {
		return ""
	}
	return filepath.Join(folder, time.Unix(meta.Date, 0).Format("2006-01"), cat, file)
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
// file's attachments and saves each categorized one (0600 files, 0700
// dirs, F5). Idempotent: an existing target is skipped, never
// overwritten - the skip check precedes the extract, so re-runs never
// re-read already-saved attachments. Dry-run reports the plan without
// writing. Failures are recorded per attachment, never aborting the
// message or the pass.
func saveMessageAttachments(file string, meta AttachMeta, folder string, dryRun bool) []AttachSave {
	msg, err := mail.ParseMessage(file)
	if err != nil {
		return []AttachSave{{Name: file, Err: err}}
	}
	var saves []AttachSave
	for i, att := range msg.Attachments {
		cat, herr := runCategorize(meta, core.Attachment{Name: att.Name, MimeType: att.MimeType, Size: att.Size})
		if herr != nil {
			saves = append(saves, AttachSave{Name: att.Name, Err: herr})
			continue
		}
		if cat == "" {
			continue
		}
		target := attachmentTarget(folder, meta, cat, att.Name)
		if target == "" {
			saves = append(saves, AttachSave{Category: cat, Name: att.Name, Err: fmt.Errorf("unsafe name %q", att.Name)})
			continue
		}
		s := AttachSave{Category: cat, Name: att.Name, Target: target}
		if _, err := os.Stat(target); err == nil {
			s.Exists = true
			saves = append(saves, s)
			continue
		}
		if dryRun {
			saves = append(saves, s)
			continue
		}
		_, _, data, err := mail.ExtractAttachment(file, i)
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
// for the hooks to exist - a build or config without a categorize
// plugin saves nothing and says so.
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
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	saved, skipped, err := runAttachmentBackfill(worker, attachmentFolder(cfg), query, dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("attachments: %d saved, %d skipped (dry-run=%v)\n", saved, skipped, dryRun)
	return nil
}

// runAttachmentBackfill is the command body (shared with the tests):
// the query's message ids (ActQueryMsgs), their snapshots (paths), and
// saveMessageAttachments per message - the filter engine's two-step.
// Prints one save/skip line per attachment.
func runAttachmentBackfill(worker workerAPI, folder, query string, dryRun bool) (saved, skipped int, err error) {
	var ids []string
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQueryMsgs, Query: query,
		Emit: func(chunk []core.Message) bool {
			for i := range chunk {
				ids = append(ids, chunk[i].ID)
			}
			return true
		}}); err != nil || rpl.Err != nil {
		return 0, 0, fmt.Errorf("attachments: query: %v %v", err, rpl.Err)
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: ids})
	if err != nil || rpl.Err != nil {
		return 0, 0, fmt.Errorf("attachments: snapshots: %v %v", err, rpl.Err)
	}
	for i := range rpl.Msgs {
		m := rpl.Msgs[i]
		meta := AttachMeta{From: m.Author, Subject: m.Subject, Date: m.Timestamp}
		for _, p := range m.Paths {
			for _, s := range saveMessageAttachments(p, meta, folder, dryRun) {
				if s.Err != nil {
					return 0, 0, fmt.Errorf("attachments: %v", s.Err)
				}
				if s.Exists {
					skipped++
					fmt.Printf("skip %s (exists)\n", s.Target)
				} else {
					saved++
					fmt.Printf("save %s\n", s.Target)
				}
			}
		}
	}
	return saved, skipped, nil
}
