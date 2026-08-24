package zaman

import "unicode/utf8"

// NumberSystem parses a numeral starting at byte pos in s.
type NumberSystem interface {
	// Parse returns the value and the byte index just past the match.
	Parse(s string, pos int) (value, end int, ok bool)
}

// DigitSystem maps script digits to 0-9.
type DigitSystem struct {
	Digits map[rune]int
}

// NewDigitSystem builds a system from a string of digit runes in 0-9 order.
func NewDigitSystem(digits string) *DigitSystem {
	m := make(map[rune]int, len(digits))
	v := 0
	for _, r := range digits {
		m[r] = v
		v++
	}
	return &DigitSystem{Digits: m}
}

func (d *DigitSystem) Parse(s string, pos int) (int, int, bool) {
	v, i, any := 0, pos, false
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		dig, ok := d.Digits[r]
		if !ok {
			break
		}
		v, i, any = v*10+dig, i+sz, true
	}
	return v, i, any
}

// ComposedNumerals parses positional numeral words (Chinese, Japanese):
// digit words 一..九 with unit words 十/百/千 and big bases 万/億.
type ComposedNumerals struct {
	Digits map[rune]int
	Units  map[rune]int
}

func (c *ComposedNumerals) Parse(s string, pos int) (int, int, bool) {
	big, cur, i, any := 0, 0, pos, false
	for i < len(s) {
		r, sz := utf8.DecodeRuneInString(s[i:])
		if d, ok := c.Digits[r]; ok {
			cur, i, any = d, i+sz, true
			continue
		}
		if f, ok := c.Units[r]; ok {
			if cur == 0 {
				cur = 1
			}
			if f >= 10000 {
				big = (big + cur) * f
			} else {
				big += cur * f
			}
			cur, i, any = 0, i+sz, true
			continue
		}
		break
	}
	return big + cur, i, any
}

// Any tries each system in order and keeps the longest match. Used where a
// locale accepts several digit scripts (Persian + western, Arabic-Indic +
// western) or words plus digits.
type Any []NumberSystem

func (a Any) Parse(s string, pos int) (int, int, bool) {
	bestEnd, bestVal, found := pos, 0, false
	for _, sys := range a {
		if v, end, ok := sys.Parse(s, pos); ok && end > bestEnd {
			bestVal, bestEnd, found = v, end, true
		}
	}
	return bestVal, bestEnd, found
}

var (
	westernDigits     = NewDigitSystem("0123456789")
	arabicIndicDigits = NewDigitSystem("٠١٢٣٤٥٦٧٨٩")
	persianDigits     = NewDigitSystem("۰۱۲۳۴۵۶۷۸۹")
)
