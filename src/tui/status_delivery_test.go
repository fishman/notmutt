// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// TestStatusTickDoesNotGateBusEvents is the regression for the report
// "opening messages is delayed by 5 seconds when i categorize
// attachments": the status auto-clear's 5s sleep was batched with the
// bus reader (batch(EventCmd, statusTickCmd)), holding the next bus
// event ~5s late. The clear fires on its own loop arm now, so a second
// status while one is showing must render in milliseconds, not ~5s.

import (
	"strings"
	"testing"

	"notmutt/config"
	"notmutt/core"
)

func TestStatusTickDoesNotGateBusEvents(t *testing.T) {
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

	status := func(want string) func([]fakeCell) bool {
		return func(cs []fakeCell) bool { return strings.Contains(rowText(cs, w, h-1), want) }
	}
	waitScreen(t, s, w, h, status("inbox")) // the initial frame's status row

	// a status message goes up (the categorize result shape) ...
	bus.Publish(core.LuaLog{Text: "first status"})
	waitScreen(t, s, w, h, status("first status"))

	// ... and the NEXT bus event must not wait behind its auto-clear:
	// waitScreen bounds the wait at ~1s, the bug held it ~5s
	bus.Publish(core.LuaLog{Text: "second status"})
	waitScreen(t, s, w, h, status("second status"))
}
