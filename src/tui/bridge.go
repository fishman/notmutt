// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import "notmutt/core"

type EventMsg struct{ Event core.Event }

// EventCmd forwards one bus event into the loop; re-arm after every
// EventMsg (and Init) or the loop dies. A nil channel waits forever (tests).
func EventCmd(ch <-chan core.Event) Cmd {
	return func() []any {
		return []any{EventMsg{Event: <-ch}}
	}
}
