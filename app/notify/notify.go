// Package notify providers error delivery via email
package notify

import (
	"bytes"
	"html/template"
	"os"
	"time"

	"github.com/pkg/errors"
)

// MakeErrorHTML creates html error string to be send
func MakeErrorHTML(spec, command, errorLog string) (string, error) {
	tmpl := `<!DOCTYPE html>
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

	t, err := template.New("msg").Parse(tmpl)
	if err != nil {
		return "", errors.Wrap(err, "can't parse message template")
	}
	buf := bytes.Buffer{}
	err = t.Execute(&buf, data)
	return buf.String(), errors.Wrap(err, "failed to apply template")
}

// MakeCompletionHTML creates html completion string to be send
func MakeCompletionHTML(spec, command, errorLog string) (string, error) {
	tmpl := `<!DOCTYPE html>
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
		<p>Cronn task completed on <span class="bold">{{.Host}}</span> at {{.TS.Format "2006-01-02T15:04:05Z07:00"}}</p>
		<ul>
			<li>Command: <span class="bold">{{.Command}}</span></li>
			<li>Spec: <span class="bold">{{.Spec}}</span></li>
		</ul>>
	</body>
</html>
`

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

	t, err := template.New("msg").Parse(tmpl)
	if err != nil {
		return "", errors.Wrap(err, "can't parse message template")
	}
	buf := bytes.Buffer{}
	err = t.Execute(&buf, data)
	return buf.String(), errors.Wrap(err, "failed to apply template")
}
