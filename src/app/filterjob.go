// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os/exec"
	"strings"
	"sync"

	"notmutt/config"
	"notmutt/core"
	"notmutt/filter"
)

// filterJob is the classification pipeline on the poll (R2): run
// `notmuch new` (the backend's wrapper returns the (pre, cur]
// bracket) and classify the delta through the filter engine and the
// mover. The
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
	j.bus.Publish(core.FilterDone{DryRun: rep.DryRun, Entries: len(rep.Entries), Moves: moved, Skips: skipped, Priority: notifyHeadlines(cfg, rep)})
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

// runFilterPipeline is the in-client poll body: the shared window
// capture of pollDiff with the bus progress callback, reported as a
// change boolean (the filter job's revision-stability gate).
func runFilterPipeline(worker workerAPI, cfg config.Config, root string, progress func(done, total int)) (bool, *filter.Report, *filter.MoveReport, error) {
	rep, mr, win, err := pollDiff(worker, cfg, root, pollSpec{}, progress)
	if err != nil || win == "" {
		return false, rep, mr, err
	}
	return true, rep, mr, nil
}

// classifyDelta runs the engine and the mover over the (pre, cur]
// lastmod bracket - the shared body of the fresh-capture poll and the
// fixed-window replay (the reproducibility harness reclassifies the
// SAME bracket, so the same rules must produce the same diff).
func classifyDelta(worker workerAPI, cfg config.Config, root string, pre, cur uint64, progress func(done, total int)) (*filter.Report, *filter.MoveReport, error) {
	rep, err := filter.New(worker, cfg, root).Run(pre, cur)
	if err != nil {
		return nil, nil, err
	}
	mv := filter.NewMover(worker, cfg, root)
	mv.Progress = progress
	mr, err := mv.Move(rep)
	if err != nil {
		return rep, nil, err
	}
	return rep, mr, nil
}

// notifyHeadlines builds the [notify] summary payload: the headline
// rows (sender, subject, timestamp) of entries carrying a priority
// tag first, the rest of the batch filling the cap - the count line
// never ships alone (F6: no ids, no bodies). max <= 0 disables the
// rows, the count stays.
func notifyHeadlines(cfg config.Config, rep *filter.Report) []core.NotifyHeadline {
	if cfg.Notify.Max <= 0 {
		return nil
	}
	out := make([]core.NotifyHeadline, 0, cfg.Notify.Max)
	for _, pass := range []bool{true, false} {
		for _, e := range rep.Entries {
			if e.Priority == pass && e.Subject != "" {
				out = append(out, core.NotifyHeadline{Sender: e.Sender, Subject: e.Subject, Timestamp: e.Timestamp})
				if len(out) >= cfg.Notify.Max {
					return out
				}
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
	reportMoveDiag("filter", mr, filterReportLines)
}

// reportMoveDiag logs a MoveReport's per-file outcomes (paths only,
// never ids or headers, F6): the poll caps a backfill replay (cap > 0),
// the apply path logs every file. Shared by the filter run and the
// staged apply - a skipped move is an issue the user must see.
func reportMoveDiag(prefix string, mr *filter.MoveReport, cap int) {
	for i, m := range mr.Moves {
		if m.Skip != "" {
			diag.Info(prefix+": skip", "reason", m.Skip, "from", m.From)
		} else {
			diag.Info(prefix+": move", "from", m.From, "to", m.To)
		}
		if cap > 0 && i+1 >= cap {
			diag.Info(prefix+": more", "skipped", len(mr.Moves)-i-1)
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
