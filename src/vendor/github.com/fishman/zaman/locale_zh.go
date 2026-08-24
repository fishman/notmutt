package zaman

import "time"

func init() {
	Register(zh())
}

// zhComposedNumerals reads 一二三... with 十百千万億 unit words.
var zhComposedNumerals = ComposedNumerals{
	Digits: map[rune]int{
		'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
		'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	},
	Units: map[rune]int{'十': 10, '百': 100, '千': 1000, '万': 10000, '亿': 100000000, '億': 100000000},
}

func zh() *Locale {
	l := &Locale{
		name:          "zh",
		lex:           tokenizeCJK,
		cjk:           true,
		num:           Any{westernDigits, &zhComposedNumerals},
		calendar:      Gregorian{},
		weekStart:     time.Monday,
		ambigRange:    6,
		defaultEndian: EndianYMD,
	}
	l.addWords(zhWords())
	l.handlers = []handler{
		{kind: []Kind{KindDayPortion, KindClock}, apply: (*span).applyPortionClock},
		{kind: []Kind{KindClock, KindDayPortion}, apply: (*span).applyClock},
		{kind: []Kind{KindClock}, apply: (*span).applyClock},
		{kind: []Kind{KindDayPortion}, apply: (*span).applyDayPortion},

		{kind: []Kind{KindYear, KindMonth, KindDay}, apply: (*span).applyYMD},
		{kind: []Kind{KindYear, KindMonth, KindScalar}, apply: (*span).applyYMD},
		{kind: []Kind{KindMonth, KindDay}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth, KindScalar}, apply: (*span).applyAbsDate},
		{kind: []Kind{KindMonth}, apply: (*span).applyMonthOnly},

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

func zhWords() []word {
	ws := []word{
		{text: "上午", toks: []Token{{Kind: KindDayPortion, Value: 0}}},
		{text: "下午", toks: []Token{{Kind: KindDayPortion, Value: 1}}},
		{text: "早上", toks: []Token{{Kind: KindDayPortion, Value: 6}}},
		{text: "晚上", toks: []Token{{Kind: KindDayPortion, Value: 20}}},
		{text: "凌晨", toks: []Token{{Kind: KindDayPortion, Value: 4}}},
		{text: "中午", toks: []Token{{Kind: KindClock, Value: 12 * 60}}},

		{text: "今天", toks: []Token{{Kind: KindRelative, Value: 0}}},
		{text: "明天", toks: []Token{{Kind: KindRelative, Value: 1}}},
		{text: "昨天", toks: []Token{{Kind: KindRelative, Value: -1}}},
		{text: "前天", toks: []Token{{Kind: KindRelative, Value: -2}}},
		{text: "大前天", toks: []Token{{Kind: KindRelative, Value: -3}}},
		{text: "后天", toks: []Token{{Kind: KindRelative, Value: 2}}},
		{text: "大后天", toks: []Token{{Kind: KindRelative, Value: 3}}},
		{text: "现在", toks: []Token{{Kind: KindRelative, Value: 0}}},

		{text: "今年", toks: []Token{{Kind: KindGrabber, Value: 0}, {Kind: KindYear}}},
		{text: "明年", toks: []Token{{Kind: KindGrabber, Value: 1}, {Kind: KindYear}}},
		{text: "去年", toks: []Token{{Kind: KindGrabber, Value: -1}, {Kind: KindYear}}},

		{text: "半小时", toks: []Token{{Kind: KindScalar, Value: 30}, {Kind: KindMinute}}},
		{text: "半个小时", toks: []Token{{Kind: KindScalar, Value: 30}, {Kind: KindMinute}}},
		{text: "一刻", toks: []Token{{Kind: KindScalar, Value: 15}, {Kind: KindMinute}}},
		{text: "一刻钟", toks: []Token{{Kind: KindScalar, Value: 15}, {Kind: KindMinute}}},
		{text: "小时", toks: []Token{{Kind: KindHour}}},

		{text: "上", toks: []Token{{Kind: KindGrabber, Value: -1}}},
		{text: "下", toks: []Token{{Kind: KindGrabber, Value: 1}}},
		{text: "这", toks: []Token{{Kind: KindGrabber, Value: 0}}},
		{text: "本", toks: []Token{{Kind: KindGrabber, Value: 0}}},

		{text: "后", toks: []Token{{Kind: KindPointer, Value: 1}}},
		{text: "前", toks: []Token{{Kind: KindPointer, Value: -1}}},

		{text: "周", toks: []Token{{Kind: KindWeek}}},
		{text: "星期", toks: []Token{{Kind: KindWeek}}},
		{text: "个月", toks: []Token{{Kind: KindMonthUnit}}},
	}
	days := map[int][]string{1: {"周一", "星期一"}, 2: {"周二", "星期二"}, 3: {"周三", "星期三"}, 4: {"周四", "星期四"}, 5: {"周五", "星期五"}, 6: {"周六", "星期六"}, 0: {"周日", "星期日", "周天", "星期天"}}
	for d, names := range days {
		for _, n := range names {
			ws = append(ws, word{text: n, toks: []Token{{Kind: KindWeekday, Value: d}}})
		}
	}
	return ws
}
