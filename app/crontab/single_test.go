package crontab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingle_List(t *testing.T) {
	s := Single{Line: "*/2 10 * * 1-5 blah foo"}
	res, err := s.List()
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, JobSpec{Spec: "*/2 10 * * 1-5", Command: "blah foo"}, res[0])

	s = Single{Line: "bad"}
	_, err = s.List()
	assert.Error(t, err)
}
