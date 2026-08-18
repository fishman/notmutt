// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package i18n serves the interface language (decision record 24):
// locale/*.toml ship inside the binary via go:embed and load through
// Bundle.LoadMessageFileFS at startup - never runtime reads. The Lua
// translate() binding lives in the app adapter under the lua build
// tag, backed by the SAME bundle. Catalog keys are the English strings
// themselves, so a missing entry falls back to the key - English works
// with an empty catalog, en.toml is the reference and merge template.
package i18n

import (
	"embed"
	"os"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locale/*.toml
var catalogs embed.FS

var (
	bundle = i18n.NewBundle(language.English)
	mu     sync.RWMutex
	local  = i18n.NewLocalizer(bundle, language.English.String())
)

func init() {
	bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	entries, err := catalogs.ReadDir("locale")
	if err != nil {
		return
	}
	for _, e := range entries {
		bundle.LoadMessageFileFS(catalogs, "locale/"+e.Name())
	}
}

// SetLanguage selects the interface language from the [ui] language
// value: "auto" resolves from the locale env, a BCP 47 tag pins one.
// The closest shipped catalog wins (base-tag match), English is the
// last-resort fallback - an unsupported language degrades to the
// reference instead of failing.
func SetLanguage(v string) {
	tag := ResolveLanguage(v)
	lang, _, _ := language.NewMatcher(bundle.LanguageTags()).Match(language.Make(tag))
	mu.Lock()
	defer mu.Unlock()
	local = i18n.NewLocalizer(bundle, lang.String(), language.English.String())
}

// ResolveLanguage maps a [ui] language value to a BCP 47 tag: "auto"
// reads LC_ALL/LC_MESSAGES/LANG in POSIX order, the charset suffix and
// underscore region normalize ("en_US.UTF-8" -> "en-US"). Unparseable
// input (the C locale) resolves to English.
func ResolveLanguage(v string) string {
	lang := v
	if v == "" || v == "auto" {
		lang = os.Getenv("LC_ALL")
		if lang == "" {
			lang = os.Getenv("LC_MESSAGES")
		}
		if lang == "" {
			lang = os.Getenv("LANG")
		}
	}
	if lang == "" {
		return language.English.String()
	}
	lang = strings.SplitN(lang, ".", 2)[0]
	lang = strings.ReplaceAll(lang, "_", "-")
	if _, err := language.Parse(lang); err != nil {
		return language.English.String()
	}
	return lang
}

// T translates id through the session language; a missing key returns
// the id itself (the key IS the English string).
func T(id string) string {
	mu.RLock()
	l := local
	mu.RUnlock()
	msg, err := l.Localize(&i18n.LocalizeConfig{MessageID: id})
	if err != nil {
		return id
	}
	return msg
}
