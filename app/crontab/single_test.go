package crontab

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSingle(t *testing.T) {
	s := NewSingle("*/2 10 * * 1-5 blah foo")
	res, err := s.List()
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, JobSpec{Spec: "*/2 10 * * 1-5", Command: "blah foo"}, res[0])

	s = NewSingle("bad")
	_, err = s.List()
	assert.Error(t, err)

	assert.Equal(t, "bad", s.String())

	ch, err := s.Changes(context.TODO())
	assert.Nil(t, ch)
	assert.Error(t, err)
}
