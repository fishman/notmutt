// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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

// runAICommand runs one command on a thread: fetch the thread messages,
// build the context from the declared data, stream the completion as
// AiChunk, and open a compose draft when the command drafts one. The
// wiring runs it on its own goroutine; the summary pager is one-job at a
// time (the TUI gates on m.summary before calling).
func runAICommand(name, threadID string, bus *core.Bus, cfg config.Config, worker workerAPI, root string) {
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
	p, err := resolveAIProvider(cfg, cmd.Provider)
	if err != nil {
		bus.Publish(core.AiResult{Err: err})
		return
	}
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	bus.Publish(core.AiStarted{JobID: jobID, ThreadID: threadID})
	out, err := ai.Chat(context.Background(), p, p.Model, cmd.Body, ctxText, func(d string) {
		bus.Publish(core.AiChunk{JobID: jobID, Text: core.SanitizeControls(d)})
	})
	if cmd.Action == "compose" && err == nil {
		if st := aiDraftCompose(cfg, root, rpl.Msgs, out); st != nil {
			st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
			bus.Publish(compose.ToEvent(st))
		}
	}
	bus.Publish(core.AiResult{JobID: jobID, Err: err})
}
