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

func TestDayParser_WeekdayEndOfDay(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tests := []struct {
		name    string
		day     time.Time
		options []Option
		want    string
	}{
		{
			name: "before threshold",
			day:  time.Date(2025, 1, 6, 16, 59, 59, 0, nytz),
			want: "20250103",
		},
		{
			name: "at threshold",
			day:  time.Date(2025, 1, 6, 17, 0, 0, 0, nytz),
			want: "20250106",
		},
		{
			name: "weekend",
			day:  time.Date(2025, 1, 4, 18, 0, 0, 0, nytz),
			want: "20250103",
		},
		{
			name:    "custom threshold",
			day:     time.Date(2025, 1, 7, 12, 0, 0, 0, nytz),
			options: []Option{EndOfDay(10)},
			want:    "20250107",
		},
		{
			name:    "custom weekend",
			day:     time.Date(2025, 1, 3, 18, 0, 0, 0, nytz),
			options: []Option{SkipWeekDays(time.Friday, time.Saturday)},
			want:    "20250102",
		},
		{
			name: "holiday before weekend",
			day:  time.Date(2025, 1, 6, 9, 0, 0, 0, nytz),
			options: []Option{Holiday(HolidayCheckerFunc(func(day time.Time) bool {
				return day.Day() == 3
			}))},
			want: "20250102",
		},
	}

	for _, tt := range tests {
		for _, alt := range []bool{false, true} {
			t.Run(tt.name+"/alt="+strconv.FormatBool(alt), func(t *testing.T) {
				options := append([]Option{TimeZone(nytz), AltTemplateFormat(alt)}, tt.options...)
				parser := NewDayTemplate(tt.day, options...)
				src := "report --date={{.WYYYYMMDDEOD}}"
				if alt {
					src = "report --date=[[.WYYYYMMDDEOD]]"
				}
				got, err := parser.Parse(src)
				require.NoError(t, err)
				assert.Equal(t, "report --date="+tt.want, got)
			})
		}
	}
}

func TestDayParser_ParseMalformed(t *testing.T) {
	nytz, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	tests := []struct {
		name        string
		template    string
		altTemplate bool
		wantErr     bool
	}{
		{
			name:        "malformed default template - unclosed",
			template:    "{{.YYYYMMDD",
			altTemplate: false,
			wantErr:     true,
		},
		{
			name:        "malformed alt template - unclosed",
			template:    "[[.YYYYMMDD",
			altTemplate: true,
			wantErr:     true,
		},
		{
			name:        "malformed default template - bad syntax",
			template:    "{{if}}",
			altTemplate: false,
			wantErr:     true,
		},
		{
			name:        "malformed alt template - bad syntax",
			template:    "[[if]]",
			altTemplate: true,
			wantErr:     true,
		},
		{
			name:        "valid template should not error",
			template:    "{{.YYYYMMDD}}",
			altTemplate: false,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDayTemplate(time.Date(2018, 1, 15, 14, 40, 0, 0, nytz),
				TimeZone(nytz), AltTemplateFormat(tt.altTemplate))
			_, err := d.Parse(tt.template)
			if tt.wantErr {
				require.Error(t, err, "Parse should return error for malformed template")
				assert.Contains(t, err.Error(), "failed to parse template")
			} else {
				assert.NoError(t, err)
			}
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
