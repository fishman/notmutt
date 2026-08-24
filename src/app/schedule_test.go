// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// schedWorker records the write actions a delivery performs (ActNew, the
// reply tag) and answers every read with nothing.
type schedWorker struct {
	actions []notmuch.ActionKind
}

func (w *schedWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	switch a.Kind {
	case notmuch.ActNew, notmuch.ActTag:
		w.actions = append(w.actions, a.Kind)
	}
	return notmuch.Reply{}, nil
}

func schedCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Schedule.Dir = t.TempDir()
	cfg.Send.Command = "true" // the transport succeeds
	// the delivery path must not depend on the host's network state
	prev := netOnline
	netOnline = func(context.Context) bool { return true }
	t.Cleanup(func() { netOnline = prev })
	return cfg
}

func schedState() compose.State {
	st := *compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab1"
	st.To = []string{"a@b.c"}
	st.Subject = "scheduled hello"
	st.Body = "the body"
	return st
}

func TestParseScheduleTime(t *testing.T) {
	now := time.Date(2026, 8, 22, 10, 30, 0, 0, time.Local)
	cases := []struct {
		in   string
		want string // expected layout "2006-01-02 15:04", "" = error
	}{
		{"tomorrow", "2026-08-23 09:00"},
		{"tomorrow 14:30", "2026-08-23 14:30"},
		{"09:00", "2026-08-23 09:00"}, // today's slot passed: tomorrow
		{"11:00", "2026-08-22 11:00"}, // today, still ahead
		{"2026-08-25 08:00", "2026-08-25 08:00"},
		{"in 90m", "2026-08-22 12:00"},
		{"in 2h", "2026-08-22 12:30"},
		{"in 3d", "2026-08-25 10:30"},
		{"", ""},
		{"someday", ""},
		{"in x", ""},
	}
	for _, c := range cases {
		got, err := parseScheduleTime(c.in, now)
		if c.want == "" {
			if err == nil {
				t.Fatalf("%q: want an error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got.Format("2006-01-02 15:04") != c.want {
			t.Fatalf("%q = %s, want %s", c.in, got.Format("2006-01-02 15:04"), c.want)
		}
	}
	// the RFC3339 case compares the instant, not the local rendering
	ts, err := parseScheduleTime("2026-08-22T11:30:00+02:00", now)
	if err != nil {
		t.Fatalf("rfc3339: %v", err)
	}
	if !ts.Equal(time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("rfc3339 = %v, want 09:30 UTC", ts)
	}
}

// TestParseScheduleTimeInternational pins the natural-language engine
// (zaman): expressions in other locales and the richer English forms
// resolve through it. The exact grammar above stays first - these only
// reach zaman.
func TestParseScheduleTimeInternational(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 8, 22, 10, 30, 0, 0, loc) // a Saturday
	dm := func(ts time.Time, d int, hh, mm int) bool {
		ts = ts.In(loc)
		want := time.Date(2026, 8, 22, hh, mm, 0, 0, loc).AddDate(0, 0, d)
		return ts.Year() == want.Year() && ts.Month() == want.Month() && ts.Day() == want.Day() &&
			ts.Hour() == want.Hour() && ts.Minute() == want.Minute()
	}
	cases := []struct {
		in   string
		name string
		ok   func(time.Time) bool
	}{
		{"next monday", "english weekday", func(ts time.Time) bool {
			l := ts.In(loc)
			// zaman's "next" weekday semantics: a Monday, in the future
			return l.Weekday() == time.Monday && l.After(now)
		}},
		{"فردا ساعت ۱۰:۳۰", "persian tomorrow 10:30", func(ts time.Time) bool { return dm(ts, 1, 10, 30) }},
		{"明天下午三点", "chinese tomorrow 3pm", func(ts time.Time) bool { return dm(ts, 1, 15, 0) }},
		{"بعد أسبوع", "arabic after a week", func(ts time.Time) bool {
			l := ts.In(loc)
			want := now.AddDate(0, 0, 7)
			return l.Year() == want.Year() && l.Month() == want.Month() && l.Day() == want.Day()
		}},
	}
	for _, c := range cases {
		ts, err := parseScheduleTime(c.in, now)
		if err != nil {
			t.Fatalf("%s (%q): %v", c.name, c.in, err)
		}
		if !c.ok(ts) {
			t.Fatalf("%s (%q) = %v", c.name, c.in, ts)
		}
	}
}

func TestScheduleAtRoundTrip(t *testing.T) {
	cfg := schedCfg(t)
	st := schedState()
	at := time.Now().Add(24 * time.Hour)
	if err := scheduleAt(cfg, "", st, at); err != nil {
		t.Fatalf("scheduleAt: %v", err)
	}
	path := filepath.Join(cfg.Schedule.Dir, st.ID+".pending")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("spool file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("spool perms = %v, want 0600 (F5)", fi.Mode().Perm())
	}
	m, err := readScheduled(path)
	if err != nil {
		t.Fatalf("readScheduled: %v", err)
	}
	if m.State.ID != st.ID || m.State.To[0] != "a@b.c" || m.State.Mode != compose.ModeCompose {
		t.Fatalf("state = %+v", m.State)
	}
	if m.State.Subject != "scheduled hello" {
		t.Fatalf("the state must carry the body text: %q", m.State.Subject)
	}
	if m.At != at.Format(time.RFC3339) {
		t.Fatalf("at = %q, want %q", m.At, at.Format(time.RFC3339))
	}
}

// TestScheduleDirDefault pins the spool location: the notmutt XDG
// data home (must-persist data - never a cache or temp dir; the
// composed bytes must survive until delivery), with the config
// override on top.
func TestScheduleDirDefault(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	cfg := config.Default()
	if got := scheduleDir(cfg); got != filepath.Join(data, "notmutt", "schedule") {
		t.Fatalf("default spool = %q, want the data home", got)
	}
	cfg.Schedule.Dir = filepath.Join(t.TempDir(), "spool")
	if got := scheduleDir(cfg); got != cfg.Schedule.Dir {
		t.Fatalf("config override = %q, want %q", got, cfg.Schedule.Dir)
	}
}

func TestScheduleLock(t *testing.T) {
	dir := t.TempDir()
	l1, err := acquireScheduleLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer l1.Close()
	if l2, err := acquireScheduleLock(dir); err == nil {
		l2.Close()
		t.Fatal("a second instance must not hold the spool lock")
	}
	l1.Close()
	l3, err := acquireScheduleLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	l3.Close()
}

// sendDueOnce runs one check and returns the scheduled results (the
// check is synchronous: the events publish before it returns).
func sendDueOnce(t *testing.T, cfg config.Config, worker workerAPI) []core.ScheduledResult {
	t.Helper()
	bus := core.NewBus()
	ch := bus.Subscribe()
	sendDue(context.Background(), bus, worker, core.NewView("inbox", "tag:inbox"), cfg, "")
	var out []core.ScheduledResult
	for {
		select {
		case e := <-ch:
			if r, ok := e.(core.ScheduledResult); ok {
				out = append(out, r)
			}
		default:
			return out
		}
	}
}

func TestSendDueDelivers(t *testing.T) {
	cfg := schedCfg(t)
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	w := &schedWorker{}
	res := sendDueOnce(t, cfg, w)
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("results = %+v, want one OK", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); !os.IsNotExist(err) {
		t.Fatal("a delivered mail must leave the spool")
	}
	if len(w.actions) == 0 {
		t.Fatal("the delivery must reindex the sent copy")
	}
}

func TestSendDueKeepsFuture(t *testing.T) {
	cfg := schedCfg(t)
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if res := sendDueOnce(t, cfg, &schedWorker{}); len(res) != 0 {
		t.Fatalf("a future mail must not deliver: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); err != nil {
		t.Fatal("a future mail must stay pending")
	}
}

// TestSendDueRetriesFailure pins the offline path: a transport failure
// keeps the mail pending for the next check, and the failure surfaces
// as a ScheduledResult.
func TestSendDueRetriesFailure(t *testing.T) {
	cfg := schedCfg(t)
	cfg.Send.Command = "false" // the transport always fails
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	res := sendDueOnce(t, cfg, &schedWorker{})
	if len(res) != 1 || res[0].OK || res[0].Err == nil {
		t.Fatalf("results = %+v, want one failure", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); err != nil {
		t.Fatal("a failed delivery must stay pending for retry")
	}
}

// TestSendDueAttachment pins the path-based attachment contract: the
// attachment reads from its file at delivery (like a live send), and
// the wire carries its bytes.
func TestSendDueAttachment(t *testing.T) {
	cfg := schedCfg(t)
	captured := filepath.Join(t.TempDir(), "wire.eml")
	stub := "#!/bin/sh\ncat > " + captured + "\n"
	stubPath := filepath.Join(t.TempDir(), "send-stub")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Send.Command = stubPath
	att := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(att, []byte("hello attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := schedState()
	if err := st.AddAttachment(att); err != nil {
		t.Fatal(err)
	}
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if res := sendDueOnce(t, cfg, &schedWorker{}); len(res) != 1 || !res[0].OK {
		t.Fatalf("results = %+v, want one OK", res)
	}
	wire, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), base64.StdEncoding.EncodeToString([]byte("hello attachment"))) {
		t.Fatalf("the wire must carry the attachment content read at delivery:\n%s", wire)
	}
}

// TestScheduledList pins the spool scan behind the s key: every
// pending mail surfaces as subject + send time, sorted by time.
func TestScheduledList(t *testing.T) {
	cfg := schedCfg(t)
	st1 := schedState()
	st1.Subject = "first"
	if err := scheduleAt(cfg, "", st1, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st2 := schedState()
	st2.ID = "tab2"
	st2.Subject = "second"
	if err := scheduleAt(cfg, "", st2, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries := scheduledList(cfg)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	if entries[0].ID != "tab2" || entries[1].ID != "tab1" {
		t.Fatalf("the list must sort by send time: %+v", entries)
	}
	if entries[0].Subject != "second" || entries[1].Subject != "first" {
		t.Fatalf("subjects = %q %q", entries[0].Subject, entries[1].Subject)
	}
}

// TestEditScheduled pins the e key's app side: the spool record is
// removed (unscheduled) and the stored state reopens as a compose
// dialogue with a fresh TabID (the original is in the TUI's opened
// set - re-attaching it would no-op).
func TestEditScheduled(t *testing.T) {
	cfg := schedCfg(t)
	st := schedState()
	st.Subject = "editable"
	st.Body = "the body"
	if err := scheduleAt(cfg, "", st, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bus := core.NewBus()
	ch := bus.Subscribe()
	editScheduled(bus, cfg, "", st.ID)
	select {
	case e := <-ch:
		opened, ok := e.(core.ComposeOpened)
		if !ok {
			t.Fatalf("edit must publish ComposeOpened, got %T", e)
		}
		if opened.TabID == st.ID || opened.Subject != "editable" || opened.Body != "the body" {
			t.Fatalf("reopened dialogue = %+v (fresh id, stored state)", opened)
		}
	case <-time.After(time.Second):
		t.Fatal("no ComposeOpened")
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); !os.IsNotExist(err) {
		t.Fatal("edit must unschedule the mail")
	}
}

// TestSendDueStampsDeliveryDate pins the wire date: a due mail's Date
// header is the delivery instant, never the schedule time (the stored
// bytes carry the composition date; the delivered message the send
// date - mutt semantics).
func TestSendDueStampsDeliveryDate(t *testing.T) {
	cfg := schedCfg(t)
	captured := filepath.Join(t.TempDir(), "wire.eml")
	stub := "#!/bin/sh\ncat > " + captured + "\n"
	stubPath := filepath.Join(t.TempDir(), "send-stub")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Send.Command = stubPath
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	sendDueOnce(t, cfg, &schedWorker{})
	wire, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	date := ""
	for _, l := range strings.Split(string(wire), "\n") {
		if strings.HasPrefix(l, "Date:") {
			date = strings.TrimSpace(strings.TrimPrefix(l, "Date:"))
			break
		}
	}
	if date == "" {
		t.Fatalf("the wire must carry a Date header:\n%s", wire)
	}
	wireTime, err := time.Parse(time.RFC1123Z, date)
	if err != nil {
		t.Fatalf("bad wire date %q: %v", date, err)
	}
	if d := time.Since(wireTime); d < -time.Minute || d > time.Minute {
		t.Fatalf("the wire date %v must be the delivery instant, not the schedule time", wireTime)
	}
}

// TestSendDueResume pins the closed-client catch-up: a mail that came
// due while no client ran delivers on the next startup check.
func TestSendDueResume(t *testing.T) {
	cfg := schedCfg(t)
	// write the spool directly - the mail "came due" while the client
	// was closed (the schedule prompt is not involved)
	m := scheduledMail{At: time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
		State: compose.State{ID: "old", Account: "gmail", From: "bob@example.com",
			To: []string{"a@b.c"}, Body: "body", Mode: compose.ModeCompose}}
	hdr, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(cfg.Schedule.Dir, 0o700)
	if err := os.WriteFile(filepath.Join(cfg.Schedule.Dir, m.State.ID+".pending"), hdr, 0o600); err != nil {
		t.Fatal(err)
	}
	res := sendDueOnce(t, cfg, &schedWorker{})
	if len(res) != 1 || !res[0].OK {
		t.Fatalf("resume results = %+v, want one OK", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, m.State.ID+".pending")); !os.IsNotExist(err) {
		t.Fatal("a resumed mail must leave the spool")
	}
}

// TestSendDueOfflineWaits pins the netcheck pre-flight: a clearly
// offline machine skips the round - the mail stays pending instead of
// spawning a transport that would certainly fail.
func TestSendDueOfflineWaits(t *testing.T) {
	cfg := schedCfg(t)
	prev := netOnline
	netOnline = func(context.Context) bool { return false }
	t.Cleanup(func() { netOnline = prev })
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if res := sendDueOnce(t, cfg, &schedWorker{}); len(res) != 0 {
		t.Fatalf("offline must skip delivery, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); err != nil {
		t.Fatal("offline must keep the mail pending")
	}
}

// TestSendDueConcurrentInstance pins the lock: while one instance
// holds the spool lock, another instance's check delivers nothing (it
// no-ops instead of double-sending).
func TestSendDueConcurrentInstance(t *testing.T) {
	cfg := schedCfg(t)
	lock, err := acquireScheduleLock(cfg.Schedule.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	st := schedState()
	if err := scheduleAt(cfg, "", st, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if res := sendDueOnce(t, cfg, &schedWorker{}); len(res) != 0 {
		t.Fatalf("a locked spool must no-op, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(cfg.Schedule.Dir, st.ID+".pending")); err != nil {
		t.Fatal("the locked instance must not touch the spool")
	}
}

// TestSendDueCorruptDrops pins the malformed-spool path: a garbage
// file drops with a log instead of hanging the check.
func TestSendDueCorruptDrops(t *testing.T) {
	cfg := schedCfg(t)
	bad := filepath.Join(cfg.Schedule.Dir, "junk.pending")
	os.MkdirAll(cfg.Schedule.Dir, 0o700)
	if err := os.WriteFile(bad, []byte("not a spool file"), 0o600); err != nil {
		t.Fatal(err)
	}
	sendDueOnce(t, cfg, &schedWorker{})
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("a corrupt spool file must drop")
	}
}
