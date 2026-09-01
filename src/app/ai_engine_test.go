// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
)

// TestRunAICommandDenyNoGrant pins the deny-by-default [ai-data] gate on
// the AI command path: an account without a grant refuses the run before
// any provider contact - the grant error publishes as AiResult, no
// stream starts.
func TestRunAICommandDenyNoGrant(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ai", "prompts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai", "prompts", "test.md"), []byte(
		"---\nname: test-cmd\ndescription: test\naction: view\ndata: [count]\n---\nSummarize.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", dir)
	cfg := config.Config{
		Accounts: map[string]config.Account{"gmail": {}},
		AI:       map[string]config.AIProvider{"local": {Type: "openai", Model: "m", BaseURL: "http://127.0.0.1:1"}},
		// no AIAccounts: deny by default
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Tags: []string{"gmail"}}})
	bus := core.NewBus()
	ch := bus.Subscribe()
	runAICommand("test-cmd", "t1", "", bus, cfg, fw, "")
	var res core.AiResult
	for e := range ch {
		if v, ok := e.(core.AiResult); ok {
			res = v
			break
		}
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "no AI data grant") {
		t.Fatalf("deny error = %v", res.Err)
	}
}

// TestChunkBatcherNoLoss pins the stream coalescer that keeps the AI
// summary complete: the event bus drops events when its bounded channel
// overflows (a locked design, TestBusSlowSubscriberDrops), and a fast
// provider emits hundreds of tokens per second - far faster than the TUI
// repaints. The batcher buffers deltas and flushes at a bounded rate, so
// NOTHING is ever dropped; the final flush delivers the last partial
// frame.
func TestChunkBatcherNoLoss(t *testing.T) {
	var got []string
	b := &chunkBatcher{minInterval: time.Hour, publish: func(s string) { got = append(got, s) }}
	b.add("a") // the first delta flushes at once (low-latency first frame)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("first delta must flush immediately, got %q", got)
	}
	b.add("b")
	b.add("c")
	if len(got) != 1 {
		t.Fatalf("under the interval nothing new may publish, got %q", got)
	}
	b.flush()
	if len(got) != 2 || got[1] != "bc" {
		t.Fatalf("flush must deliver the whole buffer intact, got %q", got)
	}
	// the zero interval (default) flushes each delta - the trickle path -
	// and must still lose nothing
	b = &chunkBatcher{publish: func(s string) { got = append(got, s) }}
	got = nil
	for _, d := range []string{"1", "2", "3"} {
		b.add(d)
	}
	if len(got) != 3 || strings.Join(got, "") != "123" {
		t.Fatalf("zero interval must not lose deltas, got %q", got)
	}
	// a tight burst through a near-zero interval must be delivered whole
	// and in order - the stream's high-rate path
	b = &chunkBatcher{minInterval: time.Nanosecond, publish: func(s string) { got = append(got, s) }}
	got = nil
	var burst strings.Builder
	for i := 0; i < 500; i++ {
		d := fmt.Sprintf("d%d ", i)
		burst.WriteString(d)
		b.add(d)
	}
	if strings.Join(got, "") != burst.String() {
		t.Fatalf("burst must be delivered losslessly and in order")
	}
}
