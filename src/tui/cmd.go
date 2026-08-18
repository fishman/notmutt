// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"os/exec"
	"time"

	"github.com/gdamore/tcell/v3"
)

// The command layer is the tea.Cmd shape with the tea runtime gone
// (decision record 23): a Cmd is a closure producing messages (or
// none); the loop runs cmds on goroutines and their results come back
// on the command channel, serialized through Update like every other
// message. batch concatenates several cmds' messages (the EventCmd +
// tick pattern); tickCmd sleeps the goroutine (the model's
// single-flight gates already bound the tick count, so a sleeping
// goroutine per tick is free); quitCmd ends the loop.

type Cmd func() []any

type quitMsg struct{}

func batch(cmds ...Cmd) Cmd {
	return func() []any {
		var out []any
		for _, c := range cmds {
			if c != nil {
				out = append(out, c()...)
			}
		}
		return out
	}
}

func tickCmd(d time.Duration, f func(time.Time) any) Cmd {
	return func() []any {
		time.Sleep(d)
		return []any{f(time.Now())}
	}
}

func quitCmd() Cmd {
	return func() []any { return []any{quitMsg{}} }
}

// loopScreen is the screen the loop owns; execCmd suspends it around
// the subprocess so the editor and attach pickers run as foreground
// TUIs (the tea.ExecProcess pattern, record 23). Tests without a loop
// never suspend. Resume forces the next frame push - the loop repaints
// on the done message, and tcell clears the screen on resume.
var loopScreen tcell.Screen

func execCmd(cmd *exec.Cmd, done func(error) any) Cmd {
	// the child must see the parent's terminal - exec.Command wires nil
	// stdio to the null device, and a foreground TUI with /dev/null on
	// all three fds launches invisible and unreadable (the tea contract
	// this migration dropped)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return func() []any {
		if s := loopScreen; s != nil {
			s.Suspend()
		}
		err := cmd.Run()
		if s := loopScreen; s != nil {
			s.Resume()
			// the suspend wiped the screen's cell buffer; the row cache
			// must not survive it (the corruption bug - a row-skip push
			// against the fresh buffer leaves the terminal blank)
			resetPushedFrames(s)
		}
		return []any{done(err)}
	}
}
