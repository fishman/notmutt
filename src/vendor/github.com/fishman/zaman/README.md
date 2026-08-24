# zaman

Natural-language date and time parsing for the whole world. One Go engine
parses English, Persian, Arabic, and Chinese - each with its own digit script
and calendar (Solar Hijri, lunar Hijri). Adding a language is adding tables
(words, numerals, calendar, handler patterns), never forking a parser.

```go
r, err := zaman.Parse("tomorrow at 10:30", nil)
r.Time // 2024-01-16 10:30:00 UTC
```

## What it does

zaman takes a natural-language phrase and resolves it to a concrete time
range, anchored to an optional reference instant (default: now). It handles
the things that break most "English date parsers with a translation layer":

- Persian: `فردا ساعت ۱۰:۳۰` -> tomorrow 10:30, on the Solar Hijri calendar,
  with Persian digits and a Saturday week start.
- Arabic: `بعد أسبوع` -> one week from now, on the lunar Hijri calendar, with
  Arabic-Indic digits.
- Chinese: `明天下午三点` -> tomorrow 3pm, from a single maximal-munch pass
  over a composed-numeral system with no spaces in sight.
- English: `next monday`, `in 2 weeks`, `december 25th`, `10:30 pm`,
  `2024-12-25`.

Each parse returns the matched substring and its byte offsets, so callers can
strip the recognized phrase out of a sentence:

```go
r, _ := zaman.Parse("meet me tomorrow at 10:30 in the lobby", nil)
r.Text  // "tomorrow at 10:30"
r.Start // 8
r.End   // 25
```

## Install

```
go get github.com/fishman/zaman
```

Go 1.25+. The single runtime dependency is `golang.org/x/text` (Unicode
normalization; see [Architecture](#architecture)).

## Usage

```go
package main

import (
	"fmt"
	"time"

	"github.com/fishman/zaman"
)

func main() {
	now := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)

	// Auto-detect the locale by Unicode script.
	r, err := zaman.Parse("فردا ساعت ۱۰:۳۰", &zaman.Options{Now: now})
	fmt.Println(r.Time) // 2024-01-16 10:30:00 +0000 UTC

	// Force a locale.
	r, _ = zaman.ParseIn("下周三", "zh", &zaman.Options{Now: now})
	fmt.Println(r.Time) // 2024-01-24 12:00:00 +0000 UTC
}
```

### API

| Call | Purpose |
|------|---------|
| `Parse(s, opts)` | auto-detect the locale, return the best match |
| `ParseIn(s, lang, opts)` | parse with a named locale, no detection |
| `List()` | registered locale names |
| `Register(l)` | add a locale |

`Options` tune parsing: `Now` (reference instant), `Locale` (parse as a
specific locale; empty auto-detects), `Location` (result timezone; nil = UTC
- pass `time.LoadLocation("Asia/Tokyo")` so "next tuesday" resolves in that
zone), `Guess` (middle/begin/end within a span), `WeekStart`,
`AmbiguousTimeRange` (when a bare clock like "10:30" may mean PM), `Endian`
(how `1/2/2024` reads).

`Result` carries `Time` (guessed point), `Span` (Begin/End range),
`Text`/`Start`/`End` (matched substring and byte offsets), and `Locale`
(which locale matched, for the auto-detect path).

## Supported locales

| Locale | Script | Digits | Calendar | Week starts | Notes |
|--------|--------|--------|----------|-------------|-------|
| `en` | Latin | western | Gregorian | Sunday | reference locale |
| `fa` | Arabic (Persian variant) | Persian + western | Solar Hijri | Saturday | ZWNJ-safe compound words (`نیمه‌شب`), noun-first grabbers (`هفته آینده`) |
| `ar` | Arabic | Arabic-Indic + western | Lunar Hijri (tabular) | Sunday | pointer-first prepositional phrases (`بعد أسبوع`), compound month names (`رمضان`) |
| `zh` | CJK | western + composed numerals (`三十二`) | Gregorian | Monday | space-free trie lexing, `下午三点` portion-first clocks |

`ja` is designed (same CJK lexer, `万`/`億` numeral bases, `KindEra` era
years) but not yet built.

## Architecture

The core is the chronic model (tokenize, tag, handle) with one deliberate
change: the locale is a table, not a fork. The pipeline is:

```
input string
  -> normalize   locale fold: NFKC, diacritic/tatweel strip, script-variant letters
  -> lex         space-splitting (Latin/Arabic) OR maximal-munch trie (CJK)
  -> tag         lexicon tries + number systems -> Token{Kind, Value, Pos, Text}
  -> handle      ordered kind-pattern handlers mutate a Span
  -> resolve     Span + Options -> time.Time
```

- **Semantic token kinds, not words.** A closed set (`KindMonth`,
  `KindClock`, `KindGrabber`, `KindScalar`, ...) every locale maps into.
  Handlers match kinds, so one handler serves any locale that emits the same
  kinds. English's `[Grabber, Week]` handler is Persian's `[Week, Grabber]`
  with the word order swapped as data.
- **Script-class lexers.** Space-script lexers split on whitespace and ZWNJ;
  the CJK lexer walks a compiled trie with no whitespace and longest-match
  wins, so `星期一` lexes whole rather than as `一` + `月` + `一`.
- **NumberSystem is a compose engine.** Three shapes cover the target
  languages: digit tables (western / Arabic-Indic / Persian), composed
  positional numerals (Chinese `三十二` = 3*10+2), and longest-match `Any`
  combos so a locale accepts several digit scripts.
- **Calendars are a seam, not an assumption.** The core never calls
  `time.Date` with calendar space. `Calendar` (Gregorian, Solar Hijri, lunar
  Hijri) does all year/month/day conversion, including span arithmetic like
  "next month" and year rollover. Persian and Arabic parse correctly on their
  own calendars, not on a Gregorian mask.
- **Normalization is one place.** `golang.org/x/text` NFKC folds Arabic
  Presentation Forms A/B and fullwidth forms; tashkeel/tatweel are dropped;
  per-locale letter tables handle script conventions NFKC does not (Arabic
  `ي`/`ة` vs Persian `ی`/`ه`).

The parse pipeline is pure and immutable: locales compile once at `init`,
parsing is goroutine-safe, and there is no mutable parser state.

## Adding a language

A contributor supplies, in order of effort:

1. **Numbers**: a `NumberSystem` (one digit table, or one
   `ComposedNumerals` table).
2. **Lexicon**: word tables for months, weekdays, day portions, relatives,
   grabbers, pointers, separators.
3. **Calendar**: reuse `Gregorian` or pick `SolarHijri`/`Islamic`.
4. **Handlers**: assemble patterns from the shared apply funcs; borrow a
   sibling locale's list as a template.
5. **Options**: week start, date order.

Each locale ships a corpus test (`TestFaCorpus`, `TestZhCorpus`, ...) of
fabricated fixtures. Non-trivial logic in the engine carries its own check
(`TestIslamicRoundTrip` walks every day across a century; the digit tables
and numerals are covered by the corpus cases).

## Design

[`DESIGN.md`](DESIGN.md) walks through the architecture; the reference
implementations that shaped it are [olebedev/when](https://github.com/olebedev/when)
(simple public surface, regex-per-language ceiling) and
[ruby chronic](https://github.com/mojombo/chronic) (the tokenize/tag/handle
grammar model, its English/whitespace/Gregorian assumptions). Both are
advisory; the decision to be a data-driven Go engine is not inherited from
either.

## Status

Working: en, fa, ar, zh. `ja` next. Deferred: lunisolar calendars, relative
repeaters ("every Tuesday"), timezone-name tables.
