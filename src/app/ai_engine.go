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

// lastAIOutput is the most recent view-action command's full output (the
// summary the user sees in the pager), retained so a later command that
// declares summary_context can build on it - the AI-command chain. It is
// AI output, not mail content, and only ever reaches the model through
// BuildContext's single path. Session-local.
var lastAIOutput string

// runAICommand runs one command on a thread: fetch the thread messages,
// resolve the account's [ai-data] grant (no grant refuses - deny by
// default), build the context from the declared data, stream the
// completion as AiChunk, and open a compose draft when the command
// drafts one. extra is user text typed in the picker (the e key),
// appended to the prompt body. The wiring runs it on its own goroutine;
// the summary pager is one job at a time (the TUI gates on m.summary).
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
	allowed, ok := cfg.AIDataGrant(account)
	if !ok {
		bus.Publish(core.AiResult{Err: fmt.Errorf("ai: account %q has no AI data grant ([ai-data])", account)})
		return
	}
	ctxText, err := aicmd.BuildContext(cmd, rpl.Msgs, cfg.MyAddrs(), allowed, aicmd.LoadDefaultContext(configDir()), aicmd.LoadAccountNote(configDir(), account))
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
	jobID, out, err := aiStream(bus, context.Background(), threadID, p, p.Model, body, ctxText)
	var pasted bool
	switch cmd.Action {
	case "view":
		if err == nil {
			lastAIOutput = out
		}
	case "compose":
		if err == nil {
			if st := aiDraftCompose(cfg, root, rpl.Msgs, out); st != nil {
				st.ID = fmt.Sprintf("%d", time.Now().UnixNano())
				bus.Publish(compose.ToEvent(st))
				pasted = true
			}
		}
	}
	bus.Publish(core.AiResult{JobID: jobID, Err: err, CloseSummary: pasted})
}
