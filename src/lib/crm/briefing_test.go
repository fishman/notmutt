// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBriefingFull pins a complete briefing: every contact, company, and
// research field renders, and the total stays under the cap. The signature
// takes no mail input, so no mail field can even be passed.
func TestBriefingFull(t *testing.T) {
	contact := Contact{FirstName: "Alpha", LastName: "Atlas", JobTitle: "Head of Procurement"}
	company := Company{
		Name: "Acme Corp", Domain: "acme.example.com", Industry: "Industrial widgets",
		Description: "Acme builds industrial widgets for the shipping trade.",
	}
	rs := []Result{
		{Title: "Acme opens a Detroit plant", URL: "https://news.example.com/acme-plant", Snippet: "Acme announced a new widget plant."},
		{Title: "Acme hires a CTO", URL: "https://news.example.com/acme-cto", Snippet: "Atlas joins from a robotics firm."},
	}
	out, err := briefing(contact, company, rs)
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	if n := utf8.RuneCountInString(out); n > maxBriefLen {
		t.Errorf("briefing = %d runes, want <= %d", n, maxBriefLen)
	}
	for _, want := range []string{
		"Alpha Atlas", "Head of Procurement",
		"Acme Corp", "acme.example.com", "Industrial widgets", "Acme builds industrial widgets",
		"Acme opens a Detroit plant", "Acme announced a new widget plant.",
		"Acme hires a CTO", "Atlas joins from a robotics firm.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("briefing missing %q\n%s", want, out)
		}
	}
}

// TestBriefingEmptyFieldsUnknown pins the placeholder rendering: blank
// contact and company fields read as "unknown", never as a blank gap.
func TestBriefingEmptyFieldsUnknown(t *testing.T) {
	out, err := briefing(Contact{}, Company{}, nil)
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	for _, want := range []string{
		"Contact: unknown - unknown",
		"Company: unknown | unknown | unknown",
		"Description: unknown",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("briefing missing %q\n%s", want, out)
		}
	}
}

// TestBriefingBoundedTruncates pins the deterministic cap: a synthetic
// oversized input (a long multi-byte description plus full research) yields
// a briefing at or under the cap with valid UTF-8 - no mid-rune cut - and
// keeps its leading contact block.
func TestBriefingBoundedTruncates(t *testing.T) {
	contact := Contact{FirstName: "Alpha", LastName: "Atlas", JobTitle: "Head of Procurement"}
	company := Company{
		Name: "Acme Corp", Domain: "acme.example.com", Industry: "Industrial widgets",
		Description: strings.Repeat("界", 4000),
	}
	rs := make([]Result, maxResults)
	for i := range rs {
		rs[i] = Result{
			Title:   strings.Repeat("界", 400),
			URL:     "https://news.example.com/" + strings.Repeat("界", 100),
			Snippet: strings.Repeat("界", maxSnippetLen),
		}
	}
	out, err := briefing(contact, company, rs)
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	if n := utf8.RuneCountInString(out); n > maxBriefLen {
		t.Errorf("briefing = %d runes, want <= %d", n, maxBriefLen)
	}
	if !utf8.ValidString(out) {
		t.Error("briefing is not valid utf-8: mid-rune truncation")
	}
	if !strings.Contains(out, "Head of Procurement") {
		t.Errorf("briefing lost the leading section\n%s", out)
	}
}

// TestBriefingInvalidUTF8 pins the error path: a field carrying malformed
// UTF-8 cannot be rendered faithfully and refuses the briefing instead of
// substituting replacement runes into the prompt.
func TestBriefingInvalidUTF8(t *testing.T) {
	contact := Contact{FirstName: "Alpha", LastName: "\xff", JobTitle: "CTO"}
	if _, err := briefing(contact, Company{}, nil); err == nil {
		t.Fatal("malformed utf-8 contact name: want an error")
	}
	contact.LastName = "Atlas"
	rs := []Result{{Title: "\xff\xfe", Snippet: "snippet"}}
	if _, err := briefing(contact, Company{}, rs); err == nil {
		t.Fatal("malformed utf-8 research title: want an error")
	}
}
