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
	diag = slog.New(slog.NewTextHandler(f, nil))
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
