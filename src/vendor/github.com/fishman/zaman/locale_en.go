package zaman

import "time"

func init() {
	Register(en())
}

func en() *Locale {
	l := &Locale{
		name:          "en",
		lex:           lexSpace,
		fold:          &fold{lower: true},
		num:           westernDigits,
		calendar:      Gregorian{},
		weekStart:     time.Sunday,
		ambigRange:    6,
		defaultEndian: EndianMDY,
	}
	l.addWords(enWords())
	l.handlers = []handler{
		{kind: []Kind{KindClock, KindDayPortion}, apply: (*span).applyClock},
		{kind: []Kind{KindClock}, apply: (*span).applyClock},
		{kind: []Kind{KindDayPortion}, apply: (*span).applyDayPortion},

		{kind: []Kind{KindMonth, KindScalar, KindScalar}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth, KindScalar}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth}, apply: (*span).applyMonthOnly},
		{kind: []Kind{KindScalar, KindMonth, KindScalar}, apply: (*span).applyAbsDayMonthYear},
		{kind: []Kind{KindScalar, KindMonth}, apply: (*span).applyAbsDayMonth},

		{kind: []Kind{KindScalar, KindScalar, KindScalar}, raw: true, apply: (*span).applyNumDate},
		{kind: []Kind{KindScalar, KindScalar}, raw: true, apply: (*span).applyNumDate2},

		{kind: []Kind{KindGrabber, KindWeekday}, apply: (*span).applyWeekday},
		{kind: []Kind{KindWeekday}, apply: (*span).applyWeekday},
		{kind: []Kind{KindGrabber, KindWeek}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindGrabber, KindMonthUnit}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindGrabber, KindYear}, apply: (*span).applyGrabberUnit},

		{kind: []Kind{KindScalar, KindWeek, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindWeek}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindMonthUnit, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindMonthUnit}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindYear, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindYear}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindDay, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindDay}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindHour, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindHour}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindMinute, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindMinute}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindSecond, KindPointer}, apply: (*span).applyScalarUnit},
		{kind: []Kind{KindScalar, KindSecond}, apply: (*span).applyScalarUnit},

		{kind: []Kind{KindRelative}, apply: (*span).applyRelDay},
	}
	return l
}

func enWords() []word {
	ws := []word{
		{text: "morning", toks: []Token{{Kind: KindDayPortion, Value: 6}}},
		{text: "afternoon", toks: []Token{{Kind: KindDayPortion, Value: 13}}},
		{text: "evening", toks: []Token{{Kind: KindDayPortion, Value: 17}}},
		{text: "night", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "am", toks: []Token{{Kind: KindDayPortion, Value: 0}}},
		{text: "pm", toks: []Token{{Kind: KindDayPortion, Value: 1}}},

		{text: "today", toks: []Token{{Kind: KindRelative, Value: 0}}},
		{text: "tomorrow", toks: []Token{{Kind: KindRelative, Value: 1}}},
		{text: "yesterday", toks: []Token{{Kind: KindRelative, Value: -1}}},
		{text: "now", toks: []Token{{Kind: KindRelative, Value: 0}}},

		{text: "next", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "last", toks: []Token{{Kind: KindGrabber, Value: -1}}},
		{text: "this", toks: []Token{{Kind: KindGrabber, Value: 0}}},

		{text: "ago", toks: []Token{{Kind: KindPointer, Value: -1}}},
		{text: "from", toks: []Token{{Kind: KindPointer, Value: 1}}},

		{text: "noon", toks: []Token{{Kind: KindClock, Value: 12 * 60}}},
		{text: "midnight", toks: []Token{{Kind: KindClock, Value: 0}}},

		{text: "week", toks: []Token{{Kind: KindWeek}}},
		{text: "weeks", toks: []Token{{Kind: KindWeek}}},
		{text: "month", toks: []Token{{Kind: KindMonthUnit}}},
		{text: "months", toks: []Token{{Kind: KindMonthUnit}}},
		{text: "year", toks: []Token{{Kind: KindYear}}},
		{text: "years", toks: []Token{{Kind: KindYear}}},
		{text: "day", toks: []Token{{Kind: KindDay}}},
		{text: "days", toks: []Token{{Kind: KindDay}}},
		{text: "hour", toks: []Token{{Kind: KindHour}}},
		{text: "hours", toks: []Token{{Kind: KindHour}}},
		{text: "minute", toks: []Token{{Kind: KindMinute}}},
		{text: "minutes", toks: []Token{{Kind: KindMinute}}},
		{text: "min", toks: []Token{{Kind: KindMinute}}},
		{text: "mins", toks: []Token{{Kind: KindMinute}}},
		{text: "second", toks: []Token{{Kind: KindSecond}}},
		{text: "seconds", toks: []Token{{Kind: KindSecond}}},

		{text: "at", toks: []Token{{Kind: KindSeparator}}},
		{text: "on", toks: []Token{{Kind: KindSeparator}}},
		{text: "in", toks: []Token{{Kind: KindSeparator}}},
		{text: "by", toks: []Token{{Kind: KindSeparator}}},
	}
	months := [13]string{"", "january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
	for m := 1; m <= 12; m++ {
		ws = append(ws, word{text: months[m], toks: []Token{{Kind: KindMonth, Value: m}}})
	}
	monthShort := [13]string{"", "jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	for m := 1; m <= 12; m++ {
		ws = append(ws, word{text: monthShort[m], toks: []Token{{Kind: KindMonth, Value: m}}})
	}
	ws = append(ws, word{text: "sept", toks: []Token{{Kind: KindMonth, Value: 9}}})
	weekdays := [7]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	for d := 0; d < 7; d++ {
		ws = append(ws, word{text: weekdays[d], toks: []Token{{Kind: KindWeekday, Value: d}}})
	}
	weekdayShort := [10]string{"sun", "mon", "tue", "tues", "wed", "thu", "thur", "thurs", "fri", "sat"}
	for _, s := range weekdayShort {
		d := -1
		for i, w := range weekdays {
			if len(s) >= 3 && s[:3] == w[:3] {
				d = i
			}
		}
		ws = append(ws, word{text: s, toks: []Token{{Kind: KindWeekday, Value: d}}})
	}
	return ws
}
