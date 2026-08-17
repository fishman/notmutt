package tui

import "notmutt/core"

type EventMsg struct{ Event core.Event }

// EventCmd forwards one bus event into the loop. Re-arm it after every
// EventMsg (and from Init) to keep the loop alive. A nil channel waits
// forever (tests), which is fine.
func EventCmd(ch <-chan core.Event) Cmd {
	return func() []any {
		return []any{EventMsg{Event: <-ch}}
	}
}
