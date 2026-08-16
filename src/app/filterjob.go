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
	changed, rep, mr, err := runFilterPipeline(j.worker, cfg, j.root, func(done, total int) {
		j.bus.Publish(core.Progress{Job: "filter", View: j.view, Done: done, Total: total})
	})
	if err != nil {
		j.fail(err)
		return
	}
	if !changed {
		return
	}
	reportFilterDiag(rep, mr)
	moved, skipped := moveCounts(mr)
	j.bus.Publish(core.FilterDone{DryRun: rep.DryRun, Entries: len(rep.Entries), Moves: moved, Skips: skipped, Priority: prioritySubjects(cfg, rep)})
}

// moveCounts splits a move report into executed moves and skips -
// shared by the FilterDone event and the poll command's summary line.
func moveCounts(mr *filter.MoveReport) (moved, skipped int) {
	for _, m := range mr.Moves {
		if m.Skip != "" {
			skipped++
		} else {
			moved++
		}
	}
	return
}

// runFilterPipeline is the poll's classification body: capture the
// revision, run `notmuch new`, capture it again, and classify the
// (pre, cur] delta through the filter engine and the mover. Changed
// reports whether the revision moved - a poll on a quiet mailbox
// produces no classification pass. Shared by the filter job (bus
// progress + FilterDone) and the headless `notmutt poll` command.
func runFilterPipeline(worker workerAPI, cfg config.Config, root string, progress func(done, total int)) (bool, *filter.Report, *filter.MoveReport, error) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		return false, nil, nil, fmt.Errorf("revision: %v %v", err, rpl.Err)
	}
	pre := rpl.Rev
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew}); err != nil || rpl.Err != nil {
		// a backend without a New path (ErrUnsupported) is expected - the
		// filter then degrades to classifying external new runs
		if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
			return false, nil, nil, fmt.Errorf("new: %v %v", err, rpl.Err)
		}
	}
	rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		return false, nil, nil, fmt.Errorf("revision: %v %v", err, rpl.Err)
	}
	if rpl.Rev == pre {
		return false, nil, nil, nil // nothing new; no classification pass
	}
	rep, err := filter.New(worker, cfg, root).Run(pre, rpl.Rev)
	if err != nil {
		return true, nil, nil, err
	}
	mv := filter.NewMover(worker, cfg, root)
	mv.Progress = progress
	mr, err := mv.Move(rep)
	if err != nil {
		return true, rep, nil, err
	}
	return true, rep, mr, nil
}

// prioritySubjects caps the [notify] priority payload: the subjects of
// entries carrying a priority tag (F6: subjects only, never ids or
// bodies), at most max; max <= 0 disables the subjects, the count
// stays.
func prioritySubjects(cfg config.Config, rep *filter.Report) []string {
	if cfg.Notify.Max <= 0 {
		return nil
	}
	var out []string
	for _, e := range rep.Entries {
		if e.Priority && e.Subject != "" {
			out = append(out, e.Subject)
			if len(out) >= cfg.Notify.Max {
				break
			}
		}
	}
	return out
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

// mailRoot resolves the notmuch mail root (argv-only, F4 - never
// interpolated): setup detection and the filter job share it. The
// engine's file stats and the mover's copies need it; a failure
// disables the filter job - the client still works, the poll degrades
// to the refresher's plain new.
func mailRoot() (string, error) {
	out, err := exec.CommandContext(context.Background(), "notmuch", "config", "get", "database.path").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
