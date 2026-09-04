// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package crm

import (
	"context"
	"errors"
	"strings"

	"notmutt/config"
)

const (
	// researchSystem is the researcher role prompt: a numbered list, one
	// fact per line, so the reply parses line-wise into Results.
	researchSystem = `You are a researcher. State concrete, current facts about the company the user names: what it sells, its size, and recent news. Reply only as a numbered list, one fact per line:
<fact> | <source-url-or-UNKNOWN> | <2-line snippet>
Keep every fact on one physical line; the snippet is a one-to-two-sentence summary on that same line.`

	maxResults    = 8
	maxSnippetLen = 600
)

// ChatFn is the chat backend Research calls. The signature matches ai.Chat
// exactly, so the app adapter passes that function value directly and tests
// pass a fake; injecting the call keeps Research a pure function over
// (provider, company, reply) with no network and no app imports.
type ChatFn func(ctx context.Context, p config.AIProvider, model, system, text string, emit func(string)) (string, error)

// Result is one researched fact.
type Result struct {
	Title, URL, Snippet string
}

// Research asks the model for concrete current facts about company (what it
// sells, size, recent news) and parses the numbered-list reply into at most
// maxResults Results. A reply line matching "fact | source | snippet"
// becomes one Result; an empty or UNKNOWN source yields a blank URL. A line
// that does not match wraps as a Result carrying that line as its Snippet,
// and a reply with no structured line at all collapses to one wrapping
// Result, so a refusal stays readable. An empty reply returns no Results.
// Every field truncates to maxSnippetLen runes. Chat errors return unchanged.
// Research touches no mail and imports no app package.
func Research(ctx context.Context, p config.AIProvider, company Company, chat ChatFn) ([]Result, error) {
	if chat == nil {
		return nil, errors.New("crm: research: nil chat fn")
	}
	out, err := chat(ctx, p, p.Model, researchSystem, researchText(company), func(string) {})
	if err != nil {
		return nil, err
	}
	return parseFacts(out), nil
}

// researchText names the company for the model. Fields present on the
// client Company are joined; an all-blank Company degrades to a stable
// placeholder rather than an empty user message.
func researchText(c Company) string {
	parts := make([]string, 0, 4)
	if s := strings.TrimSpace(c.Name); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(c.Domain); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(c.Industry); s != "" {
		parts = append(parts, "industry: "+s)
	}
	if s := strings.TrimSpace(c.Description); s != "" {
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "unknown company"
	}
	return strings.Join(parts, " - ")
}

// parseFacts turns the model reply into Results. Blank lines are ignored;
// parsing stops once maxResults results have been collected. When no line
// parsed, the whole reply is returned as one wrapping Result.
func parseFacts(out string) []Result {
	var results []Result
	structured := 0
	for _, line := range strings.Split(out, "\n") {
		if len(results) == maxResults {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r, ok := parseLine(line); ok {
			structured++
			results = append(results, r)
			continue
		}
		results = append(results, Result{Snippet: capSnippet(line)})
	}
	if structured == 0 {
		if t := strings.TrimSpace(out); t != "" {
			return []Result{{Snippet: capSnippet(t)}}
		}
		return nil
	}
	return results
}

// parseLine parses one reply line into a Result. A line matches when it
// carries two '|' separators and a non-empty fact; an optional leading
// list ordinal ("1.", "1)", "1:") is dropped first. An empty or UNKNOWN
// source maps to a blank URL.
func parseLine(line string) (Result, bool) {
	line = stripOrdinal(strings.TrimSpace(line))
	fact, rest, ok := strings.Cut(line, "|")
	if !ok {
		return Result{}, false
	}
	src, snip, ok := strings.Cut(strings.TrimSpace(rest), "|")
	if !ok {
		return Result{}, false
	}
	fact = strings.TrimSpace(fact)
	src = strings.TrimSpace(src)
	if fact == "" {
		return Result{}, false
	}
	if src == "" || strings.EqualFold(src, "UNKNOWN") {
		src = ""
	}
	return Result{Title: capSnippet(fact), URL: capSnippet(src), Snippet: capSnippet(snip)}, true
}

// stripOrdinal removes a leading list ordinal ("1.", "1)", "1:") when one
// opens the line; anything else is returned untouched.
func stripOrdinal(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i == len(line) || (line[i] != '.' && line[i] != ')' && line[i] != ':') {
		return line
	}
	i++
	if i == len(line) {
		return ""
	}
	if line[i] == ' ' {
		return line[i+1:]
	}
	return line
}

// capSnippet trims and length-caps a Result snippet.
func capSnippet(s string) string {
	return truncateRunes(strings.TrimSpace(s), maxSnippetLen)
}

// truncateRunes truncates s to at most max runes, never splitting a
// multi-byte rune; shorter strings return unchanged.
func truncateRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
