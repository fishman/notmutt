package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
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
	next, nextCmd := m.Update(EventMsg{Event: core.ViewDiff{View: "inbox"}})
	if next.(Model).CursorIndex() != 1 {
		t.Fatalf("cursor by id after event = %d", next.(Model).CursorIndex())
	}
	if nextCmd == nil {
		t.Fatal("EventMsg must re-arm the bridge")
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

func TestStageToggleRead(t *testing.T) {
	m := model()
	row, _ := m.view.CursorRow()
	if hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("fixture: cursor message must be read, got %v", row.Msg.Tags)
	}
	m = press(t, m, "r")
	row, _ = m.view.CursorRow()
	if !row.Staged || !hasTag(row.StagedTags, "unread") {
		t.Fatalf("r must stage +unread: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	if hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("applied state must be untouched: %v", row.Msg.Tags)
	}
	m.width, m.height = 80, 24
	if out := m.View(); !strings.Contains(out, "*U") {
		t.Fatalf("staged glyph missing:\n%s", out)
	}
	m = press(t, m, "r")
	row, _ = m.view.CursorRow()
	if row.Staged {
		t.Fatal("staging the same op twice must cancel")
	}
}

func TestStageArchiveResolves(t *testing.T) {
	m := model()
	m = press(t, m, "a")
	row, _ := m.view.CursorRow()
	if !row.Staged {
		t.Fatal("a must stage the cursor message")
	}
	// message b is [inbox]: the resolved display is [archive]
	if !slices.Equal(row.StagedTags, []string{"archive"}) {
		t.Fatalf("StagedTags = %v, want [archive]", row.StagedTags)
	}
	if hasTag(row.Msg.Tags, "archive") || !hasTag(row.Msg.Tags, "inbox") {
		t.Fatalf("applied state must be untouched: %v", row.Msg.Tags)
	}
}

func TestUndoStaged(t *testing.T) {
	m := model()
	m = press(t, m, "a")
	m = press(t, m, "r")
	row, _ := m.view.CursorRow()
	if !row.Staged || len(row.StagedTags) != 2 {
		t.Fatalf("two staged ops expected: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	m = press(t, m, "u")
	row, _ = m.view.CursorRow()
	if row.Staged {
		t.Fatal("u must clear the staged ops")
	}
	if hasTag(row.Msg.Tags, "archive") || !hasTag(row.Msg.Tags, "inbox") {
		t.Fatalf("applied state must be untouched: %v", row.Msg.Tags)
	}
}

func TestApplyKeyInvokesHandler(t *testing.T) {
	m := model()
	called := false
	SetApplyHandler(func() { called = true })
	m = press(t, m, "$")
	if !called {
		t.Fatal("$ must invoke the apply handler")
	}
}

func TestStagingKeysGhostGuarded(t *testing.T) {
	m := ghostModel()
	// ghostModel's cursor starts on the ghost root row
	for _, key := range []string{"r", "a", "d", "u", "$"} {
		m = press(t, m, key)
	}
	if m.view.HasStaged() {
		t.Fatal("staging keys must be no-ops on ghost rows")
	}
}

func TestStageUndoConcurrent(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox", "unread"}},
	})})
	view.SetCursor("a")
	m := New(view, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
				{ID: "a", Timestamp: 100, Tags: []string{"inbox", "unread"}},
			})})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			m.stageKey("r")
			m.stageKey("a")
			m.stageKey("u")
		}
	}()
	wg.Wait()
}

func TestPadCellsRightExactWidth(t *testing.T) {
	// 2-cell rune at the boundary: truncation stops at 15 cells, padding
	// must restore the slot to exactly 16
	got := padCellsRight(strings.Repeat("你", 7)+"a"+"你", 16)
	if runewidth.StringWidth(got) != 16 {
		t.Fatalf("padCellsRight returned %d cells, want 16: %q", runewidth.StringWidth(got), got)
	}
	if runewidth.StringWidth(padCellsRight("short", 16)) != 16 {
		t.Fatal("short pad must also be exactly 16 cells")
	}
}

func TestRenderSanitizesControls(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "\x1b]0;x\x07Ann", Subject: "hello\x1b[31m", Tags: []string{"inbox", "\x1b[41mred"}},
	})})
	m := New(view, nil)
	m.width, m.height = 80, 24
	out := m.View()
	// the model's own cursor highlight (ESC[7m ... ESC[0m) is not a leak;
	// check the injected sequences specifically
	for _, leak := range []string{"\x1b]", "\x07", "\x1b[31m", "\x1b[41m"} {
		if strings.Contains(out, leak) {
			t.Fatalf("control chars leaked into render:\n%q", out)
		}
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
