package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogPrefixer_Write(t *testing.T) {
	out := bytes.NewBuffer(nil)
	prefixer := NewLogPrefixer(out, "du /var/lib/monitoring")

	n, err := prefixer.Write([]byte("first line of the output\n"))
	require.NoError(t, err)
	assert.Equal(t, 25, n)

	n, err = prefixer.Write([]byte("second line of the output\n"))
	require.NoError(t, err)
	assert.Equal(t, 26, n)

	expectedOutput :=
		"{du /var/lib/moni...} first line of the output\n" +
			"{du /var/lib/moni...} second line of the output\n"
	assert.Equal(t, expectedOutput, out.String())
}

func TestLogPrefixer_prefixForCommand(t *testing.T) {
	prefixer := &LogPrefixer{}

	assert.Equal(t, []byte("{ls -la} "), prefixer.prefixForCommand("ls -la"))
	assert.Equal(t, []byte("{cat /var/lib/pid} "), prefixer.prefixForCommand("cat /var/lib/pid"))
	assert.Equal(t, []byte("{du /var/lib/moni...} "), prefixer.prefixForCommand("du /var/lib/monitoring"))
}
