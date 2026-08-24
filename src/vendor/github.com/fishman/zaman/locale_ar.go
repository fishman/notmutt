package zaman

import "time"

func init() {
	Register(ar())
}

// arabicLetterFold folds hamza-bearing alifs and script-convention variants.
// NFKC handles Presentation Forms; this map handles what it does not.
var arabicLetterFold = map[rune]rune{
	'أ': 'ا',
	'إ': 'ا',
	'آ': 'ا',
	'ٱ': 'ا',
	'ي': 'ى',
	'ة': 'ه',
}

func ar() *Locale {
	l := &Locale{
		name:          "ar",
		lex:           lexSpace,
		fold:          &fold{letters: arabicLetterFold},
		num:           Any{arabicIndicDigits, persianDigits, westernDigits},
		calendar:      Islamic{},
		weekStart:     time.Sunday,
		ambigRange:    6,
		defaultEndian: EndianDMY,
	}
	l.addWords(arWords())
	l.handlers = []handler{
		{kind: []Kind{KindClock, KindDayPortion}, apply: (*span).applyClock},
		{kind: []Kind{KindClock}, apply: (*span).applyClock},
		{kind: []Kind{KindHour, KindScalar, KindDayPortion}, apply: (*span).applyHourScalar},
		{kind: []Kind{KindHour, KindScalar}, apply: (*span).applyHourScalar},
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
		// noun-first adjective order: الأسبوع القادم
		{kind: []Kind{KindWeek, KindGrabber}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindMonthUnit, KindGrabber}, apply: (*span).applyGrabberUnit},
		{kind: []Kind{KindYear, KindGrabber}, apply: (*span).applyGrabberUnit},

		{kind: []Kind{KindPointer, KindWeek}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindMonthUnit}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindYear}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindDay}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindHour}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindMinute}, apply: (*span).applyPointerUnit},
		{kind: []Kind{KindPointer, KindSecond}, apply: (*span).applyPointerUnit},

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

func arWords() []word {
	ws := []word{
		{text: "ص", toks: []Token{{Kind: KindDayPortion, Value: 0}}},
		{text: "م", toks: []Token{{Kind: KindDayPortion, Value: 1}}},
		{text: "صباح", toks: []Token{{Kind: KindDayPortion, Value: 6}}},
		{text: "صباحا", toks: []Token{{Kind: KindDayPortion, Value: 6}}},
		{text: "مساء", toks: []Token{{Kind: KindDayPortion, Value: 17}}},
		{text: "ليل", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "الليل", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "الليلة", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "فجر", toks: []Token{{Kind: KindDayPortion, Value: 5}}},
		{text: "ظهر", toks: []Token{{Kind: KindClock, Value: 12 * 60}}},

		{text: "اليوم", toks: []Token{{Kind: KindRelative, Value: 0}}},
		{text: "غدا", toks: []Token{{Kind: KindRelative, Value: 1}}},
		{text: "أمس", toks: []Token{{Kind: KindRelative, Value: -1}}},
		{text: "البارحة", toks: []Token{{Kind: KindRelative, Value: -1}}},
		{text: "الآن", toks: []Token{{Kind: KindRelative, Value: 0}}},

		{text: "القادم", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "المقبل", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "الماضي", toks: []Token{{Kind: KindGrabber, Value: -1}}},
		{text: "السابق", toks: []Token{{Kind: KindGrabber, Value: -1}}},

		{text: "قبل", toks: []Token{{Kind: KindPointer, Value: -1}}},
		{text: "منذ", toks: []Token{{Kind: KindPointer, Value: -1}}},
		{text: "بعد", toks: []Token{{Kind: KindPointer, Value: 1}}},

		{text: "أسبوع", toks: []Token{{Kind: KindWeek}}},
		{text: "الأسبوع", toks: []Token{{Kind: KindWeek}}},
		{text: "شهر", toks: []Token{{Kind: KindMonthUnit}}},
		{text: "الشهر", toks: []Token{{Kind: KindMonthUnit}}},
		{text: "سنة", toks: []Token{{Kind: KindYear}}},
		{text: "السنة", toks: []Token{{Kind: KindYear}}},
		{text: "عام", toks: []Token{{Kind: KindYear}}},
		{text: "يوم", toks: []Token{{Kind: KindDay}}},
		{text: "ساعة", toks: []Token{{Kind: KindHour}}},
		{text: "الساعة", toks: []Token{{Kind: KindHour}}},
		{text: "دقيقة", toks: []Token{{Kind: KindMinute}}},
		{text: "الدقيقة", toks: []Token{{Kind: KindMinute}}},
		{text: "ثانية", toks: []Token{{Kind: KindSecond}}},
		{text: "الثانية", toks: []Token{{Kind: KindSecond}}},

		{text: "في", toks: []Token{{Kind: KindSeparator}}},

		// Compound month names resolve on their distinctive first word:
		// ربيع الأول, جمادى الأولى, ذو الحجة.
		{text: "محرم", toks: []Token{{Kind: KindMonth, Value: 1}}},
		{text: "صفر", toks: []Token{{Kind: KindMonth, Value: 2}}},
		{text: "ربيع", toks: []Token{{Kind: KindMonth, Value: 3}}},
		{text: "جمادى", toks: []Token{{Kind: KindMonth, Value: 5}}},
		{text: "رجب", toks: []Token{{Kind: KindMonth, Value: 7}}},
		{text: "شعبان", toks: []Token{{Kind: KindMonth, Value: 8}}},
		{text: "رمضان", toks: []Token{{Kind: KindMonth, Value: 9}}},
		{text: "شوال", toks: []Token{{Kind: KindMonth, Value: 10}}},
		{text: "ذو", toks: []Token{{Kind: KindMonth, Value: 12}}},
	}
	weekdays := [7]string{"الأحد", "الاثنين", "الثلاثاء", "الأربعاء", "الخميس", "الجمعة", "السبت"}
	for d := 0; d < 7; d++ {
		ws = append(ws, word{text: weekdays[d], toks: []Token{{Kind: KindWeekday, Value: d}}})
	}
	return ws
}
