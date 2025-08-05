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

	// write YAML with empty spec
	_, err = tmp.WriteString(`jobs:
  - spec: ""
    command: "test"`)
	require.NoError(t, err)

	ctab := New(tmp.Name(), time.Hour, nil)
	_, err = ctab.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty spec")

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
	assert.Contains(t, err.Error(), "empty command")
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
