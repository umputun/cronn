package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// APIStatusResponse is the JSON response for /api/v1/status
type APIStatusResponse struct {
	Jobs      []APIJob  `json:"jobs"`
	Stats     APIStats  `json:"stats"`
	Timestamp time.Time `json:"timestamp"`
}

// APIJob represents a job in JSON API response
type APIJob struct {
	ID         string    `json:"id"`
	Command    string    `json:"command"`
	Schedule   string    `json:"schedule"`
	NextRun    time.Time `json:"next_run,omitzero"`
	LastRun    time.Time `json:"last_run,omitzero"`
	LastStatus string    `json:"last_status"`
	IsRunning  bool      `json:"is_running"`
	Enabled    bool      `json:"enabled"`
	Steps      []APIStep `json:"steps,omitempty"`
}

// APIStep is one step of a job with steps in JSON API response
type APIStep struct {
	Name    string `json:"name,omitempty"`
	Command string `json:"command"`
}

// APIStepRun is one step of an execution of a job with steps in JSON API response
type APIStepRun struct {
	Index      int       `json:"index"`
	Name       string    `json:"name,omitempty"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	ExitCode   int       `json:"exit_code"`
	Attempts   int       `json:"attempts"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
	Output     string    `json:"output"`
}

// APIStats represents aggregated statistics in JSON API response
type APIStats struct {
	Total   int `json:"total"`
	Running int `json:"running"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Idle    int `json:"idle"`
}

// APIExecution represents an execution in JSON API response (without output)
type APIExecution struct {
	ID              int       `json:"id"`
	StartedAt       time.Time `json:"started_at,omitzero"`
	FinishedAt      time.Time `json:"finished_at,omitzero"`
	Status          string    `json:"status"`
	ExitCode        int       `json:"exit_code"`
	ExecutedCommand string    `json:"executed_command,omitempty"`
}

// APIHistoryResponse is the JSON response for job history
type APIHistoryResponse struct {
	Job        APIJob         `json:"job"`
	Executions []APIExecution `json:"executions"`
}

// APILogsResponse is the JSON response for execution logs
type APILogsResponse struct {
	ID              int          `json:"id"`
	JobID           string       `json:"job_id"`
	StartedAt       time.Time    `json:"started_at,omitzero"`
	FinishedAt      time.Time    `json:"finished_at,omitzero"`
	Status          string       `json:"status"`
	ExitCode        int          `json:"exit_code"`
	ExecutedCommand string       `json:"executed_command,omitempty"`
	Output          string       `json:"output"`
	Steps           []APIStepRun `json:"steps,omitempty"`
}

// toAPIExecution converts persistence.ExecutionInfo to APIExecution
func toAPIExecution(e persistence.ExecutionInfo) APIExecution {
	return APIExecution{
		ID:              e.ID,
		StartedAt:       e.StartedAt,
		FinishedAt:      e.FinishedAt,
		Status:          e.Status.String(),
		ExitCode:        e.ExitCode,
		ExecutedCommand: e.ExecutedCommand,
	}
}

// toAPIJob converts persistence.JobInfo to APIJob
func toAPIJob(job persistence.JobInfo) APIJob {
	return APIJob{
		ID:         job.ID,
		Command:    job.Command,
		Schedule:   job.Schedule,
		NextRun:    job.NextRun,
		LastRun:    job.LastRun,
		LastStatus: job.LastStatus.String(),
		IsRunning:  job.IsRunning,
		Enabled:    job.Enabled,
		Steps:      toAPISteps(job.Steps),
	}
}

// toAPISteps converts the steps of a job, nil for a job without steps
func toAPISteps(steps []persistence.JobStep) []APIStep {
	if len(steps) == 0 {
		return nil
	}
	res := make([]APIStep, len(steps))
	for i, st := range steps {
		res[i] = APIStep{Name: st.Name, Command: st.Command}
	}
	return res
}

// toAPIStepRuns converts the recorded steps of an execution, and joins their outputs into one, each under a
// header line, as the output of a run of a job with steps is stored by step only
func toAPIStepRuns(steps []persistence.StepInfo, total int) (runs []APIStepRun, output string) {
	parts := make([]string, 0, len(steps))
	for _, st := range steps {
		runs = append(runs, APIStepRun{Index: st.Index, Name: st.Name, Command: st.Command, Status: st.Status.String(),
			ExitCode: st.ExitCode, Attempts: st.Attempts, StartedAt: st.StartedAt, FinishedAt: st.FinishedAt,
			Output: st.Output})
		if st.Status == enums.StepStatusSkipped {
			continue
		}
		part := fmt.Sprintf("== step %d/%d: %s ==", st.Index, total, st.Command)
		if st.Output != "" {
			part += "\n" + st.Output
		}
		parts = append(parts, part)
	}
	return runs, strings.Join(parts, "\n")
}

// handleAPIStatus returns JSON status for all jobs - designed for CLI/jq consumption
func (s *Server) handleAPIStatus(w http.ResponseWriter, _ *http.Request) {
	s.jobsMu.RLock()
	allJobs := make([]persistence.JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobCopy := job
		if jobCopy.Enabled {
			s.updateNextRun(&jobCopy)
		}
		allJobs = append(allJobs, jobCopy)
	}
	s.jobsMu.RUnlock()

	// sort by crontab order for consistent output
	s.sortJobs(allJobs, enums.SortModeDefault)

	// convert to API response and count stats
	jobs := make([]APIJob, 0, len(allJobs))
	stats := APIStats{Total: len(allJobs)}

	for _, job := range allJobs {
		jobs = append(jobs, toAPIJob(job))

		// count stats
		if job.IsRunning {
			stats.Running++
		}
		switch job.LastStatus {
		case enums.JobStatusSuccess:
			stats.Success++
		case enums.JobStatusFailed:
			stats.Failed++
		case enums.JobStatusIdle:
			stats.Idle++
		}
	}

	resp := APIStatusResponse{
		Jobs:      jobs,
		Stats:     stats,
		Timestamp: time.Now(),
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleAPIJobHistory returns JSON execution history for a job
func (s *Server) handleAPIJobHistory(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		s.writeJSONError(w, http.StatusBadRequest, "job ID required")
		return
	}

	s.jobsMu.RLock()
	job, exists := s.jobs[jobID]
	s.jobsMu.RUnlock()

	if !exists {
		s.writeJSONError(w, http.StatusNotFound, "job not found")
		return
	}

	// update next run time for enabled jobs only
	if job.Enabled {
		s.updateNextRun(&job)
	}

	// get execution history from database
	executions, err := s.store.GetExecutions(jobID, 50)
	if err != nil {
		log.Printf("[ERROR] failed to get executions for job %s: %v", jobID, err)
		s.writeJSONError(w, http.StatusInternalServerError, "failed to load execution history")
		return
	}

	// convert to API format
	apiExecs := make([]APIExecution, 0, len(executions))
	for _, e := range executions {
		apiExecs = append(apiExecs, toAPIExecution(e))
	}

	resp := APIHistoryResponse{
		Job:        toAPIJob(job),
		Executions: apiExecs,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// handleAPIExecutionLogs returns JSON with execution details including output
func (s *Server) handleAPIExecutionLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	execIDStr := r.PathValue("exec_id")

	if jobID == "" || execIDStr == "" {
		s.writeJSONError(w, http.StatusBadRequest, "job ID and execution ID required")
		return
	}

	// parse execution ID
	execID, err := strconv.Atoi(execIDStr)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "invalid execution ID")
		return
	}

	// get execution from database
	execution, err := s.store.GetExecutionByID(execID)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			s.writeJSONError(w, http.StatusNotFound, "execution not found")
			return
		}
		log.Printf("[ERROR] failed to get execution %d: %v", execID, err)
		s.writeJSONError(w, http.StatusInternalServerError, "failed to load execution")
		return
	}

	// verify execution belongs to the requested job.
	// return 404 (not 403) to avoid confirming execution exists for other jobs
	if execution.JobID != jobID {
		s.writeJSONError(w, http.StatusNotFound, "execution not found")
		return
	}

	resp := APILogsResponse{
		ID:              execution.ID,
		JobID:           execution.JobID,
		StartedAt:       execution.StartedAt,
		FinishedAt:      execution.FinishedAt,
		Status:          execution.Status.String(),
		ExitCode:        execution.ExitCode,
		ExecutedCommand: execution.ExecutedCommand,
		Output:          execution.Output,
	}
	if execution.StepFirst > 0 {
		steps, err := s.store.GetExecutionSteps(execution.ID)
		if err != nil {
			log.Printf("[ERROR] failed to get steps of execution %d: %v", execID, err)
			s.writeJSONError(w, http.StatusInternalServerError, "failed to load execution steps")
			return
		}
		total := 0
		for _, st := range steps {
			total = max(total, st.Index)
		}
		if job, ok := s.jobByID(jobID); ok && len(job.Steps) > 0 {
			total = len(job.Steps)
		}
		resp.Steps, resp.Output = toAPIStepRuns(steps, total)
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// writeJSON writes a JSON response
func (s *Server) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[WARN] failed to encode JSON response: %v", err)
	}
}

// writeJSONError writes a JSON error response
func (s *Server) writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]string{"error": message}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[WARN] failed to encode JSON error response: %v", err)
	}
}
