package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOutputCapture(t *testing.T) {
	oc := NewOutputCapture(10)
	require.NotNil(t, oc)
	assert.Equal(t, 10, oc.maxLogLines)
	assert.Empty(t, oc.log)
}

func TestOutputCapture_Write(t *testing.T) {
	t.Run("writes within limit", func(t *testing.T) {
		oc := NewOutputCapture(5)
		n, err := oc.Write([]byte("line1\nline2\nline3"))
		require.NoError(t, err)
		assert.Equal(t, 17, n)
		assert.Equal(t, "line1\nline2\nline3", oc.GetOutput())
	})

	t.Run("circular buffer beyond limit", func(t *testing.T) {
		oc := NewOutputCapture(3)
		_, err := oc.Write([]byte("line1\nline2\nline3\nline4\nline5"))
		require.NoError(t, err)
		assert.Equal(t, "line3\nline4\nline5", oc.GetOutput())
	})

	t.Run("zero limit disables capture", func(t *testing.T) {
		oc := NewOutputCapture(0)
		n, err := oc.Write([]byte("line1\nline2\nline3"))
		require.NoError(t, err)
		assert.Equal(t, 17, n)
		assert.Empty(t, oc.GetOutput())
	})

	t.Run("skips empty lines", func(t *testing.T) {
		oc := NewOutputCapture(5)
		_, err := oc.Write([]byte("line1\n\nline2\n\n\nline3"))
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2\nline3", oc.GetOutput())
	})

	t.Run("multiple writes", func(t *testing.T) {
		oc := NewOutputCapture(5)
		_, err := oc.Write([]byte("line1\nline2"))
		require.NoError(t, err)
		_, err = oc.Write([]byte("line3\nline4"))
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2\nline3\nline4", oc.GetOutput())
	})

	t.Run("exact limit boundary", func(t *testing.T) {
		oc := NewOutputCapture(3)
		_, err := oc.Write([]byte("line1\nline2\nline3"))
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2\nline3", oc.GetOutput())

		_, err = oc.Write([]byte("line4"))
		require.NoError(t, err)
		assert.Equal(t, "line2\nline3\nline4", oc.GetOutput())
	})
}

func TestOutputCapture_GetOutput(t *testing.T) {
	t.Run("returns empty for no output", func(t *testing.T) {
		oc := NewOutputCapture(10)
		assert.Empty(t, oc.GetOutput())
	})

	t.Run("returns joined lines", func(t *testing.T) {
		oc := NewOutputCapture(10)
		_, err := oc.Write([]byte("line1\nline2\nline3"))
		require.NoError(t, err)
		assert.Equal(t, "line1\nline2\nline3", oc.GetOutput())
	})
}

func TestOutputCapture_CircularBufferStress(t *testing.T) {
	oc := NewOutputCapture(5)

	// write 20 lines, should keep only last 5
	for i := 1; i <= 20; i++ {
		_, err := oc.Write([]byte("line" + string(rune('0'+i%10))))
		require.NoError(t, err)
	}

	lines := oc.log
	assert.Len(t, lines, 5)
}
