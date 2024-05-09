package service

import (
	"bytes"
	"strings"
)

// ErrorWriter implements error and collect log messages. Not thread safe
type ErrorWriter struct {
	maxErrLogLines int
	err            error
	log            []string
}

// NewErrorWriter makes io.Writer and error capturing data in and limit to last max lines
func NewErrorWriter(maximum int) *ErrorWriter {
	return &ErrorWriter{maxErrLogLines: maximum}
}

// Error returns string combining the error and log
func (e *ErrorWriter) Error() string {
	return e.err.Error() + "\n\n" + strings.Join(e.log, "\n")
}

// SerError assign error
func (e *ErrorWriter) SerError(err error) {
	e.err = err
}

// Write satisfies io.Writer to collect last N log lines
func (e *ErrorWriter) Write(p []byte) (n int, err error) {
	for _, line := range bytes.Split(p, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if len(e.log) > e.maxErrLogLines {
			e.log = e.log[1:]
		}
		e.log = append(e.log, string(line))
	}
	return len(p), err
}
