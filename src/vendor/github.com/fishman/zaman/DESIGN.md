# zaman - design

zaman is a Go library for parsing natural-language dates and times. Idiomatic
Go, a data-driven locale model: any language plugs in as tables (words,
numerals, calendar, handler patterns), never as a parser fork.

## Why it exists

The two reference implementations each get one thing right:

- **olebedev/when**: a simple public surface, but localization means rewriting
  every regex per language. Its Chinese locale proves the ceiling: 100+
  hand-enumerated word entries generated into one giant alternation regex.
- **chronic** (ruby): the right core - tokenize, tag with semantic tags,
  match handler patterns, mutate a span. Localization means forking the whole
  gem. Three structural limits: whitespace-only tokenizer (CJK has no
  spaces), English-centric normalization, and `time.Time`/Gregorian assumed
  throughout (Persian Solar Hijri and Arabic lunar Hijri break on the
  calendar, not the grammar).

zaman keeps chronic's tokenize/tag/handle split and makes the locale a table,
not a fork.

## Core idea: a locale is data

A `Locale` is one immutable, compiled-from-tables value. No parser code is
written per language; the engine is shared. Contributors add a language by
filling in tables: a lexer (space-script or CJK trie), a NumberSystem (digit
table or composed numerals), a Calendar (Gregorian, Solar Hijri, lunar
Hijri), a lexicon, and an ordered handler list.

Locales register once at init and are immutable after; all parse stages are
pure, so parsing is goroutine-safe with no mutable state.

## The pipeline

```
input string
  -> normalize   locale fold: NFKC (Arabic Presentation Forms A/B, fullwidth),
                 diacritic/tatweel strip, script-variant letters, ZWNJ rules
  -> lex         script-class lexer: space-splitting OR maximal-munch trie
  -> tag         lexicon tries + number systems -> Token{Kind, Value, Pos, Text}
  -> handle      ordered kind-pattern handlers mutate a Span
  -> resolve     Span + Options -> time.Time
```

## Token model: semantic, not lexical

One closed set of semantic `Kind`s every locale maps into: `KindMonth`,
`KindWeekday`, `KindClock` (minutes since midnight), `KindDayPortion`,
`KindGrabber`, `KindPointer`, `KindScalar`, `KindOrdinal`, `KindSeparator`,
`KindRelative`, `KindYear`/`KindWeek`/`KindMonthUnit`/`KindHour`/`KindMinute`/
`KindSecond`. Handlers match on kinds, never on words, so a handler written
for English works for Japanese if the Japanese lexicon emits the same kind.

## Lexer: script classes, not languages

Three lexers, shared by all locales of the same script:

- **spaceLexer** (Latin, Arabic): split on whitespace and ZWNJ (a word
  boundary in Persian, not noise).
- **clock reading**: a run of digits with separators - `10:30`, `۱۰:۳۰` -
  lexes as ONE `KindClock` token carrying minutes since midnight. The 12h/24h
  and AM/PM association happens at handler time, never in the lexer.
- **cjkLexer** (Chinese, Japanese): maximal-munch over a compiled trie of the
  whole lexicon. No whitespace. `明天下午三点` lexes in one pass;
  longest match wins so `星期一` beats `一`.

Arabic-script normalization is one place, shared by ar and fa: NFKC folds
Presentation Forms A/B and fullwidth forms; tashkeel/tatweel dropped;
per-locale letter tables handle what NFKC does not (Arabic `ي`/`ة` vs Persian
`ی`/`ه`). Bidi needs no engine handling: text is stored in logical order and
lexed left-to-right; RTL is a renderer's job.

## NumberSystem: the compose engine

The target languages need three shapes, and only three:

1. **DigitSystem**: a rune->digit table (western, Arabic-Indic, Persian).
   Arabic and Persian accept both their script digits and western digits.
2. **ComposedNumerals**: positional numeral words, shared by zh and ja with
   different unit tables. `三十二` = 3*10+2 = 32; big bases `万`/`億` spill
   into a total (`三十二万` = 32*10^4). One algorithm, table-driven - this is
   the correctness win over when's enumeration maps.
3. **Any**: tries each system and keeps the longest match, so a locale
   accepts several digit scripts at once.

## Calendar: lift time construction out of the core

Persian must never see Gregorian, so the core only ever asks a `Calendar`
interface (`ToTime`, `FromTime`, `DaysInMonth`) for year/month/day math -
including span arithmetic like "next month" and year rollover. Implementations
are Gregorian, Solar Hijri (fa), and lunar Hijri (ar, tabular). The handler
never does calendar math itself; that seam is what makes Persian and Arabic a
table addition rather than a fork.

## Handlers: grammar as data

An ordered list of kind-pattern handlers, longest match first, first match
wins. The apply funcs are a fixed shared library (`applyClock`,
`applyGrabberUnit`, `applyScalarUnit`, `applyWeekday`, ...); a locale
assembles its grammar from existing apply funcs and only occasionally needs a
new one (which becomes a candidate for the shared library, not locale code).
Date order (endianness), week start, and ambiguous-time range are per-locale
options, not hardcoded logic.

## Locale extension contract

To add a language a contributor supplies, in order of effort:

1. **Numbers**: a `NumberSystem` (one digit table, or one composed table).
2. **Lexicon**: word tables for months, weekdays, day portions, relatives,
   grabbers, pointers, separators.
3. **Calendar**: reuse `Gregorian` or pick `SolarHijri`/`Islamic`.
4. **Handlers**: assemble patterns from the shared apply funcs; borrow a
   sibling locale's list as a template.
5. **Options**: week start, date order.

No parser code is required for any target language. The only language-
specific code in the whole engine is the script-class lexers and the one
numeral-compose algorithm.

## Non-goals (v0.1)

- Lunisolar calendars (Chinese festivals, Japanese era years are a Gregorian
  naming convention, not a calendar). The `Calendar` seam leaves the slot.
- Timezone *name* resolution beyond a fixed table per locale. Offsets and
  IANA names belong to the OS, not the parser.
- Fuzzy/natural intervals ("between 3 and 4"). Exact spans only.
- Relative repeaters ("every Tuesday"). `Span` already represents ranges, so
  a repeater is an Add, not a redesign.
