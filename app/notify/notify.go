// Package notify providers error delivery via email
package notify

import (
	"bytes"
	"context"
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

	fromEmail string
	toEmail   []string

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
}

// NewService makes notification service with optional template files
func NewService(notifyParams Params, emailParams SendersParams) *Service {
	emailService := notify.NewEmail(emailParams.SMTPParams)

	res := Service{
		destinations: []notify.Notifier{emailService},
		fromEmail:    emailParams.FromEmail,
		toEmail:      emailParams.ToEmails,
		onError:      notifyParams.EnabledError,
		onCompletion: notifyParams.EnabledCompletion,
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

// Send notification, currently only email is supported
func (s *Service) Send(ctx context.Context, subj, text string) error {
	emailSubj := url.QueryEscape(subj)
	emails := strings.Join(s.toEmail, ",")
	emailDestination := fmt.Sprintf("mailto:%s?from=%s&subject=%s", emails, s.fromEmail, emailSubj)
	return notify.Send(ctx, s.destinations, emailDestination, text)
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
