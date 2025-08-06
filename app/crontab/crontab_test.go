package crontab

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	tbl := []struct {
		inp      string
		js       JobSpec
		wasError bool
	}{
		{"* * 1 2 3 ls -la blah", JobSpec{Spec: "* * 1 2 3", Command: "ls -la blah"}, false},
		{"*/5  * 1   2 3    ls -la  blah  ", JobSpec{Spec: "*/5 * 1 2 3", Command: "ls -la blah"}, false},
		{"* * 1 2 ", JobSpec{}, true},
		{"*", JobSpec{}, true},
		{"@reboot echo", JobSpec{Spec: "@reboot", Command: "echo"}, false},
		{"@midnight echo 123", JobSpec{Spec: "@midnight", Command: "echo 123"}, false},
		{"@every 2h30m echo 123", JobSpec{Spec: "@every 2h30m", Command: "echo 123"}, false},
		// inline comment should be stripped
		{"* * * * * echo foo # bar baz", JobSpec{Spec: "* * * * *", Command: "echo foo"}, false},
	}

	for _, tt := range tbl {
		r, err := Parse(tt.inp)
		if tt.wasError {
			assert.NotNil(t, err, tt.inp)
		} else {
			assert.Nil(t, err, tt.inp)
		}
		assert.Equal(t, tt.js, r, tt.inp)
	}
}

func TestParser_List(t *testing.T) {
	ctab := New("testfiles/crontab", time.Hour, nil)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)
	assert.Equal(t, []JobSpec{{Spec: "*/5 * * * *", Command: "ls -la ."}, {Spec: "*/2 1-18 * * *", Command: "export"},
		{Spec: "*/1 * * * *", Command: "something blah bad"}}, jobs)
	assert.Equal(t, "testfiles/crontab", ctab.String())

	ctab = New("testfiles/no-file", time.Hour, nil)
	_, err = ctab.List()
	assert.Error(t, err)
}

func TestParser_ListYAML(t *testing.T) {
	ctab := New("testfiles/crontab.yml", time.Hour, nil)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 4)
	assert.Equal(t, []JobSpec{
		{Spec: "*/5 * * * *", Command: "ls -la .", Name: "Directory listing"},
		{Spec: "*/2 1-18 * * *", Command: "export", Name: ""},
		{Spec: "@every 2h30m", Command: "echo test", Name: "Test echo job"},
		{Spec: "@midnight", Command: "backup /data", Name: "Nightly backup"},
	}, jobs)
	assert.Equal(t, "testfiles/crontab.yml", ctab.String())
	assert.True(t, ctab.isYAML)
}

func TestParser_ListYAMLInvalid(t *testing.T) {
	// test invalid YAML
	tmp, err := os.CreateTemp("", "crontab*.yml")
	require.NoError(t, err)
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	// write invalid YAML
	_, err = tmp.WriteString("not valid yaml: [\n")
	require.NoError(t, err)

	ctab := New(tmp.Name(), time.Hour, nil)
	_, err = ctab.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse YAML")
}

func TestParser_ListYAMLEmptyFields(t *testing.T) {
	// test YAML with empty fields
	tmp, err := os.CreateTemp("", "crontab*.yml")
	require.NoError(t, err)
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	// write YAML with neither spec nor sched
	_, err = tmp.WriteString(`jobs:
  - command: "test"`)
	require.NoError(t, err)

	ctab := New(tmp.Name(), time.Hour, nil)
	_, err = ctab.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "either 'spec' or 'sched' field is required")

	// write YAML with empty command
	_ = tmp.Truncate(0)
	_, _ = tmp.Seek(0, 0)
	_, err = tmp.WriteString(`jobs:
  - spec: "* * * * *"
    command: ""`)
	require.NoError(t, err)

	ctab = New(tmp.Name(), time.Hour, nil)
	_, err = ctab.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestParser_ListYAMLSched(t *testing.T) {
	ctab := New("testfiles/crontab-sched.yml", time.Hour, nil)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 4)

	// verify sched fields are converted to spec
	assert.Equal(t, "0 2 * * *", jobs[0].Spec)
	assert.Equal(t, "backup /data", jobs[0].Command)
	assert.Equal(t, "Nightly backup", jobs[0].Name)

	assert.Equal(t, "0,30 9-17 1-15 */2 1-5", jobs[1].Spec)
	assert.Equal(t, "sync files", jobs[1].Command)
	assert.Equal(t, "Business hours sync", jobs[1].Name)

	assert.Equal(t, "15 * * * *", jobs[2].Spec)
	assert.Equal(t, "check health", jobs[2].Command)
	assert.Equal(t, "Every hour at 15 minutes", jobs[2].Name)

	assert.Equal(t, "@midnight", jobs[3].Spec)
	assert.Equal(t, "cleanup temp", jobs[3].Command)
	assert.Equal(t, "Midnight cleanup", jobs[3].Name)
}

func TestParser_ListYAMLSchedConflict(t *testing.T) {
	ctab := New("testfiles/crontab-conflict.yml", time.Hour, nil)
	_, err := ctab.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "'spec' and 'sched' fields are mutually exclusive")
}

func TestParser_SchedToSpec(t *testing.T) {
	p := Parser{}

	tests := []struct {
		name     string
		sched    Schedule
		expected string
	}{
		{
			name:     "all fields",
			sched:    Schedule{Minute: "0", Hour: "2", Day: "15", Month: "*/3", Weekday: "1-5"},
			expected: "0 2 15 */3 1-5",
		},
		{
			name:     "partial fields",
			sched:    Schedule{Minute: "30", Hour: "9"},
			expected: "30 9 * * *",
		},
		{
			name:     "empty fields default to *",
			sched:    Schedule{},
			expected: "* * * * *",
		},
		{
			name:     "complex patterns",
			sched:    Schedule{Minute: "0,15,30,45", Hour: "*/2", Day: "1-7,15-21"},
			expected: "0,15,30,45 */2 1-7,15-21 * *",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.schedToSpec(tt.sched)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParser_Changes(t *testing.T) {
	tmp, err := os.CreateTemp("", "crontab")
	require.NoError(t, err)
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	_, err = tmp.WriteString("1 * * * * ls\n")
	require.NoError(t, err)
	_, err = tmp.WriteString("2 * * * * ls\n")
	require.NoError(t, err)

	ctab := New(tmp.Name(), time.Millisecond*200, nil)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	time.AfterFunc(time.Millisecond*500, func() {
		_, e := tmp.WriteString("3 * * * * ls\n")
		require.NoError(t, e)
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	ch, err := ctab.Changes(ctx)
	require.NoError(t, err)
	updJobs := <-ch
	require.Len(t, updJobs, 3)
	assert.Equal(t, "3 * * * *", updJobs[2].Spec)
	jobs, err = ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)
}

func TestParser_ChangesHup(t *testing.T) {
	tmp, err := os.CreateTemp("", "crontab")
	require.NoError(t, err)
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	_, err = tmp.WriteString("1 * * * * ls\n")
	require.NoError(t, err)
	_, err = tmp.WriteString("2 * * * * ls\n")
	require.NoError(t, err)

	hupCh := make(chan struct{})
	ctab := New(tmp.Name(), time.Hour, hupCh)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	time.AfterFunc(time.Millisecond*500, func() {
		_, e := tmp.WriteString("3 * * * * ls\n")
		require.NoError(t, e)
		hupCh <- struct{}{}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	ch, err := ctab.Changes(ctx)
	require.NoError(t, err)
	updJobs := <-ch
	assert.Len(t, updJobs, 3)
	assert.Equal(t, "3 * * * *", updJobs[2].Spec)
	jobs, err = ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)
}

func TestParseYAMLWithRepeater(t *testing.T) {
	p := New("testfiles/crontab-repeater.yml", time.Hour, nil)
	jobs, err := p.List()
	require.NoError(t, err)
	require.Len(t, jobs, 3)

	// job with full repeater config
	assert.Equal(t, "0 * * * *", jobs[0].Spec)
	assert.Equal(t, "echo 'hourly job with custom repeater'", jobs[0].Command)
	assert.Equal(t, "custom repeater job", jobs[0].Name)
	require.NotNil(t, jobs[0].Repeater)
	assert.Equal(t, 5, *jobs[0].Repeater.Attempts)
	assert.Equal(t, 2*time.Second, *jobs[0].Repeater.Duration)
	assert.Equal(t, 2.5, *jobs[0].Repeater.Factor)
	assert.Equal(t, true, *jobs[0].Repeater.Jitter)

	// job with partial repeater config
	assert.Equal(t, "*/5 * * * *", jobs[1].Spec)
	assert.Equal(t, "echo 'job with partial repeater'", jobs[1].Command)
	assert.Equal(t, "partial repeater", jobs[1].Name)
	require.NotNil(t, jobs[1].Repeater)
	assert.Equal(t, 3, *jobs[1].Repeater.Attempts)
	assert.Nil(t, jobs[1].Repeater.Duration)
	assert.Nil(t, jobs[1].Repeater.Factor)
	assert.Nil(t, jobs[1].Repeater.Jitter)

	// job without repeater config
	assert.Equal(t, "@daily", jobs[2].Spec)
	assert.Equal(t, "echo 'daily job without repeater'", jobs[2].Command)
	assert.Equal(t, "default repeater", jobs[2].Name)
	assert.Nil(t, jobs[2].Repeater)
}
