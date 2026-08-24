package zaman

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	zwnj = '‌' // Persian: written inside compounds, a word boundary
	zwj  = '‍' // invisible in normal prose, skipped
)

// fold normalizes a lexeme for matching. NFKC folds Arabic Presentation
// Forms A/B (position-shaped codepoints from legacy encoders) and fullwidth
// forms to canonical letters; combining marks (tashkeel) and tatweel are
// dropped; per-locale letter maps handle script-convention variants
// (Arabic y/ك vs Persian ی/ک), which Unicode normalization does not.
type fold struct {
	lower   bool
	letters map[rune]rune
}

func (f *fold) apply(s string) string {
	if f == nil {
		return s
	}
	s = norm.NFKC.String(s)
	b := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.In(r, unicode.Mn) || r == tatweel {
			continue
		}
		if f.lower {
			r = unicode.ToLower(r)
		}
		if rr, ok := f.letters[r]; ok {
			r = rr
		}
		b = append(b, r)
	}
	return string(b)
}

const tatweel = 'ـ'

// lexSpace tokenizes a space-script (Latin, Arabic) string into tokens.
func lexSpace(s string, l *Locale) []Token {
	var toks []Token
	i := 0
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) || r == zwj || (r == zwnj && !l.keepZWNJ) {
			i += sz
			continue
		}
		if r == '/' || r == '-' || r == ',' {
			toks = append(toks, Token{Kind: KindSeparator, Pos: i, Text: string(r)})
			i += sz
			continue
		}
		j := i
		for j < len(s) {
			r2, s2 := utf8.DecodeRuneInString(s[j:])
			if unicode.IsSpace(r2) || r2 == zwj || (r2 == zwnj && !l.keepZWNJ) || r2 == '/' || r2 == '-' || r2 == ',' {
				break
			}
			j += s2
		}
		toks = append(toks, tagLexeme(s[i:j], i, l)...)
		i = j
	}
	return toks
}

func tagLexeme(lex string, pos int, l *Locale) []Token {
	if ws, ok := l.lexicon[l.fold.apply(lex)]; ok {
		return setPos(ws, pos, lex)
	}
	if h, m, portion, ok := parseClock(lex, l); ok {
		t := []Token{{Kind: KindClock, Value: h*60 + m, Pos: pos, Text: lex}}
		if portion != "" {
			t = append(t, Token{Kind: KindDayPortion, Value: portionValue(portion), Pos: pos, Text: lex})
		}
		return t
	}
	if n, ok := parseOrdinal(lex); ok {
		return []Token{{Kind: KindOrdinal, Value: n, Pos: pos, Text: lex}}
	}
	if v, end, ok := l.num.Parse(lex, 0); ok && end == len(lex) {
		return []Token{{Kind: KindScalar, Value: v, Pos: pos, Text: lex}}
	}
	return nil
}

func setPos(toks []Token, pos int, text string) []Token {
	out := make([]Token, len(toks))
	copy(out, toks)
	for i := range out {
		out[i].Pos = pos
		out[i].Text = text
	}
	return out
}

func parseClock(lex string, l *Locale) (h, m int, portion string, ok bool) {
	s := strings.ToLower(lex)
	base, portion := s, ""
	if strings.HasSuffix(s, "am") || strings.HasSuffix(s, "pm") {
		portion = s[len(s)-2:]
		base = s[:len(s)-2]
	}
	parts := strings.Split(base, ":")
	if len(parts) > 3 {
		return 0, 0, "", false
	}
	if len(parts) == 1 && (len(parts[0]) == 0 || !strings.Contains(base, ":")) {
		return 0, 0, "", false // bare digits are a scalar, not a clock
	}
	h, end, ok := l.num.Parse(parts[0], 0)
	if !ok || end != len(parts[0]) || h < 0 || h > 23 {
		return 0, 0, "", false
	}
	if len(parts) >= 2 {
		if utf8.RuneCountInString(parts[1]) > 2 {
			return 0, 0, "", false
		}
		m, end, ok = l.num.Parse(parts[1], 0)
		if !ok || end != len(parts[1]) || m > 59 {
			return 0, 0, "", false
		}
	}
	return h, m, portion, true
}

// portionValue maps AM/PM (and implied-PM portions) to the shift applied
// by applyClockParts: 0 keeps AM, 1 forces PM.
func portionValue(p string) int {
	if p == "pm" {
		return 1
	}
	return 0
}

func parseOrdinal(lex string) (int, bool) {
	s := strings.ToLower(lex)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	rest := s[i:]
	if rest != "st" && rest != "nd" && rest != "rd" && rest != "th" {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	if err != nil {
		return 0, false
	}
	return n, true
}

// tokenizeCJK tokenizes a Chinese/Japanese string: numerals with unit
// suffixes first (maximal munch), then lexicon trie words.
func tokenizeCJK(s string, l *Locale) []Token {
	var toks []Token
	i := 0
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		if unicode.IsSpace(r) || r == zwj {
			i += sz
			continue
		}
		if v, end, ok := l.num.Parse(s, i); ok && end > i {
			if n, toks2, ok2 := cjkSuffix(s, end, v, l); ok2 {
				toks = append(toks, setPos(toks2, i, s[i:n])...)
				i = n
				continue
			}
			toks = append(toks, Token{Kind: KindScalar, Value: v, Pos: i, Text: s[i:end]})
			i = end
			continue
		}
		if ws, end, ok := l.trie.walk(s, i); ok {
			toks = append(toks, setPos(ws, i, s[i:end])...)
			i = end
			continue
		}
		i += sz
	}
	return toks
}

// cjkSuffix consumes the unit suffix after a numeral. Duration suffixes emit
// [Scalar, Unit] so the scalar-unit handler resolves them; date suffixes emit
// a single token. 年 with a two-digit-plus value is a calendar year (2024年),
// a small value is a count (三年) - the ambiguity resolves on magnitude.
func cjkSuffix(s string, i, v int, l *Locale) (int, []Token, bool) {
	r, sz := utf8.DecodeRuneInString(s[i:])
	switch r {
	case '年':
		if v >= 100 {
			return i + sz, []Token{{Kind: KindYear, Value: v}}, true
		}
		return i + sz, []Token{{Kind: KindScalar, Value: v}, {Kind: KindYear}}, true
	case '月':
		return i + sz, []Token{{Kind: KindMonth, Value: v}}, true
	case '个', 'か', 'ヶ', 'カ':
		if r2, s2 := utf8.DecodeRuneInString(s[i+sz:]); r2 == '月' {
			return i + sz + s2, []Token{{Kind: KindScalar, Value: v}, {Kind: KindMonthUnit}}, true
		}
	case '日', '号':
		return i + sz, []Token{{Kind: KindDay, Value: v}}, true
	case '天':
		return i + sz, []Token{{Kind: KindScalar, Value: v}, {Kind: KindDay}}, true
	case '周', '週':
		return i + sz, []Token{{Kind: KindScalar, Value: v}, {Kind: KindWeek}}, true
	case '分':
		return i + sz, []Token{{Kind: KindScalar, Value: v}, {Kind: KindMinute}}, true
	case '秒':
		return i + sz, []Token{{Kind: KindScalar, Value: v}, {Kind: KindSecond}}, true
	case '小':
		if r2, s2 := utf8.DecodeRuneInString(s[i+sz:]); r2 == '时' {
			return i + sz + s2, []Token{{Kind: KindScalar, Value: v}, {Kind: KindHour}}, true
		}
	case '時':
		if r2, s2 := utf8.DecodeRuneInString(s[i+sz:]); r2 == '間' {
			return i + sz + s2, []Token{{Kind: KindScalar, Value: v}, {Kind: KindHour}}, true
		}
		return cjkClock(s, i, v, l)
	case '点', '點':
		return cjkClock(s, i, v, l)
	}
	return 0, nil, false
}

// cjkClock parses N点/N時 plus the minute: 半 (30), a minute word like 一刻
// (from the trie), or a bare numeral (三点二十 = 3:20).
func cjkClock(s string, i, v int, l *Locale) (int, []Token, bool) {
	_, sz := utf8.DecodeRuneInString(s[i:])
	j := i + sz
	m := 0
	if r2, s2 := utf8.DecodeRuneInString(s[j:]); r2 == '半' {
		m, j = 30, j+s2
	} else if toks2, end2, ok2 := l.trie.walk(s, j); ok2 {
		for _, t := range toks2 {
			if t.Kind == KindScalar {
				m = t.Value
			}
		}
		j = end2
	} else if v2, e2, ok2 := l.num.Parse(s, j); ok2 && e2 > j {
		m, j = v2, e2
	}
	return j, []Token{{Kind: KindClock, Value: v*60 + m}}, true
}
