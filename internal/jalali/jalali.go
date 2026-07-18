// Package jalali converts between the Jalali (Persian) calendar and Gregorian
// dates. The conversion is the vendored FarsiWeb algorithm (zero dependencies).
package jalali

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var gdim = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
var jdim = [12]int{31, 31, 31, 31, 31, 31, 30, 30, 30, 30, 30, 29}

// Date is a Jalali calendar date (year, month, day).
type Date struct{ Y, M, D int }

// String renders the date as zero-padded YYYY/MM/DD.
func (d Date) String() string { return fmt.Sprintf("%04d/%02d/%02d", d.Y, d.M, d.D) }

// Parse accepts "1405/3/21" or "1405-03-21".
func Parse(s string) (Date, error) {
	f := strings.FieldsFunc(strings.TrimSpace(s), func(r rune) bool {
		return r == '-' || r == '/'
	})
	if len(f) != 3 {
		return Date{}, fmt.Errorf("bad jalali date %q", s)
	}
	y, e1 := strconv.Atoi(f[0])
	m, e2 := strconv.Atoi(f[1])
	d, e3 := strconv.Atoi(f[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return Date{}, fmt.Errorf("bad jalali date %q", s)
	}
	return Date{y, m, d}, nil
}

// ToTime returns midnight UTC on the equivalent Gregorian day.
func (d Date) ToTime() time.Time {
	gy, gm, gd := j2g(d.Y, d.M, d.D)
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, time.UTC)
}

// FromTime returns the Jalali date for a Gregorian time.
func FromTime(t time.Time) Date {
	jy, jm, jd := g2j(t.Year(), int(t.Month()), t.Day())
	return Date{jy, jm, jd}
}

func j2g(jy, jm, jd int) (int, int, int) {
	jy2, jm2, jd2 := jy-979, jm-1, jd-1
	jDayNo := 365*jy2 + (jy2/33)*8 + (jy2%33+3)/4
	for i := 0; i < jm2; i++ {
		jDayNo += jdim[i]
	}
	jDayNo += jd2
	gDayNo := jDayNo + 79
	gy := 1600 + 400*(gDayNo/146097)
	gDayNo %= 146097
	leap := true
	if gDayNo >= 36525 {
		gDayNo--
		gy += 100 * (gDayNo / 36524)
		gDayNo %= 36524
		if gDayNo >= 365 {
			gDayNo++
		} else {
			leap = false
		}
	}
	gy += 4 * (gDayNo / 1461)
	gDayNo %= 1461
	if gDayNo >= 366 {
		leap = false
		gDayNo--
		gy += gDayNo / 365
		gDayNo %= 365
	}
	i := 0
	for ; i < 12; i++ {
		v := gdim[i]
		if i == 1 && leap {
			v = 29
		}
		if gDayNo < v {
			break
		}
		gDayNo -= v
	}
	return gy, i + 1, gDayNo + 1
}

func g2j(gy, gm, gd int) (int, int, int) {
	gy2, gm2, gd2 := gy-1600, gm-1, gd-1
	gDayNo := 365*gy2 + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400
	for i := 0; i < gm2; i++ {
		gDayNo += gdim[i]
	}
	if gm2 > 1 && ((gy%4 == 0 && gy%100 != 0) || gy%400 == 0) {
		gDayNo++
	}
	gDayNo += gd2
	jDayNo := gDayNo - 79
	jNp := jDayNo / 12053
	jDayNo %= 12053
	jy := 979 + 33*jNp + 4*(jDayNo/1461)
	jDayNo %= 1461
	if jDayNo >= 366 {
		jy += (jDayNo - 1) / 365
		jDayNo = (jDayNo - 1) % 365
	}
	i := 0
	for ; i < 11; i++ {
		if jDayNo < jdim[i] {
			break
		}
		jDayNo -= jdim[i]
	}
	return jy, i + 1, jDayNo + 1
}
