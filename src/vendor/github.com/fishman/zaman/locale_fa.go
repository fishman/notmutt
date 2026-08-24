package zaman

import "time"

func init() {
	Register(fa())
}

// persianLetterFold maps Arabic yeh/keheh to the Persian forms so variants
// typed with the Arabic keyboard convention still match.
var persianLetterFold = map[rune]rune{
	'ي': 'ی',
	'ك': 'ک',
}

func fa() *Locale {
	l := &Locale{
		name:          "fa",
		lex:           lexSpace,
		fold:          &fold{letters: persianLetterFold},
		num:           Any{persianDigits, arabicIndicDigits, westernDigits},
		calendar:      SolarHijri{},
		keepZWNJ:      true, // یک‌شنبه, بعداز‌ظهر: ZWNJ is inside the word
		weekStart:     time.Saturday,
		ambigRange:    6,
		defaultEndian: EndianYMD,
	}
	l.addWords(faWords())
	l.handlers = []handler{
		{kind: []Kind{KindClock, KindDayPortion}, apply: (*span).applyClock},
		{kind: []Kind{KindClock}, apply: (*span).applyClock},
		{kind: []Kind{KindHour, KindScalar, KindDayPortion}, apply: (*span).applyHourScalar},
		{kind: []Kind{KindHour, KindScalar}, apply: (*span).applyHourScalar},
		{kind: []Kind{KindDayPortion}, apply: (*span).applyDayPortion},

		{kind: []Kind{KindMonth, KindScalar, KindScalar}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth, KindScalar}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth}, apply: (*span).applyMonthOnly},

		{kind: []Kind{KindScalar, KindScalar, KindScalar}, raw: true, apply: (*span).applyNumDate},
		{kind: []Kind{KindScalar, KindScalar}, raw: true, apply: (*span).applyNumDate2},

		{kind: []Kind{KindGrabber, KindWeekday}, apply: (*span).applyWeekday},
		{kind: []Kind{KindWeekday}, apply: (*span).applyWeekday},
		{kind: []Kind{KindGrabber, KindWeek}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindGrabber, KindMonthUnit}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindGrabber, KindYear}, apply: (*span).applyGrabberUnit},
		// noun-first adjective order: هفته آینده, ماه بعدی
		{kind: []Kind{KindWeek, KindGrabber}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindMonthUnit, KindGrabber}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindYear, KindGrabber}, apply: (*span).applyGrabberUnit},

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

func faWords() []word {
	ws := []word{
		{text: "صبح", toks: []Token{{Kind: KindDayPortion, Value: 6}}},
		{text: "عصر", toks: []Token{{Kind: KindDayPortion, Value: 13}}},
		{text: "شب", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "ق.ظ", toks: []Token{{Kind: KindDayPortion, Value: 0}}},
		{text: "ب.ظ", toks: []Token{{Kind: KindDayPortion, Value: 1}}},
		{text: "قبل‌ازظهر", toks: []Token{{Kind: KindDayPortion, Value: 0}}},
		{text: "بعداز‌ظهر", toks: []Token{{Kind: KindDayPortion, Value: 1}}},
		{text: "ظهر", toks: []Token{{Kind: KindClock, Value: 12 * 60}}},
		{text: "نیمه‌شب", toks: []Token{{Kind: KindClock, Value: 0}}},

		{text: "امروز", toks: []Token{{Kind: KindRelative, Value: 0}}},
		{text: "فردا", toks: []Token{{Kind: KindRelative, Value: 1}}},
		{text: "دیروز", toks: []Token{{Kind: KindRelative, Value: -1}}},
		{text: "الان", toks: []Token{{Kind: KindRelative, Value: 0}}},

		{text: "بعدی", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "آینده", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "قبلی", toks: []Token{{Kind: KindGrabber, Value: -1}}},

		{text: "پیش", toks: []Token{{Kind: KindPointer, Value: -1}}},
		{text: "بعد", toks: []Token{{Kind: KindPointer, Value: 1}}},

		{text: "هفته", toks: []Token{{Kind: KindWeek}}},
		{text: "ماه", toks: []Token{{Kind: KindMonthUnit}}},
		{text: "سال", toks: []Token{{Kind: KindYear}}},
		{text: "روز", toks: []Token{{Kind: KindDay}}},
		{text: "ساعت", toks: []Token{{Kind: KindHour}}},
		{text: "دقیقه", toks: []Token{{Kind: KindMinute}}},
		{text: "ثانیه", toks: []Token{{Kind: KindSecond}}},

		{text: "در", toks: []Token{{Kind: KindSeparator}}},
	}
	months := [13]string{"", "فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}
	for m := 1; m <= 12; m++ {
		ws = append(ws, word{text: months[m], toks: []Token{{Kind: KindMonth, Value: m}}})
	}
	weekdays := [7]string{"یک‌شنبه", "دوشنبه", "سه‌شنبه", "چهارشنبه", "پنجشنبه", "جمعه", "شنبه"}
	for d := 0; d < 7; d++ {
		ws = append(ws, word{text: weekdays[d], toks: []Token{{Kind: KindWeekday, Value: d}}})
	}
	return ws
}
