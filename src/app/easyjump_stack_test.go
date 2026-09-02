// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"

	"notmutt/config"
	"notmutt/core"
	"notmutt/tui"
)

// TestEasyjumpFullStack drives the real wiring end to end: open/render
// handlers on a real bus, the ActThread round trip, and the key
// sequence enter -> V -> F -> j -> 2 -> enter. Asserts the [N] labels
// render and survive the scroll, and digit entry still opens (2 stays
// an incomplete prefix of 20..29 with 40 links, so enter confirms it).
func TestEasyjumpFullStack(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	path := filepath.Join(t.TempDir(), "msg")
	var body strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&body, "<p>link <a href=\"https://example.com/%d\">%d</a></p>\n", i, i)
	}
	msg := "From: a@example.com\nTo: b@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: text/html; charset=utf-8\n\n" + body.String()
	if err := os.WriteFile(path, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}})

	tui.SetOpenHandler(func(req tui.OpenReq) {
		mode := req.Mode
		if mode == core.RenderAuto {
			// the fixture opens in plain; RenderAuto would resolve the
			// domain map and html-only upgrade to HTML, flipping the
			// enter -> V -> F render cycle this test drives
			mode = core.RenderPlain
		}
		go openThread(fw, bus, nil, tui.OpenReq{
			ThreadID: req.ThreadID, MsgID: req.MsgID, Preview: req.Preview,
			Headers: req.Headers, Width: req.Width, Mode: mode, LabelLinks: req.LabelLinks,
		}, nil, config.Crypto{}, false, "")
	})
	defer func() {
		tui.SetOpenHandler(func(tui.OpenReq) {})
	}()

	cfg := config.Default()
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
	})})
	st := config.NewStore(cfg)
	m := tui.New(view, ch, cfg.Bindings, cfg.TagActions, bus, st, cfg.UI)
	m, _ = m.Update(tui.WindowSizeMsg{Width: 80, Height: 24})
	pump := func() {
		for {
			select {
			case e := <-ch:
				m, _ = m.Update(tui.EventMsg{Event: e})
				if _, ok := e.(core.ThreadLoaded); ok {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for the thread render")
			}
		}
	}
	press := func(key string) {
		m, _ = m.Update(tui.KeyPressMsg{Text: key, Code: tcell.KeyRune})
	}
	press("enter") // open the thread
	pump()
	if out := strings.TrimSpace(m.View()); !strings.Contains(out, "a@example.com") {
		t.Fatalf("open must switch to the pager, view:\n%s", out)
	}
	press("V") // the html view
	pump()
	if out := strings.TrimSpace(m.View()); strings.Contains(out, "<p>") {
		t.Fatalf("v must render the html flow, not the raw source:\n%s", out)
	}
	press("F") // the easyjump request arms the key loop (no prompt)
	pump()
	if out := strings.TrimSpace(m.View()); !strings.Contains(out, "[1]") {
		t.Fatalf("the labeled reply must carry the [N] labels:\n%s", out)
	}
	// the fixture is 40 paragraphs - taller than the 22-row window, so
	// the scroll must move the viewport
	topBefore := secondLine(m.View())
	press("j") // scroll while the key loop owns the keys
	if out := strings.TrimSpace(m.View()); !strings.Contains(out, "[1]") {
		t.Fatalf("scrolling must keep the labels:\n%s", out)
	}
	if top := secondLine(m.View()); top == topBefore {
		t.Fatalf("j must scroll the labeled pager, top stayed %q", top)
	}
	press("j")
	press("2") // digits still type after the scroll
	m, _ = m.Update(tui.KeyPressMsg{Text: "enter", Code: '\r'})
	pump() // the exit re-render is async
	labelRe := regexp.MustCompile(`\[\d+\]`)
	if got := strings.TrimSpace(m.View()); labelRe.MatchString(got) {
		t.Fatalf("the exit re-render must drop the labels:\n%s", got)
	}
}

// secondLine strips the frame's status row and returns the first pager
// row (the scroll assertion compares it across presses).
func secondLine(s string) string {
	rest := s
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		return rest[:i]
	}
	return rest
}
