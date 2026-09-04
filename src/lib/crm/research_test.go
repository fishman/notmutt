// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package crm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"notmutt/config"
)

// chatArgs captures the arguments of one Research chat invocation.
type chatArgs struct {
	p      config.AIProvider
	model  string
	system string
	text   string
	emit   func(string)
}

// chatRecorder returns a fake ChatFn that records its arguments and replies
// with reply. It exercises emit, so a Research that passes nil fails here.
func chatRecorder(t *testing.T, reply string, got *chatArgs) ChatFn {
	t.Helper()
	return func(_ context.Context, p config.AIProvider, model, system, text string, emit func(string)) (string, error) {
		*got = chatArgs{p: p, model: model, system: system, text: text, emit: emit}
		if emit == nil {
			t.Error("chat emit is nil; the seam promises a callback")
			return reply, nil
		}
		emit("")
		return reply, nil
	}
}

func researchProvider() config.AIProvider {
	return config.AIProvider{Type: "anthropic", Model: "claude-research"}
}

func researchCompany() Company {
	return Company{Name: "Acme Corp", Domain: "acme.example", Industry: "Software", Description: "Makes example software"}
}

func wantResults(t *testing.T, got, want []Result) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("results = %+v\nwant %+v", got, want)
	}
}

// TestResearchParsesStructured pins structured numbered-list parsing: the
// ordinal is dropped, UNKNOWN (any case) and empty sources become a blank
// URL, blank lines are skipped, and the chat receives the provider model,
// a format-carrying system prompt, and a company-naming user text.
func TestResearchParsesStructured(t *testing.T) {
	const reply = "1. Sells analytics software | https://acme.example/products | Acme's core product line.\n" +
		"2) Employs roughly 500 people | UNKNOWN | Privately held; no public headcount.\n" +
		"\n" +
		"3: Raised a Series B in 2025 | unknown | Round led by Atlas Ventures.\n"
	want := []Result{
		{Title: "Sells analytics software", URL: "https://acme.example/products", Snippet: "Acme's core product line."},
		{Title: "Employs roughly 500 people", URL: "", Snippet: "Privately held; no public headcount."},
		{Title: "Raised a Series B in 2025", URL: "", Snippet: "Round led by Atlas Ventures."},
	}
	var got chatArgs
	results, err := Research(context.Background(), researchProvider(), researchCompany(), chatRecorder(t, reply, &got))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	wantResults(t, results, want)
	if got.model != researchProvider().Model {
		t.Errorf("model = %q, want the provider model %q", got.model, researchProvider().Model)
	}
	if !strings.Contains(got.system, "source-url-or-UNKNOWN") {
		t.Errorf("system prompt lacks the reply format: %q", got.system)
	}
	if !strings.Contains(got.text, "Acme Corp") || !strings.Contains(got.text, "acme.example") {
		t.Errorf("user text = %q, want the company name and domain", got.text)
	}
}

// TestResearchUnstructuredLineWraps pins the per-line fallback: a line that
// is not in the pipe format becomes one Result carrying that line as its
// Snippet, keeping its place among the structured facts.
func TestResearchUnstructuredLineWraps(t *testing.T) {
	const reply = "Sells analytics software | https://acme.example/products | Core product.\n" +
		"note: not in the pipe format\n" +
		"Employs 500 people | UNKNOWN | No public headcount.\n"
	want := []Result{
		{Title: "Sells analytics software", URL: "https://acme.example/products", Snippet: "Core product."},
		{Snippet: "note: not in the pipe format"},
		{Title: "Employs 500 people", URL: "", Snippet: "No public headcount."},
	}
	var got chatArgs
	results, err := Research(context.Background(), researchProvider(), researchCompany(), chatRecorder(t, reply, &got))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	wantResults(t, results, want)
}

// TestResearchGarbageCollapses pins the whole-reply fallback: an entirely
// off-format reply - even across several lines - degrades to a single
// wrapping Result rather than one Result per chopped line.
func TestResearchGarbageCollapses(t *testing.T) {
	const reply = "\nI could not find reliable current data about this company.\nDouble-check the name and try again.\n"
	want := []Result{{Snippet: "I could not find reliable current data about this company.\nDouble-check the name and try again."}}
	var got chatArgs
	results, err := Research(context.Background(), researchProvider(), researchCompany(), chatRecorder(t, reply, &got))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	wantResults(t, results, want)
}

// TestResearchEmptyReply pins the empty output: no crash, no Results.
func TestResearchEmptyReply(t *testing.T) {
	var got chatArgs
	results, err := Research(context.Background(), researchProvider(), researchCompany(), chatRecorder(t, "  \n  ", &got))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
}

// TestResearchChatError pins the error path: a chat failure surfaces
// unchanged and any partial reply is dropped, never parsed into facts.
func TestResearchChatError(t *testing.T) {
	sentinel := errors.New("chat down")
	results, err := Research(context.Background(), researchProvider(), researchCompany(), func(_ context.Context, _ config.AIProvider, _, _, _ string, _ func(string)) (string, error) {
		return "partial reply", sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the chat error", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil on error", results)
	}
}

// TestResearchCapsAndTruncates pins both caps: at most 8 Results, earliest
// lines first, and every snippet truncated to 600 runes without splitting a
// multi-byte rune.
func TestResearchCapsAndTruncates(t *testing.T) {
	snippet := strings.Repeat("界", 700)
	var b strings.Builder
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&b, "%d. Fact number %d | https://n%d.example | %s\n", i, i, i, snippet)
	}
	var got chatArgs
	results, err := Research(context.Background(), researchProvider(), researchCompany(), chatRecorder(t, b.String(), &got))
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(results) != maxResults {
		t.Fatalf("got %d results, want %d", len(results), maxResults)
	}
	for i, r := range results {
		if r.Snippet != strings.Repeat("界", maxSnippetLen) {
			t.Errorf("result %d snippet = %d runes, want %d", i, len([]rune(r.Snippet)), maxSnippetLen)
		}
	}
	if results[0].Title != "Fact number 1" {
		t.Errorf("results[0].Title = %q, want Fact number 1", results[0].Title)
	}
	if results[maxResults-1].Title != "Fact number 8" {
		t.Errorf("results[7].Title = %q, want Fact number 8 (earliest lines win)", results[maxResults-1].Title)
	}
}

// TestResearchNilChat pins the nil-chat guard: a missing callback is a
// caller error, not a panic.
func TestResearchNilChat(t *testing.T) {
	if _, err := Research(context.Background(), researchProvider(), researchCompany(), nil); err == nil {
		t.Fatal("nil chat fn: want an error")
	}
}
