// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// Frame-chrome regression: drive the real loop on the simulation
// screen; the chrome (tab bar, keyhint, status line) must survive
// cursor moves and a refresh that shrinks the list. The loop writes
// the full frame every paint (tcell diffs internally), so a short
// list must leave blank rows - never stale rows from the previous
// paint.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"

	"notmutt/config"
	"notmutt/core"
)

func TestFrameChromeSurvivesRefresh(t *testing.T) {
	const w, h = 80, 24
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	var threads []*core.Thread
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("t%d", i)
		threads = append(threads, core.NewThread(id, []*core.Message{
			{ID: fmt.Sprintf("m%d", i), Timestamp: int64(i), Author: "Ann", Subject: "s", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(threads)
	bus := core.NewBus()
	st := config.NewStore(config.Default())
	m := New(view, bus.Subscribe(), testBindings(), testTagActions(), bus, st, config.Default().UI)
	s := newSim(t, w, h)
	quitCh := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runLoop(m, s, quitCh) }()
	defer func() { close(quitCh); <-done }()

	waitScreen(t, s, w, h, func(cs []fakeCell) bool { return strings.Contains(rowText(cs, w, 22), "$ apply") })
	for i := 0; i < 6; i++ {
		s.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
		time.Sleep(15 * time.Millisecond) // each deferred paint lands on its frame tick
	}
	time.Sleep(300 * time.Millisecond) // the legend settle
	view.MergeThreads(nil)
	view.MergeThreads(threads[:2])
	bus.Publish(core.ViewDiff{View: "inbox"})
	waitScreen(t, s, w, h, func(cs []fakeCell) bool {
		// stable shrink: rows 3-21 blank, chrome intact, status shows 2
		status := rowText(cs, w, h-1)
		for r := 3; r <= 21; r++ {
			if rowText(cs, w, r) != "" {
				return false
			}
		}
		return strings.Contains(status, "inbox") && strings.Contains(status, "2")
	})
	view.MergeThreads(threads)
	bus.Publish(core.ViewDiff{View: "inbox"})
	waitScreen(t, s, w, h, func(cs []fakeCell) bool { return rowText(cs, w, 21) != "" })

	cs := cellsOf(s)
	for r := 1; r <= 21; r++ {
		if rowText(cs, w, r) == "" {
			t.Fatalf("final frame: list row %d empty", r)
		}
	}
	if got := rowText(cs, w, 0); !strings.HasPrefix(got, " inbox") {
		t.Fatalf("final: tab bar clobbered: %q", got)
	}
	if got := rowText(cs, w, 22); !strings.Contains(got, "$ apply") {
		t.Fatalf("final: keyhint clobbered: %q", got)
	}
	if got := rowText(cs, w, 23); !strings.Contains(got, "inbox") {
		t.Fatalf("final: status clobbered: %q", got)
	}
}
