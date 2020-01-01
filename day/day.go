package day

import (
	"bytes"
	"text/template"
	"time"

	"github.com/pkg/errors"
)

type Parser struct {
	timeZone       *time.Location
	tmpl           tmpl
	eodHour        int
	skipWeekDays   []time.Weekday
	holidayChecker HolidayChecker
}

type HolidayChecker interface {
	IsHoliday(day time.Time) bool
}

type HolidayCheckerFunc func(day time.Time) bool

func (h HolidayCheckerFunc) IsHoliday(day time.Time) bool {
	return h.IsHoliday(day)
}

// tmpl used to translate templates with date info
type tmpl struct {
	YYYYMMDD    string
	YYYYMMDDEOD string
	YYYY        string
	YYYYMM      string
	YYMMDD      string
	ISODATE     string
	UNIX        int64
	UNIXMSEC    int64
	MM          string
	DD          string
	YY          string
}

// NewTemplate makes day parser for given date
func NewTemplate(ts time.Time, options ...Option) *Parser {

	res := &Parser{
		timeZone:       time.Local,
		eodHour:        17,
		holidayChecker: HolidayCheckerFunc(func(day time.Time) bool { return false }),
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
		UNIX:        ts.Unix(),
		UNIXMSEC:    ts.UnixNano() / 1000000,
	}

	return res
}

// Parse translate template to final string
func (p Parser) Parse(dayTemplate string) (string, error) {
	b1 := bytes.Buffer{}
	err := template.Must(template.New("ymd").Parse(dayTemplate)).Execute(&b1, p.tmpl)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse day from %s", dayTemplate)
	}
	return b1.String(), nil
}

// toMidnight get midnight time in local tz for given time
func (p Parser) toMidnight(tm time.Time) time.Time {
	yy, mm, dd := tm.In(time.Local).Date()
	return time.Date(yy, mm, dd, 0, 0, 0, 0, time.Local)
}

// WeekdayBackward if day is weekend, get prev business day
func (p Parser) weekdayBackward(day time.Time) time.Time {

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
type Option func(l *Parser)

// TimeZone sets timezone used for all time parsings
func TimeZone(tz *time.Location) Option {
	return func(l *Parser) {
		l.timeZone = tz
	}
}

// EndOfDay sets threshold time defining end of the business day
func EndOfDay(hour int) Option {
	return func(l *Parser) {
		l.eodHour = hour
	}
}

// SkipWeekdays sets a list of wekedays to skip in detection of the current, next and prev. business days
func SkipWeekDays(days ...time.Weekday) Option {
	return func(l *Parser) {
		l.skipWeekDays = days
	}
}

// Holiday sets a checker detecting if a current day is holiday. Used for business days related logic.
func Holiday(checker HolidayChecker) Option {
	return func(l *Parser) {
		l.holidayChecker = checker
	}
}
