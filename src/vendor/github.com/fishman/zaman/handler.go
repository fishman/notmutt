package zaman

import "time"

// span is the mutable parse state. Apply funcs mutate it; result() turns it
// into a Result.
type span struct {
	now           time.Time
	cal           Calendar
	loc           *time.Location
	weekStart     time.Weekday
	ambigRange    int
	defaultEndian Endian

	begin, end   time.Time
	hasDate      bool
	hasTime      bool
	explicitYear bool
	rolloverYear bool
	textStart    int
	textEnd      int
}

func (s *span) startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.loc)
}

func (s *span) baseDay() time.Time {
	if s.hasDate {
		return s.startOfDay(s.begin)
	}
	return s.startOfDay(s.now)
}

func (s *span) setDate(y, mo, d int) {
	t := s.cal.ToTime(y, mo, d, s.loc)
	s.begin, s.end = t, t
	s.hasDate = true
}

// nowYear is the current year in the locale's calendar (jalali for fa).
func (s *span) nowYear() int {
	y, _, _ := s.cal.FromTime(s.now)
	return y
}

// nextYear shifts t one year forward in the locale's calendar.
func (s *span) nextYear(t time.Time) time.Time {
	y, mo, d := s.cal.FromTime(t)
	return s.cal.ToTime(y+1, mo, d, s.loc)
}

// currentYear is the year a bare month/day attributes to: the year already
// resolved on the span when one exists (明年三月 -> 2025), else the locale's
// current year.
func (s *span) currentYear() int {
	if s.hasDate {
		y, _, _ := s.cal.FromTime(s.begin)
		return y
	}
	return s.nowYear()
}

// applyAbsDate: [Month, Day] or [Month, Day, Year].
func (s *span) applyAbsDate(toks []Token) {
	mo, d := toks[0].Value, toks[1].Value
	y := s.currentYear()
	if len(toks) == 3 {
		y = toks[2].Value
		s.explicitYear = true
	}
	s.setDate(y, mo, d)
	s.rolloverYear = !s.explicitYear
}

// applyAbsDayMonth: [Day, Month].
func (s *span) applyAbsDayMonth(toks []Token) {
	d, mo := toks[0].Value, toks[1].Value
	s.setDate(s.nowYear(), mo, d)
	s.rolloverYear = true
}

// applyAbsDayMonthYear: [Day, Month, Year].
func (s *span) applyAbsDayMonthYear(toks []Token) {
	s.setDate(toks[2].Value, toks[1].Value, toks[0].Value)
	s.explicitYear = true
}

// applyMonthOnly: [Month] resolves to the 1st of that month.
func (s *span) applyMonthOnly(toks []Token) {
	s.setDate(s.currentYear(), toks[0].Value, 1)
	s.rolloverYear = true
}

// applyNumDate: [A, B, C]. Dash-separated reads as ISO YMD; otherwise the
// locale endian applies.
func (s *span) applyNumDate(toks []Token) {
	dash := false
	for _, t := range toks {
		if t.Kind == KindSeparator && t.Text == "-" {
			dash = true
		}
	}
	toks = strip(toks)
	a, b, c := toks[0].Value, toks[1].Value, toks[2].Value
	var y, mo, d int
	switch {
	case dash:
		y, mo, d = a, b, c
	case s.defaultEndian == EndianDMY:
		d, mo, y = a, b, c
	case s.defaultEndian == EndianYMD:
		y, mo, d = a, b, c
	default:
		mo, d, y = a, b, c
	}
	s.setDate(y, mo, d)
	s.explicitYear = true
}

// applyNumDate2: [A, B] month/day by endian, no year.
func (s *span) applyNumDate2(toks []Token) {
	toks = strip(toks)
	a, b := toks[0].Value, toks[1].Value
	mo, d := a, b
	if s.defaultEndian == EndianDMY {
		mo, d = b, a
	}
	s.setDate(s.nowYear(), mo, d)
	s.rolloverYear = true
}

// applyRelDay: [Relative]. No-ops once a date is resolved, so trailing
// "now" in "2 weeks from now" cannot clobber the computed date.
func (s *span) applyRelDay(toks []Token) {
	if s.hasDate {
		return
	}
	t := s.startOfDay(s.now).AddDate(0, 0, toks[0].Value)
	s.begin, s.end = t, t
	s.hasDate = true
}

func (s *span) nextWeekday(t time.Time, wd time.Weekday) time.Time {
	d := (int(wd) - int(t.Weekday()) + 7) % 7
	return t.AddDate(0, 0, d)
}

func (s *span) prevWeekday(t time.Time, wd time.Weekday) time.Time {
	d := (int(t.Weekday()) - int(wd) + 7) % 7
	if d == 0 {
		d = 7
	}
	return t.AddDate(0, 0, -d)
}

// applyWeekday: [Weekday] or [Grabber, Weekday]. Grabber -1 = most recent
// past, 0/none = upcoming (today inclusive), +1 = one week past upcoming.
func (s *span) applyWeekday(toks []Token) {
	grab, wd := 0, toks[len(toks)-1].Value
	if len(toks) == 2 {
		grab = toks[0].Value
	}
	today := s.startOfDay(s.now)
	var t time.Time
	switch {
	case grab == -1:
		t = s.prevWeekday(today.AddDate(0, 0, -1), time.Weekday(wd))
	case grab == +1:
		t = s.nextWeekday(today, time.Weekday(wd)).AddDate(0, 0, 7)
	default:
		t = s.nextWeekday(today, time.Weekday(wd))
	}
	s.begin, s.end = t, t
	s.hasDate = true
}

func (s *span) startOfWeek(t time.Time) time.Time {
	t = s.startOfDay(t)
	d := (int(t.Weekday()) - int(s.weekStart) + 7) % 7
	return t.AddDate(0, 0, -d)
}

// applyGrabberUnit: [Grabber, Unit] or [Unit, Grabber] (noun-first
// adjective order, Persian) -> "next week", "last month", "this year".
// Ranges over the whole period.
func (s *span) applyGrabberUnit(toks []Token) {
	grab, unit := 0, Kind(0)
	for _, t := range toks {
		switch t.Kind {
		case KindGrabber:
			grab = t.Value
		case KindWeek, KindMonthUnit, KindYear:
			unit = t.Kind
		}
	}
	switch unit {
	case KindWeek:
		t := s.startOfWeek(s.now).AddDate(0, 0, 7*grab)
		s.begin, s.end = t, t.AddDate(0, 0, 7)
	case KindMonthUnit:
		t := s.startOfMonth(s.now)
		t = s.addMonths(t, grab)
		s.begin, s.end = t, s.addMonths(t, 1)
	case KindYear:
		y, _, _ := s.cal.FromTime(s.now)
		t := s.cal.ToTime(y+grab, 1, 1, s.loc)
		s.begin, s.end = t, s.cal.ToTime(y+grab+1, 1, 1, s.loc)
		if grab != 0 {
			s.explicitYear = true
		}
	}
	s.hasDate = true
}

// startOfMonth returns the first day of t's month in the locale calendar.
func (s *span) startOfMonth(t time.Time) time.Time {
	y, mo, _ := s.cal.FromTime(t)
	return s.cal.ToTime(y, mo, 1, s.loc)
}

// addMonths shifts t n months in the locale calendar, clamping the day.
func (s *span) addMonths(t time.Time, n int) time.Time {
	y, mo, d := s.cal.FromTime(t)
	total := y*12 + (mo - 1) + n
	y, mo = total/12, total%12+1
	if mo < 1 {
		y--
		mo += 12
	}
	if dim := s.cal.DaysInMonth(y, mo); d > dim {
		d = dim
	}
	return s.cal.ToTime(y, mo, d, s.loc)
}

// applyScalarUnit: [N, unit] or [N, unit, Pointer]. "in 2 weeks" and
// "2 weeks from now" are future by default; "2 weeks ago" is past.
func (s *span) applyScalarUnit(toks []Token) {
	n, unit := toks[0].Value, toks[1].Kind
	dir := 1
	if len(toks) == 3 {
		dir = toks[2].Value
	}
	base := s.begin
	if !s.hasDate {
		base = s.now
	}
	t := s.advance(base, unit, n, dir)
	s.begin, s.end = t, t
	s.hasDate = true
	s.hasTime = true
}

func (s *span) advance(base time.Time, unit Kind, n, dir int) time.Time {
	switch unit {
	case KindSecond:
		return base.Add(time.Duration(n*dir) * time.Second)
	case KindMinute:
		return base.Add(time.Duration(n*dir) * time.Minute)
	case KindHour:
		return base.Add(time.Duration(n*dir) * time.Hour)
	case KindDay:
		return base.AddDate(0, 0, n*dir)
	case KindWeek:
		return base.AddDate(0, 0, 7*n*dir)
	case KindMonthUnit:
		return s.addMonths(base, n*dir)
	case KindYear:
		y, mo, d := s.cal.FromTime(base)
		return s.cal.ToTime(y+n*dir, mo, d, s.loc)
	}
	return base
}

// applyClock: [Clock] or [Clock, DayPortion]. Explicit am/pm adjusts the
// hour; a bare clock with a date set stays AM; a standalone bare clock
// resolves to the next occurrence (AM, plus PM when h >= ambigRange).
func (s *span) applyClock(toks []Token) {
	h, m := toks[0].Value/60, toks[0].Value%60
	if len(toks) == 2 && toks[1].Value <= 1 {
		if toks[1].Value == 1 && h < 12 {
			h += 12
		}
		if toks[1].Value == 0 && h == 12 {
			h = 0
		}
	} else if len(toks) != 2 && !s.hasDate {
		// morning/afternoon/evening/night (portion > 1) falls through: the
		// explicit clock wins over the portion's default time.
		s.setNextClock(h, m)
		return
	}
	b := s.baseDay()
	s.begin = time.Date(b.Year(), b.Month(), b.Day(), h, m, 0, 0, s.loc)
	s.end = s.begin
	s.hasTime = true
	s.hasDate = true
}

// setNextClock resolves a bare clock to the next occurrence, chronic-style:
// today-AM, today-PM (when ambiguous by ambigRange), tomorrow-AM.
func (s *span) setNextClock(h, m int) {
	today := s.startOfDay(s.now)
	cands := []time.Time{today.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)}
	if h < 12 && s.ambigRange > 0 && h >= s.ambigRange {
		cands = append(cands, today.Add((time.Duration(h)+12)*time.Hour+time.Duration(m)*time.Minute))
	}
	cands = append(cands, today.AddDate(0, 0, 1).Add(time.Duration(h)*time.Hour+time.Duration(m)*time.Minute))
	for _, c := range cands {
		if !c.Before(s.now) {
			s.begin, s.end = c, c
			break
		}
	}
	if s.begin.IsZero() {
		s.begin, s.end = cands[len(cands)-1], cands[len(cands)-1]
	}
	s.hasTime = true
	s.hasDate = true
}

// applyPointerUnit: [Pointer, Unit] -> "بعد أسبوع" is one unit in the
// pointer's direction (Arabic puts قبل/بعد before the unit).
func (s *span) applyPointerUnit(toks []Token) {
	s.applyScalarUnit([]Token{{Kind: KindScalar, Value: 1}, toks[1], toks[0]})
}

// applyPortionClock: [DayPortion, Clock] Chinese word order (下午三点).
func (s *span) applyPortionClock(toks []Token) {
	s.applyClock([]Token{toks[1], toks[0]})
}

// applyYMD: [Year, Month, Day|Scalar] absolute YMD date.
func (s *span) applyYMD(toks []Token) {
	s.setDate(toks[0].Value, toks[1].Value, toks[2].Value)
	s.explicitYear = true
}

// applyHourScalar: [Hour, Scalar(, DayPortion)] -> "ساعت ۲" is 2 o'clock.
// The hour word turns a following scalar into a clock (Persian word order).
func (s *span) applyHourScalar(toks []Token) {
	h := toks[1].Value
	if h > 23 {
		return
	}
	clk := []Token{{Kind: KindClock, Value: h * 60}}
	if len(toks) == 3 {
		clk = append(clk, toks[2])
	}
	s.applyClock(clk)
}

// applyDayPortion: [DayPortion] alone. Value > 1 is a default time of day
// (morning 6, afternoon 13, evening 17, night 20); am/pm markers do nothing.
func (s *span) applyDayPortion(toks []Token) {
	h := toks[0].Value
	if h <= 1 {
		return
	}
	b := s.baseDay()
	s.begin = time.Date(b.Year(), b.Month(), b.Day(), h, 0, 0, 0, s.loc)
	s.end = s.begin
	s.hasTime = true
	s.hasDate = true
}

// result finalizes the span into a Result.
func (s *span) result(input string, l *Locale, guess GuessMode) *Result {
	if s.rolloverYear && !s.explicitYear && s.begin.Before(s.now) {
		s.begin = s.nextYear(s.begin)
		s.end = s.nextYear(s.end)
	}
	if s.hasDate && !s.hasTime && s.begin.Equal(s.end) {
		s.end = s.begin.AddDate(0, 0, 1)
	}
	r := &Result{
		Span:   Span{Begin: s.begin, End: s.end},
		Text:   input[s.textStart:s.textEnd],
		Start:  s.textStart,
		End:    s.textEnd,
		Locale: l.name,
	}
	if guess != GuessFull {
		r.Time = r.Span.Guess(guess)
	}
	return r
}
