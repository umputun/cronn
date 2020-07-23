package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeErrorHTML(t *testing.T) {
	res, err := MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task failed")
}

func TestMakeCompletionHTML(t *testing.T) {
	res, err := MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task completed")
}
