// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

// emptyThreadWorker serves an ActThread reply with no messages - the
// one shape the fake worker cannot represent.
type emptyThreadWorker struct{}

func (emptyThreadWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	return notmuch.Reply{ID: a.ID}, nil
}

func restoreRenderHooks(hooks []BodyRenderHook, budget time.Duration) {
	renderHooks = hooks
	renderHookBudget = budget
}

// TestBodyRenderHooksTransform pins the boundary: registered hooks run
// in order on the open job's render output, and the transformed lines
// ride the ThreadLoaded event to the TUI.
func TestBodyRenderHooksTransform(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	savedBudget := renderHookBudget
	defer restoreRenderHooks(saved, savedBudget)
	RegisterBodyRenderHook(func(ctx context.Context, lines []core.Line) ([]core.Line, error) {
		return append(lines, core.Line{Text: "hook one", Kind: core.LineBody}), nil
	})
	RegisterBodyRenderHook(func(ctx context.Context, lines []core.Line) ([]core.Line, error) {
		return append(lines, core.Line{Text: "hook two", Kind: core.LineBody}), nil
	})

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		var texts []string
		for _, l := range tl.Lines {
			texts = append(texts, l.Text)
		}
		joined := strings.Join(texts, "|")
		if !strings.Contains(joined, "message a: no path") {
			t.Fatalf("the render output must arrive on the event: %q", joined)
		}
		if !strings.HasSuffix(joined, "|hook one|hook two") {
			t.Fatalf("hooks must chain in registration order: %q", joined)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestBodyRenderHookErrorFallsBack pins the fallback: a hook error
// drops the hook's output and the render keeps the last good lines -
// the open never fails because a plugin misbehaved.
func TestBodyRenderHookErrorFallsBack(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)
	RegisterBodyRenderHook(func(ctx context.Context, lines []core.Line) ([]core.Line, error) {
		return append(lines, core.Line{Text: "never seen", Kind: core.LineBody}), context.DeadlineExceeded
	})

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if tl.Err != nil {
			t.Fatalf("a hook error must not fail the open: %v", tl.Err)
		}
		for _, l := range tl.Lines {
			if l.Text == "never seen" {
				t.Fatalf("the failed hook's output must not survive: %+v", tl.Lines)
			}
		}
		if len(tl.Lines) == 0 {
			t.Fatal("the fallback must keep the un-hooked render")
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestBodyRenderHookDeadlineFallsBack pins the freeze fix: a hook that
// never returns gets killed by the chain deadline (the context the
// gopher-lua SetContext adapter wires later), and the render falls
// back to the un-hooked lines instead of hanging the open.
func TestBodyRenderHookDeadlineFallsBack(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	savedBudget := renderHookBudget
	defer restoreRenderHooks(saved, savedBudget)
	renderHookBudget = 10 * time.Millisecond
	deadlineSeen := false
	RegisterBodyRenderHook(func(ctx context.Context, lines []core.Line) ([]core.Line, error) {
		select {
		case <-ctx.Done():
			deadlineSeen = true
		case <-time.After(time.Second):
			// the deadline never fired - fail the test, don't hang it
		}
		return lines, ctx.Err()
	})

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if !deadlineSeen {
			t.Fatal("the hook must observe the chain deadline")
		}
		if tl.Err != nil {
			t.Fatalf("a deadline overrun must not fail the open: %v", tl.Err)
		}
		if len(tl.Lines) == 0 {
			t.Fatal("the deadline fallback must keep the un-hooked render")
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestOpenThreadEmptyThreadPublishesErr pins the moved empty check:
// with nothing to render, the open publishes Err and the TUI stays in
// index (the model test covers the fallback). The fake worker cannot
// represent an empty thread (it substitutes a changed-set marker), so
// this test uses a minimal stub.
func TestOpenThreadEmptyThreadPublishesErr(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := emptyThreadWorker{}

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok || tl.Err == nil {
			t.Fatalf("an empty thread must publish Err, got %T %+v", e, tl)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestOpenThreadLinks pins the F key's label render through the open
// job: labelLinks=true renders the inline "[N]" labels and ships the
// target list on the ThreadLoaded event; the unlabeled render carries
// no links - the labels are mode-scoped, never the html view's
// default.
func TestOpenThreadLinks(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	msg := "From: a@example.com\nTo: b@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: text/html; charset=utf-8\n\n" +
		"<p>see <a href=\"https://alpha.example.com/x\">alpha</a></p>\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1", Paths: []string{p}}})

	openThread(fw, bus, "t1", "", false, core.RenderHTML, false, 0, true, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if !tl.LinkLabels {
			t.Fatal("labelLinks must ride the event")
		}
		if len(tl.Links) != 1 || tl.Links[0] != "https://alpha.example.com/x" {
			t.Fatalf("links = %v", tl.Links)
		}
		joined := ""
		for _, l := range tl.Lines {
			joined += l.Text + "|"
		}
		if !strings.Contains(joined, "[1]") {
			t.Fatalf("the label render must carry the [N] label: %q", joined)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}

	openThread(fw, bus, "t1", "", false, core.RenderHTML, false, 0, false, nil, nil)
	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if tl.LinkLabels || len(tl.Links) != 0 {
			t.Fatalf("the unlabeled render must carry no links: labels=%v links=%v", tl.LinkLabels, tl.Links)
		}
		for _, l := range tl.Lines {
			if strings.Contains(l.Text, "[1]") {
				t.Fatalf("the unlabeled render must carry no labels: %q", l.Text)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}
