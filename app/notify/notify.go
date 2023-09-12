// Package notify providers error delivery via email
package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/notify"
)

//go:generate moq -out mocks/notifier.go -pkg mocks -skip-ensure -fmt goimports ../../vendor/github.com/go-pkgz/notify Notifier

// Service warps email client and template management
type Service struct {
	destinations []notify.Notifier

	fromEmail            string
	toEmails             []string
	slackChannels        []string
	telegramDestinations []string
	webhookURLs          []string

	onCompletion       bool
	onError            bool
	errorTemplate      string
	completionTemplate string
}

// Params which are used in all notify destinations
type Params struct {
	EnabledError       bool
	EnabledCompletion  bool
	ErrorTemplate      string
	CompletionTemplate string
}

// SendersParams contains params specific for notify destinations
// like email, slack, telegram or webhook
type SendersParams struct {
	notify.SMTPParams
	FromEmail string
	ToEmails  []string

	SlackToken    string
	SlackChannels []string

	TelegramToken        string
	TelegramDestinations []string

	WebhookURLs []string
}

// NewService makes notification service with optional template files
func NewService(notifyParams Params, sendersParams SendersParams) *Service {
	res := Service{
		destinations:         []notify.Notifier{},
		onError:              notifyParams.EnabledError,
		onCompletion:         notifyParams.EnabledCompletion,
		fromEmail:            sendersParams.FromEmail,
		toEmails:             sendersParams.ToEmails,
		slackChannels:        sendersParams.SlackChannels,
		telegramDestinations: sendersParams.TelegramDestinations,
		webhookURLs:          sendersParams.WebhookURLs,
	}

	if len(sendersParams.ToEmails) != 0 {
		res.destinations = append(res.destinations, notify.NewEmail(sendersParams.SMTPParams))
	}
	if len(sendersParams.WebhookURLs) != 0 {
		res.destinations = append(res.destinations, notify.NewWebhook(notify.WebhookParams{}))
	}
	if len(sendersParams.SlackChannels) != 0 {
		res.destinations = append(res.destinations, notify.NewSlack(sendersParams.SlackToken))
	}
	if len(sendersParams.TelegramDestinations) != 0 {
		tgService, err := notify.NewTelegram(notify.TelegramParams{Token: sendersParams.TelegramToken})
		if err != nil {
			log.Printf("[WARN] error setting up telegram notifications: %v", err)
		}
		if err == nil {
			res.destinations = append(res.destinations, tgService)
		}
	}

	if len(res.destinations) == 0 {
		return nil
	}

	res.errorTemplate = defaultErrorTemplate
	res.completionTemplate = defaultCompletionTemplate
	if notifyParams.ErrorTemplate != "" {
		data, err := os.ReadFile(notifyParams.ErrorTemplate) // nolint gosec
		if err == nil {
			res.errorTemplate = string(data)
		} else {
			log.Printf("[WARN] can't open email template file %s, %v", notifyParams.ErrorTemplate, err)
		}
	}
	if notifyParams.CompletionTemplate != "" {
		data, err := os.ReadFile(notifyParams.CompletionTemplate) // nolint gosec
		if err == nil {
			res.completionTemplate = string(data)
		} else {
			log.Printf("[WARN] can't open email template file %s, %v", notifyParams.CompletionTemplate, err)
		}
	}

	return &res
}

// Send notification to all set up notification destinations
func (s *Service) Send(ctx context.Context, subj, text string) error {
	var err error
	escapedSubj := url.QueryEscape(subj)
	if len(s.toEmails) != 0 {
		emails := strings.Join(s.toEmails, ",")
		destination := fmt.Sprintf("mailto:%s?from=%s&subject=%s", emails, s.fromEmail, escapedSubj)
		err = errors.Join(err, notify.Send(ctx, s.destinations, destination, text))
	}
	for _, slackChannel := range s.slackChannels {
		destination := fmt.Sprintf("slack:%s?title=%s", slackChannel, escapedSubj)
		err = errors.Join(err, notify.Send(ctx, s.destinations, destination, text))
	}
	for _, telegramDestination := range s.telegramDestinations {
		destination := fmt.Sprintf("telegram:%s?type=HTML", telegramDestination)
		err = errors.Join(err, notify.Send(ctx, s.destinations, destination, notify.TelegramSupportedHTML(text)))
	}
	for _, webhookURL := range s.webhookURLs {
		err = errors.Join(err, notify.Send(ctx, s.destinations, webhookURL, subj+"\n"+text))
	}
	return err
}

// MakeErrorHTML creates error html body from errorTemplate
func (s *Service) MakeErrorHTML(spec, command, errorLog string) (string, error) {
	data := struct {
		Spec    string
		Command string
		TS      time.Time
		Error   string
		Host    string
	}{
		Spec:    spec,
		Command: command,
		TS:      time.Now(),
		Error:   errorLog,
		Host:    os.Getenv("MHOST"),
	}

	t, err := template.New("msg").Parse(s.errorTemplate)
	if err != nil {
		return "", fmt.Errorf("can't parse message template: %w", err)
	}
	buf := bytes.Buffer{}
	if err = t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to apply template: %w", err)
	}
	return buf.String(), nil
}

// MakeCompletionHTML creates error html body from completionTemplate
func (s *Service) MakeCompletionHTML(spec, command string) (string, error) {
	data := struct {
		Spec    string
		Command string
		TS      time.Time
		Error   string
		Host    string
	}{
		Spec:    spec,
		Command: command,
		TS:      time.Now(),
		Host:    os.Getenv("MHOST"),
	}

	t, err := template.New("msg").Parse(s.completionTemplate)
	if err != nil {
		return "", fmt.Errorf("can't parse message template: %w", err)
	}
	buf := bytes.Buffer{}
	if err = t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to apply template: %w", err)
	}
	return buf.String(), nil
}

// IsOnError status enabling on-error notification
func (s *Service) IsOnError() bool { return s.onError }

// IsOnCompletion status enabling on-passed notification
func (s *Service) IsOnCompletion() bool { return s.onCompletion }

var (
	defaultErrorTemplate = `<!DOCTYPE html>
<html>
	<head>
		<meta name="viewport" content="width=device-width" />
		<meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
		<style type="text/css">
			body {
				font-family: "Arial";
				font-size: 1.0em;
			}
			ul {
				margin-top: -0.5em;
				margin-left: -0.5em;
			}
			pre {
				padding: 0.6em;
				font-size: 0.7em;
				background-color: #E8E2A0;
				font-family: "Menlo";
				overflow-x: auto;
				white-space: pre-wrap;
				white-space: -moz-pre-wrap;
				white-space: -pre-wrap;
				white-space: -o-pre-wrap;
				word-wrap: break-word;
			}
			.bold {
				color: #882828;
				font-weight: 900;
			}
		</style>
	</head>
		
	<body>
		<p>Cronn task failed on <span class="bold">{{.Host}}</span> at {{.TS.Format "2006-01-02T15:04:05Z07:00"}}</p>
		<ul>
			<li>Command: <span class="bold">{{.Command}}</span></li>
			<li>Spec: <span class="bold">{{.Spec}}</span></li>
		</ul>
		
		<pre>
{{.Error}}
		</pre>
	</body>
</html>
`

	defaultCompletionTemplate = `<!DOCTYPE html>
<html>
	<head>
		<meta name="viewport" content="width=device-width" />
		<meta http-equiv="Content-Type" content="text/html; charset=UTF-8" />
		<style type="text/css">
			body {
				font-family: "Arial";
				font-size: 1.0em;
			}
			ul {
				margin-top: -0.5em;
				margin-left: -0.5em;
			}
			.bold {
				color: #882828;
				font-weight: 900;
			}
		</style>
	</head>
		
	<body>
		<p>Cronn task completed on <span class="bold">{{.Host}}</span> at {{.TS.Format "2006-01-02T15:04:05Z07:00"}}</p>
		<ul>
			<li>Command: <span class="bold">{{.Command}}</span></li>
			<li>Spec: <span class="bold">{{.Spec}}</span></li>
		</ul>>
	</body>
</html>
`
)
