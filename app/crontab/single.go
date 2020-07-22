package crontab

import (
	"context"
	"errors"
)

// Single provides crontab interface for a single command
type Single struct {
	Line string
}

// List parses the Line and return JobSpec for it
func (s Single) List() (result []JobSpec, err error) {
	j, err := Parse(s.Line)
	if err != nil {
		return nil, err
	}
	return []JobSpec{j}, nil
}

func (s Single) String() string {
	return s.Line
}

// Changes not implemented
func (s Single) Changes(context.Context) (<-chan []JobSpec, error) {
	return nil, errors.New("not supported")
}
