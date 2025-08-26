package service

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDayParser_Parse(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tbl := []struct {
		day time.Time
		src string
		res string
		err error
	}{
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx {{.YYYYMMDD}} blah {{.YYYYMMDDEOD}}", "xxx 20161101 blah 20161031", nil},
		{time.Date(2016, 11, 1, 17, 30, 0, 0, nytz), "xxx {{.YYYYMMDD}} blah {{.YYYYMMDDEOD}}", "xxx 20161101 blah 20161101", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx {{.YYYYMM}} blah", "xxx 201611 blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx {{.YYYY}} blah", "xxx 2016 blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx {{.ISODATE}} blah", "xxx 2016-11-01T00:00:00.000Z blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx blah", "xxx blah", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "xxx {{.MM}} blah {{.DD}}", "xxx 01 blah 15", nil},
		{time.Date(2018, 1, 15, 14, 40, 22, 123000000, nytz), "xxx {{.UNIX}} blah {{.UNIXMSEC}}", "xxx 1516045222 blah 1516045222123", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "{{.MMXX}}", "", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "zz {{.YYMMDD}}", "zz 180115", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "zz {{.YY}}", "zz 18", nil},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			d := NewDayTemplate(tt.day, TimeZone(nytz))
			res, err := d.Parse(tt.src)
			if tt.err != nil {
				require.EqualError(t, err, tt.err.Error())
				return
			}
			assert.Equal(t, tt.res, res)
		})
	}
}

func TestDayParser_ParseWithAltTemplate(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tbl := []struct {
		day time.Time
		src string
		res string
		err error
	}{
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx [[.YYYYMMDD]] blah [[.YYYYMMDDEOD]]", "xxx 20161101 blah 20161031", nil},
		{time.Date(2016, 11, 1, 17, 30, 0, 0, nytz), "xxx [[.YYYYMMDD]] blah [[.YYYYMMDDEOD]]", "xxx 20161101 blah 20161101", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx [[.YYYYMM]] blah", "xxx 201611 blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx [[.YYYY]] blah", "xxx 2016 blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx [[.ISODATE]] blah", "xxx 2016-11-01T00:00:00.000Z blah", nil},
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "xxx blah", "xxx blah", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "xxx [[.MM]] blah [[.DD]]", "xxx 01 blah 15", nil},
		{time.Date(2018, 1, 15, 14, 40, 22, 123000000, nytz), "xxx [[.UNIX]] blah [[.UNIXMSEC]]", "xxx 1516045222 blah 1516045222123", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "[[.MMXX]]", "", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "zz [[.YYMMDD]]", "zz 180115", nil},
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "zz [[.YY]]", "zz 18", nil},
		// test mixing curly braces with alt template - they should be literal
		{time.Date(2018, 1, 15, 14, 40, 0, 0, nytz), "cmd {{.YY}} [[.YYYYMMDD]]", "cmd {{.YY}} 20180115", nil},
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			d := NewDayTemplate(tt.day, TimeZone(nytz), AltTemplateFormat(true))
			res, err := d.Parse(tt.src)
			if tt.err != nil {
				require.EqualError(t, err, tt.err.Error())
				return
			}
			assert.Equal(t, tt.res, res)
		})
	}
}

func TestDayParser_weekdayBackward(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tbl := []struct {
		skip []time.Weekday
		src  time.Time
		res  time.Time
	}{
		{nil, time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), time.Date(2016, 11, 1, 0, 0, 0, 0, nytz)}, // weekday
		{nil, time.Date(2016, 11, 9, 0, 0, 0, 0, nytz), time.Date(2016, 11, 9, 0, 0, 0, 0, nytz)}, // weekday
		{nil, time.Date(2017, 4, 30, 0, 0, 0, 0, nytz), time.Date(2017, 4, 28, 0, 0, 0, 0, nytz)}, // sun
		{nil, time.Date(2017, 4, 29, 0, 0, 0, 0, nytz), time.Date(2017, 4, 28, 0, 0, 0, 0, nytz)}, // sat

		{[]time.Weekday{time.Saturday}, time.Date(2017, 4, 30, 0, 0, 0, 0, nytz), time.Date(2017, 4, 30, 0, 0, 0, 0, nytz)}, // sun
		{[]time.Weekday{time.Saturday}, time.Date(2017, 4, 29, 0, 0, 0, 0, nytz), time.Date(2017, 4, 28, 0, 0, 0, 0, nytz)}, // sat
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			d := NewDayTemplate(tt.src, TimeZone(nytz), SkipWeekDays(tt.skip...))
			assert.Equal(t, tt.res, d.weekdayBackward(tt.src))
		})
	}
}

func TestDayParser_weekend(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tbl := []struct {
		day time.Time
		src string
		res string
	}{
		{time.Date(2016, 11, 1, 0, 0, 0, 0, nytz), "{{.WYYYYMMDD}} {{.YYYYMMDD}}", "20161101 20161101"}, // weekday
		{time.Date(2017, 4, 30, 0, 0, 0, 0, nytz), "{{.WYYYYMMDD}} {{.YYYYMMDD}}", "20170428 20170430"}, // sun
		{time.Date(2017, 4, 29, 0, 0, 0, 0, nytz), "{{.WYYYYMMDD}} {{.YYYYMMDD}}", "20170428 20170429"}, // sat
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			d := NewDayTemplate(tt.day, TimeZone(nytz))
			res, err := d.Parse(tt.src)
			require.NoError(t, err)
			assert.Equal(t, tt.res, res)
		})
	}
}

func TestDayParser_eod(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tbl := []struct {
		eod int
		day time.Time
		src string
		res string
	}{
		{17, time.Date(2020, 7, 16, 16, 0, 0, 0, nytz), "{{.YYYYMMDD}} blah {{.YYYYMMDDEOD}}", "20200716 blah 20200715"}, // thu
		{17, time.Date(2020, 7, 16, 17, 0, 0, 0, nytz), "{{.YYYYMMDD}} blah {{.YYYYMMDDEOD}}", "20200716 blah 20200716"}, // thu
	}

	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			d := NewDayTemplate(tt.day, TimeZone(nytz), EndOfDay(tt.eod))
			res, err := d.Parse(tt.src)
			require.NoError(t, err)
			assert.Equal(t, tt.res, res)
		})
	}
}

func TestDayParser_holiday(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	d := NewDayTemplate(time.Date(2020, 7, 16, 18, 0, 0, 0, nytz), TimeZone(nytz),
		Holiday(HolidayCheckerFunc(func(day time.Time) bool { return false })))
	res, err := d.Parse("{{.YYYYMMDD}} blah {{.YYYYMMDDEOD}}")
	require.NoError(t, err)
	assert.Equal(t, "20200716 blah 20200716", res)

	d = NewDayTemplate(time.Date(2020, 7, 16, 18, 0, 0, 0, nytz), TimeZone(nytz), Holiday(HolidayCheckerFunc(func(day time.Time) bool {
		return day.After(time.Date(2020, 7, 10, 0, 0, 0, 0, nytz))
	})))
	res, err = d.Parse("{{.YYYYMMDD}} {{.WYYYYMMDD}} blah {{.YYYYMMDDEOD}}")
	require.NoError(t, err)
	assert.Equal(t, "20200716 20200710 blah 20200709", res)
}
