package service

import (
	"bytes"
	"fmt"
	"text/template"
	"time"
)

// DayParser for command line containing template elements from tmpl, like {{.YYYYMMDD}}
// replaces all occurrences of such templates. Handles business day detection for templates
// including "EOD" (i.e. YYYYMMDDEOD).
type DayParser struct {
	timeZone       *time.Location
	tmpl           tmpl
	eodHour        int
	skipWeekDays   []time.Weekday
	holidayChecker HolidayChecker
	altTemplate    bool
}

// HolidayChecker is a single-method interface returning status of the day
type HolidayChecker interface {
	IsHoliday(day time.Time) bool
}

// HolidayCheckerFunc is an adapter to allow the use of ordinary functions as HolidayChecker.
type HolidayCheckerFunc func(day time.Time) bool

// IsHoliday checks if the day is a holiday
func (h HolidayCheckerFunc) IsHoliday(day time.Time) bool {
	return h(day)
}

// tmpl used to translate templates with date info
type tmpl struct {
	YYYYMMDD    string
	YYYYMMDDEOD string
	YYYY        string
	YYYYMM      string
	YYMMDD      string
	ISODATE     string
	MM          string
	DD          string
	YY          string

	WYYYYMMDD    string
	WYYYYMMDDEOD string
	WYYYY        string
	WYYYYMM      string
	WYYMMDD      string
	WISODATE     string
	WMM          string
	WDD          string
	WYY          string

	UNIX     int64
	UNIXMSEC int64
}

// NewDayTemplate makes day parser for given date
func NewDayTemplate(ts time.Time, options ...Option) *DayParser {

	res := &DayParser{
		timeZone:       time.Local,
		eodHour:        17,
		skipWeekDays:   []time.Weekday{time.Saturday, time.Sunday},
		holidayChecker: HolidayCheckerFunc(func(time.Time) bool { return false }), // inactive by default
		altTemplate:    false,
	}

	for _, opt := range options {
		opt(res)
	}

	var eodDay time.Time
	if ts.Hour() < res.eodHour { // if time (hour) prior to EODHour use prev business day
		eodDay = res.weekdayBackward(ts.AddDate(0, 0, -1))
	} else {
		eodDay = res.weekdayBackward(ts)
	}

	tsMidnight := res.toMidnight(ts)
	tswMidnight := res.weekdayBackward(res.toMidnight(ts))

	res.tmpl = tmpl{
		YYYYMMDD:    tsMidnight.Format("20060102"),
		YYYYMMDDEOD: eodDay.Format("20060102"),
		YYYY:        tsMidnight.Format("2006"),
		YYYYMM:      tsMidnight.Format("200601"),
		YYMMDD:      tsMidnight.Format("060102"),
		ISODATE:     tsMidnight.Format("2006-01-02T00:00:00.000Z"),
		YY:          tsMidnight.Format("06"),
		MM:          tsMidnight.Format("01"),
		DD:          tsMidnight.Format("02"),

		WYYYYMMDD: tswMidnight.Format("20060102"),
		WYYYY:     tswMidnight.Format("2006"),
		WYYYYMM:   tswMidnight.Format("200601"),
		WYYMMDD:   tswMidnight.Format("060102"),
		WISODATE:  tswMidnight.Format("2006-01-02T00:00:00.000Z"),
		WYY:       tswMidnight.Format("06"),
		WMM:       tswMidnight.Format("01"),
		WDD:       tswMidnight.Format("02"),

		UNIX:     ts.Unix(),
		UNIXMSEC: ts.UnixNano() / 1000000,
	}

	return res
}

// Parse translate template to final string
func (p DayParser) Parse(dayTemplate string) (string, error) {
	b1 := bytes.Buffer{}
	tmpl := template.New("ymd")
	if p.altTemplate {
		tmpl = tmpl.Delims("[[", "]]")
	}
	err := template.Must(tmpl.Parse(dayTemplate)).Execute(&b1, p.tmpl)
	if err != nil {
		return "", fmt.Errorf("failed to parse day from %s: %w", dayTemplate, err)
	}
	return b1.String(), nil
}

// toMidnight get midnight time in local tz for given time
func (p DayParser) toMidnight(tm time.Time) time.Time {
	yy, mm, dd := tm.In(p.timeZone).Date()
	return time.Date(yy, mm, dd, 0, 0, 0, 0, p.timeZone)
}

// WeekdayBackward if day is weekend, get prev business day
func (p DayParser) weekdayBackward(day time.Time) time.Time {

	isBusinessDay := func(day time.Time) bool {
		if p.holidayChecker.IsHoliday(day) {
			return false
		}
		for _, wd := range p.skipWeekDays {
			if day.Weekday() == wd {
				return false
			}
		}
		return true
	}

	for d := day; ; d = d.AddDate(0, 0, -1) {
		if isBusinessDay(d) {
			return d
		}
	}
}

// Option func type
type Option func(l *DayParser)

// TimeZone sets timezone used for all time parsings
func TimeZone(tz *time.Location) Option {
	return func(l *DayParser) {
		l.timeZone = tz
	}
}

// EndOfDay sets threshold time defining end of the business day
func EndOfDay(hour int) Option {
	return func(l *DayParser) {
		l.eodHour = hour
	}
}

// SkipWeekDays sets a list of weekdays to skip in detection of the current, next and prev. business days
func SkipWeekDays(days ...time.Weekday) Option {
	return func(l *DayParser) {
		if days != nil {
			l.skipWeekDays = days
		}
	}
}

// Holiday sets a checker detecting if the current day is a holiday. Used for business days related logic.
func Holiday(checker HolidayChecker) Option {
	return func(l *DayParser) {
		l.holidayChecker = checker
	}
}

// AltTemplateFormat sets alternative template format with [[.YYYYMMDD]] instead of {{.YYYYMMDD}}
func AltTemplateFormat(enabled bool) Option {
	return func(l *DayParser) {
		l.altTemplate = enabled
	}
}
