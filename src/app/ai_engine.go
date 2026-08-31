// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"notmutt/app/ai"
	"notmutt/app/aicmd"
	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
	"notmutt/tui"
)

// aiCommandList loads the configured AI commands for the picker: the
// strict load errors the whole list on a broken prompt file - a typo
// must not silently drop commands.
func aiCommandList() []tui.AICommand {
	cmds, err := aicmd.LoadCommands(filepath.Join(configDir(), "ai"))
	if err != nil {
		diag.Warn("aicmd", "err", err.Error())
		return nil
	}
	out := make([]tui.AICommand, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, tui.AICommand{Name: c.Name, Desc: c.Description})
	}
	return out
}

// chunkBatcher rate-limits streamed AI deltas to at most one publish per
// minInterval - the frame budget: a provider can emit hundreds of tokens
// per second, far faster than the TUI repaints, and the event bus drops
// events that overflow its bounded channel (a silent loss that garbles
// the summary). Deltas accumulate in a buffer and flush whole; nothing
// is dropped, the final flush sends the last partial frame. The callback
// runs on the single stream-read goroutine, so no lock is needed.
type chunkBatcher struct {
	minInterval time.Duration
	last        time.Time
	buf         strings.Builder
	publish     func(string)
}

// add buffers one delta and flushes when the interval has elapsed; flush
// is called once more after the stream ends.
func (b *chunkBatcher) add(d string) {
	b.buf.WriteString(d)
	if time.Since(b.last) < b.minInterval {
		return
	}
	b.flush()
}

func (b *chunkBatcher) flush() {
	s := b.buf.String()
	b.buf.Reset()
	if s == "" {
		return
	}
	b.last = time.Now()
	b.publish(s)
}

// lastAIOutput is the most recent view-action command's full output (the
// summary the user sees in the pager), retained so a later command that
// declares summary_context can build on it - the AI-command chain. It is
// AI output, not mail content, and only ever reaches the model through
// BuildContext's single path. Session-local.
var lastAIOutput string

// runAICommand runs one command on a thread: fetch the thread messages,
// build the context from the declared data, stream the completion as
// AiChunk, and open a compose draft when the command drafts one. extra is
// user text typed in the picker (the e key) and is appended to the prompt
// body before sending. The wiring runs it on its own goroutine; the
// summary pager is one job at a time (the TUI gates on m.summary).
func runAICommand(name, threadID, extra string, bus *core.Bus, cfg config.Config, worker workerAPI, root string) {
	cmds, err := aicmd.LoadCommands(filepath.Join(configDir(), "ai"))
	if err != nil {
		bus.Publish(core.AiResult{Err: err})
		return
	}
	var cmd *aicmd.Command
	for i := range cmds {
		if cmds[i].Name == name {
			cmd = &cmds[i]
			break
		}
	}
	if cmd == nil {
		bus.Publish(core.AiResult{Err: fmt.Errorf("aicmd: no such command %q", name)})
		return
	}
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
	if err != nil || rpl.Err != nil {
		bus.Publish(core.AiResult{Err: fmt.Errorf("thread %s: %w", threadID, errors.Join(err, rpl.Err))})
		return
	}
	account := ""
	if m := newestOf(rpl.Msgs); m != nil {
		account = resolveAccount(cfg, tagsOf(m), nil)
	}
	ctxText, err := aicmd.BuildContext(cmd, rpl.Msgs, cfg.MyAddrs(), aicmd.LoadDefaultContext(configDir()), aicmd.LoadAccountNote(configDir(), account))
	if err != nil {
		bus.Publish(core.AiResult{Err: err})
		return
	}
	if cmd.SummaryContext && lastAIOutput != "" {
		ctxText += "\nPrevious AI summary:\n" + lastAIOutput + "\n"
	}
	p, err := resolveAIProvider(cfg, cmd.Provider)
	if err != nil {
		bus.Publish(core.AiResult{Err: err})
		return
	}
	body := cmd.Body
	if extra != "" {
		body = cmd.Body + "\n\n" + extra
	}
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	bus.Publish(core.AiStarted{JobID: jobID, ThreadID: threadID})
	batcher := &chunkBatcher{minInterval: 25 * time.Millisecond, publish: func(d string) {
		bus.Publish(core.AiChunk{JobID: jobID, Text: core.SanitizeText(d)})
	}}
	out, err := ai.Chat(context.Background(), p, p.Model, body, ctxText, batcher.add)
	batcher.flush()
	if cmd.Action == "view" && err == nil {
		lastAIOutput = out
	}
	if cmd.Action == "compose" && err == nil {
		if st := aiDraftCompose(cfg, root, rpl.Msgs, out); st != nil {
			st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
			bus.Publish(compose.ToEvent(st))
		}
	}
	bus.Publish(core.AiResult{JobID: jobID, Err: err})
}
