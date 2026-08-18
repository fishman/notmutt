// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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

	"github.com/mattn/go-runewidth"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// statusMarker is the unstyled view+count boundary on the status row:
// the view pill, the bar gap, and the count pill.
func statusMarker(count string) string {
	return "inbox" + strings.Repeat(pillGap, 3) + count
}

// testBindings is the per-context binding table (R9): the mutt scheme
// from the embedded base config (config.Default derives it), plus the
// live config's arrow-key overlay - the test table mirrors the user
// config exactly, so a scheme change propagates to the tests.
func testBindings() map[string]map[string]string {
	km := config.Default().Bindings
	km["index"]["up"] = "cursor-up"
	km["index"]["down"] = "cursor-down"
	km["pager"]["up"] = "scroll-up"
	km["pager"]["down"] = "scroll-down"
	return km
}

// testTagActions mirrors the embedded base config's tag actions.
func testTagActions() map[string]string {
	return config.Default().TagActions
}

// TestActionsCoverComposeAndFuzzy pins the action vocabulary: the
// compose and fuzzy contexts exist in the builtin table, and the tab
// actions are bound in every tabbed context (R4 tabs).
func TestActionsCoverComposeAndFuzzy(t *testing.T) {
	km := testBindings()
	for _, ctx := range []string{"compose", "fuzzy"} {
		if len(Actions[ctx]) == 0 {
			t.Fatalf("Actions[%q] must cover the context", ctx)
		}
	}
	for _, ctx := range []string{"index", "pager"} {
		if !Actions[ctx]["tab-prev"] || !Actions[ctx]["tab-next"] {
			t.Fatalf("Actions[%q] must carry tab-prev/tab-next", ctx)
		}
	}
	// the help overlay (?) is bound and builtin in every tabbed context
	for _, ctx := range []string{"index", "pager", "compose"} {
		if !Actions[ctx]["help"] {
			t.Errorf("Actions[%q] must define help", ctx)
		}
		if km[ctx]["?"] != "help" {
			t.Errorf("bindings[%q] must bind ? to help", ctx)
		}
	}
	// the log overlay (~) is bound and builtin in every tabbed context
	for _, ctx := range []string{"index", "pager", "compose"} {
		if !Actions[ctx]["log"] {
			t.Errorf("Actions[%q] must define log", ctx)
		}
		if km[ctx]["~"] != "log" {
			t.Errorf("bindings[%q] must bind ~ to log", ctx)
		}
	}
}

// TestChainDataCompletes pins the data-driven chain resolver: the
// armed prefix is listed in the keyhint (queryable data, R9), and the
// second key completes the chain into the bound action.
func TestChainDataCompletes(t *testing.T) {
	got := ""
	SetReplyHandler(func(msg *core.Message, mode string) { got = mode })
	defer SetReplyHandler(func(msg *core.Message, mode string) {})
	m := model()
	// g g completes as cursor-top (j moved the cursor down first)
	m = press(t, m, "j")
	m = press(t, m, "g")
	m = press(t, m, "g")
	if m.CursorIndex() != 0 {
		t.Fatalf("g g must move the cursor to the top, idx=%d", m.CursorIndex())
	}
	// an armed prefix lists its visible continuations in the keyhint
	// (g g is a hidden generic binding - the hint shows what the
	// prefix can do, the hidden flag rules both views)
	m = press(t, m, "g")
	frame := m.render()
	clean := stripANSI(frame)
	if !strings.Contains(clean, "g r reply-all") || strings.Contains(clean, "g g cursor-top") {
		t.Fatalf("the armed prefix must list its visible chains:\n%s", clean)
	}
	if strings.Contains(clean, "j cursor-down") {
		t.Fatalf("an armed prefix must replace the base hint:\n%s", clean)
	}
	// g r completes as reply-all
	m = press(t, m, "r")
	if got != "reply-all" {
		t.Fatalf("g r must open a reply-all, got %q", got)
	}
}

// TestChainExpires pins the chain timeout: an expired prefix never
// dispatches the stale completion - the next key acts on its own.
// A chain-starting key re-arms on the expired press instead of
// wasting it, so the hint still shows what the chain can become.
func TestChainExpires(t *testing.T) {
	old := chainTimeout
	chainTimeout = 0
	defer func() { chainTimeout = old }()
	got := ""
	SetReplyHandler(func(msg *core.Message, mode string) { got = mode })
	defer SetReplyHandler(func(msg *core.Message, mode string) {})
	m := model()
	m = press(t, m, "g") // arms the prefix; the next press sees it expired
	m = press(t, m, "x") // an unbound key: the stale g r must not fire
	if got != "" {
		t.Fatalf("an expired chain must not dispatch, got %q", got)
	}
	// the expired chain-starting key re-arms the prefix: the armed
	// hint lists only the visible continuations, not the base bindings
	m = press(t, m, "g")
	m = press(t, m, "g")
	clean := stripANSI(m.render())
	if !strings.Contains(clean, "g r reply-all") || strings.Contains(clean, "j cursor-down") {
		t.Fatalf("an expired chain-starting key must re-arm the prefix:\n%s", clean)
	}
}

// TestChainExpiryResetsKeyhint pins the expiry tick: an armed chain
// whose timeout elapses resets the keyhint to the base bindings - the
// continuation view must not stay stuck until the next keypress.
func TestChainExpiryResetsKeyhint(t *testing.T) {
	old := chainTimeout
	chainTimeout = 0
	defer func() { chainTimeout = old }()
	m := model()
	m = press(t, m, "g")
	clean := stripANSI(m.render())
	if !strings.Contains(clean, "g r reply-all") || strings.Contains(clean, "j cursor-down") {
		t.Fatalf("the armed prefix must list only its visible chains:\n%s", clean)
	}
	next, _ := m.Update(chainTick{})
	m = next
	clean = stripANSI(m.render())
	if !strings.Contains(clean, "$ apply") {
		t.Fatalf("the expired chain must reset to the base hint:\n%s", clean)
	}
}

// TestHelpListsBindings pins the ? overlay: a full-frame binding list
// for the active context - the neomutt help columns over a viewport
// (the pager widget), scrolled by the pager keys, closed by any other
// keypress without firing it.
func TestHelpListsBindings(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = press(t, m, "?")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the help frame must be exactly 24 lines, got %d", got)
	}
	clean := stripANSI(frame)
	if !strings.Contains(clean, "help: index bindings") {
		t.Fatalf("the help must title the context:\n%s", clean)
	}
	// the neomutt help columns (help.c): key, function, description.
	// The columns pad to their widest entry (the fixed two-space gap
	// stays): the widest row aligns exactly, descriptions are
	// padding-robust by position
	if !strings.Contains(clean, "ctrl+d  half-page-down   Scroll down half a page") ||
		!strings.Contains(clean, "Move the cursor down") ||
		!strings.Contains(clean, "Reply to all recipients") {
		t.Fatalf("the help must list the bindings with descriptions:\n%s", clean)
	}
	// the footer derives from the pager binding data (the surface
	// borrows the pager keys)
	if !strings.Contains(clean, "down/j/k/up scroll  q closes  ? help") {
		t.Fatalf("the help footer must derive from the bindings:\n%s", clean)
	}
	// G scrolls the help to the bottom (a viewport like the mail
	// pager): the t row is past the first frame (26 index rows in a
	// 21-row window)
	m = press(t, m, "G")
	clean = stripANSI(m.render())
	if !strings.Contains(clean, "apply tag unread") {
		t.Fatalf("the help must scroll to the tag action rows:\n%s", clean)
	}
	// a non-pager key closes; a pager key (j) scrolls instead
	m = press(t, m, "x")
	if m.help {
		t.Fatal("a keypress must close the help")
	}
	if m.CursorIndex() != 0 {
		t.Fatalf("the closing key must be consumed (cursor unmoved), idx=%d", m.CursorIndex())
	}
}

// TestLogEntryStatus pins the log write path: a lua result appends to
// the session log and shows as the status line's last entry, and the
// entry survives the next keypress - the status message is persistent,
// not transient. An error entry replaces it styled as error.
func TestLogEntryStatus(t *testing.T) {
	m := model()
	next, _ := m.Update(EventMsg{Event: core.LuaResult{Output: "hello"}})
	m = next
	if m.statusMsg != "hello" || m.statusMsgErr {
		t.Fatalf("the lua result must surface as the last log entry: %q err=%v", m.statusMsg, m.statusMsgErr)
	}
	if len(m.log) != 1 || m.log[0].text != "hello" || m.log[0].err {
		t.Fatalf("log = %+v", m.log)
	}
	m = press(t, m, "j")
	if m.statusMsg != "hello" {
		t.Fatalf("a keypress must not clear the log entry, got %q", m.statusMsg)
	}
	next, _ = m.Update(EventMsg{Event: core.LuaResult{Err: errors.New("boom")}})
	m = next
	if m.statusMsg != "lua: boom" || !m.statusMsgErr {
		t.Fatalf("the lua error must replace the entry styled as error: %q err=%v", m.statusMsg, m.statusMsgErr)
	}
}

// TestJobErrorSurfaces pins the JobError and lock-timeout surfaces: a
// failed background job logs an error entry (previously the TUI
// dropped both events).
func TestJobErrorSurfaces(t *testing.T) {
	m := model()
	next, _ := m.Update(EventMsg{Event: core.JobError{Job: "apply", Err: errors.New("lock wait")}})
	m = next
	if m.statusMsg != "apply: lock wait" || !m.statusMsgErr {
		t.Fatalf("the job error must surface styled as error: %q err=%v", m.statusMsg, m.statusMsgErr)
	}
	next, _ = m.Update(EventMsg{Event: core.WorkerLockTimeout{Kind: "tag"}})
	m = next
	if m.statusMsg != "lock timeout: tag" || !m.statusMsgErr {
		t.Fatalf("the lock timeout must surface styled as error: %q err=%v", m.statusMsg, m.statusMsgErr)
	}
}

// TestFilterDoneSurfaces pins the filter summary line (R2): the run's
// counts land on the status line as an info entry.
func TestFilterDoneSurfaces(t *testing.T) {
	m := model()
	next, _ := m.Update(EventMsg{Event: core.FilterDone{DryRun: true, Entries: 3, Moves: 1, Skips: 2}})
	m = next
	if m.statusMsg != "filter: 3 entries, 1 moved, 2 skipped (dry-run)" || m.statusMsgErr {
		t.Fatalf("filter summary = %q err=%v", m.statusMsg, m.statusMsgErr)
	}
}

// TestLogRingCaps pins the ring cap: beyond logCap entries the oldest
// drop.
func TestLogRingCaps(t *testing.T) {
	m := model()
	for i := 0; i < logCap+5; i++ {
		next, _ := m.Update(EventMsg{Event: core.LuaResult{Output: "e"}})
		m = next
	}
	if len(m.log) != logCap {
		t.Fatalf("the log ring must cap at %d, got %d", logCap, len(m.log))
	}
	if m.statusMsg != "e" {
		t.Fatalf("the last entry must surface, got %q", m.statusMsg)
	}
}

// TestLogOverlay pins the ~ overlay: the session log opens over the
// frame (the help mechanism - pager scroll keys navigate, any other
// key closes), and opening one overlay closes the other.
func TestLogOverlay(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	for i := 0; i < 3; i++ {
		next, _ = m.Update(EventMsg{Event: core.LuaResult{Output: fmt.Sprintf("entry %d", i)}})
		m = next
	}
	m = press(t, m, "~")
	if !m.logOpen {
		t.Fatal("~ must open the log overlay")
	}
	frame := stripANSI(m.render())
	if strings.Count(frame, "\n")+1 != 24 {
		t.Fatalf("the log frame must be exactly 24 lines, got %d", strings.Count(frame, "\n")+1)
	}
	for _, want := range []string{"log", "entry 0", "entry 1", "entry 2"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("the log must render %q:\n%s", want, frame)
		}
	}
	// the overlays intercept keys before dispatch (the help behavior):
	// ? closes the log, ~ closes the help; each re-opens on its own
	// next press - never both open at once
	m = press(t, m, "?")
	if m.logOpen || m.help {
		t.Fatal("? must close the log overlay")
	}
	m = press(t, m, "?")
	if !m.help {
		t.Fatal("? must open the help")
	}
	m = press(t, m, "~")
	if m.logOpen || m.help {
		t.Fatal("~ must close the help overlay")
	}
	// a non-pager key closes; the closing key is consumed
	m = press(t, m, "x")
	if m.logOpen {
		t.Fatal("a keypress must close the log")
	}
	if m.CursorIndex() != 0 {
		t.Fatalf("the closing key must be consumed (cursor unmoved), idx=%d", m.CursorIndex())
	}
}

// TestLogOverlayScroll pins the overlay's viewport: enough entries to
// overflow, opened pinned to the tail, g scrolls to the top, ctrl+d
// returns half a window.
func TestLogOverlayScroll(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	for i := 0; i < 30; i++ {
		next, _ = m.Update(EventMsg{Event: core.LuaResult{Output: fmt.Sprintf("entry %d", i)}})
		m = next
	}
	m = press(t, m, "~")
	if m.logView.offset != 10 { // 30 entries in a 20-row window, tail-pinned
		t.Fatalf("the log must open at the tail, offset=%d", m.logView.offset)
	}
	m = press(t, m, "g") // scroll-top (the pager binding the overlay borrows)
	if m.logView.offset != 0 {
		t.Fatalf("g must scroll the log to the top, offset=%d", m.logView.offset)
	}
	next, _ = m.Update(KeyPressMsg{Code: 'd', Mod: modCtrl})
	m = next
	if m.logView.offset != 10 {
		t.Fatalf("ctrl+d must scroll the log back half a window, offset=%d", m.logView.offset)
	}
}

// TestSendPhaseGuardNoDoubleSend pins the send gate (Task 14 quality
// review note): while the job is in flight (PhaseSending) a second
// send press must not launch a duplicate delivery. The detach/attach
// gates share the same phase check - one test pins the mechanism.
func TestSendPhaseGuardNoDoubleSend(t *testing.T) {
	calls := 0
	SetSendHandler(func(st compose.State) { calls++ })
	defer SetSendHandler(func(st compose.State) {})
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "y")
	m = press(t, m, "y")
	if calls != 1 {
		t.Fatalf("a second send press during PhaseSending must no-op, calls=%d", calls)
	}
}

// TestSendGatesDetachAttachDuringSending pins the PhaseSending gates
// on the mutation keys: an in-flight delivery owns the Attachments
// slice (sendJob's Assemble reads it), so detach and attach must no-op
// while the job runs - not only double-send.
func TestSendGatesDetachAttachDuringSending(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Phase = compose.PhaseSending
	att := compose.Attachment{Name: "a.txt", Path: "/tmp/a.txt", Size: 3}
	m.tabs[0].Attachments = []compose.Attachment{att}
	m.formIdx = 9 // an attachment slot
	m = press(t, m, "d")
	if len(m.tabs[0].Attachments) != 1 || m.tabs[0].Attachments[0] != att {
		t.Fatalf("d during PhaseSending must not mutate the attachments: %+v", m.tabs[0].Attachments)
	}
	m = press(t, m, "a")
	if m.dialogue != nil {
		t.Fatal("a during PhaseSending must not open the prompt")
	}
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("phase = %v", m.tabs[0].Phase)
	}
}

// TestAbortNoOpDuringSending pins the abort gate: q must not arm the
// abort confirm while the delivery is in flight - the tab closes when
// the send result lands.
func TestAbortNoOpDuringSending(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Phase = compose.PhaseSending
	m = press(t, m, "q")
	m = press(t, m, "q")
	if len(m.tabs) != 1 {
		t.Fatalf("q during PhaseSending must keep the tab, got %d", len(m.tabs))
	}
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("q during PhaseSending must not change the phase, got %v", m.tabs[0].Phase)
	}
}

// TestSendRetryReArmsGate pins the retry path: after a failure the
// first y re-arms PhaseSending and dispatches; a second press during
// the retry is gated (one job in flight).
func TestSendRetryReArmsGate(t *testing.T) {
	calls := 0
	SetSendHandler(func(st compose.State) { calls++ })
	defer SetSendHandler(func(st compose.State) {})
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Phase = compose.PhaseFailed
	m = press(t, m, "y")
	m = press(t, m, "y")
	if calls != 1 {
		t.Fatalf("a second send press during the retry must no-op, calls=%d", calls)
	}
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("phase = %v", m.tabs[0].Phase)
	}
}

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}, References: []string{"b"}},
		{ID: "b", Timestamp: 200, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}},
	})})
	cfg := config.Default()
	// generated accounts: tests never use real account names
	cfg.Accounts = map[string]config.Account{"alpha": {}, "beta": {}, "gamma": {}, "delta": {}}
	return New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(cfg), cfg.UI)
}

// TestOpenReplyGhostRow: reply on a ghost row (nil Msg, multi-root
// thread) must hand the thread id to the app seam - the app's
// thread-fetch fallback rehydrates the original instead of a silent
// no-op.
func TestOpenReplyGhostRow(t *testing.T) {
	var gotMsg *core.Message
	var gotMode string
	old := onReply
	onReply = func(msg *core.Message, mode string) {
		gotMsg, gotMode = msg, mode
	}
	defer func() { onReply = old }()

	m := ghostModel() // cursor starts on the ghost root row
	press(t, m, "r")
	if gotMode != "reply" || gotMsg == nil || gotMsg.ThreadID != "t1" {
		t.Fatalf("ghost reply must pass the thread id, got mode=%q msg=%+v", gotMode, gotMsg)
	}
}

// ghostModel builds a thread whose messages share no reference chain:
// core emits a synthetic ghost root row (Msg == nil) at the thread start.
func ghostModel() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 200, Author: "Ann", Subject: "hello"},
		{ID: "b", Timestamp: 100, Author: "Bob", Subject: "re: hello"},
	})})
	return New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
}

func press(t *testing.T, m Model, key string) Model {
	t.Helper()
	next, _ := m.Update(KeyPressMsg{Text: key, Code: []rune(key)[0]})
	return next
}

// pressType presses a special key (arrows, ctrl+...): actionForKey
// resolves the canonical name via msg.String() ("up", "down", "ctrl+d").
func pressType(t *testing.T, m Model, k rune) Model {
	t.Helper()
	next, _ := m.Update(KeyPressMsg{Code: k})
	return next
}

// textD returns the active text dialogue (the form-entry prompt), or
// nil when the dialogue is another type.
func textD(m Model) *textDialogue {
	d, _ := m.dialogue.(*textDialogue)
	return d
}

// picker returns the active list-choose dialogue's fuzzy (the picker
// surface), or nil when the dialogue is not a chooser.
func picker(m Model) *fuzzy {
	switch d := m.dialogue.(type) {
	case *listDialogue:
		return d.f
	case *fileDialogue:
		return d.f // promoted
	}
	return nil
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
	return New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
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
	m = press(t, m, "t") // soft toggle resolves against the thread's tags
	if !m.view.IsStaged("t:t1") {
		t.Fatal("t on the unread stub must stage t:t1")
	}
	ops, _ := m.view.StagedOps()
	if got := ops["t:t1"]; len(got) != 1 || got[0] != (core.TagOp{Tag: "unread", Add: false}) {
		t.Fatalf("t on the unread stub must stage -unread for the thread: %v", got)
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

// TestGotoDispatch pins the goto-<view> dispatch: the action drives
// the store (the single write path, R8), an unknown view is a no-op
// (load validation rejects it at startup), and the derived account key
// resolves through the chain machinery (g 1 -> goto-gmail from the
// placeholder account).
func TestGotoDispatch(t *testing.T) {
	m := model()
	m, _ = m.dispatchAction("goto-archive", 0)
	if m.st.Config().ActiveView != "archive" {
		t.Fatalf("active view = %q", m.st.Config().ActiveView)
	}
	m, _ = m.dispatchAction("goto-nope", 0)
	if m.st.Config().ActiveView != "archive" {
		t.Fatalf("unknown view must be a no-op: %q", m.st.Config().ActiveView)
	}
	m2 := model()
	m2 = press(t, m2, "g")
	m2 = press(t, m2, "1")
	if m2.st.Config().ActiveView != "gmail" {
		t.Fatalf("g 1 must go to the gmail view, active = %q", m2.st.Config().ActiveView)
	}
}

// TestFilterPrompt pins the live F filter: the prompt narrows the view
// on every key, backspace widens, esc restores the pre-open filter and
// closes, and the rows the model renders are the filtered set.
func TestFilterPrompt(t *testing.T) {
	m := stubModel() // two threads: "hello" (Ann), "re: hello" (Bob); both carry the inbox tag, so the filter text must hit the author field to discriminate
	m = press(t, m, "F")
	if d := textD(m); d == nil || d.field != "filter" {
		t.Fatalf("F must open the filter prompt: %+v", m.dialogue)
	}
	m = press(t, m, "bob")
	if len(m.rows) != 1 || m.rows[0].Msg.Author != "Bob" {
		t.Fatalf("typing must narrow live: %d rows", len(m.rows))
	}
	m = press(t, m, "x")
	if len(m.rows) != 0 {
		t.Fatalf("'bobx' must match nothing: %d rows", len(m.rows))
	}
	m = pressType(t, m, KeyBackspace)
	if len(m.rows) != 1 {
		t.Fatalf("backspace must widen again: %d rows", len(m.rows))
	}
	m = pressType(t, m, KeyEsc)
	if m.dialogue != nil {
		t.Fatal("esc must close the prompt")
	}
	if len(m.rows) != 2 {
		t.Fatalf("esc must restore the pre-open filter: %d rows", len(m.rows))
	}
	// the committed filter survives a reopen-edit cycle: enter keeps the
	// live filter, F reopens prefilled, esc restores the committed text
	m = press(t, m, "F")
	m = press(t, m, "a")
	if len(m.rows) != 1 {
		t.Fatalf("typing 'a' must narrow to Ann: %d rows", len(m.rows))
	}
	m = pressType(t, m, KeyEnter)
	if m.dialogue != nil {
		t.Fatal("enter must close the prompt")
	}
	if len(m.rows) != 1 {
		t.Fatalf("enter must keep the live filter: %d rows", len(m.rows))
	}
	m = press(t, m, "F")
	m = press(t, m, "b")
	if len(m.rows) != 0 {
		t.Fatalf("the reopened prompt must prefill 'a': %d rows", len(m.rows))
	}
	m = pressType(t, m, KeyEsc)
	if m.dialogue != nil {
		t.Fatal("esc must close the prompt")
	}
	if len(m.rows) != 1 {
		t.Fatalf("esc must restore the committed filter: %d rows", len(m.rows))
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
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
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
	_, cmd := m.Update(KeyPressMsg{Text: "q", Code: 'q'})
	if cmd == nil {
		t.Fatal("q must return a quit command")
	}
}

func TestEventMsgRepaints(t *testing.T) {
	m := model()
	m.view.SetCursor("a")
	next, nextCmd := m.Update(EventMsg{Event: core.ViewDiff{View: "inbox"}})
	if next.CursorIndex() != 1 {
		t.Fatalf("cursor by id after event = %d", next.CursorIndex())
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
	m = press(t, m, "t")
	// auto-advance: the cursor moved to the next row; the staged row is
	// the one under the previous cursor
	if m.CursorIndex() != 1 {
		t.Fatalf("t must advance the cursor one row, got %d", m.CursorIndex())
	}
	m = press(t, m, "k")
	row, _ = m.view.CursorRow()
	if !row.Staged || !hasTag(row.StagedTags, "unread") {
		t.Fatalf("t must stage +unread: staged=%v tags=%v", row.Staged, row.StagedTags)
	}
	if hasTag(row.Msg.Tags, "unread") {
		t.Fatalf("applied state must be untouched: %v", row.Msg.Tags)
	}
	m.width, m.height = 80, 24
	if out := m.View(); !strings.Contains(out, "*N") {
		t.Fatalf("staged glyph missing:\n%s", out)
	}
	m = press(t, m, "t")
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
	m = press(t, m, "t") // stage +unread on row 0, advance again
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
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
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
	m := New(view, nil, map[string]map[string]string{"index": {"x": "archive"}}, testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
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
	m = press(t, m, "t")
	ops, _ := m.view.StagedOps()
	got := ops["a"]
	if len(got) != 1 || got[0] != (core.TagOp{Tag: "unread", Add: false}) {
		t.Fatalf("t on the unread message must stage -unread: %v", got)
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
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
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

// TestComposeFormSanitizesControls pins the F1 rule on the compose
// form: Subject/To/Cc carry the replied-to message's headers
// (attacker-controlled), so the form rows must pass the same
// sanitizer as the index rows and the preview pane.
func TestComposeFormSanitizesControls(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Subject = "evil\x1b[31mred"
	m.tabs[0].To = []string{"x\x07y@example.com"}
	m.width, m.height = 80, 24
	out := m.View()
	for _, leak := range []string{"\x1b[31m", "\x07"} {
		if strings.Contains(out, leak) {
			t.Fatalf("control chars leaked into the compose form:\n%q", out)
		}
	}
	clean := stripANSI(out)
	if !strings.Contains(clean, "Subject: evil") || !strings.Contains(clean, "xy@example.com") {
		t.Fatalf("the sanitized fields must still render:\n%s", clean)
	}
}

func TestProgressBarRendersAndClears(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	if strings.Contains(m.View(), "refresh 5/10") {
		t.Fatal("no bar before any progress event")
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 5, Total: 10})
	out := m.View()
	if !strings.Contains(out, "refresh 5/10") {
		t.Fatalf("bar missing:\n%s", out)
	}
	if !strings.Contains(stripANSI(out), statusMarker("2")) {
		t.Fatalf("status line missing view + count:\n%s", out)
	}
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 10, Total: 10})
	if strings.Contains(m.View(), "refresh 10/10") {
		t.Fatal("bar must clear on completion")
	}
}

func TestProgressBarEmptyView(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 80, 24
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	if !strings.Contains(m.View(), "refresh 1/5") {
		t.Fatalf("empty view must still render the status line:\n%s", m.View())
	}
}

// TestEmptyViewLooksFilled pins the load-time surface: an empty view
// renders like a populated one - blank rows fill the list area (the
// indicator sits on the first, cursor-style), and the keyhint bar and
// status row always render, with or without a progress job. The
// literal "empty" text never appears.
func TestEmptyViewLooksFilled(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 160, 24
	out := m.View()
	if strings.Contains(out, "empty") {
		t.Fatalf("the literal empty text must not render:\n%s", out)
	}
	strip := stripANSI(out)
	if !strings.Contains(strip, statusMarker("0")) {
		t.Fatalf("idle empty view must still render the status line:\n%s", strip)
	}
	// the hint row truncates at the frame width (a row is a hint, not
	// a table), so the check anchors on the sorted row's head
	if si, sh := strings.Index(strip, "$ apply"), strings.Index(strip, statusMarker("0")); si < 0 || si > sh {
		t.Fatalf("hint row must sit above the status line:\n%s", strip)
	}
	// the cursor marker sits on the first blank row (indicator-styled
	// glyph, config data); the row fill itself stays normal
	if !strings.Contains(out, "229;192;123") {
		t.Fatalf("the first blank row must carry the indicator style:\n%s", out)
	}
	lines := strings.Split(strip, "\n")
	// the frame invariant: tabBar + list + keyhint + status = height.
	// One line over and the renderer writes out of bounds.
	if len(lines) != 24 || lines[1] != "▌"+strings.Repeat(" ", 159) {
		t.Fatalf("frame must be exactly the terminal height with blank list rows:\n%s", strip)
	}
	// loading: the progress bar rides the same status line
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	if !strings.Contains(m.View(), "refresh 1/5") {
		t.Fatalf("loading empty view must render the bar:\n%s", m.View())
	}
}

// TestDialogueKeepsKeyhint pins the overlay layout: the dialogue box
// splices ABOVE the keyhint bar, so the hotkey row (h-2) and the
// status line (h-1) stay visible while a prompt is open.
func TestDialogueKeepsKeyhint(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 160, 24
	m.dialogue = &confirmDialogue{label: "abort?", action: "abort"}
	strip := stripANSI(m.View())
	lines := strings.Split(strip, "\n")
	if len(lines) != 24 {
		t.Fatalf("frame = %d lines, want 24:\n%s", len(lines), strip)
	}
	if !strings.Contains(lines[22], "$ apply") {
		t.Fatalf("the keyhint row must survive the dialogue:\n%s", strip)
	}
	if !strings.Contains(lines[23], statusMarker("0")) {
		t.Fatalf("the status line must survive the dialogue:\n%s", strip)
	}
	if !strings.Contains(lines[20], "abort?") {
		t.Fatalf("the box content must sit above the keyhint bar:\n%s", strip)
	}
	if strings.Contains(lines[22], "abort?") {
		t.Fatalf("the box must not cover the keyhint row:\n%s", strip)
	}
}

// TestProgressBarEmptyViewHints pins the empty+progress render path:
// the keyhint row renders above the status line exactly like the
// populated index render (R9 slot reservation - the bar view never
// drops the hint row).
func TestProgressBarEmptyViewHints(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	m := New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 160, 24
	m = pressEvent(t, m, core.Progress{Job: "refresh", Done: 1, Total: 5})
	strip := stripANSI(m.View())
	if !strings.Contains(strip, "refresh 1/5") {
		t.Fatalf("empty view must still render the status line:\n%s", strip)
	}
	if !strings.Contains(strip, "$ apply") {
		t.Fatalf("empty view must render the keyhint row:\n%s", strip)
	}
	if si, sh := strings.Index(strip, "$ apply"), strings.Index(strip, statusMarker("0")); si < 0 || si > sh {
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
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
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
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	path := fixtureMsg(t, "body line\n")
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    loadedLines(t, []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}),
		}})
		m = next
	})
	press(t, m, "enter")
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	if out := m.View(); strings.Contains(out, "255;0;0") {
		t.Fatalf("dark theme must not render the red pager header:\n%s", out)
	}
	if err := st.SetThemeVariant("red"); err != nil {
		t.Fatal(err)
	}
	m = pressEvent(t, m, core.ConfigChanged{Section: "theme"})
	if out := m.View(); !strings.Contains(out, "255;0;0") {
		t.Fatalf("variant switch must re-style the open pager:\n%s", out)
	}
}

func pressEvent(t *testing.T, m Model, e core.Event) Model {
	t.Helper()
	next, _ := m.Update(EventMsg{Event: e})
	return next
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
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)
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
	if next.progressOn {
		t.Fatal("bar must clear from the snapshot even with the completion event dropped")
	}
}

func pump(t *testing.T, m Model, ch <-chan core.Event) Model {
	t.Helper()
	select {
	case e := <-ch:
		next, _ := m.Update(EventMsg{Event: e})
		return next
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
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)
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
	return New(view, nil, testBindings(), testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
}

// openPager presses the open key with an open handler that injects the
// loaded lines as a bus event. loadedLines renders a message set the
// way the app's open job does:
// handlers publish the same ThreadLoaded the app would (the model
// attaches lines, it never renders).
func loadedLines(t *testing.T, msgs []core.Message) []core.Line {
	t.Helper()
	lines, _, _, err := mail.RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// thread as a bus event, mirroring the app's worker publish path. The
// handler updates the model synchronously, so the returned model
// carries the pager state.
func openPager(t *testing.T, m Model, path string) Model {
	t.Helper()
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    loadedLines(t, []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}),
		}})
		m = next
	})
	press(t, m, "enter")
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
	out := stripANSI(m.View())
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
		t.Fatal("enter must open the pager")
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
	m.width, m.height = 160, 24
	strip := stripANSI(m.View())
	// the hint row truncates at the frame width, so the check anchors
	// on the sorted row's head
	if !strings.Contains(strip, "$ apply") {
		t.Fatalf("index hint row missing:\n%s", strip)
	}
	status := statusMarker("2")
	if si, sh := strings.Index(strip, "$ apply"), strings.Index(strip, status); si < 0 || si > sh {
		t.Fatalf("hint row must sit above the status line:\n%s", strip)
	}
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	strip = stripANSI(m.View())
	if !strings.Contains(strip, "q back") || strings.Contains(strip, "j scroll-down") {
		t.Fatalf("the pager hint must show the visible keys, not the hidden j/k:\n%s", strip)
	}
	if strings.Contains(strip, "g g cursor-top") {
		t.Fatalf("pager must not show index bindings:\n%s", strip)
	}
}

// TestKeyhintHidesPaging pins the hidden flag: the generic paging
// keys stay out of the keyhint row (the help dialog shows every
// binding - the ? overlay lists them with descriptions).
func TestKeyhintHidesPaging(t *testing.T) {
	m := model()
	m.width, m.height = 160, 24
	strip := stripANSI(m.View())
	if strings.Contains(strip, "half-page-down") || strings.Contains(strip, "page-down") {
		t.Fatalf("the paging bindings must stay out of the keyhint row:\n%s", strip)
	}
	m = press(t, m, "?")
	clean := stripANSI(m.render())
	if !strings.Contains(clean, "ctrl+d  half-page-down   Scroll down half a page") {
		t.Fatalf("the help dialog must list the hidden binding:\n%s", clean)
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
	m := New(view, nil, map[string]map[string]string{"index": {"enter": "open"}, "pager": {"q": "back"}}, testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 40, 10
	next, cmd := m.Update(KeyPressMsg{Text: "q", Code: 'q'})
	m = next
	if cmd != nil {
		t.Fatal("q unbound in index mode must not quit")
	}
	if m.mode != "index" {
		t.Fatalf("q unbound in index mode must not change mode, mode=%q", m.mode)
	}
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	if m.mode != "pager" {
		t.Fatalf("enter must open the pager, mode=%q", m.mode)
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
	m := New(view, nil, map[string]map[string]string{"index": {"enter": "open"}, "pager": {"q": "quit"}}, testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, "body line\n"))
	_, cmd := m.Update(KeyPressMsg{Text: "q", Code: 'q'})
	if cmd == nil {
		t.Fatal("q bound to quit in pager mode must return a quit command")
	}
}

func TestPagerPageKeys(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	// a real ctrl+d key: KeyMsg.String() resolves to "ctrl+d" and the
	// dispatch finds the half-page-down binding
	next, _ := m.Update(KeyPressMsg{Code: 'd', Mod: modCtrl})
	m = next
	if m.pager.vp.offset != 3 {
		t.Fatalf("ctrl+d must scroll half a window, offset=%d", m.pager.vp.offset)
	}
	m = press(t, m, "g") // scroll-top
	if m.pager.vp.offset != 0 {
		t.Fatalf("g must scroll back to the top, offset=%d", m.pager.vp.offset)
	}
}

func TestPagerReopenPreservesContentAndScroll(t *testing.T) {
	m := model()
	m.width, m.height = 40, 10
	path := fixtureMsg(t, strings.Repeat("line\n", 30))
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    loadedLines(t, []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}),
		}})
		m = next
	})
	press(t, m, "enter") // the handler rebinds m with the loaded pager
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
	press(t, m, "enter") // re-open the same thread - the guard skips re-render
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
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    loadedLines(t, []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}),
		}})
		m = next
	})
	press(t, m, "enter")
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	press(t, m, "q") // back to index, pager kept alive
	next, _ := m.Update(WindowSizeMsg{Width: 40, Height: 10})
	m = next
	if m.pager.vp.width != 40 || m.pager.vp.height != 7 {
		t.Fatalf("resize in index mode must re-size the pager window: %dx%d", m.pager.vp.width, m.pager.vp.height)
	}
	press(t, m, "enter") // re-open the same thread - render happens at the new width
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
	lines := strings.Split(stripANSI(m.View()), "\n")
	if !strings.HasPrefix(lines[1], "▌1  ") {
		t.Fatalf("row 1 must pad to the 2-cell slot: %q", lines[1])
	}
	if !strings.HasPrefix(lines[10], " 10 ") {
		t.Fatalf("row 10 must fit the 2-cell slot: %q", lines[10])
	}
}

// TestArrowKeysMoveCursor pins the config overlay: up/down are bound to
// the cursor actions in the index context (the defaults are untouched,
// the user's config file adds the keys).
func TestArrowKeysMoveCursor(t *testing.T) {
	m := rowsModel(3)
	m = pressType(t, m, KeyDown)
	if m.CursorIndex() != 1 {
		t.Fatalf("down must move the cursor down, got %d", m.CursorIndex())
	}
	m = pressType(t, m, KeyUp)
	if m.CursorIndex() != 0 {
		t.Fatalf("up must move the cursor up, got %d", m.CursorIndex())
	}
}

func TestArrowKeysScrollPager(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}},
	})})
	m := New(view, nil, map[string]map[string]string{
		"index": {"enter": "open"},
		"pager": {"up": "scroll-up", "down": "scroll-down", "q": "back"},
	}, testTagActions(), nil, config.NewStore(config.Default()), config.Default().UI)
	m.width, m.height = 40, 10
	m = openPager(t, m, fixtureMsg(t, strings.Repeat("line\n", 30)))
	m = pressType(t, m, KeyDown)
	if m.pager.vp.offset != 1 {
		t.Fatalf("down in pager mode must scroll down, offset=%d", m.pager.vp.offset)
	}
	m = pressType(t, m, KeyUp)
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
	lines := strings.Split(stripANSI(m.View()), "\n")
	if !strings.HasPrefix(lines[1], "▌22") {
		t.Fatalf("the new page must render from its first row: %q", lines[1])
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
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    loadedLines(t, []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{bad}}}),
		}})
		m = next
	})
	press(t, m, "enter")
	if m.mode != "pager" {
		t.Fatalf("a parse failure must open the pager with an error line, mode=%q", m.mode)
	}
	out := stripANSI(m.View())
	if !strings.Contains(out, "failed to parse message") {
		t.Fatalf("error line missing:\n%s", out)
	}
}

func TestThreadLoadedErrorFallsBackToIndex(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{ThreadID: threadID, Err: errors.New("boom")}})
		m = next
	})
	m = press(t, m, "enter")
	if m.mode != "index" {
		t.Fatalf("a failed load must stay in index, mode=%q", m.mode)
	}
	if m.pager != nil {
		t.Fatal("a failed load must drop the pager")
	}
}

func openDialogue(t *testing.T, m Model, id string) Model {
	t.Helper()
	next, _ := m.Update(EventMsg{Event: core.ComposeOpened{
		TabID: id, Mode: "reply", Account: "gmail", From: "Bob <bob@example.com>",
		To: []string{"a@b.c"}, Subject: "Re: x", Body: "> quoted",
	}})
	mm := next
	// the buffer file (BodyPath) dies with the tab; tests that never
	// close the tab leave it to this cleanup
	t.Cleanup(func() {
		for i := range mm.tabs {
			os.Remove(mm.tabs[i].BodyPath)
		}
	})
	return mm
}

func TestComposeOpenedAttachesDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	if m.mode != "compose" {
		t.Fatalf("mode = %q", m.mode)
	}
	if len(m.tabs) != 1 || m.tabIdx != 1 || m.tabs[0].Subject != "Re: x" {
		t.Fatalf("tabs = %+v idx %d", m.tabs, m.tabIdx)
	}
}

func TestTabSwitchParksDialogue(t *testing.T) {
	m := openDialogue(t, openDialogue(t, model(), "t1"), "t2")
	if m.tabIdx != 2 {
		t.Fatalf("tabIdx = %d", m.tabIdx)
	}
	// ] steps to the mail surface, then back through the dialogues
	m = press(t, m, "]")
	if m.mode != "index" || m.tabIdx != 0 {
		t.Fatalf("park: mode %q idx %d", m.mode, m.tabIdx)
	}
	if len(m.tabs) != 2 {
		t.Fatalf("parking must keep the dialogues: %d", len(m.tabs))
	}
	m = press(t, m, "]")
	if m.mode != "compose" || m.tabIdx != 1 {
		t.Fatalf("re-attach: mode %q idx %d", m.mode, m.tabIdx)
	}
	if m.tabs[0].Subject != "Re: x" {
		t.Fatalf("dialogue state must survive: %+v", m.tabs[0])
	}
	m = press(t, m, "[")
	if m.mode != "index" || m.tabIdx != 0 {
		t.Fatalf("[ steps back toward the mail surface: mode %q idx %d", m.mode, m.tabIdx)
	}
	m = press(t, m, "[")
	if m.mode != "compose" || m.tabIdx != 2 {
		t.Fatalf("[ must reach the last dialogue from the mail surface: mode %q idx %d", m.mode, m.tabIdx)
	}
}

func TestReplyKeyOpensDialogue(t *testing.T) {
	got := ""
	SetReplyHandler(func(msg *core.Message, mode string) { got = mode })
	defer SetReplyHandler(func(msg *core.Message, mode string) {})
	// the model() fixture view has a cursor message at row 0 (message
	// "a" of thread t1)
	m := model()
	m = press(t, m, "r")
	if got != "reply" {
		t.Fatalf("r must open a reply, got %q", got)
	}
	m = press(t, m, "f")
	if got != "forward" {
		t.Fatalf("f must open a forward, got %q", got)
	}
	m = press(t, m, "m")
	if got != "compose" {
		t.Fatalf("m must open a blank compose, got %q", got)
	}
	// the gr chain: g then r
	m = press(t, m, "g")
	m = press(t, m, "r")
	if got != "reply-all" {
		t.Fatalf("g r must open a reply-all, got %q", got)
	}
}

func TestSendResultClosesTab(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: true}})
	m = next
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("success must close the tab: %d %q", len(m.tabs), m.mode)
	}
}

func TestSendResultFailureKeepsDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: false, Output: "boom"}})
	m = next
	if len(m.tabs) != 1 || m.tabs[0].Phase != compose.PhaseFailed || m.tabs[0].Output != "boom" {
		t.Fatalf("failure must keep the dialogue: %+v", m.tabs)
	}
	if m.mode != "compose" {
		t.Fatalf("mode = %q", m.mode)
	}
}

// TestSendResultSnapshotResolvesDroppedCompletion pins the bus
// last-value recovery: a SendResult dropped from the channel under
// backpressure (64-deep subscriber) must still resolve the dialogue -
// the snapshot is polled on the next keypress instead of wedging the
// tab in PhaseSending forever.
func TestSendResultSnapshotResolvesDroppedCompletion(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)
	m = openDialogue(t, m, "t1")
	m.tabs[0].Phase = compose.PhaseSending

	// Saturate the subscriber channel so the completion event drops.
	for i := 0; i < 64; i++ {
		bus.Publish(core.ViewDiff{View: "inbox"})
	}
	bus.Publish(core.SendResult{TabID: "t1", OK: true})
	for i := 0; i < 64; i++ {
		<-ch // drain: the SendResult never made it into the channel
	}
	select {
	case e := <-ch:
		t.Fatalf("the SendResult must have dropped, got %v", e)
	default:
	}

	// The next keypress polls the snapshot and resolves the dialogue.
	next, _ := m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("the dropped completion must close the tab: %d %q", len(m.tabs), m.mode)
	}
}

// TestSendResultSnapshotFailureKeepsDialogue pins the failure half of
// the snapshot recovery: a dropped failed result re-applies the
// PhaseFailed state with its output on the next keypress.
func TestSendResultSnapshotFailureKeepsDialogue(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)
	m = openDialogue(t, m, "t1")
	m.tabs[0].Phase = compose.PhaseSending

	for i := 0; i < 64; i++ {
		bus.Publish(core.ViewDiff{View: "inbox"})
	}
	bus.Publish(core.SendResult{TabID: "t1", OK: false, Output: "boom"})
	next, _ := m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if len(m.tabs) != 1 || m.tabs[0].Phase != compose.PhaseFailed || m.tabs[0].Output != "boom" {
		t.Fatalf("a dropped failure must keep the dialogue failed: %+v", m.tabs)
	}
}

// TestSendRetryClearsSnapshot pins the re-arm: a retry after a
// failure must not re-apply the stale failure snapshot while the new
// job is in flight - the dialogue stays Sending until the new result
// lands (a stale failure would reopen the send gates).
func TestSendRetryClearsSnapshot(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)
	m = openDialogue(t, m, "t1")
	m.tabs[0].Phase = compose.PhaseFailed
	bus.Publish(core.SendResult{TabID: "t1", OK: false, Output: "old failure"})

	m = press(t, m, "y") // retry: re-arms Sending and clears the snapshot
	next, _ := m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("the stale failure must not re-apply during the retry, phase=%v", m.tabs[0].Phase)
	}
}

// TestComposeOpenedSnapshotAttachesOnce pins the ComposeOpened
// snapshot: a dropped open event attaches the dialogue on the next
// keypress, and a closed dialogue never resurrects from the same
// snapshot.
func TestComposeOpenedSnapshotAttachesOnce(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	m := New(view, ch, testBindings(), testTagActions(), bus, config.NewStore(config.Default()), config.Default().UI)

	// The open event drops: the channel is full when it publishes.
	for i := 0; i < 64; i++ {
		bus.Publish(core.ViewDiff{View: "inbox"})
	}
	bus.Publish(core.ComposeOpened{TabID: "t1", Mode: "reply", Subject: "Re: x"})
	next, _ := m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if len(m.tabs) != 1 || m.tabs[0].Subject != "Re: x" {
		t.Fatalf("the dropped open must attach the dialogue: %+v", m.tabs)
	}

	// The same snapshot must not resurrect a closed dialogue.
	m.tabs[0].Phase = compose.PhaseSending
	bus.Publish(core.SendResult{TabID: "t1", OK: true})
	next, _ = m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if len(m.tabs) != 0 {
		t.Fatalf("a closed dialogue must never resurrect, got %d tabs", len(m.tabs))
	}
	next, _ = m.Update(KeyPressMsg{Text: "j", Code: 'j'})
	m = next
	if len(m.tabs) != 0 {
		t.Fatalf("the ComposeOpened snapshot must not re-attach, got %d tabs", len(m.tabs))
	}
}

func TestSendArmsSeam(t *testing.T) {
	got := compose.State{}
	SetSendHandler(func(st compose.State) { got = st })
	defer SetSendHandler(func(st compose.State) {})
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "y")
	if got.ID != "t1" {
		t.Fatalf("send seam must receive the dialogue: %+v", got)
	}
	if m.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("phase = %v", m.tabs[0].Phase)
	}
}

func TestQuitConfirmDiscardsStaged(t *testing.T) {
	m := model()
	m.view.Stage("a", core.TagOp{Tag: "archive", Add: true})
	m = press(t, m, "q")
	if d, ok := m.dialogue.(*confirmDialogue); !ok || d.action != "quit-confirmed" {
		t.Fatalf("q with staged ops must arm the quit confirm: %+v", m.dialogue)
	}
	m = press(t, m, "esc")
	if m.dialogue != nil {
		t.Fatalf("esc cancels the quit confirm: %+v", m.dialogue)
	}
	m = press(t, m, "q")
	m = press(t, m, "enter")
	if m.dialogue != nil {
		t.Fatalf("enter confirms and closes the dialogue: %+v", m.dialogue)
	}
}

func TestQuitSkipsConfirmWithoutStaged(t *testing.T) {
	m := model()
	m = press(t, m, "q")
	if m.dialogue != nil {
		t.Fatalf("plain quit must not arm a dialogue: %+v", m.dialogue)
	}
}

func TestAbortConfirmDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "q")
	if m.tabs[0].Phase != compose.PhaseAborting {
		t.Fatalf("q arms aborting: %v", m.tabs[0].Phase)
	}
	if d, ok := m.dialogue.(*confirmDialogue); !ok || d.action != "abort" {
		t.Fatalf("q must arm the abort confirm dialogue: %+v", m.dialogue)
	}
	m = press(t, m, "j") // text keys are ignored while the confirm is open
	if m.dialogue == nil || m.tabs[0].Phase != compose.PhaseAborting {
		t.Fatalf("the confirm dialogue must capture keys: %v %v", m.dialogue, m.tabs[0].Phase)
	}
	m = press(t, m, "esc")
	if m.dialogue != nil || m.tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("esc cancels the abort: %v %v", m.dialogue, m.tabs[0].Phase)
	}
	m = press(t, m, "q")
	m = press(t, m, "enter")
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("enter confirms the abort: %d %q", len(m.tabs), m.mode)
	}
}

// TestAbortConfirmSaveDraft pins the confirm dialogue's d key: the
// draft handler receives the composition, the tab closes; a handler
// error keeps the tab open with the error box and resets the phase.
func TestAbortConfirmSaveDraft(t *testing.T) {
	var got *compose.State
	SetDraftHandler(func(st compose.State) error {
		st2 := st
		got = &st2
		return nil
	})
	defer SetDraftHandler(nil)
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m.tabs[0].Subject = "draft me"
	m = press(t, m, "q")
	if d, ok := m.dialogue.(*confirmDialogue); !ok || !d.draft {
		t.Fatalf("the abort confirm must arm the draft option: %+v", m.dialogue)
	}
	if s := stripANSI(m.render()); !strings.Contains(s, "d = save draft") {
		t.Fatal("the confirm must advertise the draft key")
	}
	m = press(t, m, "d")
	if len(m.tabs) != 0 || m.mode != "index" {
		t.Fatalf("d must close the tab: %d %q", len(m.tabs), m.mode)
	}
	if got == nil || got.Subject != "draft me" {
		t.Fatalf("the draft handler must receive the composition: %+v", got)
	}
	if m.dialogue != nil {
		t.Fatalf("d must close the dialogue: %+v", m.dialogue)
	}
}

// TestAbortConfirmDraftFailure keeps the composition on a failed
// draft write: the error box opens, the phase resets so the user can
// fix or abort again.
func TestAbortConfirmDraftFailure(t *testing.T) {
	SetDraftHandler(func(st compose.State) error {
		return errors.New("no such file")
	})
	defer SetDraftHandler(nil)
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "q")
	m = press(t, m, "d")
	if len(m.tabs) != 1 {
		t.Fatalf("a failed draft must keep the tab: %d", len(m.tabs))
	}
	if m.tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("a failed draft must reset the phase: %v", m.tabs[0].Phase)
	}
	d, ok := m.dialogue.(*errorDialogue)
	if !ok || d.output != "no such file" {
		t.Fatalf("a failed draft must open the error box: %+v", m.dialogue)
	}
}

// TestBodyBufferFileLivesForTab pins the mutt msgbody model: the
// message text is backed by a temp file created at open, reused by e,
// removed when the tab closes.
func TestBodyBufferFileLivesForTab(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	p := m.tabs[0].BodyPath
	if p == "" {
		t.Fatal("a dialogue must back the body with a buffer file")
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("the buffer file must exist with 0600 (F5): %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "> quoted") {
		t.Fatal("the buffer file must mirror the dialogue body")
	}
	if strings.Contains(string(raw), "To:") || strings.Contains(string(raw), "Subject:") {
		t.Fatal("the editor buffer holds only the mail content, never the email header (mutt msgbody)")
	}
	// e reuses the same file - the row's path never churns
	next, _ := m.Update(KeyPressMsg{Text: "e", Code: 'e'})
	m = next
	if m.tabs[0].BodyPath != p {
		t.Fatalf("e must reuse the buffer file, path = %q, want %q", m.tabs[0].BodyPath, p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("e must leave the buffer file in place: %v", err)
	}
	// closing the tab removes it
	m = press(t, m, "q")
	m = press(t, m, "enter")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("closing the tab must remove the buffer file: %v", err)
	}
}

func TestAttachPromptAndDetach(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	path := filepath.Join(t.TempDir(), "att.txt")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, "a")
	if m.dialogue == nil {
		t.Fatal("a must open the prompt")
	}
	// type the absolute path rune by rune (the prompt appends each Text)
	for _, r := range path {
		m = press(t, m, string(r))
	}
	m = pressType(t, m, KeyEnter) // String() resolves to "enter"
	if m.dialogue != nil {
		t.Fatal("enter must close the prompt")
	}
	if len(m.tabs[0].Attachments) != 1 || m.tabs[0].Attachments[0].Name != "att.txt" {
		t.Fatalf("attachments = %+v", m.tabs[0].Attachments)
	}
	// form cursor to the attachment (slot 9, one down from the
	// message-text row), then d detaches
	m = press(t, m, "j")
	m = press(t, m, "d")
	if len(m.tabs[0].Attachments) != 0 {
		t.Fatalf("d must detach the cursor attachment: %+v", m.tabs[0].Attachments)
	}
}

// TestPromptTextCursor pins the v2 cursor contract: the terminal text
// cursor shows only when the model declares it in View - the dialogue
// input row while a prompt is live, the fuzzy matcher row while the
// picker is open, nowhere otherwise.
func TestPromptTextCursor(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	if _, _, show := m.textCursor(); show {
		t.Fatal("cursor without a prompt")
	}
	m = press(t, m, "a")
	m = press(t, m, "xy")
	x, y, show := m.textCursor()
	if !show {
		t.Fatal("an open input prompt must declare the cursor")
	}
	// "attach path: " label + 2 typed cells, the box content row
	// above the keyhint (Y = height-4), after the border (X = 1)
	if x != 1+len("attach path: ")+2 || y != 20 {
		t.Fatalf("input cursor at (%d, %d), want (16, 20)", x, y)
	}
	m = press(t, m, "esc")
	if _, _, show := m.textCursor(); show {
		t.Fatal("cursor after closing the prompt")
	}
	// '?' on the empty attach prompt opens the command picker; the
	// matcher row is the second frame line
	SetAttachCommandSource(func() []AttachCommand {
		return []AttachCommand{{Name: "yazi", Argv: []string{"yazi"}}}
	})
	m = press(t, m, "a")
	m = press(t, m, "?")
	if picker(m) == nil {
		t.Fatal("? must open the command picker")
	}
	m = press(t, m, "ya")
	x, y, _ = m.textCursor()
	if x != len("attach command:")+1+2 || y != 1 {
		t.Fatalf("fuzzy cursor at (%d, %d), want (18, 1)", x, y)
	}
}

// TestFieldEditFrom pins the mutt compose field keys: f opens the From
// prompt pre-filled with the current address, typing appends, enter
// applies; esc cancels without touching the state.
func TestFieldEditFrom(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "f")
	if d := textD(m); d == nil || d.field != "from" {
		t.Fatalf("f must open the From field prompt: %+v", m.dialogue)
	}
	if d := textD(m); d == nil || d.input != "Bob <bob@example.com>" {
		t.Fatalf("the From field must pre-fill: %+v", m.dialogue)
	}
	m = pressType(t, m, KeyEsc)
	if m.dialogue != nil || m.tabs[0].From != "Bob <bob@example.com>" {
		t.Fatalf("esc must cancel the field edit: prompt=%v From=%q", m.dialogue != nil, m.tabs[0].From)
	}
	m = press(t, m, "f")
	m = press(t, m, "x") // typing appends to the pre-filled input
	m = pressType(t, m, KeyEnter)
	if m.dialogue != nil {
		t.Fatal("enter must close the field prompt")
	}
	if m.tabs[0].From != "Bob <bob@example.com>x" {
		t.Fatalf("From = %q", m.tabs[0].From)
	}
}

// TestFieldEditToSplitsAddrs pins the To editor: t pre-fills the list,
// a comma entry splits through the shared SplitAddrs helper (DRY with
// the other compose field editors).
func TestFieldEditToSplitsAddrs(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "t")
	if d := textD(m); d == nil || d.input != "a@b.c" {
		t.Fatalf("t must pre-fill the To list: %+v", m.dialogue)
	}
	for _, r := range ", d@e.f" {
		m = press(t, m, string(r))
	}
	m = pressType(t, m, KeyEnter)
	if len(m.tabs[0].To) != 2 || m.tabs[0].To[0] != "a@b.c" || m.tabs[0].To[1] != "d@e.f" {
		t.Fatalf("To = %v", m.tabs[0].To)
	}
}

// TestAttachPromptRendersBox pins the dialogue box overlay: the
// keyhint keeps its row, the box (border, content row, border)
// splices above the status line, and the typed text renders in the
// content row - pasted ESC bytes never reach the terminal (F1).
func TestAttachPromptRendersBox(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = press(t, m, "a")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the dialogue frame must be exactly 24 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	if !strings.Contains(lines[1], "a attach") {
		t.Fatalf("the keyhint must stay on its row:\n%s", frame)
	}
	if !strings.Contains(lines[len(lines)-4], "attach path:") {
		t.Fatalf("the dialogue box must render above the keyhint bar:\n%s", frame)
	}
	if !strings.Contains(lines[len(lines)-1], "compose") { // box is 3 rows: 19/20/21; keyhint 22, status last
		t.Fatalf("the status line must stay the last row:\n%s", frame)
	}
	m = press(t, m, "h")
	m = press(t, m, "i")
	if !strings.Contains(stripANSI(m.render()), "attach path: hi") {
		t.Fatalf("typed input must render in the box:\n%s", m.render())
	}
	textD(m).input = "x\x1b[31m"
	if out := m.render(); strings.Contains(out, "\x1b[31m") {
		t.Fatalf("control chars leaked into the dialogue box:\n%q", out)
	}
}

// TestConfirmBoxRendersHint pins the confirm box: the abort confirm
// (q) renders its label and the enter/esc hint in the box content
// row (three lines from the end - the middle of the 3-row box).
func TestConfirmBoxRendersHint(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = press(t, m, "q")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the confirm frame must be exactly 24 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	row := lines[len(lines)-4]
	if !strings.Contains(row, "Abort composition?") {
		t.Fatalf("the confirm label must render in the box content row:\n%s", frame)
	}
	if !strings.Contains(row, "(enter = quit, esc = cancel, d = save draft)") {
		t.Fatalf("the confirm hint must render in the box content row:\n%s", frame)
	}
}

// TestDialogueBoxRendersInIndex pins the box in index mode: an armed
// dialogue splices above the status line and the frame stays exactly
// m.height lines. The dialogue is modal and cannot be armed by index
// actions today - the direct arm pins the render path.
func TestDialogueBoxRendersInIndex(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m.dialogue = &textDialogue{label: "go: "}
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the dialogue frame must be exactly 24 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	if !strings.Contains(lines[len(lines)-4], "go:") {
		t.Fatalf("the box content row must render the label:\n%s", frame)
	}
	if !strings.Contains(lines[len(lines)-1], "inbox") {
		t.Fatalf("the status line must stay the last row:\n%s", frame)
	}
}

// TestDialogueBoxKeepsKeyhintInFullIndex pins the full-height splice:
// with the list filling the window the box replaces the last three
// list rows, and the keyhint and status rows survive; the frame stays
// exactly m.height lines.
func TestDialogueBoxKeepsKeyhintInFullIndex(t *testing.T) {
	m := model()
	threads := make([]*core.Thread, 0, 24)
	for i := 0; i < 24; i++ {
		threads = append(threads, core.NewThread(fmt.Sprintf("t%d", i), []*core.Message{
			{ID: fmt.Sprintf("m%d", i), Timestamp: int64(i), Author: "Ann", Subject: "hello", Tags: []string{"inbox"}, References: []string{"r"}},
		}))
	}
	m.view.MergeThreads(threads)
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m.dialogue = &textDialogue{label: "go: "}
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the dialogue frame must be exactly 24 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	last := len(lines) - 1
	if !strings.Contains(lines[last-3], "go:") {
		t.Fatalf("the box content row must render the label:\n%s", frame)
	}
	if !strings.HasPrefix(lines[last-4], "╭") || !strings.HasPrefix(lines[last-2], "╰") {
		t.Fatalf("the box must splice into the list rows (borders at len-5/len-3):\n%s", frame)
	}
	if !strings.Contains(lines[last-1], "? help") {
		t.Fatalf("the keyhint row must survive the box:\n%s", frame)
	}
	if !strings.Contains(lines[last], "inbox") {
		t.Fatalf("the status line must stay the last row:\n%s", frame)
	}
}

// TestEditGatedDuringSending pins the edit gate: an in-flight
// delivery's result is discarded when the send completes, so e must
// not launch the editor while the job runs.
func TestEditGatedDuringSending(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "y") // arms PhaseSending and the spinner tick
	next, cmd := m.Update(KeyPressMsg{Text: "e", Code: 'e'})
	if cmd != nil {
		t.Fatal("e during PhaseSending must not launch the editor")
	}
	if next.tabs[0].Phase != compose.PhaseSending {
		t.Fatalf("phase = %v", next.tabs[0].Phase)
	}
}

func TestFuzzyPickerSwitchesAccount(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "A")
	if p := picker(m); p == nil || p.kind != "account" {
		t.Fatalf("A must open the account picker: %+v", m.dialogue)
	}
	m = press(t, m, "j") // sel = 1: past a narrowed list's end
	// type-to-filter, one key at a time
	for _, r := range "alpha" {
		m = press(t, m, string(r))
	}
	m = pressType(t, m, KeyEnter) // enter selects
	if picker(m) != nil {
		t.Fatal("enter must close the picker")
	}
	if m.tabs[0].Account != "alpha" {
		t.Fatalf("account = %q", m.tabs[0].Account)
	}
	// the switch also applies the account's From: a stale sel past the
	// narrowed list would silently close the picker, leaving the
	// prefill's From untouched (the Account check alone cannot see it -
	// the fixture already opens on gmail)
	if m.tabs[0].From != "" {
		t.Fatalf("the switch must apply the account's From, got %q", m.tabs[0].From)
	}
}

func TestEditorEditArmsExec(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.formIdx = 1 // the redesign: e arms the body editor at any slot
	next, cmd := m.Update(KeyPressMsg{Text: "e", Code: 'e'})
	if cmd == nil {
		t.Fatal("e must return an exec command")
	}
	if next.tabs[0].Phase != compose.PhaseEditing {
		t.Fatalf("phase = %v", next.tabs[0].Phase)
	}
}

func TestComposeFrameShape(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the compose frame must be exactly 24 lines, got %d:\n%s", got, frame)
	}
	last := stripANSI(strings.Split(frame, "\n")[23])
	if !strings.Contains(last, "gmail") {
		t.Fatalf("the status row must show the dialogue's account: %q", last)
	}
	if !strings.Contains(frame, "Re: x") || !strings.Contains(frame, "a@b.c") {
		t.Fatalf("the form must show the fields:\n%s", frame)
	}
}

// TestEditorDoneForClosedTabIsNoOp pins the editor result lookup: the
// result is addressed by tab ID, so a tab closed (or replaced) while
// the editor runs never panics and never lands in another dialogue.
func TestEditorDoneForClosedTabIsNoOp(t *testing.T) {
	m := model()
	m = openDialogue(t, m, "a")
	id := m.tabs[0].ID
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: id, OK: true}})
	m = next
	next, _ = m.Update(editorDoneMsg{tabID: id, path: "/nonexistent"})
	m = next
	if len(m.tabs) != 0 {
		t.Fatalf("a stale editor result must not resurrect a tab, got %d tabs", len(m.tabs))
	}
	// a replaced dialogue is untouched by the stale result
	m = openDialogue(t, m, "b")
	subject := m.tabs[0].Subject
	next, _ = m.Update(editorDoneMsg{tabID: id, path: "/nonexistent"})
	m = next
	if m.tabs[0].Subject != subject {
		t.Fatalf("a stale editor result must not touch another dialogue")
	}
}

func TestComposeRenderFuzzyPopup(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = press(t, m, "A")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the popup frame must be exactly 24 lines, got %d", got)
	}
	if !strings.Contains(frame, "account:") || !strings.Contains(frame, "alpha") {
		t.Fatalf("the popup must show the title and entries:\n%s", frame)
	}
}

// TestFuzzyQueryRowSurvivesManyMatches pins the query row: when the
// match list exceeds the frame, the user's filter input must stay
// visible - the old clip cut the last element (the query row) mid-
// type. The match list clips instead; the frame stays exact.
func TestFuzzyQueryRowSurvivesManyMatches(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 7})
	m = next
	m = press(t, m, "A")
	frame := m.render()
	if got := strings.Count(frame, "\n") + 1; got != 7 {
		t.Fatalf("the popup frame must be exactly 7 lines, got %d", got)
	}
	lines := strings.Split(stripANSI(frame), "\n")
	// 5 popup rows after the tab bar: the matcher row on top (title +
	// filter input - the title doubles as the prompt, no standalone
	// title line), then 3 matches (4 accounts exist)
	if got := strings.TrimSpace(lines[1]); got != "account:" {
		t.Fatalf("the matcher row must stay visible above the matches:\n%s", frame)
	}
	if strings.Contains(frame, "gamma") {
		t.Fatalf("the match list must clip to fill, not the query row:\n%s", frame)
	}
}

// TestLegendNoTickWithReleaseReporting pins the FIX2 gate: on a
// terminal that reports key releases, movement never arms the legend
// debounce tick - the real KeyReleaseMsg resolves the legend, so a
// held key no longer churns the tick's 80-100 render cycles/sec.
func TestLegendNoTickWithReleaseReporting(t *testing.T) {
	m := stubModel()
	m.keyReleases = true
	m = press(t, m, "j")
	if m.legendTickOn {
		t.Fatal("movement must not arm the legend tick when the terminal reports releases")
	}
	if !m.legendPending {
		t.Fatal("movement must still mark the legend pending")
	}
	// a sequence of presses stays tick-free (the hold)
	m = press(t, m, "j")
	m = press(t, m, "j")
	if m.legendTickOn {
		t.Fatal("a hold must never arm the legend tick with release reporting on")
	}
	next, _ := m.Update(KeyReleaseMsg{})
	m = next
	if m.legendPending || m.legendTickOn {
		t.Fatal("the release must resolve the legend")
	}
	if m.legend == "" {
		t.Fatal("the release must resolve the cursor's tag icons")
	}
}

// TestLegendTickFallbackResolves pins the no-release-reporting path:
// movement arms the debounce tick, and the settled tick (its move
// count matching) resolves the legend.
func TestLegendTickFallbackResolves(t *testing.T) {
	m := stubModel()
	m = press(t, m, "j")
	if !m.legendTickOn {
		t.Fatal("without release reporting, movement must arm the debounce tick")
	}
	if !m.legendPending {
		t.Fatal("the press must mark the legend pending")
	}
	next, _ := m.Update(legendTick{moves: m.legendMoves})
	m = next
	if m.legendPending || m.legendTickOn {
		t.Fatal("the settled tick must resolve the legend")
	}
	if m.legend == "" {
		t.Fatal("the tick must resolve the cursor's tag icons")
	}
}

// TestKeyboardEnhancementsMsgSetsReleasePath pins the wiring: the
// terminal's answer to the ReportEventTypes request flips the model's
// keyReleases flag, which gates the legend tick arming.
func TestKeyboardEnhancementsMsgSetsReleasePath(t *testing.T) {
	m := stubModel()
	next, _ := m.Update(KeyboardEnhancementsMsg{Flags: kittyReportEventTypes})
	m = next
	if !m.keyReleases {
		t.Fatal("release reporting must be recorded from the enhancement message")
	}
	// a terminal answering without release reporting keeps the tick path
	next, _ = m.Update(KeyboardEnhancementsMsg{Flags: kittyDisambiguateEscapeCodes})
	m = next
	if m.keyReleases {
		t.Fatal("a disambiguation-only answer must not enable the release path")
	}
	m = press(t, m, "j")
	if !m.legendTickOn {
		t.Fatal("without release reporting the tick fallback must stay armed")
	}
}

// TestPaintGateDeferredNavigation pins the ShouldRender gate's core
// promise: a navigation defers its paint to the frame tick (paint
// false, renderDue true, one tick in flight), the tick re-arms the
// gate exactly once, and an idle tick turns the gate off again - the
// model never renders on a timer with nothing to show.
func TestPaintGateDeferredNavigation(t *testing.T) {
	m := model()
	m = press(t, m, "j")
	if m.paint {
		t.Fatal("a navigation must defer its paint")
	}
	if !m.renderDue || !m.frameTickOn {
		t.Fatalf("the deferral must arm the frame tick: renderDue=%v frameTickOn=%v", m.renderDue, m.frameTickOn)
	}
	if m.ShouldRender() {
		t.Fatal("the gate must report false for a deferred paint")
	}
	// the tick lands the deferred paint exactly once
	next, _ := m.Update(frameTick{})
	m = next
	if !m.paint || m.renderDue || m.frameTickOn {
		t.Fatalf("the tick must re-arm the gate once: paint=%v renderDue=%v frameTickOn=%v", m.paint, m.renderDue, m.frameTickOn)
	}
	if !m.ShouldRender() {
		t.Fatal("the tick must arm the paint")
	}
	// an idle tick (nothing deferred) turns the gate off and dies
	next, _ = m.Update(frameTick{})
	m = next
	if m.paint || m.frameTickOn {
		t.Fatalf("an idle tick must not paint: paint=%v frameTickOn=%v", m.paint, m.frameTickOn)
	}
}

// TestPaintGateSkipsTheFrameBuild pins the model-side gate (the
// vendored tea loop renders after every update - View owns the
// coalescing): a deferred navigation's View returns the last painted
// frame unchanged, and the tick's View builds the moved frame.
func TestPaintGateSkipsTheFrameBuild(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	before := m.View()
	m = press(t, m, "j")
	if m.paint {
		t.Fatal("a navigation must defer its paint")
	}
	if got := m.View(); got != before {
		t.Fatal("a deferred View must return the last painted frame")
	}
	next, _ = m.Update(frameTick{})
	m = next
	if got := m.View(); got == before {
		t.Fatal("the tick's paint must build the moved frame")
	}
}

// TestPaintGateImmediacy pins the exceptions: every message class
// except navigation paints immediately, including the release that
// resolves a held key.
func TestPaintGateImmediacy(t *testing.T) {
	m := model()
	// the fresh model renders unconditionally at startup (the loop's
	// initial render, before the gate); the first message paints
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	if !m.paint {
		t.Fatal("a resize must paint immediately")
	}
	m = press(t, m, "?")
	if !m.help || !m.paint {
		t.Fatalf("the help overlay must paint immediately: help=%v paint=%v", m.help, m.paint)
	}
	// close the overlay again
	m = press(t, m, "?")
	if m.help {
		t.Fatal("the second ? must close the overlay")
	}
	m = pressEvent(t, m, core.ViewDiff{View: "inbox"})
	if !m.paint {
		t.Fatal("a refresh event must paint immediately")
	}
	// a navigation defers, then the release paints immediately and
	// settles the deferred paint - the in-flight tick lands as a no-op
	m = press(t, m, "j")
	if m.paint {
		t.Fatal("the navigation must have deferred")
	}
	next, _ = m.Update(KeyReleaseMsg{})
	m = next
	if !m.paint || m.renderDue {
		t.Fatalf("the release must paint immediately and settle the deferral: paint=%v renderDue=%v", m.paint, m.renderDue)
	}
	next, _ = m.Update(frameTick{})
	m = next
	if m.paint {
		t.Fatal("the settled tick must not paint a second time")
	}
}

// TestPaintGateHoldBurst pins the cadence: 50 rapid navigation
// presses inside one frame window paint nothing, and each frame tick
// lands exactly one paint - a hold renders at the fixed cadence, not
// once per terminal repeat.
func TestPaintGateHoldBurst(t *testing.T) {
	m := model()
	paints := 0
	for w := 0; w < 5; w++ {
		for i := 0; i < 10; i++ {
			m = press(t, m, "j")
			if m.ShouldRender() {
				t.Fatalf("window %d press %d must not paint during the deferred window", w, i)
			}
			if !m.frameTickOn {
				t.Fatalf("window %d press %d must keep the single tick in flight", w, i)
			}
		}
		next, _ := m.Update(frameTick{})
		m = next
		if !m.ShouldRender() {
			t.Fatalf("window %d must land exactly one paint", w)
		}
		paints++
	}
	if paints != 5 {
		t.Fatalf("50 presses across 5 frame windows must paint 5 times, got %d", paints)
	}
}

// TestAttachPromptCommandPicker pins the '?' flow end to end: the
// picker lists the registered commands, selecting arms the attach
// prompt with "@name", and enter arms the exec (a non-nil Cmd) and
// closes the prompt. The picker outranks the prompt while both are
// live - the dispatch order change this test enforces.
func TestAttachPromptCommandPicker(t *testing.T) {
	saved := attachCommands
	defer func() { attachCommands = saved }()
	attachCommands = func() []AttachCommand {
		return []AttachCommand{
			{Name: "yazi", Argv: []string{"yazi", "--chooser-file"}},
			{Name: "fzf", Argv: []string{"fzf"}},
		}
	}
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "a")
	if d := textD(m); d == nil || d.field != "attach" {
		t.Fatalf("a must open the attach prompt: %+v", m.dialogue)
	}
	m = press(t, m, "?")
	if p := picker(m); p == nil || p.kind != "attachcmd" {
		t.Fatalf("? must open the command picker: %+v", m.dialogue)
	}
	if got := strings.Join(picker(m).entries, ","); got != "fzf,yazi" {
		t.Fatalf("entries = %q, want sorted fzf,yazi", got)
	}
	m = press(t, m, "j") // fzf -> yazi
	m = press(t, m, "enter")
	if picker(m) != nil {
		t.Fatal("select must close the picker")
	}
	if d := textD(m); d == nil || d.input != "@yazi" {
		t.Fatalf("the selection must arm the prompt: %+v", m.dialogue)
	}
	next, cmd := m.Update(KeyPressMsg{Text: "enter", Code: []rune("enter")[0]})
	m = next
	if cmd == nil {
		t.Fatal("enter must return the command exec")
	}
	if m.dialogue != nil {
		t.Fatal("the command run must close the prompt")
	}
}

// TestAttachCmdUnknownKeepsPrompt pins the unknown-command path: no
// exec is armed and the prompt keeps the text for correction.
func TestAttachCmdUnknownKeepsPrompt(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "a")
	m = press(t, m, "@nope")
	next, cmd := m.Update(KeyPressMsg{Text: "enter", Code: []rune("enter")[0]})
	m = next
	if cmd != nil {
		t.Fatal("an unknown command must not exec")
	}
	if d := textD(m); d == nil || d.input != "@nope" {
		t.Fatalf("the prompt must stay open with the text: %+v", m.dialogue)
	}
}

// TestAttachCmdResultAddsFiles pins the success path: the chooser file
// yields one attachment per line, blank lines are skipped, and the
// chooser file is removed after the read.
func TestAttachCmdResultAddsFiles(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	f1, _ := os.CreateTemp("", "notmutt-attach-*")
	f2, _ := os.CreateTemp("", "notmutt-attach-*")
	chooser, err := os.CreateTemp("", "notmutt-chooser-*")
	if err != nil {
		t.Fatal(err)
	}
	lines := f1.Name() + "\n" + f2.Name() + "\n   \n"
	if _, err := chooser.WriteString(lines); err != nil {
		t.Fatal(err)
	}
	chooser.Close()

	next, _ := m.Update(attachCmdDoneMsg{path: chooser.Name(), tabID: "t1"})
	m = next
	got := m.tabs[0].Attachments
	if len(got) != 2 || got[0].Path != f1.Name() || got[1].Path != f2.Name() {
		t.Fatalf("attachments = %+v", got)
	}
	if _, err := os.Stat(chooser.Name()); !os.IsNotExist(err) {
		t.Fatal("the chooser file must be removed after the read")
	}
}

// TestAttachCmdFailureErrorBox pins the failure path: the error box
// opens with the command's output, and y re-runs the command.
func TestAttachCmdFailureErrorBox(t *testing.T) {
	SetAttachCommandSource(func() []AttachCommand {
		return []AttachCommand{{Name: "yazi", Argv: []string{"yazi", "--chooser-file"}}}
	})
	defer SetAttachCommandSource(nil)
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(attachCmdDoneMsg{err: errors.New("boom"), path: "/nonexistent", tabID: "t1", name: "yazi"})
	m = next
	d, ok := m.dialogue.(*errorDialogue)
	if !ok || d.name != "yazi" || d.output != "boom" {
		t.Fatalf("the failure must open the error box: %+v", m.dialogue)
	}
	next, cmd := m.Update(KeyPressMsg{Text: "y", Code: 'y'})
	m = next
	if cmd == nil {
		t.Fatal("y must re-arm the attach command exec")
	}
	if m.dialogue != nil {
		t.Fatalf("y must close the error box: %+v", m.dialogue)
	}
}

// TestAttachCmdClosedTabNoOp pins the stale-result lookup: a result for
// a tab closed while the command ran changes nothing.
func TestAttachCmdClosedTabNoOp(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: true}})
	m = next
	if len(m.tabs) != 0 {
		t.Fatalf("the send must close the tab: %d", len(m.tabs))
	}
	next, _ = m.Update(attachCmdDoneMsg{err: errors.New("boom"), path: "/nonexistent", tabID: "t1", name: "yazi"})
	m = next
	if m.dialogue != nil {
		t.Fatalf("a stale result must not resurrect the prompt: %+v", m.dialogue)
	}
}

// TestComposeFrameMuttLayout pins the mutt frame: the tab bar on the
// first line, the keyhint on the second, the sender-info rows (Bcc,
// Reply-To, Fcc), the Security divider, the content-type entry, and
// the prompt box splicing above the status line.
func TestComposeFrameMuttLayout(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	frame := m.render()
	lines := strings.Split(frame, "\n")
	if lines[0] != m.tabBar() {
		t.Fatalf("line 0 must be the tab bar: %q", lines[0])
	}
	if !strings.Contains(stripANSI(lines[1]), "a attach") {
		t.Fatalf("line 1 must be the keyhint: %q", stripANSI(lines[1]))
	}
	for _, want := range []string{"Bcc:", "Reply-To:", "Fcc:", "Security: none", "- I", "[text/plain, quoted-printable, utf-8", "--- Attachments", "--- Preview"} {
		if !strings.Contains(stripANSI(frame), want) {
			t.Fatalf("the frame must show %q:\n%s", want, frame)
		}
	}
	// the message-text row shows the buffer file path (truncated to
	// its column area like any long name), attachments the A marker
	// (mutt's attach list)
	if !strings.Contains(stripANSI(frame), truncCells(m.tabs[0].BodyPath, 80-9-len("[text/plain, quoted-printable, utf-8, 0.0K]"))) {
		t.Fatalf("the message-text row must show the buffer file path:\n%s", frame)
	}
	m.tabs[0].Attachments = []compose.Attachment{{Name: "x.txt", Size: 3}}
	frame = stripANSI(m.render())
	if !strings.Contains(frame, "2 x.txt") || !strings.Contains(frame, "[application/octet-stream, base64, 0.0K]") {
		t.Fatalf("the attachment row must show the A marker with its wire facts:\n%s", frame)
	}
	// the prompt box splices above the status line; the keyhint stays
	// on line 1
	m = press(t, m, "a")
	frame = m.render()
	if !strings.Contains(stripANSI(strings.Split(frame, "\n")[1]), "a attach") {
		t.Fatalf("the keyhint must stay on line 1: %q", stripANSI(strings.Split(frame, "\n")[1]))
	}
	if !strings.Contains(stripANSI(strings.Split(frame, "\n")[20]), "attach path:") {
		t.Fatalf("the attach prompt must splice into the box: %q", stripANSI(strings.Split(frame, "\n")[20]))
	}
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("the prompt frame must still be exactly 24 lines, got %d", got)
	}
}

// TestComposeCursorIndexMarker pins the compose cursor: the focused
// attachment row renders the index's selection marker - one
// indicator-styled cell at the line start (ui.Glyphs.Cursor), never a
// full-line highlight.
func TestComposeCursorIndexMarker(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	if m.formIdx != 8 {
		t.Fatalf("a fresh dialogue must land on the message-text row, formIdx = %d", m.formIdx)
	}
	row := ""
	for _, l := range strings.Split(m.render(), "\n") {
		if strings.Contains(stripANSI(l), "- I ") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatal("the message-text row must render")
	}
	// padRow wraps the line in the row style, so the marker run sits
	// right after the outer open - the index's exact marker: the cursor
	// glyph in the indicator style, one cell at the line start
	marker := m.styles.sgr.normal.open + m.styles.sgr.indicator.render(m.ui.Glyphs.Cursor)
	if !strings.HasPrefix(row, marker) {
		t.Fatalf("the focused row must start with the index cursor marker:\n%s", row)
	}
	if got := strings.Count(row, m.styles.sgr.indicator.open); got != 1 {
		t.Fatalf("the indicator style must appear once (the marker cell), got %d:\n%s", got, row)
	}
	if !strings.Contains(row, m.styles.sgr.normal.open) {
		t.Fatalf("the row content must keep the normal style (no full-line highlight):\n%s", row)
	}

	// j moves onto the first attachment - the marker travels with the
	// cursor slot, the message-text row loses it
	m.tabs[0].Attachments = []compose.Attachment{{Name: "x.txt", Size: 3}}
	m = press(t, m, "j")
	if m.formIdx != 9 {
		t.Fatalf("j must move onto the first attachment, formIdx = %d", m.formIdx)
	}
	for _, l := range strings.Split(m.render(), "\n") {
		if strings.Contains(stripANSI(l), "- I ") && strings.Contains(l, m.styles.sgr.indicator.open) {
			t.Fatalf("the message-text row must lose the marker after j:\n%s", l)
		}
	}
}

// TestTabBarStrip pins the tab strip: the mail surface tab and every
// dialogue, the active one highlighted, subjects F1-sanitized, the
// strip padded to the full width (R11 slot reservation).
func TestTabBarStrip(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = openDialogue(t, m, "t2")
	m.tabs[1].Subject = "second"
	m = openDialogue(t, m, "t3")
	m.tabs[2].Subject = "third"
	if m.tabIdx != 3 {
		t.Fatalf("tabIdx = %d", m.tabIdx)
	}
	// the active tab (the last-opened dialogue) renders in the active
	// style; the others in the bar style
	strip := m.tabBar()
	if !strings.Contains(strip, m.styles.TabActive.Render(" third ")) {
		t.Fatalf("the active tab must render in the active style:\n%s", strip)
	}
	if !strings.Contains(strip, m.styles.Tabbar.Render(" inbox ")) ||
		!strings.Contains(strip, m.styles.Tabbar.Render(" second ")) {
		t.Fatalf("the other tabs must render in the bar style:\n%s", strip)
	}
	// the subject is mail-derived: control characters sanitize (F1) -
	// the ESC byte is gone, the inert "[31m" text remains
	m.tabs[0].Subject = "Re: \x1b[31mx"
	if s := m.tabBar(); strings.Contains(s, "\x1b[31m") || !strings.Contains(s, "Re: [31m") {
		t.Fatalf("a subject ESC must sanitize in the strip: %q", s)
	}
	// the strip pads to the full width (R11 slot reservation)
	if w := runewidth.StringWidth(stripANSI(m.tabBar())); w != 80 {
		t.Fatalf("the strip must fill the width, got %d cells", w)
	}
}

// TestTabBarDropsTrailing pins the width fitting: tabs drop from the
// tail to fit, and the active tab survives by trading places with the
// dropped tail.
func TestTabBarDropsTrailing(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 20, Height: 24})
	m = next
	m = openDialogue(t, m, "t2")
	m.tabs[1].Subject = "aaaa"
	m = openDialogue(t, m, "t3")
	m.tabs[2].Subject = "bbbb"
	// width 20: four tabs do not fit - the tail drops twice (bbbb,
	// then aaaa), the mail tab and the first dialogue survive
	m.tabIdx = 0
	if s := m.tabBar(); strings.Contains(s, "bbbb") || strings.Contains(s, "aaaa") || !strings.Contains(s, "Re: x") {
		t.Fatalf("the tail tabs must drop to fit:\n%s", s)
	}
	// the active tab survives: an active tail trades places with the
	// dropped tabs instead of being cut
	m.tabIdx = 3
	if s := m.tabBar(); strings.Contains(s, "aaaa") || strings.Contains(s, "Re: x") || !strings.Contains(s, "bbbb") {
		t.Fatalf("the active tab must survive the drop:\n%s", s)
	}
}

// TestFramesChrome pins the chrome on every tab: each frame opens
// with the tab bar and closes with the status line - the tab bar
// never displaces the status row, including in the compose window.
func TestFramesChrome(t *testing.T) {
	m := model()
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	// render() runs on a value copy - the frame's status line reads
	// the copy's rows, so the outer model warms the same state
	m.rows = m.view.Rows()
	check := func(name, frame string) {
		t.Helper()
		lines := strings.Split(frame, "\n")
		if lines[0] != m.tabBar() || lines[len(lines)-1] != m.statusLineWith(m.styles, m.ui) {
			t.Fatalf("%s frame chrome:\n%s", name, frame)
		}
	}
	check("index", m.render())
	m = press(t, m, "enter")
	check("pager", m.render())
	m = openDialogue(t, m, "t1")
	check("compose", m.render())
	m = press(t, m, "?")
	check("help", m.render())
}

// TestComposeSlotEditFieldPrompts pins the field hotkeys: x/b/r open
// the Cc/Bcc/Reply-To prompts, enter splits the addresses into the
// dialogue state.
func TestComposeSlotEditFieldPrompts(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	for _, tc := range []struct {
		key   string
		field string
		label string
	}{
		{"c", "cc", "Cc: "},
		{"b", "bcc", "Bcc: "},
		{"r", "replyto", "Reply-To: "},
	} {
		m = press(t, m, tc.key)
		if d := textD(m); d == nil || d.field != tc.field {
			t.Fatalf("%s must open the %s prompt: %+v", tc.key, tc.field, m.dialogue)
		}
		if d := textD(m); d == nil || d.label != tc.label {
			t.Fatalf("%s prompt label = %q, want %q", tc.key, textD(m).label, tc.label)
		}
		m = press(t, m, "x@y.z, q@w.e")
		m = press(t, m, "enter")
		if m.dialogue != nil {
			t.Fatal("enter must close the prompt")
		}
	}
	st := m.tabs[0]
	if len(st.Cc) != 2 || st.Cc[0] != "x@y.z" || st.Cc[1] != "q@w.e" {
		t.Fatalf("Cc = %v", st.Cc)
	}
	if len(st.Bcc) != 2 || st.Bcc[0] != "x@y.z" || st.Bcc[1] != "q@w.e" {
		t.Fatalf("Bcc = %v", st.Bcc)
	}
	if len(st.ReplyTo) != 2 || st.ReplyTo[0] != "x@y.z" || st.ReplyTo[1] != "q@w.e" {
		t.Fatalf("Reply-To = %v", st.ReplyTo)
	}
}

// TestComposeSlotEditSecurityCycle pins the S hotkey: the security
// flag cycles none -> sign -> encrypt -> sign+encrypt -> none.
func TestComposeSlotEditSecurityCycle(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	want := []compose.Security{compose.SecuritySign, compose.SecurityEncrypt, compose.SecuritySignEncrypt, compose.SecurityNone}
	for _, w := range want {
		m = press(t, m, "S")
		if m.tabs[0].Security != w {
			t.Fatalf("security = %v, want %v", m.tabs[0].Security, w)
		}
	}
}

// TestFormNavRestrictedToAttachments pins the mutt attach-list
// navigation: j/k move within the message-text row and the attachment
// list only - with no attachments they are no-ops, and the cursor
// never enters the settings rows.
func TestFormNavRestrictedToAttachments(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Attachments = []compose.Attachment{
		{Name: "a.txt", Size: 3}, {Name: "b.txt", Size: 3},
	}
	if m.formIdx != 8 {
		t.Fatalf("a fresh dialogue must land on the message-text row, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "j")
	if m.formIdx != 9 {
		t.Fatalf("j must move onto the first attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "j")
	if m.formIdx != 10 {
		t.Fatalf("j must move onto the second attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "j")
	if m.formIdx != 10 {
		t.Fatalf("j must clamp at the last attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 9 {
		t.Fatalf("k must move back to the first attachment, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 8 {
		t.Fatalf("k must move back onto the message-text row, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 8 {
		t.Fatalf("k must stop at the message-text row, formIdx = %d", m.formIdx)
	}
	m.tabs[0].Attachments = nil
	m = press(t, m, "j")
	if m.formIdx != 8 {
		t.Fatalf("j with no attachments must no-op, formIdx = %d", m.formIdx)
	}
	m = press(t, m, "k")
	if m.formIdx != 8 {
		t.Fatalf("k with no attachments must no-op, formIdx = %d", m.formIdx)
	}
}

// TestDetachProtectsMessageText pins the mutt attach-list rule: the
// message-text row (slot 8) is not an attachment - d on it is a no-op
// and the cursor stays.
func TestDetachProtectsMessageText(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Attachments = []compose.Attachment{{Name: "a", Size: 1}}
	m.formIdx = 8
	m = press(t, m, "d")
	if len(m.tabs[0].Attachments) != 1 {
		t.Fatalf("d on the message-text row must not detach: %+v", m.tabs[0].Attachments)
	}
	if m.formIdx != 8 {
		t.Fatalf("d on the message-text row must not move the cursor, formIdx = %d", m.formIdx)
	}
}

// TestDetachClampMidList pins the detach clamp: removing the last
// attachment shrinks the list and the cursor clamps back into it.
func TestDetachClampMidList(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Attachments = []compose.Attachment{{Name: "a", Size: 1}, {Name: "b", Size: 1}, {Name: "c", Size: 1}}
	m.formIdx = 11 // the last attachment
	m = press(t, m, "d")
	if m.formIdx != 10 {
		t.Fatalf("detach must clamp into the shrunk list, formIdx = %d", m.formIdx)
	}
	if len(m.tabs[0].Attachments) != 2 {
		t.Fatalf("detach must remove the attachment, got %d", len(m.tabs[0].Attachments))
	}
}

// TestFieldHotkeysPrefill pins the prefill contract: x/b/r arm the
// prompt dialogue with the field's current values joined into the
// input.
func TestFieldHotkeysPrefill(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Cc = []string{"c@old", "c2@old"}
	m.tabs[0].Bcc = []string{"b@old"}
	m.tabs[0].ReplyTo = []string{"r@old"}
	for _, tc := range []struct {
		key, field, want string
	}{
		{"c", "cc", "c@old, c2@old"},
		{"b", "bcc", "b@old"},
		{"r", "replyto", "r@old"},
	} {
		m = press(t, m, tc.key)
		if d := textD(m); d == nil || d.input != tc.want {
			t.Fatalf("%s must prefill the %s value, input = %q, want %q", tc.key, tc.field, textD(m).input, tc.want)
		}
		m = press(t, m, "esc")
	}
}

// TestEditUnconditionalAtAnySlot pins the redesign's 'e': the body
// editor arms on every slot, including the former account slot.
func TestEditUnconditionalAtAnySlot(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.formIdx = 0
	next, cmd := m.Update(KeyPressMsg{Text: "e", Code: 'e'})
	m = next
	if cmd == nil {
		t.Fatal("e at slot 0 must arm the body editor")
	}
	if picker(m) != nil {
		t.Fatal("e must not open the account picker")
	}
}

// TestComposeTableColonAlign pins the two-column table: every settings
// row's label colon sits at the same cell (labelW = 9 at the default
// label set: "Security:" / "Reply-To:" are the widest).
func TestComposeTableColonAlign(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	frame := stripANSI(m.render())
	lines := strings.Split(frame, "\n")
	seam := -1
	for _, l := range lines {
		if !strings.Contains(l, "Account:") {
			continue
		}
		seam = strings.Index(l, ":")
		break
	}
	if seam != 8 {
		t.Fatalf("the label column seam must sit at cell 8, got %d:\n%s", seam, frame)
	}
	seen := 0
	for _, l := range lines {
		if c := strings.Index(l, ":"); c == seam {
			seen++
		}
	}
	if seen < 9 {
		t.Fatalf("all nine settings rows must align at the seam, aligned = %d:\n%s", seen, frame)
	}
}

// TestComposeLongValueKeepsFrameHeight pins the truncation contract:
// a long settings value truncates in place - a word-wrapped value
// would embed newlines and displace the status line (frame height).
func TestComposeLongValueKeepsFrameHeight(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.composeTab().Subject = strings.Repeat("s", 100)
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	frame := stripANSI(m.render())
	if n := strings.Count(frame, "\n") + 1; n != 24 {
		t.Fatalf("the frame must stay exactly 24 lines with a long value, got %d:\n%s", n, frame)
	}
	// the value run must truncate to its cell budget (80 - labelW 9 -
	// the seam 1) - never wrap to a second line
	plain := stripANSI(composeLabel("Subject", strings.Repeat("s", 100), 9, 80, m.styles))
	if strings.Contains(plain, "\n") {
		t.Fatal("the value must never wrap to a second line")
	}
	// " Subject: " is the 10-cell label seam ("Subject:" right-aligned
	// in 9 cells plus the separator); the value run follows
	if v := strings.TrimPrefix(plain, " Subject: "); len(v) > 70 {
		t.Fatalf("the value must truncate to 70 cells, got %d", len(v))
	}
}

// TestDialogueLabelStyledBlue pins the dialogue restyle: the content
// row renders the label in compose.label (blue) and the entry in the
// normal style, with no indicator background on the text. Cc is empty
// on the test message, so the typed entry is a standalone run.
func TestDialogueLabelStyledBlue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m = press(t, m, "c")
	for _, ch := range "writer@example.org" {
		m = press(t, m, string(ch))
	}
	frame := m.render()
	lines := strings.Split(frame, "\n")
	row := lines[len(lines)-4] // the box content row
	if !strings.Contains(row, m.styles.ComposeLabel.Render("Cc: ")) {
		t.Fatalf("the label must render in compose.label:\n%s", frame)
	}
	if !strings.Contains(row, m.styles.Normal.Render("writer@example.org")) {
		t.Fatalf("the entry must render in the normal style:\n%s", frame)
	}
	if strings.Contains(row, m.styles.sgr.indicator.open) {
		t.Fatalf("the content row must carry no background fill:\n%s", frame)
	}
}

// TestComposePreviewScrolls pins the preview pager: ctrl+d scrolls
// the compose preview (built on render), ctrl+u returns.
func TestComposePreviewScrolls(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Body = strings.Repeat("line\n", 200)
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	m.render() // the preview pager builds at render (syncPreviewPager)
	if m.previewPager == nil || len(m.previewPager.lines) < 200 {
		t.Fatalf("the preview pager must hold the body lines: %d", len(m.previewPager.lines))
	}
	next, _ = m.Update(KeyPressMsg{Code: 'd', Mod: modCtrl})
	m = next
	if m.previewPager.vp.offset <= 0 {
		t.Fatal("ctrl+d must scroll the compose preview")
	}
	next, _ = m.Update(KeyPressMsg{Code: 'u', Mod: modCtrl})
	m = next
	if m.previewPager.vp.offset != 0 {
		t.Fatalf("ctrl+u must scroll back, offset=%d", m.previewPager.vp.offset)
	}
}

// TestComposeContentTypeRow pins the derived content-type entry: a
// markdown body renders text/markdown in the form row.
func TestComposeContentTypeRow(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.tabs[0].Body = "# t\n\n- x\n"
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	frame := m.render()
	if !strings.Contains(frame, "[text/markdown, quoted-printable, utf-8") {
		t.Fatalf("the content-type row must show text/markdown:\n%s", frame)
	}
}

// TestSendOverlaySpinner pins the sending overlay: a tab in
// PhaseSending renders the "Sending" row with the spinner frame and
// the switch-tabs note above the status line; the overlay is
// phase-based, never a modal - the tab strip keeps its tabs.
func TestSendOverlaySpinner(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.width, m.height = 80, 24
	m = press(t, m, "y") // arms PhaseSending + the spinner tick
	if !m.anySending() {
		t.Fatal("the send press must leave the tab in PhaseSending")
	}
	frame := stripANSI(m.render())
	if !strings.Contains(frame, "Sending "+spinnerChar(m.spin)) {
		t.Fatalf("the overlay must show the spinner:\n%s", frame)
	}
	if !strings.Contains(frame, "switch tabs while sending") {
		t.Fatalf("the overlay must carry the switch-tabs note:\n%s", frame)
	}
}

// TestSendTickReArmsWhileSending pins the spinner tick gate: a tick
// while a send is in flight advances the frame and re-arms itself (a
// non-nil cmd); a tick with no send in flight dies silently.
func TestSendTickReArmsWhileSending(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, cmd := m.Update(sendTick{})
	if cmd != nil {
		t.Fatalf("no send in flight: the tick must die, got %v", cmd)
	}
	m = press(t, m, "y") // arms PhaseSending + the tick
	before := m.spin
	next, cmd = m.Update(sendTick{})
	m = next
	if m.spin != before+1 {
		t.Fatalf("the tick must advance the frame: %d -> %d", before, m.spin)
	}
	if cmd == nil {
		t.Fatal("a tick during a send must re-arm itself")
	}
	m.onSendResult(core.SendResult{TabID: "t1", OK: true})
	next, cmd = m.Update(sendTick{})
	if cmd != nil {
		t.Fatalf("the tick must die once the send completes, got %v", cmd)
	}
}

// TestSendErrorDialogue pins the failure half: the result opens the
// error dialogue with the output for review and flags the status
// message; esc dismisses, y re-enters the send gate on the failed
// tab, e re-opens its body for editing.
func TestSendErrorDialogue(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "y") // the send press
	m = pressType(t, m, 'x')
	calls := 0
	SetSendHandler(func(st compose.State) { calls++ })
	defer SetSendHandler(func(st compose.State) {})
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: false, Output: "boom"}})
	m = next
	if d, ok := m.dialogue.(*errorDialogue); !ok || d.output != "boom" {
		t.Fatalf("failure must open the error dialogue: %+v", m.dialogue)
	}
	if !m.statusMsgErr || m.statusMsg != "send failed" {
		t.Fatalf("failure must flag the status message: %q %v", m.statusMsg, m.statusMsgErr)
	}
	// esc closes the box; the tab stays failed with the output
	m = press(t, m, "esc")
	if m.dialogue != nil {
		t.Fatalf("esc must close the error dialogue: %+v", m.dialogue)
	}
	if m.tabs[0].Phase != compose.PhaseFailed || m.tabs[0].Output != "boom" {
		t.Fatalf("the failed tab must survive: %+v", m.tabs[0])
	}
	// y retries on the failed tab
	m = press(t, m, "y")
	if m.dialogue != nil || m.tabs[0].Phase != compose.PhaseSending || calls != 1 {
		t.Fatalf("y must re-enter the send gate: %+v", m.tabs[0])
	}
}

// TestErrorBoxGrowsUpward pins the overflow rule: a long failure
// output grows the box upward instead of truncating at a fixed row
// count - every output line renders, the status line survives below
// the box, and beyond the frame capacity the tail indicator takes
// over.
func TestErrorBoxGrowsUpward(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(WindowSizeMsg{Width: 80, Height: 24})
	m = next
	lines := make([]string, 6)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	next, _ = m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: false, Output: strings.Join(lines, "\n")}})
	m = next
	got := strings.Split(stripANSI(m.render()), "\n")
	found := 0
	for _, l := range got {
		if strings.Contains(l, "line ") {
			found++
		}
	}
	if found != 6 {
		t.Fatalf("the box must grow to show all 6 output lines, found %d:\n%s", found, strings.Join(got, "\n"))
	}
	if !strings.Contains(got[23], "compose") {
		t.Fatalf("the status line must survive the grown box:\n%s", strings.Join(got, "\n"))
	}
	big := make([]string, 30)
	for i := range big {
		big[i] = fmt.Sprintf("err %d", i)
	}
	next, _ = m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: false, Output: strings.Join(big, "\n")}})
	m = next
	got = strings.Split(stripANSI(m.render()), "\n")
	hasTail := false
	for _, l := range got {
		hasTail = hasTail || strings.Contains(l, "...")
	}
	if !hasTail {
		t.Fatalf("the overflow must end in the tail indicator:\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(got[23], "compose") {
		t.Fatalf("the status line must survive the capped box:\n%s", strings.Join(got, "\n"))
	}
}

// TestSendOkStatusMessage pins the success half: the result closes
// the tab and leaves the "sent to ..." status message; no To renders
// a bare "sent".
func TestSendOkStatusMessage(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.SendResult{TabID: "t1", OK: true}})
	m = next
	if len(m.tabs) != 0 {
		t.Fatalf("success must close the tab: %d", len(m.tabs))
	}
	if m.statusMsg != "sent to a@b.c" || m.statusMsgErr {
		t.Fatalf("success must leave the sent message: %q", m.statusMsg)
	}
}

// TestAddrCompletion pins the compose Tab address completion: Tab in
// an address field with at least 4 characters opens the fuzzy picker
// over the harvested sender corpus (the go.notmuch result, delivered
// as AddressIndex); selecting fills the field with the display form.
func TestAddrCompletion(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.AddressIndex{Addrs: []core.AddressEntry{
		{Addr: "a@b.c", Name: "Ann"},
		{Addr: "bob@x.io"},
		{Addr: "unrelated@z"},
	}}})
	m = next
	// "t" opens the To field prefilled with the dialogue's To (a@b.c)
	m = press(t, m, "t")
	if d := textD(m); d == nil || d.field != "to" || d.input != "a@b.c" {
		t.Fatalf("the To field must be open prefilled: %+v", m.dialogue)
	}
	m = press(t, m, "tab")
	p := picker(m)
	if p == nil || p.kind != "address" {
		t.Fatalf("Tab with >= 4 chars must open the address picker")
	}
	if len(p.entries) != 1 || p.entries[0] != "Ann <a@b.c>" {
		t.Fatalf("entries must be pre-filtered by the input: %v", p.entries)
	}
	if p.query != "a@b.c" {
		t.Fatalf("the picker query must read back the input: %q", p.query)
	}
	// enter selects: the field lands the display form
	m = press(t, m, "enter")
	if d := textD(m); d == nil || d.input != "Ann <a@b.c>" {
		t.Fatalf("the selection must fill the field: %+v", m.dialogue)
	}
	// a bare address entry displays as the address only
	m = press(t, m, "esc") // close the To field
	m = press(t, m, "b")   // the Bcc field, empty
	for _, r := range "bob@" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "tab")
	p = picker(m)
	if p == nil {
		t.Fatalf("Tab with >= 4 chars must open the picker")
	}
	if len(p.entries) != 1 || p.entries[0] != "bob@x.io" {
		t.Fatalf("bare addresses must match by address: %v", p.entries)
	}
	// a multi-sender line completes only the section after the last
	// comma: the earlier senders stay, and the gate counts the section
	m = press(t, m, "enter")    // select bob@x.io, closing the picker
	m = pressType(t, m, KeyEsc) // close the Bcc field
	m = press(t, m, "t")        // the To field, prefilled with the dialogue's To
	for _, r := range ", b" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "tab")
	if picker(m) != nil {
		t.Fatalf("a short section must not open the picker")
	}
	for _, r := range "ob@" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "tab")
	if p := picker(m); p == nil || p.query != "bob@" {
		t.Fatalf("the picker must complete the section after the last comma: %+v", m.dialogue)
	}
	m = press(t, m, "enter")
	if d := textD(m); d == nil || d.input != "a@b.c, bob@x.io" {
		t.Fatalf("the selection must replace only the section: %+v", m.dialogue)
	}
	// the section under completion follows the edit cursor, not the last
	// comma: with the cursor inside the middle section, the picker
	// completes that one and preserves the trailing section
	m = pressType(t, m, KeyEsc) // close the To field
	m = press(t, m, "t")
	for _, r := range ", bob@, ca" {
		m = press(t, m, string(r))
	}
	for i := 0; i < 5; i++ { // cursor from the end into "bob@"
		m = press(t, m, "left")
	}
	m = press(t, m, "tab")
	if p := picker(m); p == nil || p.query != "bob@" {
		t.Fatalf("the picker must complete the cursor's section, not the last one: %+v", m.dialogue)
	}
	m = press(t, m, "enter")
	if d := textD(m); d == nil || d.input != "a@b.c, bob@x.io, ca" {
		t.Fatalf("the selection must replace only the cursor's section: %+v", m.dialogue)
	}
}

// TestDialogueCursorEditing pins the mutt-style edit cursor: typing and
// backspace land at the cursor, left/right move it, and enter commits
// the edited line.
func TestDialogueCursorEditing(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "b") // the Bcc field, empty
	for _, r := range "abcde" {
		m = press(t, m, string(r))
	}
	d := textD(m)
	if d.cur != 5 {
		t.Fatalf("the cursor must start at the end: cur=%d", d.cur)
	}
	m = press(t, m, "left")
	m = press(t, m, "left")
	m = press(t, m, "backspace") // deletes before the cursor
	if d.input != "abde" || d.cur != 2 {
		t.Fatalf("backspace must delete before the cursor: %q cur=%d", d.input, d.cur)
	}
	m = press(t, m, "X") // inserts at the cursor
	if d.input != "abXde" || d.cur != 3 {
		t.Fatalf("typing must insert at the cursor: %q cur=%d", d.input, d.cur)
	}
	m = press(t, m, "right")
	m = press(t, m, "backspace")
	if d.input != "abXe" || d.cur != 3 {
		t.Fatalf("right then backspace must delete the moved-to char: %q cur=%d", d.input, d.cur)
	}
	m = pressType(t, m, KeyEnter)
	if m.dialogue != nil {
		t.Fatal("enter must close the prompt")
	}
	if st := &m.tabs[m.tabIdx-1]; len(st.Bcc) != 1 || st.Bcc[0] != "abXe" {
		t.Fatalf("enter must commit the edited line: %+v", st.Bcc)
	}
	// cursor clamping: left and backspace at position 0 are no-ops (the
	// reopened Bcc field prefills with the committed "abXe")
	m = press(t, m, "b")
	d = textD(m)             // the reopen is a fresh dialogue object
	for i := 0; i < 6; i++ { // from the end past position 0
		m = press(t, m, "left")
	}
	if d.cur != 0 {
		t.Fatalf("left must clamp at 0: cur=%d", d.cur)
	}
	m = press(t, m, "backspace")
	if d.input != "abXe" || d.cur != 0 {
		t.Fatalf("backspace at 0 must be a no-op: %q cur=%d", d.input, d.cur)
	}
}

// TestDialogueEditorKeys pins the readline word keys: c-w kills the
// word before the cursor, c-u clears the line, alt-f/alt-b move by
// word (alt-f lands on the word END, alt-b on the previous START).
func TestDialogueEditorKeys(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "b") // the Bcc field
	for _, r := range "one two three" {
		m = press(t, m, string(r))
	}
	d := textD(m)
	m = press(t, m, "ctrl+w")
	if d.input != "one two " || d.cur != 8 {
		t.Fatalf("c-w must kill the word before the cursor: %q cur=%d", d.input, d.cur)
	}
	m = press(t, m, "ctrl+w")
	m = press(t, m, "ctrl+w")
	if d.input != "" || d.cur != 0 {
		t.Fatalf("c-w must kill back to the line start: %q cur=%d", d.input, d.cur)
	}
	for _, r := range "a b c" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "alt+b") // from the end to the start of "c"
	if d.cur != 4 {
		t.Fatalf("alt-b must land on the previous word start: cur=%d", d.cur)
	}
	m = press(t, m, "alt+b") // to the start of "b"
	if d.cur != 2 {
		t.Fatalf("alt-b must step back one word: cur=%d", d.cur)
	}
	m = press(t, m, "alt+b") // to the start of "a"
	if d.cur != 0 {
		t.Fatalf("alt-b must land on the line start: cur=%d", d.cur)
	}
	m = press(t, m, "alt+f") // to the end of "a"
	if d.cur != 1 {
		t.Fatalf("alt-f must land on the word end: cur=%d", d.cur)
	}
	m = press(t, m, "right")
	m = press(t, m, "right") // past "b": mid-word kill splices around the cursor
	m = press(t, m, "ctrl+w")
	if d.input != "a  c" || d.cur != 2 {
		t.Fatalf("c-w mid-line must splice: %q cur=%d", d.input, d.cur)
	}
	m = press(t, m, "ctrl+u")
	if d.input != "" || d.cur != 0 {
		t.Fatalf("c-u must clear the line: %q cur=%d", d.input, d.cur)
	}
}

// TestCtrlGCancels pins ctrl+g as the readline cancel: it cancels an
// edit prompt exactly like esc - the filter restores its pre-open text,
// a field prompt just closes without committing.
func TestCtrlGCancels(t *testing.T) {
	m := stubModel() // two threads
	m = press(t, m, "F")
	m = press(t, m, "bob")
	if len(m.rows) != 1 {
		t.Fatalf("typing must narrow live: %d rows", len(m.rows))
	}
	next, _ := m.Update(KeyPressMsg{Code: 'g', Mod: modCtrl})
	m = next
	if m.dialogue != nil {
		t.Fatal("ctrl+g must cancel the filter prompt")
	}
	if len(m.rows) != 2 {
		t.Fatalf("ctrl+g must restore the pre-open filter: %d rows", len(m.rows))
	}
	m = openDialogue(t, model(), "t1")
	st := &m.tabs[m.tabIdx-1]
	before := st.Subject
	m = press(t, m, "s") // the Subject field, prefilled
	m = press(t, m, "x")
	if d := textD(m); d == nil || d.input != before+"x" {
		t.Fatalf("the subject prompt must be open with input: %+v", m.dialogue)
	}
	next, _ = m.Update(KeyPressMsg{Code: 'g', Mod: modCtrl})
	m = next
	if m.dialogue != nil {
		t.Fatal("ctrl+g must cancel the subject prompt")
	}
	if st.Subject != before {
		t.Fatalf("cancelling must not commit the edit: %q", st.Subject)
	}
}

// TestDefaultChooser pins the Tab preference: registration order IS
// the preference (the Lua script's call order decides, not a compiled
// list - the script is data).
func TestDefaultChooser(t *testing.T) {
	if d := defaultChooser([]AttachCommand{{Name: "yazi", Argv: []string{"yazi"}}, {Name: "ranger", Argv: []string{"ranger"}}}); d != "yazi" {
		t.Fatalf("the first registered chooser must win, got %q", d)
	}
	if d := defaultChooser([]AttachCommand{{Name: "ranger", Argv: []string{"ranger"}}, {Name: "yazi", Argv: []string{"yazi"}}}); d != "ranger" {
		t.Fatalf("call order must decide, got %q", d)
	}
	if d := defaultChooser(nil); d != "" {
		t.Fatalf("no commands must yield no chooser, got %q", d)
	}
}

// TestAttachTabChooser pins Tab in the attach prompt: with a
// registered command it runs the default chooser (the first
// registered), and without one it opens the built-in directory
// picker, which descends into directories and attaches a selected
// file.
func TestAttachTabChooser(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	// with a registered yazi, Tab returns an exec cmd and closes the
	// prompt (the returned Cmd is never invoked in tests)
	SetAttachCommandSource(func() []AttachCommand {
		return []AttachCommand{
			{Name: "ranger", Argv: []string{"ranger"}},
			{Name: "yazi", Argv: []string{"yazi", "--chooser-file"}},
		}
	})
	m = press(t, m, "a")
	next, cmd := m.Update(KeyPressMsg{Text: "tab", Code: 't'})
	m = next
	if m.dialogue != nil {
		t.Fatal("Tab with a chooser must close the prompt")
	}
	if cmd == nil {
		t.Fatal("Tab must arm the chooser exec")
	}
	// without any command, Tab opens the built-in picker over the
	// current directory; a directory entry descends, a file attaches
	SetAttachCommandSource(func() []AttachCommand { return nil })
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	os.Mkdir(sub, 0o700)
	os.WriteFile(filepath.Join(dir, "alpha.txt"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(sub, "beta.txt"), []byte("y"), 0o600)
	m.fileDir = dir
	m = press(t, m, "a")
	m = press(t, m, "tab")
	if _, ok := m.dialogue.(*fileDialogue); !ok {
		t.Fatalf("Tab without a chooser must open the built-in picker: %+v", m.dialogue)
	}
	m = press(t, m, "j") // the second entry: sub/ (ReadDir sorts)
	m = press(t, m, "enter")
	if _, ok := m.dialogue.(*fileDialogue); !ok || m.fileDir != filepath.Join(dir, "sub") {
		t.Fatalf("a directory must descend: dir=%q", m.fileDir)
	}
	m = press(t, m, "enter") // beta.txt
	if m.dialogue != nil {
		t.Fatal("selecting a file must close the prompt")
	}
	st := &m.tabs[m.tabIdx-1]
	if len(st.Attachments) != 1 || st.Attachments[0].Name != "beta.txt" {
		t.Fatalf("the selection must attach: %+v", st.Attachments)
	}
}

// TestAddrCompletionLengthGate pins the 4-character gate: Tab with a
// shorter input is a no-op.
func TestAddrCompletionLengthGate(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	next, _ := m.Update(EventMsg{Event: core.AddressIndex{Addrs: []core.AddressEntry{
		{Addr: "a@b.c", Name: "Ann"},
	}}})
	m = next
	m = press(t, m, "b") // the Bcc field, empty
	m = pressType(t, m, 'a')
	m = press(t, m, "tab")
	if picker(m) != nil {
		t.Fatalf("Tab with < 4 chars must not open the picker")
	}
}

// TestAddrCompletionLazyDebounce pins the lazy harvest: without a
// corpus, Tab arms the debounce tick (single-flight); the settle
// guard re-arms a too-young tick and fires the request once the
// triggers settle; the AddressIndex result resolves the pending
// trigger by opening the picker on the still-open field.
func TestAddrCompletionLazyDebounce(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m = press(t, m, "b") // the Bcc field, empty
	for _, r := range "bob@" {
		m = press(t, m, string(r))
	}
	next, cmd := m.Update(KeyPressMsg{Code: KeyTab})
	m = next
	if cmd == nil {
		t.Fatal("a first trigger without a corpus must arm the debounce tick")
	}
	if !m.addrPending {
		t.Fatal("the trigger must mark the harvest in flight")
	}
	// a too-young tick re-arms instead of firing
	m.addrReqAt = time.Now()
	next, cmd = m.Update(addrReqTick{})
	if cmd == nil {
		t.Fatal("a too-young tick must re-arm itself")
	}
	// a ripe tick fires the request through the seam
	reqs := 0
	SetAddressRequestHandler(func() { reqs++ })
	defer SetAddressRequestHandler(func() {})
	m.addrReqAt = time.Now().Add(-time.Second)
	next, cmd = m.Update(addrReqTick{})
	if cmd != nil || reqs != 1 {
		t.Fatalf("a ripe tick must fire exactly one request: %v %d", cmd, reqs)
	}
	// the result resolves the pending trigger: the open Bcc field
	// picks up the corpus
	next, _ = m.Update(EventMsg{Event: core.AddressIndex{Addrs: []core.AddressEntry{
		{Addr: "bob@x.io"},
	}}})
	m = next
	if m.addrPending {
		t.Fatal("the result must clear the in-flight flag")
	}
	p := picker(m)
	if p == nil || p.kind != "address" || len(p.entries) != 1 {
		t.Fatalf("the result must open the picker on the pending field: %+v", m.dialogue)
	}
}

// TestModelToggleRender runs the render toggle (the v key): the
// dispatch asks the app for the other part view, and the re-open's
// ThreadLoaded reply replaces the pager content.
func TestModelToggleRender(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   threadID,
			RenderMode: core.RenderPlain,
			Mime:       "text/plain",
			Lines:      []core.Line{{Text: "plain view"}},
		}})
		m = next
	})
	var got string
	var gotMode core.RenderMode
	SetRenderHandler(func(threadID string, mode core.RenderMode, headers bool, _ int, _ bool) {
		got, gotMode = threadID, mode
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "plain view") {
		t.Fatalf("the pager must show the plain view:\n%s", out)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "text/plain") {
		t.Fatalf("the status bar must show the rendered mime:\n%s", out)
	}

	// the toggle asks for the html view of the open thread
	m = press(t, m, "v")
	if got != "t1" || gotMode != core.RenderHTML {
		t.Fatalf("toggle must ask for the html view, got %q mode=%v", got, gotMode)
	}

	// the app's re-open reply replaces the content
	next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Lines:      []core.Line{{Text: "rendered html view"}},
	}})
	m = next
	if out := stripANSI(m.View()); !strings.Contains(out, "rendered html view") {
		t.Fatalf("the reply must replace the pager content:\n%s", out)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "text/html") {
		t.Fatalf("the mime label must follow the render:\n%s", out)
	}

	// toggling back asks for the plain view
	m = press(t, m, "v")
	if gotMode != core.RenderPlain {
		t.Fatalf("second toggle must ask for the plain view, got %v", gotMode)
	}

	// the source key asks for the raw html source (outside the cycle)
	m = press(t, m, "ctrl+u")
	if gotMode != core.RenderSource {
		t.Fatalf("ctrl+u must ask for the source view, got %v", gotMode)
	}

	// the source reply replaces the content
	next, _ = m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		RenderMode: core.RenderSource,
		Mime:       "text/html",
		Lines:      []core.Line{{Text: "raw source"}},
	}})
	m = next
	if out := stripANSI(m.View()); !strings.Contains(out, "raw source") {
		t.Fatalf("the source reply must replace the pager content:\n%s", out)
	}
}

// TestModelCollapseThread pins the C and ctrl+v keys: the cursor
// thread collapses to its root row (the cursor re-anchors there) and
// expands back; ctrl+v flattens every thread and restores the tree.
func TestModelCollapseThread(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{
		core.NewThread("t1", []*core.Message{
			{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
			{ID: "b", Timestamp: 90, References: []string{"a"}, Tags: []string{"inbox"}},
		}),
		core.NewThread("t2", []*core.Message{{ID: "c", Timestamp: 80, Tags: []string{"inbox"}}}),
	})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	if rows := m.view.Rows(); len(rows) != 3 {
		t.Fatalf("the tree must render 3 rows, got %d", len(rows))
	}

	// move to the child row, then collapse t1: 2 rows, cursor on the
	// surviving root row
	m = press(t, m, "j")
	m = press(t, m, "C")
	rows := m.view.Rows()
	if len(rows) != 2 {
		t.Fatalf("collapse must leave 2 rows, got %d", len(rows))
	}
	if rows[0].Msg == nil || rows[0].Msg.ID != "a" {
		t.Fatalf("the surviving row must be the thread root, got %+v", rows[0].Msg)
	}
	if i := m.CursorIndex(); i != 0 {
		t.Fatalf("the cursor must re-anchor on the root row, got %d", i)
	}

	// expand back: the full tree again
	m = press(t, m, "C")
	if rows := m.view.Rows(); len(rows) != 3 {
		t.Fatalf("expanding must restore the tree, got %d rows", len(rows))
	}

	// ctrl+v flattens everything, a second press restores the tree
	m = press(t, m, "ctrl+v")
	if rows := m.view.Rows(); len(rows) != 2 {
		t.Fatalf("collapse-all must leave one row per thread, got %d", len(rows))
	}
	m = press(t, m, "ctrl+v")
	if rows := m.view.Rows(); len(rows) != 3 {
		t.Fatalf("collapse-all again must restore the tree, got %d", len(rows))
	}
}

// TestModelOpenHeaders pins the h key: it flips the header-block
// toggle AND opens - the toggle state rides the open seam, and every
// subsequent open (enter, preview promotion) carries it.
func TestModelOpenHeaders(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	var gotTID string
	var gotPreview, gotHeaders bool
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		gotTID, gotPreview, gotHeaders = threadID, preview, headers
	})
	m = press(t, m, "h")
	if gotTID != "t1" || gotPreview || !gotHeaders {
		t.Fatalf("h must open with the headers, got %q preview=%v headers=%v", gotTID, gotPreview, gotHeaders)
	}
	if !m.showHeaders {
		t.Fatalf("h must arm the header toggle")
	}
	m = press(t, m, "enter") // a normal open keeps the toggled state
	if gotTID != "t1" || gotPreview || !gotHeaders {
		t.Fatalf("enter must open with the armed headers, got %q preview=%v headers=%v", gotTID, gotPreview, gotHeaders)
	}
	m = press(t, m, "h") // h again flips back
	if gotHeaders {
		t.Fatalf("the second h must open without headers")
	}
}

// fixtureHtml writes a text/html message (the F-key links fixture).
func fixtureHtml(t *testing.T, body string) string {
	t.Helper()
	msg := "From: a@example.com\nTo: b@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: text/html; charset=utf-8\n\n" + body
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestOpenLinksHTML pins the F key in the html view (easyjump style):
// F re-renders with the inline "[N]" labels (the render handler sees
// labelLinks=true) and the key loop owns the digits - no prompt, the
// selection IS the feedback. A digit that completes a number (n*10 > N)
// opens that link's target through the openLink seam on the spot; the
// exit re-render drops the labels. F again or esc exits without
// opening; a dead digit (above the label count) is ignored. The render
// replies are injected like the app's bus would deliver them - the
// seam records the requests.
func TestOpenLinksHTML(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	path := fixtureHtml(t, "<p>see <a href=\"https://alpha.example.com/x\">alpha</a>"+
		" and <a href=\"https://beta.example.com/b\">beta</a></p>\n")
	msgs := []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}
	inject := func(mode core.RenderMode, labelLinks bool) {
		lines, _, links, err := mail.RenderThread(msgs, mode, false, 0, labelLinks)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: "t1", RenderMode: mode, Mime: "text/html",
			LinkLabels: labelLinks, Links: links, Lines: lines,
		}})
		m = next
	}
	var labelCalls []bool
	SetRenderHandler(func(threadID string, mode core.RenderMode, headers bool, _ int, labelLinks bool) {
		labelCalls = append(labelCalls, labelLinks)
	})
	var opened string
	SetOpenLinkHandler(func(url string) { opened = url })
	defer func() {
		SetOpenHandler(func(string, bool, bool, int) {})
		SetRenderHandler(func(string, core.RenderMode, bool, int, bool) {})
		SetOpenLinkHandler(func(string) {})
	}()

	m = press(t, m, "enter") // open: the default handler is a no-op
	inject(core.RenderPlain, false)
	if out := stripANSI(m.View()); strings.Contains(out, "[1]") {
		t.Fatalf("the plain open must carry no labels:\n%s", out)
	}
	m = press(t, m, "v") // toggle: the html view
	if len(labelCalls) != 1 || labelCalls[0] {
		t.Fatalf("the html toggle is unlabeled, calls=%v", labelCalls)
	}
	inject(core.RenderHTML, false)
	if out := stripANSI(m.View()); strings.Contains(out, "[1]") {
		t.Fatalf("the html view must not show labels before F:\n%s", out)
	}
	m = press(t, m, "F") // the label render request
	if len(labelCalls) != 2 || !labelCalls[1] {
		t.Fatalf("F must re-render labeled, calls=%v", labelCalls)
	}
	inject(core.RenderHTML, true) // the labeled reply lands: the key loop arms
	if !m.linkMode || m.dialogue != nil {
		t.Fatalf("the labeled reply must arm the key loop, not a prompt: %+v", m.dialogue)
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "[1]") || !strings.Contains(out, "[2]") {
		t.Fatalf("the label render must carry the [N] labels:\n%s", out)
	}
	m = press(t, m, "2") // label 2: complete (2*10 > 2): opens on the spot
	if opened != "https://beta.example.com/b" {
		t.Fatalf("a complete number must open link 2: %q", opened)
	}
	if m.linkInput != "" || m.pager.linkSel != "" {
		t.Fatalf("the auto-open must clear the digits: input=%q sel=%q", m.linkInput, m.pager.linkSel)
	}
	if len(labelCalls) != 3 || labelCalls[2] {
		t.Fatalf("the exit re-render must be unlabeled, calls=%v", labelCalls)
	}
	inject(core.RenderHTML, false) // the exit reply drops the labels
	if out := stripANSI(m.View()); strings.Contains(out, "[1]") {
		t.Fatalf("the exit re-render must drop the labels:\n%s", out)
	}
	// F again exits without opening; a dead digit is ignored
	m = press(t, m, "F")
	inject(core.RenderHTML, true)
	m = pressType(t, m, KeyEsc)
	if opened != "https://beta.example.com/b" || m.linkInput != "" {
		t.Fatalf("esc must exit the label mode without opening: %q input=%q", opened, m.linkInput)
	}
	inject(core.RenderHTML, false) // the exit reply clears the mode
	if m.linkMode {
		t.Fatal("the exit reply must clear the label mode")
	}
	m = press(t, m, "F")
	inject(core.RenderHTML, true)
	m = press(t, m, "9") // dead digit: above the label count
	if opened != "https://beta.example.com/b" || m.linkInput != "" || !m.linkMode {
		t.Fatalf("a dead digit must be ignored: %q input=%q", opened, m.linkInput)
	}
	m = press(t, m, "F") // F exits without opening
	if opened != "https://beta.example.com/b" || m.linkInput != "" {
		t.Fatalf("F must exit the label mode without opening: %q", opened)
	}
}

// TestOpenLinksNoLinks pins the linkless html mail: the labeled reply
// with an empty link list must NOT arm a prompt - no numbers exist, a
// dead prompt is a UX hole; the mode reports instead and every digit
// is a no-op.
func TestOpenLinksNoLinks(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	path := fixtureHtml(t, "<p>no links here, just text</p>\n")
	msgs := []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}
	var labelCalls []bool
	SetRenderHandler(func(threadID string, mode core.RenderMode, headers bool, _ int, labelLinks bool) {
		labelCalls = append(labelCalls, labelLinks)
	})
	var opened string
	SetOpenLinkHandler(func(url string) { opened = url })
	defer func() {
		SetOpenHandler(func(string, bool, bool, int) {})
		SetRenderHandler(func(string, core.RenderMode, bool, int, bool) {})
		SetOpenLinkHandler(func(string) {})
	}()
	inject := func(mode core.RenderMode, labelLinks bool) {
		lines, _, links, err := mail.RenderThread(msgs, mode, false, 0, labelLinks)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: "t1", RenderMode: mode, Mime: "text/html",
			LinkLabels: labelLinks, Links: links, Lines: lines,
		}})
		m = next
	}
	m = press(t, m, "enter")
	inject(core.RenderPlain, false)
	m = press(t, m, "v")
	inject(core.RenderHTML, false)
	m = press(t, m, "F") // the label render request
	if len(labelCalls) != 2 || !labelCalls[1] {
		t.Fatalf("F must still request the label render, calls=%v", labelCalls)
	}
	inject(core.RenderHTML, true) // the reply: zero links
	if m.dialogue != nil {
		t.Fatalf("a linkless labeled reply must not arm a prompt: %+v", m.dialogue)
	}
	m = press(t, m, "1") // no labels: the digit is dead
	if opened != "" || m.linkInput != "" {
		t.Fatalf("a linkless entry must ignore digits: %q input=%q", opened, m.linkInput)
	}
}

// TestHeadersTogglePager pins the h key in the pager: h re-renders the
// open thread with the header block flipped (the render handler sees
// headers=true), and the reply's headers flag replaces the summary
// with the full raw block. h again drops the block.
func TestHeadersTogglePager(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	path := fixtureMsg(t, "see the body\n")
	msgs := []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}
	var headersSeen []bool
	SetRenderHandler(func(threadID string, mode core.RenderMode, headers bool, _ int, _ bool) {
		headersSeen = append(headersSeen, headers)
	})
	defer func() {
		SetOpenHandler(func(string, bool, bool, int) {})
		SetRenderHandler(func(string, core.RenderMode, bool, int, bool) {})
	}()
	m = openPager(t, m, path)
	inject := func(headers bool) {
		lines, _, _, err := mail.RenderThread(msgs, core.RenderPlain, headers, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: "t1", RenderMode: core.RenderPlain, Headers: headers, Lines: lines,
		}})
		m = next
	}
	if out := stripANSI(m.View()); strings.Contains(out, "Content-Type:") {
		t.Fatalf("the open render must show the summary, not the block:\n%s", out)
	}
	m = press(t, m, "h") // toggle the headers on
	if len(headersSeen) != 1 || !headersSeen[0] {
		t.Fatalf("h must re-render with headers=true, seen=%v", headersSeen)
	}
	inject(true) // the reply lands: the full block replaces the summary
	if out := stripANSI(m.View()); !strings.Contains(out, "Subject: hello") || !strings.Contains(out, "Content-Type:") {
		t.Fatalf("the header reply must show the full block:\n%s", out)
	}
	m = press(t, m, "h") // and back off
	inject(false)
	if out := stripANSI(m.View()); strings.Contains(out, "Content-Type:") {
		t.Fatalf("h must drop the block again:\n%s", out)
	}
}

// TestOpenLinksPlain pins the F key's plain-view fallback: the pager
// lists the visible links (extracted from the rendered lines) in the
// fuzzy picker, and selecting one opens it.
func TestOpenLinksPlain(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	m = openPager(t, m, fixtureMsg(t, "see https://alpha.example.com/x\n"))
	m = press(t, m, "F")
	p := picker(m)
	if p == nil || p.kind != "openlink" {
		t.Fatalf("F in the plain view must open the link picker: %+v", m.dialogue)
	}
	var opened string
	SetOpenLinkHandler(func(url string) { opened = url })
	m = press(t, m, "enter") // the sorted list: the url sorts before the mailto email
	if opened != "https://alpha.example.com/x" {
		t.Fatalf("selecting the url entry must open it: %q", opened)
	}
}

// TestLinkModeScrolls pins the easyjump scroll contract: while the
// label mode owns the keys, the pager scroll keys drive the labeled
// view (links below the fold stay selectable - the number entry is
// independent of the scroll position), digits still extend the number,
// and a completed number opens its link on the spot.
func TestLinkModeScrolls(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	var body strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&body, "<p><a href=\"https://example.com/%d\">link %d</a></p>\n", i, i)
	}
	path := fixtureHtml(t, body.String())
	msgs := []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}
	var opened string
	SetOpenLinkHandler(func(url string) { opened = url })
	defer func() {
		SetOpenHandler(func(string, bool, bool, int) {})
		SetRenderHandler(func(string, core.RenderMode, bool, int, bool) {})
		SetOpenLinkHandler(func(string) {})
	}()
	m = press(t, m, "enter") // open: the default handler is a no-op
	var links []string
	inject := func(labelLinks bool) {
		lines, _, ls, err := mail.RenderThread(msgs, core.RenderHTML, false, 0, labelLinks)
		links = ls
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: "t1", RenderMode: core.RenderHTML, Mime: "text/html",
			LinkLabels: labelLinks, Links: links, Lines: lines,
		}})
		m = next
	}
	inject(false)        // the plain html view
	m = press(t, m, "F") // the label render request
	inject(true)         // the labeled reply lands: the key loop owns the keys
	if !m.linkMode || m.dialogue != nil {
		t.Fatalf("the labeled reply must arm the key loop, not a prompt: %+v", m.dialogue)
	}
	if len(links) != 40 {
		t.Fatalf("fixture must carry 40 links, got %d", len(links))
	}
	m = press(t, m, "j") // scroll down: the key loop stays armed
	if m.pager.vp.offset != 1 {
		t.Fatalf("j must scroll the labeled pager, offset=%d", m.pager.vp.offset)
	}
	if m.linkInput != "" || m.pager.linkSel != "" {
		t.Fatalf("scrolling must not touch the digits: input=%q sel=%q", m.linkInput, m.pager.linkSel)
	}
	m = press(t, m, "2") // digits still extend after the scroll
	if m.linkInput != "2" || m.pager.linkSel != "[2]" {
		t.Fatalf("the digit must extend the entry and select its marker: input=%q sel=%q", m.linkInput, m.pager.linkSel)
	}
	if opened != "" {
		t.Fatalf("an incomplete number must not open yet (2*10 <= 40): %q", opened)
	}
	m = press(t, m, "0") // 20 completes (20*10 > 40): opens on the spot
	if opened != "https://example.com/20" {
		t.Fatalf("the completed number must open its link: %q", opened)
	}
	if m.linkInput != "" || m.pager.linkSel != "" {
		t.Fatalf("the auto-open must clear the digits: input=%q sel=%q", m.linkInput, m.pager.linkSel)
	}
}

// TestEasyjumpHighlight pins the live selection: the marker of the
// number under entry renders reversed (the easyjump highlight follows
// the digits - no prompt), an incomplete number stays a highlight, a
// complete one opens on the spot, and backspace drops the highlight
// with the digits.
func TestEasyjumpHighlight(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	// 25 links: the digits 1-2 are incomplete (n*10 <= 25), so the
	// highlight shows while the entry is still a prefix of a longer one
	var body strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&body, "<p><a href=\"https://example.com/%d\">link %d</a></p>\n", i, i)
	}
	path := fixtureHtml(t, body.String())
	msgs := []core.Message{{ID: "a", ThreadID: "t1", Paths: []string{path}}}
	var opened string
	SetOpenLinkHandler(func(url string) { opened = url })
	defer func() {
		SetOpenHandler(func(string, bool, bool, int) {})
		SetRenderHandler(func(string, core.RenderMode, bool, int, bool) {})
		SetOpenLinkHandler(func(string) {})
	}()
	m = press(t, m, "enter") // open: the default handler is a no-op
	inject := func(labelLinks bool) {
		lines, _, ls, err := mail.RenderThread(msgs, core.RenderHTML, false, 0, labelLinks)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: "t1", RenderMode: core.RenderHTML, Mime: "text/html",
			LinkLabels: labelLinks, Links: ls, Lines: lines,
		}})
		m = next
	}
	inject(false)        // the plain html view
	m = press(t, m, "F") // the label render request
	inject(true)         // the labeled reply lands
	if out := m.View(); strings.Contains(out, "\x1b[7m[1]") {
		t.Fatalf("no digits must select nothing:\n%s", out)
	}
	m = press(t, m, "1") // incomplete (1*10 <= 25): a highlight, no open
	if out := m.View(); !strings.Contains(out, "\x1b[7m[1]") {
		t.Fatalf("the digit entry must highlight its marker reversed:\n%s", out)
	}
	if opened != "" || m.linkInput != "1" || m.pager.linkSel != "[1]" {
		t.Fatalf("an incomplete number must stay a highlight: %q input=%q sel=%q", opened, m.linkInput, m.pager.linkSel)
	}
	m = press(t, m, "2") // 12 completes (12*10 > 25): opens on the spot
	if opened != "https://example.com/12" || m.linkInput != "" || m.pager.linkSel != "" {
		t.Fatalf("a complete number must open immediately: %q input=%q sel=%q", opened, m.linkInput, m.pager.linkSel)
	}
	inject(false) // the exit reply clears the mode
	if m.linkMode {
		t.Fatal("the exit reply must clear the label mode")
	}
	// backspace drops the digits and the highlight together
	m = press(t, m, "F")
	inject(true)
	m = press(t, m, "1")
	if m.pager.linkSel != "[1]" {
		t.Fatalf("the re-armed entry must highlight again: sel=%q", m.pager.linkSel)
	}
	m = press(t, m, "backspace")
	if m.linkInput != "" || m.pager.linkSel != "" {
		t.Fatalf("backspace must drop the digits and the highlight: input=%q sel=%q", m.linkInput, m.pager.linkSel)
	}
	m = press(t, m, "2") // enter confirms the highlighted link
	m = pressType(t, m, KeyEnter)
	if opened != "https://example.com/2" {
		t.Fatalf("enter must open the highlighted link: %q", opened)
	}
}

// TestNextMatch pins the search scan: a real row whose author or
// subject contains the query matches case-insensitively, the scan
// starts after the cursor and wraps, ghost rows never match, and an
// empty query or no match returns -1.
func TestNextMatch(t *testing.T) {
	rows := []core.Row{
		{Msg: &core.Message{ID: "a", ThreadID: "t1", Author: "Alpha", Subject: "build report"}},
		{Msg: nil}, // ghost root: never a match
		{Msg: &core.Message{ID: "b", ThreadID: "t2", Author: "Boris", Subject: "beta notes"}},
		{Msg: &core.Message{ID: "c", ThreadID: "t3", Author: "Carol", Subject: "gamma plan"}},
	}
	cases := []struct {
		name  string
		start int
		query string
		want  int
	}{
		{"subject match", 0, "beta", 2},
		{"author match", 0, "carol", 3},
		{"case-insensitive", 1, "ALPHA", 0},
		{"wrap past the end", 2, "build", 0},
		{"start past the end wraps", 4, "gamma", 3},
		{"no match", 0, "zzz", -1},
		{"empty query", 0, "", -1},
		{"ghost-only row", 0, "build", 0},
	}
	for _, c := range cases {
		if got := nextMatch(rows, c.start, c.query); got != c.want {
			t.Errorf("%s: nextMatch(%d, %q) = %d, want %d", c.name, c.start, c.query, got, c.want)
		}
	}
	if got := nextMatch(nil, 0, "x"); got != -1 {
		t.Fatalf("an empty row set must not match: %d", got)
	}
}

// TestIndexSearch pins the / prompt and the n key: / opens the search
// prompt, enter commits the pattern, closes the prompt and jumps the
// cursor to the next match; the matched part highlights in the rows,
// and n repeats the search from the cursor (wrapping). A miss logs the
// no-match notice and leaves the cursor.
func TestIndexSearch(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	// the view sorts newest-first: t3 row 0, t2 row 1, t1 row 2
	view.MergeThreads([]*core.Thread{
		core.NewThread("t1", []*core.Message{{ID: "a", Timestamp: 100, Author: "Alpha", Subject: "build report", Tags: []string{"inbox"}}}),
		core.NewThread("t2", []*core.Message{{ID: "b", Timestamp: 200, Author: "Boris", Subject: "beta notes", Tags: []string{"inbox"}}}),
		core.NewThread("t3", []*core.Message{{ID: "c", Timestamp: 300, Author: "Carol", Subject: "gamma plan", Tags: []string{"inbox"}}}),
	})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	searchSGR := sgrOf(m.styles.Index.Search).open

	// / opens the prompt prefilled with the last pattern (empty here)
	m = press(t, m, "/")
	if d := textD(m); d == nil || d.field != "search" {
		t.Fatalf("/ must open the search prompt, got %+v", m.dialogue)
	}
	// type the pattern and enter: the prompt closes, the cursor jumps
	// to the match
	m = press(t, m, "b")
	m = press(t, m, "enter")
	if m.dialogue != nil {
		t.Fatal("enter must close the search prompt (the pattern is saved)")
	}
	if !m.paint {
		t.Fatal("enter must schedule the repaint (the prompt box must hide immediately)")
	}
	if m.CursorIndex() != 1 {
		t.Fatalf("enter must jump to the matching row, cursor=%d", m.CursorIndex())
	}
	if out := m.View(); !strings.Contains(out, searchSGR) {
		t.Fatalf("the matched part must render highlighted:\n%q", out)
	}
	// n repeats the search from the cursor: the next match
	m = press(t, m, "n")
	if m.CursorIndex() != 2 {
		t.Fatalf("n must jump to the next match, cursor=%d", m.CursorIndex())
	}
	// n wraps past the end of the list
	m = press(t, m, "n")
	if m.CursorIndex() != 1 {
		t.Fatalf("n must wrap to the first match, cursor=%d", m.CursorIndex())
	}
	// a miss leaves the cursor and logs the notice
	m = press(t, m, "/")
	m = press(t, m, "z")
	m = press(t, m, "enter")
	if m.dialogue != nil {
		t.Fatal("a miss must still close the search prompt")
	}
	if m.CursorIndex() != 1 {
		t.Fatalf("a miss must not move the cursor, cursor=%d", m.CursorIndex())
	}
	if m.statusMsg != "search: no match" {
		t.Fatalf("a miss must log the notice, got %q", m.statusMsg)
	}
}

// TestPagerEnterNextMail pins the pager's enter key: the index cursor
// advances and the pager loads the next thread; a press on the last
// row is a no-op (the same-thread guard skips the reload).
func TestPagerEnterNextMail(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	// the view sorts newest-first: t2 (300) is row 0, t1 the next
	view.MergeThreads([]*core.Thread{
		core.NewThread("t1", []*core.Message{{ID: "a", Timestamp: 100, Tags: []string{"inbox"}}}),
		core.NewThread("t2", []*core.Message{{ID: "b", Timestamp: 300, Tags: []string{"inbox"}}}),
	})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.width, m.height = 80, 24
	opens := 0
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		opens++
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID, RenderMode: core.RenderPlain, Mime: "text/plain",
			Lines: []core.Line{{Text: "mail " + threadID}},
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m (t2)
	if m.mode != "pager" {
		t.Fatalf("enter in the index must open the pager, mode=%q", m.mode)
	}
	press(t, m, "enter") // enter in the pager: next mail (rebinds with t1)
	if m.CursorIndex() != 1 {
		t.Fatalf("the pager's enter must advance the cursor, cursor=%d", m.CursorIndex())
	}
	if out := stripANSI(m.View()); !strings.Contains(out, "mail t1") {
		t.Fatalf("the pager must load the next thread:\n%s", out)
	}
	before := opens
	press(t, m, "enter") // last row: no-op
	if opens != before {
		t.Fatalf("a same-thread press must not reload, opens=%d", opens)
	}
}
