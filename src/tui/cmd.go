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
// (record 23): a Cmd is a closure producing messages; the loop runs
// cmds on goroutines, results serialized through Update on the command
// channel. batch concatenates cmds' messages (the EventCmd + tick
// pattern); tickCmd sleeps its goroutine (single-flight gates bound
// the tick count); quitCmd ends the loop.

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
// the subprocess so editors run as foreground TUIs (record 23). Tests
// without a loop never suspend; tcell clears the screen on resume.
var loopScreen tcell.Screen

func execCmd(cmd *exec.Cmd, done func(error) any) Cmd {
	// the child must see the parent's terminal - exec.Command wires nil
	// stdio to /dev/null, launching a foreground TUI invisible and unreadable
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
			// the suspend wiped the cell buffer; the row cache must not
			// survive it (a row-skip push leaves the terminal blank)
			resetPushedFrames(s)
		}
		return []any{done(err)}
	}
}
