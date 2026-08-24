package zaman

import (
	"fmt"
	"sort"
	"time"
)

// GuessMode selects how a time is derived from a parsed Span.
type GuessMode uint8

const (
	GuessMiddle GuessMode = iota
	GuessBegin
	GuessEnd
	GuessFull // no time: caller uses Result.Span directly
)

// Endian selects how ambiguous numeric dates (1/2/2024) are read.
type Endian uint8

const (
	EndianMDY Endian = iota
	EndianDMY
	EndianYMD
)

// Options tune parsing. Zero values mean "use the locale default".
type Options struct {
	Now                time.Time      // reference instant; zero = time.Now
	Locale             string         // parse as this locale; empty = auto-detect
	Location           *time.Location // result timezone; nil = UTC
	Guess              GuessMode      // zero = GuessMiddle
	WeekStart          time.Weekday   // zero = locale default
	AmbiguousTimeRange int            // bare clock adds the PM occurrence when hour >= range; 0 = locale default; 24 = never
	Endian             Endian         // zero = locale default
}

// Span is a resolved time range. A point-in-time parse has Begin == End.
type Span struct {
	Begin time.Time
	End   time.Time
}

func (s Span) IsZero() bool { return s.Begin.IsZero() }

// Guess derives a single time from the span.
func (s Span) Guess(mode GuessMode) time.Time {
	switch mode {
	case GuessBegin:
		return s.Begin
	case GuessEnd:
		return s.End
	default:
		return s.Begin.Add(s.End.Sub(s.Begin) / 2)
	}
}

// Result is the outcome of a parse.
type Result struct {
	Time   time.Time // guessed point within Span
	Span   Span
	Text   string // matched substring, for stripping from a sentence
	Start  int    // byte offset of Text in the input
	End    int
	Locale string // locale that matched (auto-detect report)
}

// registry holds registered locales. Immutable after init.
var registry = map[string]*Locale{}

// Register adds a locale to the registry.
func Register(l *Locale) error {
	if l == nil || l.name == "" {
		return fmt.Errorf("zaman: register requires a named locale")
	}
	if _, dup := registry[l.name]; dup {
		return fmt.Errorf("zaman: locale %q already registered", l.name)
	}
	registry[l.name] = l
	return nil
}

// List returns the registered locale names, sorted.
func List() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Parse parses s and returns the best match. When Options.Locale names a
// locale, Parse is that locale and generation follows its calendar, digits,
// and defaults; otherwise the locale is auto-detected by script.
func Parse(s string, opts *Options) (*Result, error) {
	if opts != nil && opts.Locale != "" {
		return ParseIn(s, opts.Locale, opts)
	}
	var best *Result
	for _, name := range detect(s) {
		l := registry[name]
		if l == nil {
			continue
		}
		r, err := l.parse(s, opts)
		if err != nil {
			return nil, err
		}
		if r != nil && (best == nil || len(r.Text) > len(best.Text)) {
			best = r
		}
	}
	return best, nil
}

// ParseIn parses s with the named locale.
func ParseIn(s, lang string, opts *Options) (*Result, error) {
	l := registry[lang]
	if l == nil {
		return nil, fmt.Errorf("zaman: unknown locale %q", lang)
	}
	return l.parse(s, opts)
}

// detect narrows the locale candidates by Unicode script. Cheap sniff, not
// a full parse: CJK runes -> zh/ja, Arabic-script runes -> fa/ar, else en.
func detect(s string) []string {
	hasCJK, hasArabic, hasPersian := false, false, false
	for _, r := range s {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF, r >= 0x3000 && r <= 0x30FF, r >= 0x3400 && r <= 0x4DBF:
			hasCJK = true
		case r == 0x067E || r == 0x0686 || r == 0x0698 || r == 0x06AF || r == 0x06A9 || r == 0x06CC || (r >= 0x06F0 && r <= 0x06F9):
			hasPersian, hasArabic = true, true
		case r >= 0x0600 && r <= 0x06FF, r >= 0xFB50 && r <= 0xFEFF:
			hasArabic = true
		}
	}
	switch {
	case hasPersian:
		return []string{"fa", "ar"}
	case hasArabic:
		return []string{"ar", "fa"}
	case hasCJK:
		return []string{"zh", "ja"}
	}
	return []string{"en"}
}
