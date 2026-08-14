package tui

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

var testKeys = map[string]string{
	"j": "cursor-down", "k": "cursor-up", "q": "quit",
	"r": "toggle-read", "a": "archive", "d": "delete",
	"u": "undo", "$": "apply",
}

var testTagActions = map[string]string{
	"toggle-read": "unread",
	"archive":     "archive",
	"delete":      "deleted",
}

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}, References: []string{"b"}},
		{ID: "b", Timestamp: 200, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}},
	})})
	return New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

// ghostModel builds a thread whose messages share no reference chain:
// core emits a synthetic ghost root row (Msg == nil) at the thread start.
func ghostModel() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 200, Author: "Ann", Subject: "hello"},
		{ID: "b", Timestamp: 100, Author: "Bob", Subject: "re: hello"},
	})})
	return New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

func press(t *testing.T, m tea.Model, key string) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

// stubModel builds the step-one fill state: search summaries (message id
// empty) before the viewport hydrate replaces them with full threads.
func stubModel() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{
		core.NewThread("t1", []*core.Message{{ThreadID: "t1", Timestamp: 200, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}}}),
		core.NewThread("t2", []*core.Message{{ThreadID: "t2", Timestamp: 100, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}}}),
	})
	return New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

// TestStubThreadStaging pins the stub-row rules: the cursor tracks
// summary rows by index (no message id to anchor by), and tag actions
// on a stub stage a THREAD-level op - the summary stands for the whole
// thread, and apply emits thread:<id>. A soft tag toggle resolves
// against the thread's applied tags.
func TestStubThreadStaging(t *testing.T) {
	m := stubModel()
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor must track stub rows by index, got %d", m.CursorIndex())
	}
	m = press(t, m, "a") // folder tag action on a stub: thread-level stage
	if !m.view.IsStaged("t:t2") {
		t.Fatal("a on a stub must stage the thread identity t:t2")
	}
	row, _ := m.view.CursorRow()
	if !row.Staged || !slices.Equal(row.StagedTags, []string{"archive"}) {
		t.Fatalf("stub row must render the resolved thread state: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	m.width, m.height = 80, 24
	if out := m.View(); !strings.Contains(out, "*") {
		t.Fatalf("staged glyph missing:\n%s", out)
	}
	m = press(t, m, "u") // undo clears the thread op
	if m.view.HasStaged() {
		t.Fatal("u on a stub must clear the staged thread op")
	}
	m = press(t, m, "k")
	if m.CursorIndex() != 0 {
		t.Fatalf("cursor after k on stubs = %d", m.CursorIndex())
	}
	m = press(t, m, "r") // soft toggle resolves against the thread's tags
	if !m.view.IsStaged("t:t1") {
		t.Fatal("r on the unread stub must stage t:t1")
	}
	ops, _ := m.view.StagedOps()
	if got := ops["t:t1"]; len(got) != 1 || got[0] != (core.TagOp{Tag: "unread", Add: false}) {
		t.Fatalf("r on the unread stub must stage -unread for the thread: %v", got)
	}
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
	m := New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
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
			m.stage("toggle-read")
			m.stage("archive")
			m.undo()
		}
	}()
	wg.Wait()
}

func TestRebinding(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox", "unread"}},
	})})
	m := New(view, nil, map[string]string{"x": "archive"}, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m = press(t, m, "x")
	row, _ := m.view.CursorRow()
	if !row.Staged || !hasTag(row.StagedTags, "archive") {
		t.Fatalf("x must stage +archive: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	before := append([]string(nil), row.StagedTags...)
	m = press(t, m, "a")
	row, _ = m.view.CursorRow()
	if !slices.Equal(row.StagedTags, before) {
		t.Fatalf("unbound a must not change the staged state: %v -> %v", before, row.StagedTags)
	}
}

func TestTagActionMapsToConfigTag(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, map[string]string{"x": "toggle-read"}, map[string]string{"toggle-read": "wip"}, nil, config.NewStore(config.Default()), config.Default().UI)
	m = press(t, m, "x")
	row, _ := m.view.CursorRow()
	// wip is in no group, so it is soft: it toggles from the applied
	// state; the cursor message is read, so +wip
	if !row.Staged || !hasTag(row.StagedTags, "wip") {
		t.Fatalf("x must stage +wip: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
}

func TestStageToggleReadRemoves(t *testing.T) {
	m := model()
	m.view.SetCursor("a")
	m = press(t, m, "r")
	ops, _ := m.view.StagedOps()
	got := ops["a"]
	if len(got) != 1 || got[0] != (core.TagOp{Tag: "unread", Add: false}) {
		t.Fatalf("r on the unread message must stage -unread: %v", got)
	}
}

func TestTagActionFolderAddCustomName(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "b", Timestamp: 200, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, map[string]string{"y": "wip"}, map[string]string{"wip": "archive"}, nil, config.NewStore(config.Default()), config.Default().UI)
	m = press(t, m, "y")
	row, _ := m.view.CursorRow()
	// archive is a folder tag: a custom action name still stages +archive
	if !row.Staged || !slices.Equal(row.StagedTags, []string{"archive"}) {
		t.Fatalf("y must stage +archive: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
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
	m := New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	out := m.View()
	// the model's own cursor highlight (indicator style SGR) is not a
	// leak; check the injected sequences specifically
	for _, leak := range []string{"\x1b]", "\x07", "\x1b[31m", "\x1b[41m"} {
		if strings.Contains(out, leak) {
			t.Fatalf("control chars leaked into render:\n%q", out)
		}
	}
}

func TestProgressBarRendersAndClears(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	if strings.Contains(m.View(), "refresh") {
		t.Fatal("no bar before any progress event")
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 5, Total: 10})
	out := m.View()
	if !strings.Contains(out, "refresh 5/10") {
		t.Fatalf("bar missing:\n%s", out)
	}
	if !strings.Contains(stripANSI(out), "inbox|2") {
		t.Fatalf("status line missing view + count:\n%s", out)
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 10, Total: 10})
	if strings.Contains(m.View(), "refresh") {
		t.Fatal("bar must clear on completion")
	}
}

func TestProgressBarEmptyView(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testKeys, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	if !strings.Contains(m.View(), "refresh 1/5") {
		t.Fatalf("empty view must still render the status line:\n%s", m.View())
	}
}

func TestProgressBarSegments(t *testing.T) {
	ui := config.Default().UI
	// Done=5/Total=10 in 20 cells: half filled
	fill, empty := progressBar(ui, core.Progress{Job: "refresh", Done: 5, Total: 10}, 20)
	if fill != "##########" || empty != "----------" {
		t.Fatalf("half progress: fill %q empty %q", fill, empty)
	}
	// the glyphs are config data (R11): non-default glyphs must be used
	ui.Glyphs.ProgressFill, ui.Glyphs.ProgressEmpty = "=", "."
	fill, empty = progressBar(ui, core.Progress{Job: "refresh", Done: 5, Total: 10}, 20)
	if fill != "==========" || empty != ".........." {
		t.Fatalf("glyph config: fill %q empty %q", fill, empty)
	}
	// negative cells (narrow-terminal clamp path): never panics, empty bar
	if _, empty := progressBar(ui, core.Progress{Job: "refresh", Done: 5, Total: 10}, -3); empty != "" {
		t.Fatalf("negative cells: empty %q", empty)
	}
	// zero total: no division by zero, empty bar
	if _, empty := progressBar(ui, core.Progress{Job: "refresh", Done: 0, Total: 0}, 4); empty != "...." {
		t.Fatalf("zero total: empty %q", empty)
	}
}

// TestThemeVariantSwitchLive pins the live variant switch: the store
// owns the theme and the model re-reads it on ConfigChanged{theme}, so
// a switch to a variant with a distinct status fg re-renders the
// status line in the new color.
func TestThemeVariantSwitchLive(t *testing.T) {
	cfg := config.Default()
	cfg.Theme.Variants["red"] = config.StyleTable{Status: config.Style{Fg: "#ff0000"}}
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testKeys, testTagActions, nil, st, cfg.UI)
	m.width, m.height = 80, 24
	if out := m.View(); strings.Contains(out, "255;0;0") {
		t.Fatalf("dark theme must not render the red status fg:\n%s", out)
	}
	if err := st.SetThemeVariant("red"); err != nil {
		t.Fatal(err)
	}
	m = pressEvent(t, m, core.ConfigChanged{Section: "theme"})
	if out := m.View(); !strings.Contains(out, "255;0;0") {
		t.Fatalf("variant switch must re-render the status line in the new color:\n%s", out)
	}
}

func pressEvent(t *testing.T, m tea.Model, e core.Event) Model {
	t.Helper()
	next, _ := m.Update(EventMsg{Event: e})
	return next.(Model)
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

// TestProgressBarSurvivesDroppedCompletion pins the stuck-bar fix: the
// bus keeps the latest progress as a snapshot, so a completion event
// dropped from the channel under backpressure still clears the bar via
// the tick (the tail of a publish burst used to vanish with the bar
// stuck mid-progress).
func TestProgressBarSurvivesDroppedCompletion(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testKeys, testTagActions, bus, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24

	bus.Publish(core.Progress{Job: "cache", View: "inbox", Done: 33, Total: 37})
	m = pump(t, m, ch)
	if !m.progressOn {
		t.Fatal("bar should be on mid-job")
	}

	// Saturate the subscriber channel so the completion event drops.
	for i := 0; i < 64; i++ {
		bus.Publish(core.ViewDiff{View: "inbox"})
	}
	bus.Publish(core.Progress{Job: "cache", View: "inbox", Done: 37, Total: 37})
	for i := 0; i < 64; i++ {
		m = pump(t, m, ch)
	}
	// All 64 drained events are the ViewDiffs: the completion never made
	// it into the channel.
	if m.progressOn {
		t.Fatal("bar must not go off mid-job")
	}

	// The tick re-reads the snapshot and clears the bar.
	next, _ := m.Update(progressTick{})
	if next.(Model).progressOn {
		t.Fatal("bar must clear from the snapshot even with the completion event dropped")
	}
}

func pump(t *testing.T, m Model, ch <-chan core.Event) Model {
	t.Helper()
	select {
	case e := <-ch:
		next, _ := m.Update(EventMsg{Event: e})
		return next.(Model)
	case <-time.After(2 * time.Second):
		t.Fatal("no event on the bus channel")
		return m
	}
}

// TestProgressBarPerView pins the scoping: progress from another virtual
// folder (account, unread, sent, drafts - every view has its own fill)
// never turns on this view's bar; only the current view's snapshot does.
func TestProgressBarPerView(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testKeys, testTagActions, bus, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24

	// another view's fill publishes: the inbox bar stays off
	bus.Publish(core.Progress{Job: "refresh", View: "unread", Done: 1, Total: 5})
	m = pump(t, m, ch)
	if m.progressOn {
		t.Fatal("another view's progress must not turn on this bar")
	}
	// this view's fill turns it on
	bus.Publish(core.Progress{Job: "refresh", View: "inbox", Done: 2, Total: 5})
	m = pump(t, m, ch)
	if !m.progressOn {
		t.Fatal("this view's progress must turn on the bar")
	}
	if !strings.Contains(m.View(), "refresh 2/5") {
		t.Fatalf("bar must show this view's progress:\n%s", m.View())
	}
	// completion clears only this view's bar
	bus.Publish(core.Progress{Job: "refresh", View: "inbox", Done: 5, Total: 5})
	m = pump(t, m, ch)
	if m.progressOn {
		t.Fatal("bar must clear on this view's completion")
	}
}
