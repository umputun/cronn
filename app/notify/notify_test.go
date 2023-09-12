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

func TestService_AllDestinations(t *testing.T) {
	svc := NewService(Params{}, SendersParams{
		ToEmails:             []string{"test@example.com"},
		TelegramDestinations: []string{"username1"},
		SlackChannels:        []string{"general"},
		WebhookURLs:          []string{"https://example.com/cronn_notifications"},
	})
	require.NotNil(t, svc)
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

func TestService_SendEmail(t *testing.T) {
	tests := []struct {
		name                 string
		destination          string
		mockSendErr          error
		expectedErrMsg       string
		fromEmail            string
		toEmails             []string
		slackChannels        []string
		telegramDestinations []string
		webhookURLs          []string
		shouldSend           bool
	}{
		{
			name:        "Send Email",
			destination: "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			toEmails:    []string{"to@example.com", "to2@example.com"},
			shouldSend:  true,
		},
		{
			name:           "Error sending email",
			destination:    "mailto:to@example.com?from=from@example.com&subject=Test+Subject",
			mockSendErr:    errors.New("mock error"),
			expectedErrMsg: "mock error",
			toEmails:       []string{"to@example.com"},
			shouldSend:     true,
		},
		{
			name:                 "Send to Telegram, unsuccessfully as there is no destination",
			destination:          "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			telegramDestinations: []string{"username"},
			expectedErrMsg:       "unsupported destination schema: telegram",
		},
		{
			name:           "Send to Slack, unsuccessfully as there is no destination",
			destination:    "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			slackChannels:  []string{"general"},
			expectedErrMsg: "unsupported destination schema: slack",
		},
		{
			name:           "Send to webhook, unsuccessfully as there is no destination",
			destination:    "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			webhookURLs:    []string{"https://example.com/cronn_notifications"},
			expectedErrMsg: "unsupported destination schema: https",
		},
		{
			name:                 "Send Email and unsuccessfully send to Telegram and Slack as there is no destination for it",
			destination:          "mailto:to@example.com,to2@example.com?from=from@example.com&subject=Test+Subject",
			toEmails:             []string{"to@example.com", "to2@example.com"},
			telegramDestinations: []string{"username"},
			slackChannels:        []string{"general"},
			shouldSend:           true,
			expectedErrMsg:       "unsupported destination schema: slack\nunsupported destination schema: telegram",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mailtoNotifier := &mocks.NotifierMock{
				SendFunc: func(_ context.Context, dest string, text string) error {
					assert.Equal(t, "Test Text", text)
					assert.Equal(t, tt.destination, dest)
					return tt.mockSendErr
				},
				SchemaFunc: func() string {
					return "mailto"
				},
			}

			s := Service{
				destinations:         []notify.Notifier{mailtoNotifier},
				fromEmail:            "from@example.com",
				toEmails:             tt.toEmails,
				slackChannels:        tt.slackChannels,
				telegramDestinations: tt.telegramDestinations,
				webhookURLs:          tt.webhookURLs,
			}

			err := s.Send(context.Background(), "Test Subject", "Test Text")
			if tt.shouldSend {
				assert.Len(t, mailtoNotifier.SendCalls(), 1)
			} else {
				assert.Zero(t, mailtoNotifier.SendCalls())
			}
			if tt.expectedErrMsg == "" {
				require.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expectedErrMsg)
			}
		})
	}
}
