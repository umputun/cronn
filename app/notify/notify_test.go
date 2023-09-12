package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/go-pkgz/notify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umputun/cronn/app/notify/mocks"
)

func TestService_EmptyDestinations(t *testing.T) {
	svc := NewService(Params{}, SendersParams{})
	require.Nil(t, svc)
}

func TestMakeErrorHTMLDefault(t *testing.T) {
	svc := NewService(Params{}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	res, err := svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task failed")
}

func TestMakeErrorHTMLCustom(t *testing.T) {
	svc := NewService(Params{ErrorTemplate: "testfiles/err.tmpl"}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	res, err := svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "Command failed: ls -la")
	assert.Contains(t, res, "Spec: * * * * *")

	svc = NewService(Params{ErrorTemplate: "testfiles/err-bad.tmpl"}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	res, err = svc.MakeErrorHTML("* * * * *", "ls -la", "some log")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
}

func TestMakeCompletionHTMLDefault(t *testing.T) {
	svc := NewService(Params{}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	res, err := svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
	assert.Contains(t, res, "<li>Spec: <span class=\"bold\">* * * * *</span></li>")
	assert.Contains(t, res, "Cronn task completed")
}

func TestMakeCompletionHTMLCustom(t *testing.T) {
	svc := NewService(Params{CompletionTemplate: "testfiles/completed.tmpl"}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	res, err := svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "Command done: ls -la")
	assert.Contains(t, res, "Spec: * * * * *")

	svc = NewService(Params{CompletionTemplate: "testfiles/completed-bad.tmpl"}, SendersParams{ToEmails: []string{"test@example.com"}})
	res, err = svc.MakeCompletionHTML("* * * * *", "ls -la")
	require.NoError(t, err)
	assert.Contains(t, res, "<li>Command: <span class=\"bold\">ls -la</span></li>")
}

func TestService_IsOnCompletion(t *testing.T) {
	svc := NewService(Params{EnabledCompletion: true}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	assert.True(t, svc.IsOnCompletion())

	svc = NewService(Params{EnabledCompletion: false}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	assert.False(t, svc.IsOnCompletion())
}

func TestService_IsOnError(t *testing.T) {
	svc := NewService(Params{EnabledError: true}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	assert.True(t, svc.IsOnError())

	svc = NewService(Params{EnabledError: false}, SendersParams{ToEmails: []string{"test@example.com"}})
	require.NotNil(t, svc)
	assert.False(t, svc.IsOnError())
}

func TestService_Send(t *testing.T) {
	tests := []struct {
		name           string
		subj           string
		text           string
		destination    string
		mockSendErr    error
		expectedErrMsg string
	}{
		{
			name:        "Successful Send",
			subj:        "Test Subject",
			text:        "Test Text",
			destination: "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			mockSendErr: nil,
		},
		{
			name:           "Send Error",
			subj:           "Problem Subject",
			text:           "Problem Text",
			destination:    "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Problem+Subject",
			mockSendErr:    errors.New("mock error"),
			expectedErrMsg: "mock error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mailtoNotifier := &mocks.NotifierMock{
				SendFunc: func(_ context.Context, dest string, text string) error {
					assert.Equal(t, tt.text, text)
					assert.Equal(t, tt.destination, dest)
					return tt.mockSendErr
				},
				SchemaFunc: func() string {
					return "mailto"
				},
			}

			s := Service{
				destinations: []notify.Notifier{mailtoNotifier},
				fromEmail:    "from@example.com",
				toEmail:      []string{"to@example.com", "to2@example.com"},
			}

			err := s.Send(context.Background(), tt.subj, tt.text)
			assert.Len(t, mailtoNotifier.SendCalls(), 1)
			if tt.expectedErrMsg == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedErrMsg)
			}
		})
	}
}
