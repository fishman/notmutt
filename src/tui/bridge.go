package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"notmutt/core"
)

type EventMsg struct{ Event core.Event }

// EventCmd forwards one bus event into BubbleTea. Re-arm it after every
// EventMsg (and from Init) to keep the loop alive. A nil channel waits
// forever (tests), which is fine.
func EventCmd(ch <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		return EventMsg{Event: <-ch}
	}
}
