// Package request contains request types for job event handlers
package request

import (
	"time"

	"github.com/umputun/cronn/app/web/enums"
)

// StepState is the state of one step in a run of a job with steps
type StepState struct {
	Index      int // 1-based position of the step in the job
	Name       string
	Command    string // command as executed, templates resolved
	Status     enums.StepStatus
	ExitCode   int
	Attempts   int
	StartedAt  time.Time
	FinishedAt time.Time
	Output     string
}

// OnJobStart contains parameters for job start event
type OnJobStart struct {
	Command         string
	ExecutedCommand string
	Schedule        string
	StartTime       time.Time
	// LiveOutput returns the output captured so far, all retry attempts included.
	// It is nil when web output capture is disabled.
	LiveOutput func() string
	// LiveSteps returns the states of the run's steps so far. It is nil for a job without steps
	LiveSteps func() []StepState
}

// OnJobComplete contains parameters for job completion event
type OnJobComplete struct {
	Command         string
	ExecutedCommand string
	Schedule        string
	StartTime       time.Time
	EndTime         time.Time
	ExitCode        int
	Output          string
	Steps           []StepState // states of the run's steps, empty for a job without steps
	Err             error
}

// RecordExecution contains parameters for recording job execution
type RecordExecution struct {
	JobID           string
	StartedAt       time.Time
	FinishedAt      time.Time
	Status          enums.JobStatus
	ExitCode        int
	ExecutedCommand string
	Output          string
	Steps           []StepState
}
