// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// The frame invariant: every view renders its content between the tab
// bar (top) and the keyhint + status rows (bottom), and the frame is
// always exactly m.height lines - a taller or shorter frame loses the
// bottom chrome (pushFrame writes only screen-height rows) or leaves
// stale rows. Regression for the report "in the summary view the status
// bar and the hotkey list disappears".

import (
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
)

// TestSummaryFrameKeepsChrome pins the invariant on the AI summary view:
// opening a summary must render the full frame - tab bar, content,
// keyhint, status - like every other view.
func TestSummaryFrameKeepsChrome(t *testing.T) {
	m := model()
	m.mode = "pager"
	m.pager = newPager("t1", "", []core.Line{{Text: "the mail body", Kind: core.LineBody}})
	next, _ := m.Update(EventMsg{Event: core.AiStarted{JobID: "j1", ThreadID: "t1"}})
	m = next
	m, _ = m.Update(EventMsg{Event: core.AiChunk{JobID: "j1", Text: "Summary text"}})
	lines := strings.Split(stripANSI(m.View()), "\n")
	if len(lines) != m.height {
		t.Fatalf("summary frame = %d lines, want %d:\n%s", len(lines), m.height, strings.Join(lines, "\n"))
	}
	if strings.TrimSpace(lines[m.height-2]) == "" {
		t.Fatalf("keyhint row empty in the summary frame (row %d):\n%s", m.height-2, strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[m.height-1], "inbox") {
		t.Fatalf("status row missing in the summary frame (row %d):\n%s", m.height-1, strings.Join(lines, "\n"))
	}
}

// TestSummaryFrameKeepsChromeLoop drives the real loop with a streamed
// summary: the chrome must survive the stream paint (pushFrame writes
// only screen-height rows, so an over-tall frame clips the keyhint and
// status).
func TestSummaryFrameKeepsChromeLoop(t *testing.T) {
	const w, h = 80, 24
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
	})})
	bus := core.NewBus()
	st := config.NewStore(config.Default())
	m := New(view, bus.Subscribe(), testBindings(), testTagActions(), bus, st, config.Default().UI)
	s := newSim(t, w, h)
	quitCh := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runLoop(m, s, quitCh) }()
	defer func() { close(quitCh); <-done }()

	waitScreen(t, s, w, h, func(cs []fakeCell) bool { return strings.Contains(rowText(cs, w, 22), "$ apply") })
	bus.Publish(core.AiStarted{JobID: "j1", ThreadID: "t1"})
	bus.Publish(core.AiChunk{JobID: "j1", Text: "The streamed summary"})
	waitScreen(t, s, w, h, func(cs []fakeCell) bool {
		return strings.Contains(rowText(cs, w, 1), "The streamed summary")
	})
	time.Sleep(50 * time.Millisecond)
	cs := cellsOf(s)
	if got := rowText(cs, w, 22); strings.TrimSpace(got) == "" {
		t.Fatalf("keyhint row empty in the streamed summary frame")
	}
	if got := rowText(cs, w, 23); !strings.Contains(got, "inbox") {
		t.Fatalf("status row missing in the streamed summary frame: %q", got)
	}
	if got := rowText(cs, w, 0); !strings.HasPrefix(got, " inbox") {
		t.Fatalf("tab bar clobbered in the summary frame: %q", got)
	}
}
