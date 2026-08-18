// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"notmutt/core"
)

// diag is the persistent diagnostic log (slog, stdlib): app-side
// errors and warnings that survive the session ring, appended to
// <cachedir>/notmutt/notmutt.log (0600, F5). F6 applies to the file as
// to everything else: no bodies, headers, or passphrases - the ring's
// screen-only entries ("sent to <addresses>") never reach it. The
// package default discards; Run() swaps in the file handler, so tests
// and callers get no log churn.
var diag = slog.New(slog.NewTextHandler(io.Discard, nil))

// diagLogMax caps the diagnostic log: at the cap the file rotates to
// notmutt.log.1 (one generation kept) - the file never grows past it.
const diagLogMax = 10 << 20 // 10 MiB

// openDiagLog points diag at the cache-dir file, creating the dir and
// appending to the file (0600). A file failure falls back to stderr -
// diagnostics never silently vanish.
func openDiagLog() {
	base, err := os.UserCacheDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "notmutt")
	if err := os.MkdirAll(dir, 0700); err != nil {
		diag = slog.New(slog.NewTextHandler(os.Stderr, nil))
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "notmutt.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		diag = slog.New(slog.NewTextHandler(os.Stderr, nil))
		return
	}
	diag = slog.New(slog.NewTextHandler(&cappedFile{f: f, path: filepath.Join(dir, "notmutt.log"), cap: diagLogMax}, nil))
}

// cappedFile is a size-capped log file: when a write would push the
// size past the cap, the file rotates to <path>.1 (one generation kept,
// overwriting the old one) and the write starts fresh. slog serializes
// handler calls, so the rotate/check never races.
type cappedFile struct {
	f    *os.File
	path string
	cap  int64
}

func (c *cappedFile) Write(p []byte) (int, error) {
	if c.f == nil {
		return 0, os.ErrClosed
	}
	st, err := c.f.Stat()
	if err != nil {
		return 0, err
	}
	if st.Size()+int64(len(p)) > c.cap {
		c.rotate()
	}
	return c.f.Write(p)
}

func (c *cappedFile) rotate() {
	c.f.Close()
	if err := os.Rename(c.path, c.path+".1"); err != nil {
		os.Truncate(c.path, 0)
	}
	c.f, _ = os.OpenFile(c.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
}

// runDiagBus routes the diagnostic bus events into the file log: job
// failures and lock timeouts. The message is job kind and error text
// only (F6); the TUI ring stays the content-adjacent surface.
func runDiagBus(bus *core.Bus) {
	ch := bus.Subscribe()
	for e := range ch {
		switch e := e.(type) {
		case core.JobError:
			diag.Error("job failed", "job", e.Job, "err", e.Err.Error())
		case core.WorkerLockTimeout:
			diag.Warn("lock timeout", "kind", e.Kind)
		}
	}
}
