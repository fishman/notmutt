// Package zaman parses natural-language dates and times in any language.
// English, Persian, Arabic, and Chinese each use their own digit script and
// calendar (Solar Hijri, lunar Hijri); a locale is a table of words,
// numerals, and handler patterns, never a parser fork.
//
// # Quick start
//
//	r, err := zaman.Parse("tomorrow at 10:30", nil)
//	r.Time // 2024-01-16 10:30:00 +0000 UTC
//
// Parse auto-detects the locale by Unicode script and returns the best match.
// ParseIn forces a locale and skips detection.
//
// # Locales
//
// Registered by default: en (Gregorian), fa (Solar Hijri), ar (lunar Hijri),
// zh (Gregorian). Parse accepts the digit scripts, calendars, and word order
// each language actually uses: Persian digits with a Saturday week start,
// Arabic-Indic digits with Hijri month names, Chinese composed numerals with
// no whitespace. Register adds a locale without forking the engine.
//
// # Model
//
// A Locale is one immutable value: a lexer (space-splitting for Latin/Arabic
// script, maximal-munch trie for CJK), a NumberSystem (digit tables or
// composed positional numerals), a Calendar (Gregorian, Solar Hijri, lunar
// Hijri), a lexicon, and an ordered handler list. Handlers match token kinds,
// never words, so a handler written for one locale works for any locale whose
// lexicon emits the same kinds.
//
// The only dependency is golang.org/x/text (Unicode NFKC normalization,
// folding Arabic Presentation Forms A/B and fullwidth forms before matching).
//
// # Results
//
// A parse returns a Result carrying a time range (Span), a guessed point
// within it (Time), the matched substring, and its byte offsets in the input
// so a caller can strip the recognized phrase from a sentence.
package zaman
