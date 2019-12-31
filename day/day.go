package day

import (
	"bytes"
	"text/template"
	"time"

	"github.com/pkg/errors"
)

// DayTemplate used to translate templates with date info
type DayTemplate struct {
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
func NewTemplate(ts time.Time) *DayTemplate {

	tsMidnight := toMidnight(ts)

	return &DayTemplate{
		YYYYMMDD: tsMidnight.Format("20060102"),
		YYYY:     tsMidnight.Format("2006"),
		YYYYMM:   tsMidnight.Format("200601"),
		YYMMDD:   tsMidnight.Format("060102"),
		ISODATE:  tsMidnight.Format("2006-01-02T00:00:00.000Z"),
		YY:       tsMidnight.Format("06"),
		MM:       tsMidnight.Format("01"),
		DD:       tsMidnight.Format("02"),
		UNIX:     ts.Unix(),
		UNIXMSEC: ts.UnixNano() / 1000000,
	}
}

// Parse translate template to final string
func (d DayTemplate) Parse(dayTemplate string) (string, error) {
	b1 := bytes.Buffer{}
	err := template.Must(template.New("ymd").Parse(dayTemplate)).Execute(&b1, d)
	if err != nil {
		return "", errors.Wrapf(err, "failed to parse day from %s", dayTemplate)
	}
	return b1.String(), nil
}

// toMidnight get midnight time in local tz for given time
func toMidnight(tm time.Time) time.Time {
	yy, mm, dd := tm.In(time.Local).Date()
	return time.Date(yy, mm, dd, 0, 0, 0, 0, time.Local)
}
