// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestBriefingFull pins a complete briefing: every contact, company, and
// research field renders, and the total stays under the cap. The signature
// takes no mail input, so no mail field can even be passed.
func TestBriefingFull(t *testing.T) {
	contact := Contact{
		FirstName: "Alpha", LastName: "Atlas", JobTitle: "Head of Procurement",
		ID: "12345", Email: "alpha@example.com", CompanyID: "67890",
		CreatedAt: time.Unix(1700000000, 0),
	}
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
	// The routing fields (id, email, company id, created) are identity, not
	// briefing content: none may leak into the prompt.
	for _, leak := range []string{
		"12345", "alpha@example.com", "67890", contact.CreatedAt.Format("2006-01-02"),
	} {
		if strings.Contains(out, leak) {
			t.Errorf("briefing leaks routing field %q\n%s", leak, out)
		}
	}
	// Research lines are 1-indexed and carry the source URL.
	for _, want := range []string{
		"\n1. Acme opens a Detroit plant | https://news.example.com/acme-plant | Acme announced a new widget plant.",
		"\n2. Acme hires a CTO | https://news.example.com/acme-cto | Atlas joins from a robotics firm.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("briefing missing research line %q\n%s", want, out)
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

// TestBriefingResearchBlanksUnknown pins the never-blank posture for
// research lines: a wrapping Result (blank title and URL by construction)
// renders placeholder cells, not empty gaps.
func TestBriefingResearchBlanksUnknown(t *testing.T) {
	out, err := briefing(Contact{}, Company{}, []Result{{Snippet: "no structured facts returned"}})
	if err != nil {
		t.Fatalf("briefing: %v", err)
	}
	if want := "\n1. unknown | unknown | no structured facts returned"; !strings.Contains(out, want) {
		t.Errorf("briefing research line missing %q\n%s", want, out)
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
