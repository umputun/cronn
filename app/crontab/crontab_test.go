package crontab

import (
	"context"
	"io/ioutil"
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
	}

	ctab := Parser{}
	for _, tt := range tbl {
		r, err := ctab.parse(tt.inp)
		if tt.wasError {
			assert.NotNil(t, err, tt.inp)
		} else {
			assert.Nil(t, err, tt.inp)
		}
		assert.Equal(t, tt.js, r, tt.inp)
	}
}

func TestParser_List(t *testing.T) {
	ctab := New("testfiles/crontab", time.Hour)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)
	assert.Equal(t, []JobSpec{{Spec: "*/5 * * * *", Command: "ls -la ."}, {Spec: "*/2 1-18 * * *", Command: "export"},
		{Spec: "*/1 * * * *", Command: "something blah bad"}}, jobs)
	assert.Equal(t, "testfiles/crontab", ctab.String())

	ctab = New("testfiles/no-file", time.Hour)
	_, err = ctab.List()
	assert.Error(t, err)
}

func TestParser_Changes(t *testing.T) {
	tmp, err := ioutil.TempFile("", "crontab")
	require.NoError(t, err)
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	_, err = tmp.WriteString("1 * * * * ls\n")
	require.NoError(t, err)
	_, err = tmp.WriteString("2 * * * * ls\n")
	require.NoError(t, err)

	ctab := New(tmp.Name(), time.Millisecond*200)
	jobs, err := ctab.List()
	require.NoError(t, err)
	assert.Len(t, jobs, 2)

	time.AfterFunc(time.Millisecond*500, func() {
		_, err = tmp.WriteString("3 * * * * ls\n")
		require.NoError(t, err)
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
