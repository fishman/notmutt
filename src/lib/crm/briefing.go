// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua && crm

package crm

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"notmutt/core"
)

// maxBriefLen caps the assembled briefing in runes. Contact and company
// fields are small, but the company description and research results are
// unbounded foreign text, so the joined prompt truncates to stay bounded
// before it reaches the model.
const maxBriefLen = 8000

// briefing assembles the analysis prompt from CRM + research: a labeled
// block for the contact, one for the company, then one line per researched
// fact. It takes no mail input by construction - mail grounding is a
// separate gated call (Task 10). A blank contact or company field renders
// as "unknown". Every incoming field is UTF-8-validated and F1-sanitized
// (core.SanitizeText) before joining; a field that is not valid UTF-8
// cannot be rendered faithfully and errors. The joined output is capped at
// maxBriefLen runes: an overflow truncates the tail, never mid-rune.
func briefing(contact Contact, company Company, rs []Result) (string, error) {
	sec, err := contactSection(contact)
	if err != nil {
		return "", err
	}
	sections := []string{sec}
	sec, err = companySection(company)
	if err != nil {
		return "", err
	}
	sections = append(sections, sec)
	if sec, err = researchSection(rs); err != nil {
		return "", err
	} else if sec != "" {
		sections = append(sections, sec)
	}
	return truncateRunes(strings.Join(sections, "\n\n"), maxBriefLen), nil
}

// contactSection renders the contact identity as "name - title"; a blank
// name or title renders as "unknown".
func contactSection(c Contact) (string, error) {
	name, err := briefField("contact name", c.FirstName+" "+c.LastName)
	if err != nil {
		return "", err
	}
	title, err := briefField("contact title", c.JobTitle)
	if err != nil {
		return "", err
	}
	return "Contact: " + orUnknown(name) + " - " + orUnknown(title), nil
}

// companySection renders the company: name/domain/industry on one line with
// the description under it; a blank field renders as "unknown".
func companySection(c Company) (string, error) {
	name, err := briefField("company name", c.Name)
	if err != nil {
		return "", err
	}
	domain, err := briefField("company domain", c.Domain)
	if err != nil {
		return "", err
	}
	industry, err := briefField("company industry", c.Industry)
	if err != nil {
		return "", err
	}
	desc, err := briefField("company description", c.Description)
	if err != nil {
		return "", err
	}
	return "Company: " + orUnknown(name) + " | " + orUnknown(domain) + " | " + orUnknown(industry) +
		"\nDescription: " + orUnknown(desc), nil
}

// researchSection renders one numbered line per Result. An empty slice
// yields no section. Lines are not re-truncated here: the snippets are
// already capped by Research and the whole joins under the briefing cap.
func researchSection(rs []Result) (string, error) {
	if len(rs) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("Research:")
	for i, r := range rs {
		title, err := briefField("research title", r.Title)
		if err != nil {
			return "", err
		}
		url, err := briefField("research url", r.URL)
		if err != nil {
			return "", err
		}
		snippet, err := briefField("research snippet", r.Snippet)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n%d. %s | %s | %s", i+1, title, url, snippet)
	}
	return b.String(), nil
}

// briefField trims and F1-sanitizes one foreign field before it joins the
// prompt. Malformed UTF-8 cannot be rendered without substitution and is a
// caller error rather than silent garbage in the model prompt.
func briefField(name, s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("crm: briefing: %s: not valid utf-8", name)
	}
	return core.SanitizeText(strings.TrimSpace(s)), nil
}

// orUnknown maps a blank field to the literal "unknown" placeholder.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
