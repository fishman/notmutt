// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

// probeWorker counts ActThread calls: the full walk must make the
// refresh path need none.
type probeWorker struct {
	inner  workerAPI
	thread int
}

func (p *probeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	if a.Kind == notmuch.ActThread {
		p.thread++
	}
	return p.inner.Call(a)
}

// TestRealProbe is a manual probe against the real DB: NOTMUTT_REALPROBE=1.
// Exercises the session path - full reload + refresh cycle through the
// full-walk backend - and reports the two reported-missing threads'
// rows in the view at each step. Read-only.
func TestRealProbe(t *testing.T) {
	if os.Getenv("NOTMUTT_REALPROBE") == "" {
		t.Skip("set NOTMUTT_REALPROBE=1 to run against the real DB")
	}
	const (
		missing28 = "00000000000028e4"
		missing16 = "0000000000016fbf"
	)
	bus := core.NewBus()
	real := notmuch.NewWorker(bus, notmuch.NewCGO(), 10*time.Second)
	var worker workerAPI = &probeWorker{inner: real}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go real.Start(ctx)
	view := core.NewView("inbox", "tag:inbox")
	ref := newRefresher(bus, worker, view, 0)

	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""})
	fmt.Printf("open: err=%v rplerr=%v\n", err, rpl.Err)
	rpl, err = worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: "tag:inbox"})
	fmt.Printf("count: %d err=%v rplerr=%v\n", rpl.Count, err, rpl.Err)

	step := func(msg string) {
		t.Logf("== %s", msg)
	}

	step("full reload through the full walk")
	ch := bus.Subscribe()
	start := time.Now()
	ref.cycle()
	// the TUI's cadence: one flatten per ViewDiff event
	flattens := 0
	for {
		select {
		case e := <-ch:
			if _, ok := e.(core.ViewDiff); ok {
				view.Rows()
				flattens++
			}
		default:
			goto drained
		}
	}
drained:
	fmt.Printf("fullReload: %s with %d flatten passes\n", time.Since(start), flattens)
	report(view, "after reload")
	reportThreads(worker, view, missing28)
	reportThreads(worker, view, missing16)
	fmt.Printf("ActThread calls: %d (must be 0 on the refresh path)\n", worker.(*probeWorker).thread)

	step("second cycle: no-op (rev unchanged)")
	ref.cycle()
	report(view, "after second cycle")

	step("tree data: first 12 rows Depth/Count and the indicator chars")
	rows := view.Rows()
	for i, r := range rows[:min(12, len(rows))] {
		tree := ""
		if r.Msg != nil && r.Count > 1 && r.Depth == 0 {
			tree = "+ "
		}
		fmt.Printf("  row[%d] d=%d count=%d tree=%q\n", i, r.Depth, r.Count, tree)
	}

	step("row flags: the first 6 rows' tags and flag chars")
	rows = view.Rows()
	for i, r := range rows[:min(6, len(rows))] {
		tags := ""
		if r.Msg != nil {
			tags = strings.Join(r.Msg.Tags, ",")
		}
		chars := ""
		for _, t := range r.Msg.Tags {
			switch t {
			case "unread":
				chars += "N"
			case "replied":
				chars += "R"
			case "forwarded":
				chars += "F"
			case "deleted":
				chars += "D"
			}
		}
		fmt.Printf("  row[%d] ghost=%v tags=[%s] flagchars=[%s]\n", i, r.Msg == nil, tags, chars)
	}

	step("flat view: unread is a chronological list, every row N")
	uv := core.NewView("unread", "tag:unread")
	uv.SetThreaded(false)
	uref := newRefresher(bus, worker, uv, 0)
	start = time.Now()
	uref.cycle()
	rows = uv.Rows()
	unread, delflag := 0, 0
	for i, r := range rows {
		if r.Msg == nil {
			continue
		}
		if i < 3 {
			fmt.Printf("  flat row[%d]: id=%.20s tags=%v\n", i, r.Msg.ID, r.Msg.Tags)
		}
		if slices.Contains(r.Msg.Tags, "unread") {
			unread++
		}
		if slices.Contains(r.Msg.Tags, "deleted") {
			delflag++
		}
	}
	fmt.Printf("unread flat: threads=%d rows=%d unread-tagged=%d deleted-tagged=%d %s\n",
		len(uv.Threads), len(rows), unread, delflag, time.Since(start))
	fmt.Printf("  ActThread calls: %d (flat refresh must fetch nothing)\n", worker.(*probeWorker).thread)
}

// report prints the view's hydration state: the walk must leave no
// stub rows behind.
func report(view *core.View, label string) {
	hyd, stubs := 0, 0
	for _, th := range view.Threads {
		if view.Hydrated(th.ID) {
			hyd++
		} else {
			stubs++
		}
	}
	fmt.Printf("== %s: threads=%d hydrated=%d stubs=%d\n", label, len(view.Threads), hyd, stubs)
}

// reportThreads prints one missing thread's view rows vs its direct
// fetch: same message ids means the walk carried everything the fetch
// path used to.
func reportThreads(w workerAPI, view *core.View, tid string) {
	var rowIDs []string
	for _, r := range view.Rows() {
		if r.Msg != nil && r.ThreadID == tid {
			rowIDs = append(rowIDs, r.Msg.ID)
		}
	}
	rpl, _ := w.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
	fetchIDs := make([]string, len(rpl.Msgs))
	for i, m := range rpl.Msgs {
		fetchIDs[i] = m.ID
	}
	fmt.Printf("thread %s: view=%d fetch=%d ids-equal=%v\n",
		tid, len(rowIDs), len(fetchIDs), idsEqual(fetchIDs, rowIDs))
}

func idsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}
