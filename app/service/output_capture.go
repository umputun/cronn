package service

import (
	"bytes"
	"strings"
	"sync"
)

// OutputCapture captures command output (stdout+stderr combined).
// it collects last N log lines in a circular buffer. thread safe for concurrent writes.
type OutputCapture struct {
	maxLogLines int
	log         []string
	mu          sync.Mutex
}

// NewOutputCapture creates io.Writer that captures output limited to last max lines
func NewOutputCapture(maximum int) *OutputCapture {
	return &OutputCapture{maxLogLines: maximum}
}

// Write satisfies io.Writer interface, captures last N log lines in circular buffer
func (o *OutputCapture) Write(p []byte) (n int, err error) {
	if o.maxLogLines == 0 {
		return len(p), nil // disabled, don't capture anything
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for line := range bytes.SplitSeq(p, []byte("\n")) {
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
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.log, "\n")
}
