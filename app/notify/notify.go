// Package notify providers error delivery via email
package notify

import (
	"bytes"
	"html/template"
	"io/ioutil"
	"os"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
)

type Service struct {
	*Email
	errorTemplate      string
	completionTemplate string
}

// NewService makes notification service with optional template files
func NewService(email *Email, errTmplFile, complTmplFile string) *Service {
	res := Service{Email: email}

	res.errorTemplate = defaultErrorTemplate
	res.completionTemplate = defaultCompletionTemplate
	if errTmplFile != "" {
		data, err := ioutil.ReadFile(errTmplFile)
		if err == nil {
			res.errorTemplate = string(data)
		} else {
			log.Printf("[WARN] can't open email template file %s, %v", errTmplFile, err)
		}
	}
	if complTmplFile != "" {
		data, err := ioutil.ReadFile(complTmplFile)
		if err == nil {
			res.completionTemplate = string(data)
		} else {
			log.Printf("[WARN] can't open email template file %s, %v", complTmplFile, err)
		}
	}

	return &res
}

func (s Service) MakeErrorHTML(spec, command, errorLog string) (string, error) {
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
		return "", errors.Wrap(err, "can't parse message template")
	}
	buf := bytes.Buffer{}
	err = t.Execute(&buf, data)
	return buf.String(), errors.Wrap(err, "failed to apply template")
}

func (s Service) MakeCompletionHTML(spec, command string) (string, error) {
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
		return "", errors.Wrap(err, "can't parse message template")
	}
	buf := bytes.Buffer{}
	err = t.Execute(&buf, data)
	return buf.String(), errors.Wrap(err, "failed to apply template")
}

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
