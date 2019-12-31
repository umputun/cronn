package crontab

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
