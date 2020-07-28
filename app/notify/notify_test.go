package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeErrorHTMLDefault(t *testing.T) {
	svc := NewService(nil, "", "")
	res, err := svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task failed")
}

func TestMakeErrorHTMLCustom(t *testing.T) {
	svc := NewService(nil, "testfiles/err.tmpl", "")
	res, err := svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "Command failed: ls -la")
	assert.Contains(t, res, "Spec: * * * * *")

	svc = NewService(nil, "testfiles/err-bad.tmpl", "")
	res, err = svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
}

func TestMakeCompletionHTMLDefault(t *testing.T) {
	svc := NewService(nil, "", "")
	res, err := svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task completed")
}

func TestMakeCompletionHTMLCustom(t *testing.T) {
	svc := NewService(nil, "", "testfiles/completed.tmpl")
	res, err := svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "Command done: ls -la")
	assert.Contains(t, res, "Spec: * * * * *")

	svc = NewService(nil, "", "testfiles/completed-bad.tmpl")
	res, err = svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
}
