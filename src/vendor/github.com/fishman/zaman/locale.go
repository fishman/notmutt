package zaman

import (
	"strings"
	"time"
)

// word pairs a lexeme with the tokens it emits.
type word struct {
	text string
	toks []Token
}

// handler is a token-kind pattern plus the apply func it triggers. raw
// handlers receive separator tokens too (numeric dates need to distinguish
// dash-ISO from slash-endian); others get separators stripped.
type handler struct {
	kind  []Kind
	raw   bool
	apply func(s *span, toks []Token)
}

func strip(toks []Token) []Token {
	out := make([]Token, 0, len(toks))
	for _, t := range toks {
		if t.Kind != KindSeparator {
			out = append(out, t)
		}
	}
	return out
}

// Locale is one language/calendar configuration.
type Locale struct {
	name     string
	lex      func(s string, l *Locale) []Token
	fold     *fold
	cjk      bool
	num      NumberSystem
	calendar Calendar
	lexicon  map[string][]Token
	trie     trie
	handlers []handler

	keepZWNJ      bool // lexer keeps ZWNJ inside compounds; lexicon matches both
	weekStart     time.Weekday
	ambigRange    int
	defaultEndian Endian
}

// addWords folds each word into the lexicon (space scripts) or the trie
// (CJK scripts).
func (l *Locale) addWords(ws []word) {
	if l.lexicon == nil {
		l.lexicon = make(map[string][]Token, len(ws))
	}
	for _, w := range ws {
		if l.cjk {
			l.trie.insert(w.text, w.toks)
			continue
		}
		key := w.text
		if l.fold != nil {
			key = l.fold.apply(key)
		}
		l.lexicon[key] = w.toks
		if l.keepZWNJ {
			l.lexicon[strings.ReplaceAll(key, "‌", "")] = w.toks
		}
	}
}

// parse runs the pipeline over s and returns the best Result or nil when
// nothing matched.
func (l *Locale) parse(s string, opts *Options) (*Result, error) {
	sig := l.lex(s, l)
	if len(sig) == 0 {
		return nil, nil
	}
	sp := &span{
		now:           now(opts),
		cal:           l.calendar,
		loc:           loc(opts),
		weekStart:     l.weekStart,
		ambigRange:    l.ambigRange,
		defaultEndian: l.defaultEndian,
	}
	if opts != nil {
		if opts.WeekStart != 0 {
			sp.weekStart = opts.WeekStart
		}
		if opts.AmbiguousTimeRange != 0 {
			sp.ambigRange = opts.AmbiguousTimeRange
		}
		if opts.Endian != 0 {
			sp.defaultEndian = opts.Endian
		}
	}
	guess := GuessMiddle
	if opts != nil {
		guess = opts.Guess
	}
	i, matched := 0, false
	for i < len(sig) {
		apply, raw, toks, end := l.matchHandler(sig, i)
		if apply == nil {
			i++
			continue
		}
		if !raw {
			toks = strip(toks)
		}
		if !matched {
			sp.textStart = toks[0].Pos
		}
		if tEnd := toks[len(toks)-1].Pos + len(toks[len(toks)-1].Text); tEnd > sp.textEnd {
			sp.textEnd = tEnd
		}
		apply(sp, toks)
		i = end
		matched = true
	}
	if !matched {
		return nil, nil
	}
	return sp.result(s, l, guess), nil
}

// matchHandler returns the longest pattern matching sig at i (ties go to the
// handler listed first). Separators are skipped between matched tokens; end
// is the index just past the last consumed token.
func (l *Locale) matchHandler(sig []Token, i int) (func(*span, []Token), bool, []Token, int) {
	bestN := -1
	var best func(*span, []Token)
	var bestToks []Token
	bestRaw, bestEnd := false, i
	for _, h := range l.handlers {
		var m []Token
		ti, k := i, 0
		for ti < len(sig) && k < len(h.kind) {
			t := sig[ti]
			if t.Kind == KindSeparator {
				m = append(m, t)
				ti++
				continue
			}
			if t.Kind != h.kind[k] {
				break
			}
			m = append(m, t)
			ti, k = ti+1, k+1
		}
		if k == len(h.kind) && len(h.kind) > bestN {
			bestN = len(h.kind)
			best, bestToks, bestRaw, bestEnd = h.apply, m, h.raw, ti
		}
	}
	return best, bestRaw, bestToks, bestEnd
}

// loc returns the result timezone: Options.Location, else UTC.
func loc(opts *Options) *time.Location {
	if opts != nil && opts.Location != nil {
		return opts.Location
	}
	return time.UTC
}

// now returns the reference instant in the result timezone.
func now(opts *Options) time.Time {
	if opts != nil && !opts.Now.IsZero() {
		return opts.Now.In(loc(opts))
	}
	return time.Now().In(loc(opts))
}
