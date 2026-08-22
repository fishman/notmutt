// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package i18n

import (
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// TestResolveLanguage pins the [ui] language resolution: "auto" reads
// the locale env in POSIX order, charset and underscore region
// normalize, unparseable input (the C locale) degrades to English.
func TestResolveLanguage(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")
	t.Setenv("LANG", "it_IT.UTF-8")
	for _, tc := range []struct {
		in, want string
	}{
		{"auto", "de-DE"},  // LC_ALL wins over the lower-precedence vars
		{"de", "de"},       // a pinned tag ignores the env
		{"en_US", "en-US"}, // underscore region normalizes
		{"C", "en"},        // the C locale is not a tag
		{"", "de-DE"},      // empty means auto
	} {
		if got := ResolveLanguage(tc.in); got != tc.want {
			t.Errorf("ResolveLanguage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_MESSAGES", "")
	t.Setenv("LANG", "")
	if got := ResolveLanguage("auto"); got != "en" {
		t.Errorf("ResolveLanguage(auto) with no env = %q, want en", got)
	}
}

// TestSetLanguageAndT pins the catalog lookup: a missing key returns
// the key itself, a registered translation serves the selected
// language, and the closest shipped catalog wins.
func TestSetLanguageAndT(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	if got := T("save attachment to: "); got != "save attachment to: " {
		t.Fatalf("T(en) = %q", got)
	}
	if got := T("missing key"); got != "missing key" {
		t.Fatalf("T(missing) = %q, want the key itself", got)
	}

	bundle.AddMessages(language.Make("de"), &i18n.Message{
		ID: "save attachment to: ", Other: "anhang speichern unter: ",
	})
	SetLanguage("de")
	if got := T("save attachment to: "); got != "anhang speichern unter: " {
		t.Fatalf("T(de) = %q", got)
	}
	if got := T("missing key"); got != "missing key" {
		t.Fatalf("T(de, missing) = %q, want the key itself", got)
	}

	SetLanguage("de-DE") // base-tag match against the de catalog
	if got := T("save attachment to: "); got != "anhang speichern unter: " {
		t.Fatalf("T(de-DE) = %q", got)
	}

	SetLanguage("auto") // back to the pinned env: English
	if got := T("save attachment to: "); got != "save attachment to: " {
		t.Fatalf("T(auto) = %q, want English", got)
	}
}
