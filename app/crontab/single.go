package crontab

import (
	"context"
	"errors"
)

// Single provides crontab interface for a single command
type Single struct {
	line string
}

// NewSingle creates crontab for a single command
func NewSingle(line string) *Single {
	return &Single{line: line}
}

// List parses the Line and return JobSpec for it
func (s Single) List() (result []JobSpec, err error) {
	j, err := Parse(s.line)
	if err != nil {
		return nil, err
	}
	return []JobSpec{j}, nil
}

func (s Single) String() string {
	return s.line
}

// Changes not implemented
func (s Single) Changes(context.Context) (<-chan []JobSpec, error) {
	return nil, errors.New("not supported")
}
