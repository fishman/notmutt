// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
)

// draftText is a minimal message with threading headers, fabricated
// addresses only (AGENTS.md).
const draftText = "From: Alpha <alpha@example.com>\n" +
	"To: beta@example.com\n" +
	"Subject: Project X\n" +
	"Message-ID: <mid-3@example.com>\n" +
	"References: <r0@example.com> <r1@example.com>\n" +
	"Date: Tue, 01 Jan 2019 00:00:00 +0000\n" +
	"MIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\nbody\n"

func draftMsg(path, author string, ts int64, id string) core.Message {
	return core.Message{ID: id, ThreadID: "t", Timestamp: ts, Author: author,
		Subject: "S" + author, Paths: []string{path}}
}

func draftFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func draftCfg(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"acme": {From: "Me <me@example.com>"}}
	return cfg
}

// TestAIDraftCompose proves the prefill: To = distinct non-own senders,
// Subject = the newest message's, threading headers from the newest
// message, own identity and fcc from the account chain.
func TestAIDraftCompose(t *testing.T) {
	cfg := draftCfg(t)
	root := t.TempDir()
	pAlpha := draftFile(t, draftText)
	pBeta := draftFile(t, draftText)
	pOwn := draftFile(t, draftText)
	msgs := []core.Message{
		draftMsg(pAlpha, "alpha@example.com", 100, "m1"),
		draftMsg(pBeta, "beta@example.com", 200, "m2"),
		draftMsg(pOwn, "me@example.com", 300, "m3"),
	}
	msgs[2].References = []string{"<r0@example.com>", "<r1@example.com>"}
	st := aiDraftCompose(cfg, root, msgs, "drafted by ai")
	if st == nil {
		t.Fatal("nil draft for a parseable thread")
	}
	if st.Mode != compose.ModeCompose {
		t.Errorf("mode = %v, want compose", st.Mode)
	}
	if st.Account != "acme" || st.From != "Me <me@example.com>" {
		t.Errorf("identity = %q / %q, want acme", st.Account, st.From)
	}
	if want := []string{"alpha@example.com", "beta@example.com"}; !reflect.DeepEqual(st.To, want) {
		t.Errorf("To = %v, want %v", st.To, want)
	}
	if st.Subject != "Sme@example.com" {
		t.Errorf("Subject = %q, want newest subject", st.Subject)
	}
	if st.Body != "drafted by ai" {
		t.Errorf("Body = %q", st.Body)
	}
	if st.MessageID != "<mid-3@example.com>" {
		t.Errorf("MessageID = %q", st.MessageID)
	}
	if want := []string{"<r0@example.com>", "<r1@example.com>", "<mid-3@example.com>"}; !reflect.DeepEqual(st.References, want) {
		t.Errorf("References = %v, want %v", st.References, want)
	}
	if st.OriginalID != "m3" {
		t.Errorf("OriginalID = %q, want m3", st.OriginalID)
	}
	if st.Fcc == "" {
		t.Error("fcc missing")
	}
	if st.ID != "" {
		t.Errorf("ID set (%q) - the caller owns the fresh id", st.ID)
	}
}

// TestWrapEmail proves the generated body is a mail, not a blob: long
// lines fill to the cap at word boundaries with the word order kept,
// paragraphs stay separate (runs of blank lines collapse to one), inner
// newlines collapse to spaces, short text passes through untouched, and
// a single word longer than the cap stays whole.
func TestWrapEmail(t *testing.T) {
	if got := wrapEmail("", 72); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := wrapEmail("short body", 72); got != "short body" {
		t.Errorf("short = %q", got)
	}
	if got := wrapEmail("aaa bbb\n\nccc ddd", 72); got != "aaa bbb\n\nccc ddd" {
		t.Errorf("paragraphs = %q", got)
	}
	if got := wrapEmail("a b\n\n\nc", 72); got != "a b\n\nc" {
		t.Errorf("blank-line collapse = %q", got)
	}
	if got := wrapEmail("aaa\nbbb", 72); got != "aaa bbb" {
		t.Errorf("inner newline = %q", got)
	}
	out := wrapEmail(strings.Repeat("word ", 20), 10)
	for i, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if len(line) > 10 {
			t.Fatalf("line %d over the cap: %q", i, line)
		}
	}
	if strings.Join(strings.Fields(out), " ") != strings.Join(strings.Fields(strings.Repeat("word ", 20)), " ") {
		t.Fatalf("wrap must keep the word order: %q", out)
	}
	if got := wrapEmail("supercalifragilistic", 5); got != "supercalifragilistic" {
		t.Errorf("long word = %q", got)
	}
}

// TestAIDraftComposeWrapWidth proves the [compose] wrap-width setting
// drives the generated body's line width.
func TestAIDraftComposeWrapWidth(t *testing.T) {
	cfg := draftCfg(t)
	root := t.TempDir()
	p := draftFile(t, draftText)
	cfg.Compose.WrapWidth = 12
	long := strings.Repeat("word ", 30)
	st := aiDraftCompose(cfg, root, []core.Message{draftMsg(p, "alpha@example.com", 100, "m1")}, long)
	if st == nil {
		t.Fatal("nil draft")
	}
	for i, line := range strings.Split(st.Body, "\n") {
		if line != "" && len(line) > 12 {
			t.Fatalf("line %d over the configured width: %q", i, line)
		}
	}
	if strings.Join(strings.Fields(st.Body), " ") != strings.Join(strings.Fields(long), " ") {
		t.Fatalf("wrap must keep the word order: %q", st.Body)
	}
}

// TestAIDraftComposeUnparseable proves a thread with no parseable message
// yields nil - the draft must never be a blank mail.
func TestAIDraftComposeUnparseable(t *testing.T) {
	cfg := draftCfg(t)
	root := t.TempDir()
	if st := aiDraftCompose(cfg, root, nil, "x"); st != nil {
		t.Errorf("empty thread built a draft: %+v", st)
	}
	p := draftFile(t, "not a message")
	if st := aiDraftCompose(cfg, root, []core.Message{draftMsg(p, "alpha@example.com", 1, "m")}, "x"); st != nil {
		t.Errorf("unparseable thread built a draft: %+v", st)
	}
}

// TestResolveAIProvider proves the provider gate: an empty set errors, ""
// picks the first sorted name, a missing name errors, a present name wins.
func TestResolveAIProvider(t *testing.T) {
	cfg := config.Default()
	if _, err := resolveAIProvider(cfg, ""); err == nil {
		t.Fatal("empty provider set must error")
	}
	cfg.AI = map[string]config.AIProvider{
		"zebra": {Model: "z"},
		"alpha": {Model: "a"},
	}
	if p, err := resolveAIProvider(cfg, ""); err != nil || p.Model != "a" {
		t.Errorf("default provider = %+v, %v; want alpha", p, err)
	}
	if p, err := resolveAIProvider(cfg, "zebra"); err != nil || p.Model != "z" {
		t.Errorf("named provider = %+v, %v; want zebra", p, err)
	}
	if _, err := resolveAIProvider(cfg, "nope"); err == nil {
		t.Error("missing provider must error")
	}
}
