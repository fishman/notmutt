package zaman

import "time"

// Calendar resolves a civil date (year, month, day) to a time in loc.
type Calendar interface {
	ToTime(year, month, day int, loc *time.Location) time.Time
	DaysInMonth(year, month int) int
	FromTime(t time.Time) (year, month, day int)
}

// Gregorian is the proleptic Gregorian calendar.
type Gregorian struct{}

func (Gregorian) ToTime(y, m, d int, loc *time.Location) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, loc)
}

func (Gregorian) DaysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func (Gregorian) FromTime(t time.Time) (int, int, int) {
	y, m, d := t.Date()
	return y, int(m), d
}

// SolarHijri is the Persian (jalaali) calendar. Exact within jalaali years
// -61..3177 (Borkowski 1996, same as ICU's en-US-u-ca-persian through 2256).
type SolarHijri struct{}

func (SolarHijri) ToTime(y, m, d int, loc *time.Location) time.Time {
	gy, gm, gd := d2g(j2d(y, m, d))
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, loc)
}

func (SolarHijri) FromTime(t time.Time) (int, int, int) {
	gy, gm, gd := t.Date()
	return d2j(g2d(gy, int(gm), gd))
}

func (SolarHijri) DaysInMonth(y, m int) int {
	if m <= 6 {
		return 31
	}
	if m <= 11 {
		return 30
	}
	if isLeapJalaali(y) {
		return 30
	}
	return 29
}

// Islamic is the tabular (arithmetic) lunar Hijri calendar, the common-case
// approximation of Umm al-Qura: a fixed 30-year cycle with leap years at
// 2,5,7,10,13,16,18,21,24,26,29. Civil epoch: 1 Muharram 1 AH = 19 Jul 622
// CE (JDN 1948440).
type Islamic struct{}

const islamicEpoch = 1948440

// isIslamicLeap reports whether year has a 30-day Dhu al-Hijjah.
func isIslamicLeap(y int) bool { return (y*11+14)%30 < 11 }

func (Islamic) ToTime(y, m, d int, loc *time.Location) time.Time {
	gy, gm, gd := d2g(islamicToJdn(y, m, d))
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, loc)
}

func (Islamic) FromTime(t time.Time) (int, int, int) {
	gy, gm, gd := t.Date()
	return jdnToIslamic(g2d(gy, int(gm), gd))
}

func (Islamic) DaysInMonth(y, m int) int {
	if m%2 == 1 || (m == 12 && isIslamicLeap(y)) {
		return 30
	}
	return 29
}

func islamicToJdn(y, m, d int) int {
	days := 0
	for i := 1; i < m; i++ {
		if i%2 == 1 || (i == 12 && isIslamicLeap(y)) {
			days += 30
		} else {
			days += 29
		}
	}
	return islamicEpoch + (y-1)*354 + (3+11*y)/30 + days + d - 1
}

func jdnToIslamic(jdn int) (int, int, int) {
	y := (30*(jdn-islamicEpoch) + 10646) / 10631
	if start := islamicToJdn(y, 1, 1); jdn < start {
		y--
	}
	dayOfYear := jdn - islamicToJdn(y, 1, 1) + 1
	m, dim := 1, 29
	for {
		if m%2 == 1 || (m == 12 && isIslamicLeap(y)) {
			dim = 30
		} else {
			dim = 29
		}
		if dayOfYear <= dim {
			break
		}
		dayOfYear -= dim
		m++
	}
	return y, m, dayOfYear
}

// jalaali conversion, ported from jalaali-js (Borkowski). Integer division
// in Go truncates toward zero, matching the reference's Math.trunc semantics.

var jalaaliBreaks = [...]int{-61, 9, 38, 199, 426, 686, 756, 818, 1111, 1181, 1210, 1635, 2060, 2097, 2192, 2262, 2324, 2394, 2456, 3178}

// jalCalCore locates jy in the cycle table and returns Farvardin 1's
// Gregorian (gy, March day) plus the cycle jump and offset.
func jalCalCore(jy int) (gy, march, jump, n int) {
	gy = jy + 621
	leapJ := -14
	jp := jalaaliBreaks[0]
	for i := 1; i < len(jalaaliBreaks); i++ {
		jm := jalaaliBreaks[i]
		jump = jm - jp
		if jy < jm {
			break
		}
		leapJ += (jump/33)*8 + (jump%33)/4
		jp = jm
	}
	n = jy - jp
	leapJ += (n/33)*8 + (n%33+3)/4
	if jump%33 == 4 && jump-n == 4 {
		leapJ++
	}
	leapG := gy/4 - ((gy/100)+1)*3/4 - 150
	march = 20 + leapJ - leapG
	return gy, march, jump, n
}

// isLeapJalaali reports whether jy has 366 days (leap field 0).
func isLeapJalaali(jy int) bool { return leapOf(jy) == 0 }

func leapOf(jy int) int {
	jp := jalaaliBreaks[0]
	jump := 0
	for i := 1; i < len(jalaaliBreaks); i++ {
		jm := jalaaliBreaks[i]
		jump = jm - jp
		if jy < jm {
			break
		}
		jp = jm
	}
	return leapFromCycle(jump, jy-jp)
}

func leapFromCycle(jump, n int) int {
	if jump-n < 6 {
		n = n - jump + (jump+4)/33*33
	}
	x := (n+1)%33 - 1
	if x == -1 {
		return 4
	}
	return x % 4
}

// j2d converts a jalaali date to a Julian Day number.
func j2d(jy, jm, jd int) int {
	gy, march, _, _ := jalCalCore(jy)
	return g2d(gy, 3, march) + (jm-1)*31 - (jm/7)*(jm-7) + jd - 1
}

// d2j converts a Julian Day number to a jalaali date.
func d2j(jdn int) (int, int, int) {
	gy, _, _ := d2g(jdn)
	jy := gy - 621
	if jy > jalaaliBreaks[len(jalaaliBreaks)-1]-1 {
		jy = jalaaliBreaks[len(jalaaliBreaks)-1] - 1
	}
	leap := leapOf(jy) // leap field of the candidate year, read before the shift
	gy, march, _, _ := jalCalCore(jy)
	jdn1f := g2d(gy, 3, march)
	k := jdn - jdn1f
	if k >= 0 {
		if k <= 185 {
			return jy, 1 + k/31, k%31 + 1
		}
		k -= 186
	} else {
		jy--
		k += 179
		if leap == 1 {
			k++
		}
	}
	return jy, 7 + k/30, k%30 + 1
}

// g2d converts a proleptic Gregorian date to a Julian Day number.
func g2d(gy, gm, gd int) int {
	d := ((gy+(gm-8)/6+100100)*1461)/4 + (153*((gm+9)%12)+2)/5 + gd - 34840408
	d = d - ((gy+100100+(gm-8)/6)/100*3)/4 + 752
	return d
}

// d2g converts a Julian Day number to a proleptic Gregorian date.
func d2g(jdn int) (int, int, int) {
	j := 4*jdn + 139361631
	j = j + ((4*jdn+183187720)/146097*3)/4*4 - 3908
	i := (j%1461)/4*5 + 308
	gd := (i%153)/5 + 1
	gm := (i/153)%12 + 1
	gy := j/1461 - 100100 + (8-gm)/6
	return gy, gm, gd
}
