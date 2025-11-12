package service

import (
	"bytes"
	"strings"
)

// OutputCapture implements error interface and captures command output (stdout+stderr combined).
// it collects last N log lines in a circular buffer. not thread safe.
type OutputCapture struct {
	maxLogLines int
	err         error
	log         []string
}

// NewOutputCapture creates io.Writer and error interface implementation that captures output limited to last max lines
func NewOutputCapture(maximum int) *OutputCapture {
	return &OutputCapture{maxLogLines: maximum}
}

// Error returns string combining the error and captured log output
func (o *OutputCapture) Error() string {
	return o.err.Error() + "\n\n" + strings.Join(o.log, "\n")
}

// SetError assigns error to be wrapped with captured output
func (o *OutputCapture) SetError(err error) {
	o.err = err
}

// Write satisfies io.Writer interface, captures last N log lines in circular buffer
func (o *OutputCapture) Write(p []byte) (n int, err error) {
	if o.maxLogLines == 0 {
		return len(p), nil // disabled, don't capture anything
	}
	for _, line := range bytes.Split(p, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if len(o.log) >= o.maxLogLines {
			o.log = o.log[1:]
		}
		o.log = append(o.log, string(line))
	}
	return len(p), err
}

// GetOutput returns the captured log output as a single string
func (o *OutputCapture) GetOutput() string {
	return strings.Join(o.log, "\n")
}
