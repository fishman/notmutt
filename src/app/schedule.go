// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fishman/zaman"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/lib/netcheck"
	"notmutt/lib/xdg"
)

// Scheduled mail: the composed message is assembled once and stored in
// a spool (0600, F5) with its envelope metadata; at the due time the
// client delivers it through the same core as a live send
// (deliverSend). Two properties drive the design:
//   - multiple instances share the spool, so the due-check runs under
//     a flock (schedule_lock_*.go): only the lock holder scans and
//     delivers, a second instance's check no-ops - a mail is never
//     delivered twice.
//   - the client may be closed or offline at the due time, so the
//     check runs at startup (the resume path) and on a cadence, and a
//     transport failure keeps the mail pending for the next try.

// scheduledMail is the spool header: the delivery identity (envelope
// recipients for the transport argv, the account for the fcc, the
// reply/forward tag) and the due time. The assembled message bytes
// scheduledMail is the spool record: when to send and the full compose
// state. The message assembles at DELIVERY time - the wire Date and
// Message-ID are the send instant, never the schedule time - and the
// attachments read from their paths exactly like a live send (a file
// that vanished by then fails the same way a live send would).
type scheduledMail struct {
	At    string        `json:"at"` // RFC3339 (local)
	State compose.State `json:"state"`
}

// scheduleDir resolves the spool: the config override, else the
// notmutt data home (XDG_DATA_HOME - the data that must persist;
// never a cache or temp dir, the composed bytes must survive until
// delivery), else the state home.
func scheduleDir(cfg config.Config) string {
	if cfg.Schedule.Dir != "" {
		return cfg.Schedule.Dir
	}
	if base := xdg.DataHome(); base != "" {
		return filepath.Join(base, "notmutt", "schedule")
	}
	return filepath.Join(xdg.StateHome(), "notmutt", "schedule")
}

// scheduleAt stores the composed dialogue for delivery at t: the
// dialogue state serializes to the spool as <id>.pending, 0600 (F5).
// The message is NOT assembled here - delivery assembles it, so the
// wire date is the send instant. The id is the dialogue's - unique
// per tab; an O_EXCL create keeps racing instances from colliding on
// one name.
func scheduleAt(cfg config.Config, root string, st compose.State, at time.Time) error {
	st.BodyPath = "" // the editor buffer dies with the tab; the body text rides the state
	st.Phase = 0
	st.Output = ""
	if st.Fcc == "" {
		st.Fcc = sentPath(root, st.Account, cfg.Accounts[st.Account])
	}
	m := scheduledMail{At: at.Format(time.RFC3339), State: st}
	hdr, err := json.Marshal(m)
	if err != nil {
		return err
	}
	dir := scheduleDir(cfg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, st.ID+".pending"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(hdr)
	return err
}

// readScheduled loads the spool record.
func readScheduled(path string) (scheduledMail, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scheduledMail{}, err
	}
	var m scheduledMail
	if err := json.Unmarshal(data, &m); err != nil {
		return scheduledMail{}, fmt.Errorf("schedule: %s: %w", path, err)
	}
	return m, nil
}

// deliverScheduled assembles the stored state NOW (the delivery
// instant stamps the Date and Message-ID; attachments read from their
// paths like a live send) and runs the delivery core.
func deliverScheduled(worker workerAPI, cfg config.Config, root string, m scheduledMail) error {
	st := m.State
	if st.Fcc == "" {
		st.Fcc = sentPath(root, st.Account, cfg.Accounts[st.Account])
	}
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		return err
	}
	_, _, err := deliverSend(worker, cfg, root, st, buf.Bytes())
	return err
}

// netOnline is the connectivity seam (the platform netcheck package);
// tests override it to force the delivery path.
var netOnline = netcheck.Online

// sendDue delivers every due scheduled mail (the startup catch-up and
// the periodic check share it). The spool lock serializes multiple
// instances: the holder delivers, the others' checks no-op. A clearly
// offline machine (the netcheck pre-flight) skips the round - the
// mail stays pending and the transport error would only confirm it. A
// transport failure keeps the mail pending - the next tick or the
// next client start retries, so a closed or offline client resumes
// when it can. A corrupt file drops with a log; delivery is
// at-least-once (a crash between transport and removal can double).
func sendDue(ctx context.Context, bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, root string) {
	if !netOnline(ctx) {
		diag.Info("schedule", "state", "offline: mail waits")
		return
	}
	dir := scheduleDir(cfg)
	lock, err := acquireScheduleLock(dir)
	if err != nil {
		return // another instance owns the spool this round
	}
	defer lock.Close()
	now := time.Now()
	entries, err := os.ReadDir(dir)
	if err != nil {
		diag.Warn("schedule", "err", err.Error())
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".pending") {
			continue
		}
		path := filepath.Join(dir, name)
		m, err := readScheduled(path)
		if err != nil {
			diag.Warn("schedule", "drop", err.Error())
			os.Remove(path)
			continue
		}
		at, err := time.Parse(time.RFC3339, m.At)
		if err != nil || at.After(now) {
			continue // not due yet
		}
		if err := deliverScheduled(worker, cfg, root, m); err != nil {
			bus.Publish(core.ScheduledResult{ID: m.State.ID, At: m.At, OK: false, Err: err})
			diag.Warn("schedule", "send", m.State.ID, "err", err.Error())
			continue // stays .pending: retry next tick
		}
		os.Remove(path)
		bus.Publish(core.ScheduledResult{ID: m.State.ID, At: m.At, OK: true})
	}
	bus.Publish(core.ViewDiff{View: view.ViewName()})
}

// runScheduler is the scheduled-mail loop: the startup check delivers
// mail that came due while the client was closed (the resume path),
// then a tick re-checks on the configured cadence for mail due during
// the session. The spool lock inside sendDue keeps concurrent
// instances safe.
func runScheduler(ctx context.Context, bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, root string) {
	sendDue(ctx, bus, worker, view, cfg, root)
	iv := time.Duration(cfg.Schedule.Interval) * time.Second
	if iv <= 0 {
		iv = 60 * time.Second
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sendDue(ctx, bus, worker, view, cfg, root)
		}
	}
}

// scheduleJob stores the dialogue for delivery at the parsed time and
// reports the outcome: OK closes the compose tab, an error keeps it
// open (like a failed send).
func scheduleJob(bus *core.Bus, worker workerAPI, view *core.View, cfg config.Config, root string, st compose.State, atStr string) {
	at, err := parseScheduleTime(atStr, time.Now())
	if err != nil {
		bus.Publish(core.ScheduledResult{ID: st.ID, OK: false, Err: err})
		return
	}
	if err := scheduleAt(cfg, root, st, at); err != nil {
		bus.Publish(core.ScheduledResult{ID: st.ID, OK: false, Err: err})
		return
	}
	bus.Publish(core.ScheduledResult{ID: st.ID, OK: true, At: at.Format("Mon Jan 2 15:04")})
}

// parseScheduleTime resolves the schedule prompt: the exact grammar
// first (its semantics stay tuned - tomorrow = 09:00, HH:MM = the next
// occurrence), then the natural-language engine (zaman) for every
// locale and the richer expressions: "next monday", "فردا ساعت ۱۰:۳۰",
// "明天下午三点", "بعد أسبوع".
func parseScheduleTime(s string, now time.Time) (time.Time, error) {
	if t, ok := parseExact(s, now); ok {
		return t, nil
	}
	r, err := zaman.Parse(s, &zaman.Options{Now: now, Location: now.Location()})
	if err == nil && r != nil && !r.Span.IsZero() {
		return r.Time, nil
	}
	return time.Time{}, fmt.Errorf("schedule: cannot parse %q (tomorrow, HH:MM, YYYY-MM-DD HH:MM, in Nm/Nh/Nd, or natural language)", s)
}

// parseExact is the exact grammar: the forms whose semantics are
// tuned (RFC3339, tomorrow [HH:MM], HH:MM next occurrence,
// YYYY-MM-DD HH:MM, in Nm/Nh/Nd). ok=false when the input is not one
// of these - the caller falls through to the natural-language engine.
func parseExact(s string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	// RFC3339 first: the timestamp is case-sensitive (the T separator)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	s = strings.ToLower(s)
	if s == "tomorrow" {
		s = "tomorrow 09:00"
	}
	if rest, ok := strings.CutPrefix(s, "tomorrow "); ok {
		var hh, mm int
		if _, err := fmt.Sscanf(rest, "%d:%d", &hh, &mm); err != nil {
			return time.Time{}, false
		}
		return time.Date(now.Year(), now.Month(), now.Day()+1, hh, mm, 0, 0, now.Location()), true
	}
	if rest, ok := strings.CutPrefix(s, "in "); ok {
		var n int
		var unit string
		if _, err := fmt.Sscanf(rest, "%d%s", &n, &unit); err != nil || n < 0 {
			return time.Time{}, false
		}
		switch unit {
		case "m", "min", "mins":
			return now.Add(time.Duration(n) * time.Minute), true
		case "h", "hr", "hrs", "hour", "hours":
			return now.Add(time.Duration(n) * time.Hour), true
		case "d", "day", "days":
			return now.AddDate(0, 0, n), true
		}
		return time.Time{}, false
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, now.Location()); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("15:04", s, now.Location()); err == nil {
		t = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
		if !t.After(now) {
			t = t.AddDate(0, 0, 1) // today's slot already passed: tomorrow
		}
		return t, true
	}
	return time.Time{}, false
}
