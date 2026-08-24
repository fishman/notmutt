package zaman

// Kind is a semantic token category. Handlers match on kinds, never on
// words, so one handler works across locales that emit the same kind.
type Kind uint8

const (
	KindInvalid    Kind = iota
	KindMonth           // month of year, Value 1..12
	KindWeekday         // day of week, Value time.Weekday (0=Sunday)
	KindDay             // day-of-month field, or "day" unit
	KindWeek            // "week" unit
	KindMonthUnit       // "month" unit
	KindYear            // year field, or "year" unit
	KindHour            // "hour" unit
	KindMinute          // "minute" unit
	KindSecond          // "second" unit
	KindClock           // time of day, Value = minutes since midnight
	KindDayPortion      // morning/noon/evening/night + AM/PM
	KindGrabber         // last/this/next, Value -1/0/+1
	KindPointer         // past/future, Value -1/+1
	KindScalar          // bare number
	KindOrdinal         // 1st, 2nd, third
	KindSeparator       // at, on, in, "/", "-", CJK unit suffix
	KindRelative        // today/yesterday/tomorrow, Value = day offset
	KindEra             // Japanese era index
)

// Token is one recognized unit of the input.
type Token struct {
	Kind  Kind
	Value int
	Pos   int    // byte offset in the original input
	Text  string // the input substring this token came from
}

func (k Kind) String() string {
	switch k {
	case KindMonth:
		return "month"
	case KindWeekday:
		return "weekday"
	case KindDay:
		return "day"
	case KindWeek:
		return "week"
	case KindMonthUnit:
		return "monthunit"
	case KindYear:
		return "year"
	case KindHour:
		return "hour"
	case KindMinute:
		return "minute"
	case KindSecond:
		return "second"
	case KindClock:
		return "clock"
	case KindDayPortion:
		return "dayportion"
	case KindGrabber:
		return "grabber"
	case KindPointer:
		return "pointer"
	case KindScalar:
		return "scalar"
	case KindOrdinal:
		return "ordinal"
	case KindSeparator:
		return "separator"
	case KindRelative:
		return "relative"
	case KindEra:
		return "era"
	}
	return "invalid"
}
