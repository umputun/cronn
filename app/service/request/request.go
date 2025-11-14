// Package request contains request types for job event handlers
package request

import (
	"time"

	"github.com/umputun/cronn/app/web/enums"
)

// OnJobStart contains parameters for job start event
type OnJobStart struct {
	Command         string
	ExecutedCommand string
	Schedule        string
	StartTime       time.Time
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
}
