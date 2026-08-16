package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"notmutt/config"
	"notmutt/core"
	"notmutt/filter"
	"notmutt/notmuch"
)

// filterJob is the classification pipeline on the poll (R2): capture
// the revision, run `notmuch new`, capture it again, and classify the
// (pre, cur] delta through the filter engine and the mover. The
// running guard makes overlapping polls no-ops - a backfill takes
// minutes and the cadence must not pile jobs. ActNew's WorkerDone
// cycles the view; the engine's own tag bumps land in later cycles.
// Disabled by [filter] enabled=false (the poll then runs a plain
// `notmuch new` in the refresher).
type filterJob struct {
	bus     *core.Bus
	worker  workerAPI
	st      *config.Store
	root    string // the mail root; snapshot paths are relative to it
	view    string // the current view's name, for the progress bar
	mu      sync.Mutex
	running bool
}

func newFilterJob(bus *core.Bus, worker workerAPI, st *config.Store, root, view string) *filterJob {
	return &filterJob{bus: bus, worker: worker, st: st, root: root, view: view}
}

func (j *filterJob) run() {
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		return
	}
	j.running = true
	j.mu.Unlock()
	defer func() { j.mu.Lock(); j.running = false; j.mu.Unlock() }()

	cfg := j.st.Config()
	rpl, err := j.worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		diag.Warn("filter: revision", "err", fmt.Sprintf("%v %v", err, rpl.Err))
		return
	}
	pre := rpl.Rev
	if rpl, err := j.worker.Call(notmuch.Action{Kind: notmuch.ActNew}); err != nil || rpl.Err != nil {
		// a backend without a New path (ErrUnsupported) is expected - the
		// filter then degrades to classifying external new runs
		if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
			diag.Warn("filter: new", "err", fmt.Sprintf("%v %v", err, rpl.Err))
			return
		}
	}
	rpl, err = j.worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		diag.Warn("filter: revision", "err", fmt.Sprintf("%v %v", err, rpl.Err))
		return
	}
	if rpl.Rev == pre {
		return // nothing new; no classification pass
	}
	rep, err := filter.New(j.worker, cfg, j.root).Run(pre, rpl.Rev)
	if err != nil {
		j.fail(err)
		return
	}
	mv := filter.NewMover(j.worker, cfg, j.root)
	mv.Progress = func(done, total int) {
		j.bus.Publish(core.Progress{Job: "filter", View: j.view, Done: done, Total: total})
	}
	mr, err := mv.Move(rep)
	if err != nil {
		j.fail(err)
		return
	}
	reportFilterDiag(rep, mr)
	moved, skipped := 0, 0
	for _, m := range mr.Moves {
		if m.Skip != "" {
			skipped++
		} else {
			moved++
		}
	}
	j.bus.Publish(core.FilterDone{DryRun: rep.DryRun, Entries: len(rep.Entries), Moves: moved, Skips: skipped})
}

func (j *filterJob) fail(err error) {
	j.bus.Publish(core.JobError{Job: "filter", Err: err})
	diag.Error("filter", "err", err.Error())
}

// filterReportLines caps the per-file report lines on diag: a backfill
// moves thousands of files and the log must not replay them all. The
// lines carry paths only, never message ids or headers (F6).
const filterReportLines = 100

func reportFilterDiag(rep *filter.Report, mr *filter.MoveReport) {
	if len(rep.Entries) == 0 {
		return
	}
	diag.Info("filter", "dry-run", rep.DryRun, "entries", len(rep.Entries), "moves", len(mr.Moves))
	n := 0
	for _, m := range mr.Moves {
		if m.Skip != "" {
			diag.Info("filter: skip", "reason", m.Skip, "from", m.From)
		} else {
			diag.Info("filter: move", "from", m.From, "to", m.To)
		}
		if n++; n >= filterReportLines {
			diag.Info("filter: more", "skipped", len(mr.Moves)-n)
			return
		}
	}
}

// mailRoot resolves the notmuch mail root once (argv-only, F4 - the
// setupAccounts pattern): the engine's file stats and the mover's
// copies need it. A failure disables the filter job - the client still
// works, the poll degrades to the refresher's plain new.
func mailRoot() (string, error) {
	out, err := exec.CommandContext(context.Background(), "notmuch", "config", "get", "database.path").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
