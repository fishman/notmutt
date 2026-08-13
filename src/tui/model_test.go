package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/core"
)

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}, References: []string{"b"}},
		{ID: "b", Timestamp: 200, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}},
	})})
	return New(view, nil)
}

// ghostModel builds a thread whose messages share no reference chain:
// core emits a synthetic ghost root row (Msg == nil) at the thread start.
func ghostModel() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 200, Author: "Ann", Subject: "hello"},
		{ID: "b", Timestamp: 100, Author: "Bob", Subject: "re: hello"},
	})})
	return New(view, nil)
}

func press(t *testing.T, m tea.Model, key string) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

func TestCursorMoves(t *testing.T) {
	m := model()
	if m.CursorIndex() != 0 {
		t.Fatalf("cursor starts at 0, got %d", m.CursorIndex())
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor after j = %d", m.CursorIndex())
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor must clamp at bottom, got %d", m.CursorIndex())
	}
	m = press(t, m, "k")
	if m.CursorIndex() != 0 {
		t.Fatalf("cursor after k = %d", m.CursorIndex())
	}
}

func TestRenderShowsRows(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	out := m.View()
	if out == "" {
		t.Fatal("empty render")
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("subject missing from render:\n%s", out)
	}
	if !strings.Contains(out, "Ann") {
		t.Fatalf("author missing from render:\n%s", out)
	}
}

func TestQuit(t *testing.T) {
	m := model()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q must return a quit command")
	}
}

func TestEventMsgRepaints(t *testing.T) {
	m := model()
	m.view.SetCursor("a")
	next, _ := m.Update(EventMsg{Event: core.ViewDiff{View: "inbox"}})
	if next.(Model).CursorIndex() != 1 {
		t.Fatalf("cursor by id after event = %d", next.(Model).CursorIndex())
	}
}

func TestGhostRowRendersAndCursorSkips(t *testing.T) {
	m := ghostModel()
	m.width, m.height = 80, 24
	out := m.View()
	if !strings.Contains(out, "[...]") {
		t.Fatalf("ghost row missing from render:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("real rows missing from render:\n%s", out)
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor should sit on the first real row, got %d", m.CursorIndex())
	}
	m = press(t, m, "k")
	if m.CursorIndex() != 1 {
		t.Fatalf("k must not move the cursor onto the ghost row, got %d", m.CursorIndex())
	}
}

func TestToggleRead(t *testing.T) {
	m := model()
	var gotID string
	var gotAdd bool
	calls := 0
	SetTagOpHandler(func(id string, add bool) { calls++; gotID, gotAdd = id, add })
	m = press(t, m, "t")
	if calls != 1 || gotID != "b" || !gotAdd {
		t.Fatalf("hook wrong: calls=%d id=%q add=%v", calls, gotID, gotAdd)
	}
	row, _ := m.view.CursorRow()
	if !hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("unread not added to cursor message: %v", row.Msg.Tags)
	}
	m = press(t, m, "t")
	if calls != 2 || gotAdd {
		t.Fatalf("second toggle must remove: id=%q add=%v", gotID, gotAdd)
	}
	if hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("unread not removed: %v", row.Msg.Tags)
	}
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
