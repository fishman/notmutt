package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

var testKeys = map[string]string{
	"j": "cursor-down", "k": "cursor-up", "o": "open", "q": "quit",
	"r": "toggle-read", "a": "archive", "d": "delete",
	"u": "undo", "$": "apply",
	// the arrow and G keys come from the user config overlay (the
	// defaults are untouched); the test table mirrors the live config
	"up": "cursor-up", "down": "cursor-down", "G": "cursor-bottom",
}

// statusMarker is the unstyled view+count boundary on the status row:
// the view pill, the bar gap, and the count pill.
func statusMarker(count string) string {
	return "inbox" + strings.Repeat(pillGap, 3) + count
}

var testTagActions = map[string]string{
	"toggle-read": "unread",
	"archive":     "archive",
	"delete":      "deleted",
}

// testBindings is the per-context binding table (R9): the index context
// carries the staging keys, the pager context the scroll keys.
var testBindings = map[string]map[string]string{
	"index": testKeys,
	"pager": {
		"j": "scroll-down", "k": "scroll-up",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"g": "scroll-top", "G": "scroll-bottom",
		"up": "scroll-up", "down": "scroll-down",
		"q": "back",
	},
}

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}, References: []string{"b"}},
		{ID: "b", Timestamp: 200, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}},
	})})
	return New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

// ghostModel builds a thread whose messages share no reference chain:
// core emits a synthetic ghost root row (Msg == nil) at the thread start.
func ghostModel() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 200, Author: "Ann", Subject: "hello"},
		{ID: "b", Timestamp: 100, Author: "Bob", Subject: "re: hello"},
	})})
	return New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

func press(t *testing.T, m tea.Model, key string) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Text: key, Code: []rune(key)[0]})
	return next.(Model)
}

// pressType presses a special key (arrows, ctrl+...): actionForKey
// resolves the canonical name via msg.String() ("up", "down", "ctrl+d").
func pressType(t *testing.T, m tea.Model, k rune) Model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: k})
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
	return New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
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
	if out := m.View().Content; !strings.Contains(out, "*") {
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

// TestCountedG pins the counted-g jump: 12g moves the cursor to row 12
// (1-based), and the gg chain still jumps to the top.
func TestCountedG(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	var threads []*core.Thread
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("t%d", i)
		threads = append(threads, core.NewThread(id, []*core.Message{
			{ID: fmt.Sprintf("m%d", i), ThreadID: id, Timestamp: int64(i), Author: "Ann", Subject: "s", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(threads)
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m = press(t, m, "1")
	m = press(t, m, "2")
	m = press(t, m, "g")
	if m.CursorIndex() != 11 {
		t.Fatalf("12g must land on row 12 (index 11), got %d", m.CursorIndex())
	}
	m = press(t, m, "g")
	m = press(t, m, "g")
	if m.CursorIndex() != 0 {
		t.Fatalf("gg must still jump to the top, got %d", m.CursorIndex())
	}
}

func TestRenderShowsRows(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	out := m.View().Content
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
	_, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
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
	out := m.View().Content
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
	// auto-advance: the cursor moved to the next row; the staged row is
	// the one under the previous cursor
	if m.CursorIndex() != 1 {
		t.Fatalf("r must advance the cursor one row, got %d", m.CursorIndex())
	}
	m = press(t, m, "k")
	row, _ = m.view.CursorRow()
	if !row.Staged || !hasTag(row.StagedTags, "unread") {
		t.Fatalf("r must stage +unread: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	if hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("applied state must be untouched: %v", row.Msg.Tags)
	}
	m.width, m.height = 80, 24
	if out := m.View().Content; !strings.Contains(out, "*N") {
		t.Fatalf("staged glyph missing:\n%s", out)
	}
	m = press(t, m, "r")
	m = press(t, m, "k")
	row, _ = m.view.CursorRow()
	if row.Staged {
		t.Fatal("staging the same op twice must cancel")
	}
}

func TestStageArchiveResolves(t *testing.T) {
	m := model()
	m = press(t, m, "a")
	m = press(t, m, "k") // back up after the auto-advance
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
	m = press(t, m, "a") // stage +archive on row 0, auto-advance to row 1
	m = press(t, m, "k") // back to the staged row
	m = press(t, m, "r") // stage +unread on row 0, advance again
	m = press(t, m, "k") // back to the staged row
	row, _ := m.view.CursorRow()
	if !row.Staged || len(row.StagedTags) != 2 {
		t.Fatalf("two staged ops expected: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	m = press(t, m, "u") // undo clears the ops and auto-advances
	m = press(t, m, "k") // back to the unstaged row
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
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, nil, map[string]map[string]string{"index": {"x": "archive"}}, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, nil, map[string]map[string]string{"index": {"x": "toggle-read"}}, map[string]string{"toggle-read": "wip"}, nil, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, nil, map[string]map[string]string{"index": {"y": "wip"}}, map[string]string{"wip": "archive"}, nil, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	out := m.View().Content
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
	if strings.Contains(m.View().Content, "refresh") {
		t.Fatal("no bar before any progress event")
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 5, Total: 10})
	out := m.View().Content
	if !strings.Contains(out, "refresh 5/10") {
		t.Fatalf("bar missing:\n%s", out)
	}
	if !strings.Contains(stripANSI(out), statusMarker("2")) {
		t.Fatalf("status line missing view + count:\n%s", out)
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 10, Total: 10})
	if strings.Contains(m.View().Content, "refresh") {
		t.Fatal("bar must clear on completion")
	}
}

func TestProgressBarEmptyView(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	if !strings.Contains(m.View().Content, "refresh 1/5") {
		t.Fatalf("empty view must still render the status line:\n%s", m.View().Content)
	}
}

// TestEmptyViewLooksFilled pins the load-time surface: an empty view
// renders like a populated one - blank rows fill the list area (the
// indicator sits on the first, cursor-style), and the keyhint bar and
// status row always render, with or without a progress job. The
// literal "empty" text never appears.
func TestEmptyViewLooksFilled(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	out := m.View().Content
	if strings.Contains(out, "empty") {
		t.Fatalf("the literal empty text must not render:\n%s", out)
	}
	strip := stripANSI(out)
	if !strings.Contains(strip, statusMarker("0")) {
		t.Fatalf("idle empty view must still render the status line:\n%s", strip)
	}
	if si, sh := strings.Index(strip, "j cursor-down"), strings.Index(strip, statusMarker("0")); si < 0 || si > sh {
		t.Fatalf("hint row must sit above the status line:\n%s", strip)
	}
	if !strings.Contains(out, "229;192;123") {
		t.Fatalf("the first blank row must carry the indicator style:\n%s", out)
	}
	lines := strings.Split(strip, "\n")
	if len(lines) < 24 || lines[0] != strings.Repeat(" ", 80) {
		t.Fatalf("the list area must fill with blank rows:\n%s", strip)
	}
	// loading: the progress bar rides the same status line
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	if !strings.Contains(m.View().Content, "refresh 1/5") {
		t.Fatalf("loading empty view must render the bar:\n%s", m.View().Content)
	}
}

// TestProgressBarEmptyViewHints pins the empty+progress render path:
// the keyhint row renders above the status line exactly like the
// populated index render (R9 slot reservation - the bar view never
// drops the hint row).
func TestProgressBarEmptyViewHints(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 120, 24
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	strip := stripANSI(m.View().Content)
	if !strings.Contains(strip, "refresh 1/5") {
		t.Fatalf("empty view must still render the status line:\n%s", strip)
	}
	if !strings.Contains(strip, "j cursor-down") {
		t.Fatalf("empty view must render the keyhint row:\n%s", strip)
	}
	if si, sh := strings.Index(strip, "j cursor-down"), strings.Index(strip, statusMarker("0")); si < 0 || si > sh {
		t.Fatalf("hint row must sit above the status line:\n%s", strip)
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
	m := New(view, nil, testBindings, testTagActions, nil, st, cfg.UI)
	m.width, m.height = 80, 24
	if out := m.View().Content; strings.Contains(out, "255;0;0") {
		t.Fatalf("dark theme must not render the red status fg:\n%s", out)
	}
	if err := st.SetThemeVariant("red"); err != nil {
		t.Fatal(err)
	}
	m = pressEvent(t, m, core.ConfigChanged{Section: "theme"})
	if out := m.View().Content; !strings.Contains(out, "255;0;0") {
		t.Fatalf("variant switch must re-render the status line in the new color:\n%s", out)
	}
}

// TestPagerRestylesOnThemeSwitch pins the live variant switch for an
// open pager: the pager's render is cached, so the theme change must
// re-style it - the old colors must not linger until the next resize
// or re-open.
func TestPagerRestylesOnThemeSwitch(t *testing.T) {
	cfg := config.Default()
	cfg.Theme.Variants["red"] = config.StyleTable{Pager: config.PagerStyleTable{Header: config.Style{Fg: "#ff0000"}}}
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings, testTagActions, nil, st, cfg.UI)
	m.width, m.height = 80, 24
	path := fixtureMsg(t, "body line\n")
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Msgs:     []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}},
		}})
		m = next.(Model)
	})
	press(t, m, "o")
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	if out := m.View().Content; strings.Contains(out, "255;0;0") {
		t.Fatalf("dark theme must not render the red pager header:\n%s", out)
	}
	if err := st.SetThemeVariant("red"); err != nil {
		t.Fatal(err)
	}
	m = pressEvent(t, m, core.ConfigChanged{Section: "theme"})
	if out := m.View().Content; !strings.Contains(out, "255;0;0") {
		t.Fatalf("variant switch must re-style the open pager:\n%s", out)
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
	m := New(view, ch, testBindings, testTagActions, bus, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, ch, testBindings, testTagActions, bus, config.NewStore(config.Default()), config.Default().UI)
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
	if !strings.Contains(m.View().Content, "refresh 2/5") {
		t.Fatalf("bar must show this view's progress:\n%s", m.View().Content)
	}
	// completion clears only this view's bar
	bus.Publish(core.Progress{Job: "refresh", View: "inbox", Done: 5, Total: 5})
	m = pump(t, m, ch)
	if m.progressOn {
		t.Fatal("bar must clear on this view's completion")
	}
}

// fixtureMsg writes a synthetic message file and returns its path (mail
// content in tests is synthetic; no real mail is used).
func fixtureMsg(t *testing.T, body string) string {
	t.Helper()
	msg := "From: a@example.com\nTo: b@example.com\nSubject: hello\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: text/plain; charset=utf-8\n\n" + body
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// rowsModel builds a view of n single-message threads (n rows) - the
// count tests need more rows than the two-message model() fixture.
func rowsModel(n int) Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	var threads []*core.Thread
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%d", i)
		threads = append(threads, core.NewThread(id, []*core.Message{
			{ID: fmt.Sprintf("m%d", i), Timestamp: int64(i), Author: "Ann", Subject: "s", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(threads)
	return New(view, nil, testBindings, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
}

// openPager presses "o" with an open handler that injects the loaded
// thread as a bus event, mirroring the app's worker publish path. The
// handler updates the model synchronously, so the returned model
// carries the pager state.
func openPager(t *testing.T, m Model, path string) Model {
	t.Helper()
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Msgs:     []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}},
		}})
		m = next.(Model)
	})
	press(t, m, "o")
	return m
}

func TestOpenSwitchesToPager(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager mode, mode=%q", m.mode)
	}
	if m.pager == nil || len(m.pager.lines) == 0 {
		t.Fatal("pager content missing")
	}
	out := stripANSI(m.View().Content)
	for _, want := range []string{"hello", "a@example.com", "body line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("pager render missing %q:\n%s", want, out)
		}
	}
}

func TestPagerBackReturnsToIndex(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager mode, mode=%q", m.mode)
	}
	m = press(t, m, "q")
	if m.mode != "index" {
		t.Fatalf("q in pager mode must return to index, mode=%q", m.mode)
	}
}

func TestPagerKeyOnlyActiveInPager(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	if m.mode != "pager" || m.pager == nil {
		t.Fatal("o must open the pager")
	}
	cur := m.CursorIndex()
	m = press(t, m, "j")
	if m.pager.vp.offset != 1 {
		t.Fatalf("j in pager mode must scroll the window one line, offset=%d", m.pager.vp.offset)
	}
	if m.CursorIndex() != cur {
		t.Fatalf("j in pager mode must not move the cursor")
	}
	m = press(t, m, "G")
	want := len(m.pager.lines) - m.pager.vp.height
	if m.pager.vp.offset != want {
		t.Fatalf("G must jump to the last page, offset=%d want=%d", m.pager.vp.offset, want)
	}
	m = press(t, m, "g")
	if m.pager.vp.offset != 0 {
		t.Fatalf("g must jump to the first line, offset=%d", m.pager.vp.offset)
	}
	m = press(t, m, "q")
	if m.mode != "index" {
		t.Fatalf("q must return to index, mode=%q", m.mode)
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("j in index mode must move the cursor, got %d", m.CursorIndex())
	}
}

func TestKeyhintBar(t *testing.T) {
	km := map[string]string{"j": "cursor-down", "q": "quit"}
	hint := keyhintRow(km, 30)
	if !strings.Contains(hint, "j cursor-down") || !strings.Contains(hint, "q quit") {
		t.Fatalf("hint must derive from the binding map: %q", hint)
	}
	if runewidth.StringWidth(hint) > 30 {
		t.Fatalf("hint exceeds width: %q", hint)
	}
	if w := runewidth.StringWidth(keyhintRow(km, 10)); w > 10 {
		t.Fatalf("narrow hint must truncate to the width, got %d cells: %q", w, keyhintRow(km, 10))
	}
}

// TestKeyhintRowInView pins the hint row in the render: the active
// context's binding map renders above the status line in index mode,
// and the pager context's table replaces it in pager mode.
func TestKeyhintRowInView(t *testing.T) {
	m := model()
	m.width, m.height = 120, 24
	strip := stripANSI(m.View().Content)
	if !strings.Contains(strip, "j cursor-down") {
		t.Fatalf("index hint row missing:\n%s", strip)
	}
	status := statusMarker("2")
	if si, sh := strings.Index(strip, "j cursor-down"), strings.Index(strip, status); si < 0 || si > sh {
		t.Fatalf("hint row must sit above the status line:\n%s", strip)
	}
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	strip = stripANSI(m.View().Content)
	if !strings.Contains(strip, "j scroll-down") {
		t.Fatalf("pager hint row missing:\n%s", strip)
	}
	if strings.Contains(strip, "j cursor-down") {
		t.Fatalf("pager must not show index bindings:\n%s", strip)
	}
}

func TestPagerKeysOnlyInPager(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
	})})
	// q is bound only in the pager context: in index mode it must be a
	// no-op (no quit, no back), in pager mode it returns to index
	m := New(view, nil, map[string]map[string]string{"index": {"o": "open"}, "pager": {"q": "back"}}, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 40, 10
	next, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("q unbound in index mode must not quit")
	}
	if m.mode != "index" {
		t.Fatalf("q unbound in index mode must not change mode, mode=%q", m.mode)
	}
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	if m.mode != "pager" {
		t.Fatalf("o must open the pager, mode=%q", m.mode)
	}
	m = press(t, m, "q")
	if m.mode != "index" {
		t.Fatalf("q in pager mode must return to index, mode=%q", m.mode)
	}
}

func TestPagerQuitKeyExits(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
	})})
	// the emacs pager binds q to quit: in pager mode the key exits the
	// app (the spec's "quit in the pager exits the app; back returns to
	// the index - both bound")
	m := New(view, nil, map[string]map[string]string{"index": {"o": "open"}, "pager": {"q": "quit"}}, testTagActions, nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	_, cmd := m.Update(tea.KeyPressMsg{Text: "q", Code: 'q'})
	if cmd == nil {
		t.Fatal("q bound to quit in pager mode must return a quit command")
	}
}

func TestPagerPageKeys(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	// real ctrl+d/ctrl+u keys: KeyMsg.String() resolves to "ctrl+d" and
	// the dispatch finds the page-down/page-up bindings
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = next.(Model)
	if m.pager.vp.offset != 4 {
		t.Fatalf("ctrl+d must scroll half a window, offset=%d", m.pager.vp.offset)
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = next.(Model)
	if m.pager.vp.offset != 0 {
		t.Fatalf("ctrl+u must scroll back half a window, offset=%d", m.pager.vp.offset)
	}
}

func TestPagerReopenPreservesContentAndScroll(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	path := fixtureMsg(t, strings.Repeat("line\n", 30))
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Msgs:     []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}},
		}})
		m = next.(Model)
	})
	press(t, m, "o") // the handler rebinds m with the loaded pager
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	m = press(t, m, "j")
	if m.pager.vp.offset != 1 {
		t.Fatalf("j must scroll the window, offset=%d", m.pager.vp.offset)
	}
	m = press(t, m, "q")
	if m.mode != "index" {
		t.Fatalf("back must return to index, mode=%q", m.mode)
	}
	press(t, m, "o") // re-open the same thread - the guard skips re-render
	if m.mode != "pager" {
		t.Fatalf("re-open must switch to pager, mode=%q", m.mode)
	}
	if len(m.pager.lines) == 0 {
		t.Fatal("pager content must survive re-open")
	}
	if m.pager.vp.offset != 1 {
		t.Fatalf("scroll position must survive re-open, offset=%d", m.pager.vp.offset)
	}
}

func TestPagerResizeInIndexModeUpdatesWidth(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	path := fixtureMsg(t, strings.Repeat("line\n", 30))
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Msgs:     []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}},
		}})
		m = next.(Model)
	})
	press(t, m, "o")
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	press(t, m, "q") // back to index, pager kept alive
	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = next.(Model)
	if m.pager.vp.width != 40 || m.pager.vp.height != 8 {
		t.Fatalf("resize in index mode must re-size the pager window: %dx%d", m.pager.vp.width, m.pager.vp.height)
	}
	press(t, m, "o") // re-open the same thread - render happens at the new width
	if m.mode != "pager" {
		t.Fatalf("re-open must switch to pager, mode=%q", m.mode)
	}
	if m.pager.vp.width != 40 {
		t.Fatalf("re-open must render at the resized width, got %d", m.pager.vp.width)
	}
}

// TestNumberColumnGrows pins the variable number slot: its width
// tracks the widest row number (12 rows -> 2 cells), so single-digit
// rows pad to it and the column never re-aligns per row.
func TestNumberColumnGrows(t *testing.T) {
	m := rowsModel(12)
	m.width, m.height = 80, 24
	lines := strings.Split(stripANSI(m.View().Content), "\n")
	if !strings.HasPrefix(lines[0], "1  ") {
		t.Fatalf("row 1 must pad to the 2-cell slot: %q", lines[0])
	}
	if !strings.HasPrefix(lines[9], "10 ") {
		t.Fatalf("row 10 must fit the 2-cell slot: %q", lines[9])
	}
}

// TestArrowKeysMoveCursor pins the config overlay: up/down are bound to
// the cursor actions in the index context (the defaults are untouched,
// the user's config file adds the keys).
func TestArrowKeysMoveCursor(t *testing.T) {
	m := rowsModel(3)
	m = pressType(t, m, tea.KeyDown)
	if m.CursorIndex() != 1 {
		t.Fatalf("down must move the cursor down, got %d", m.CursorIndex())
	}
	m = pressType(t, m, tea.KeyUp)
	if m.CursorIndex() != 0 {
		t.Fatalf("up must move the cursor up, got %d", m.CursorIndex())
	}
}

func TestArrowKeysScrollPager(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	m = pressType(t, m, tea.KeyDown)
	if m.pager.vp.offset != 1 {
		t.Fatalf("down in pager mode must scroll down, offset=%d", m.pager.vp.offset)
	}
	m = pressType(t, m, tea.KeyUp)
	if m.pager.vp.offset != 0 {
		t.Fatalf("up in pager mode must scroll back, offset=%d", m.pager.vp.offset)
	}
}

// TestGGTopGToBottom pins the chained command: g is unbound in the index
// context, so the first g arms the chain and the second fires the top
// jump (G bound to cursor-bottom jumps the other way). Any other key
// clears the chain.
func TestGGTopGToBottom(t *testing.T) {
	m := rowsModel(3)
	m = press(t, m, "G")
	if m.CursorIndex() != 2 {
		t.Fatalf("G must jump the cursor to the last row, got %d", m.CursorIndex())
	}
	m = press(t, m, "g")
	if m.CursorIndex() != 2 {
		t.Fatalf("a single g must not move, got %d", m.CursorIndex())
	}
	m = press(t, m, "g")
	if m.CursorIndex() != 0 {
		t.Fatalf("gg must jump the cursor to the first row, got %d", m.CursorIndex())
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("a move after the chain must clear it, got %d", m.CursorIndex())
	}
}

// TestCountedMove pins the digit prefix: 3j moves 3 rows, 2k moves back,
// 99j clamps at the last row. The window is sized so the moves stay
// inside one page (a counted move crossing a page edge pages - pinned
// separately in TestIndexPagesAtEdges).
func TestCountedMove(t *testing.T) {
	m := rowsModel(5)
	m.width, m.height = 80, 24
	m = press(t, m, "3")
	m = press(t, m, "j")
	if m.CursorIndex() != 3 {
		t.Fatalf("3j must move 3 rows, got %d", m.CursorIndex())
	}
	m = press(t, m, "2")
	m = press(t, m, "k")
	if m.CursorIndex() != 1 {
		t.Fatalf("2k must move 2 rows back, got %d", m.CursorIndex())
	}
	m = press(t, m, "9")
	m = press(t, m, "9")
	m = press(t, m, "j")
	if m.CursorIndex() != 4 {
		t.Fatalf("99j must clamp at the last row, got %d", m.CursorIndex())
	}
}

// TestIndexPagesAtEdges pins the read-position model in the index: the
// window holds still while the cursor moves within the page (row 1
// stays the top line); only when the cursor crosses the bottom edge
// does the window jump a full page and the cursor land on the new
// page's first line (up: the new page's last line).
func TestIndexPagesAtEdges(t *testing.T) {
	m := rowsModel(60)
	m.width, m.height = 80, 24
	h := m.listHeight()
	for i := 0; i < h-1; i++ {
		m = press(t, m, "j")
	}
	if m.CursorIndex() != h-1 || m.indexOffset != 0 {
		t.Fatalf("j must hold the window until the page edge, cursor=%d offset=%d", m.CursorIndex(), m.indexOffset)
	}
	m = press(t, m, "j")
	if m.CursorIndex() != h || m.indexOffset != h {
		t.Fatalf("j past the bottom edge must page down, cursor=%d offset=%d", m.CursorIndex(), m.indexOffset)
	}
	lines := strings.Split(stripANSI(m.View().Content), "\n")
	if !strings.HasPrefix(lines[0], "23") {
		t.Fatalf("the new page must render from its first row: %q", lines[0])
	}
	m = press(t, m, "k")
	if m.CursorIndex() != h-1 || m.indexOffset != 0 {
		t.Fatalf("k at the top edge must page up, cursor=%d offset=%d", m.CursorIndex(), m.indexOffset)
	}
}

func TestCountResetsOnOtherKey(t *testing.T) {
	m := rowsModel(5)
	m = press(t, m, "4")
	m = press(t, m, "u") // undo: no staged ops, no-op - but clears the count
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("a non-movement key must clear the count, got %d", m.CursorIndex())
	}
}

// TestPagerLineScrollClamps pins the line-scroll model (the glow
// design): j/k move the scroll window one line per press - every press
// changes every visible line, so the renderer repaints the window; the
// clamp pins the tail at the bottom and the head at the top.
func TestPagerLineScrollClamps(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	h := m.pager.vp.height
	for i := 0; i < h; i++ {
		m = press(t, m, "j")
	}
	if m.pager.vp.offset != h {
		t.Fatalf("j must scroll line by line, offset=%d want=%d", m.pager.vp.offset, h)
	}
	for i := 0; i < 30; i++ {
		m = press(t, m, "j")
	}
	want := len(m.pager.lines) - h
	if m.pager.vp.offset != want {
		t.Fatalf("j at the bottom must clamp to the last page, offset=%d want=%d", m.pager.vp.offset, want)
	}
	for i := 0; i < 30; i++ {
		m = press(t, m, "k")
	}
	if m.pager.vp.offset != 0 {
		t.Fatalf("k at the top must clamp to the first page, offset=%d", m.pager.vp.offset)
	}
}

func TestThreadLoadedParseFailureShowsErrorLine(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("this is not a mail message at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := model()
	m.width, m.height = 80, 24
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Msgs:     []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{bad}}},
		}})
		m = next.(Model)
	})
	press(t, m, "o")
	if m.mode != "pager" {
		t.Fatalf("a parse failure must open the pager with an error line, mode=%q", m.mode)
	}
	out := stripANSI(m.View().Content)
	if !strings.Contains(out, "failed to parse message") {
		t.Fatalf("error line missing:\n%s", out)
	}
}

func TestThreadLoadedEmptyFallsBackToIndex(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{ThreadID: threadID}})
		m = next.(Model)
	})
	press(t, m, "o")
	if m.mode != "index" || m.pager != nil {
		t.Fatalf("an empty thread reply must stay in index, mode=%q pager=%v", m.mode, m.pager != nil)
	}
}

func TestThreadLoadedErrorFallsBackToIndex(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	SetOpenHandler(func(threadID string) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{ThreadID: threadID, Err: errors.New("boom")}})
		m = next.(Model)
	})
	m = press(t, m, "o")
	if m.mode != "index" {
		t.Fatalf("a failed load must stay in index, mode=%q", m.mode)
	}
	if m.pager != nil {
		t.Fatal("a failed load must drop the pager")
	}
}
