package web

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// OnJobStart implements service.JobEventHandler interface
func (s *Server) OnJobStart(req request.OnJobStart) {
	event := JobEvent{
		JobID:           HashCommand(req.Command),
		Command:         req.Command,
		ExecutedCommand: req.ExecutedCommand,
		Schedule:        req.Schedule,
		EventType:       enums.EventTypeStarted,
		StartedAt:       req.StartTime,
		LiveOutput:      req.LiveOutput,
	}
	select {
	case s.eventChan <- event:
	default:
		log.Printf("[WARN] event channel full, dropping event")
	}
}

// OnJobComplete implements service.JobEventHandler interface
func (s *Server) OnJobComplete(req request.OnJobComplete) {
	eventType := enums.EventTypeCompleted
	if req.Err != nil {
		eventType = enums.EventTypeFailed
	}
	event := JobEvent{
		JobID:           HashCommand(req.Command),
		Command:         req.Command,
		ExecutedCommand: req.ExecutedCommand,
		Schedule:        req.Schedule,
		EventType:       eventType,
		ExitCode:        req.ExitCode,
		Output:          req.Output,
		StartedAt:       req.StartTime,
		FinishedAt:      req.EndTime,
	}
	select {
	case s.eventChan <- event:
	default:
		log.Printf("[WARN] event channel full, dropping event")
	}
}

// syncJobs syncs jobs from crontab file
func (s *Server) syncJobs(ctx context.Context) {
	// initial sync
	if err := s.loadJobsFromCrontab(); err != nil {
		log.Printf("[WARN] failed to load jobs from crontab: %v", err)
	}

	// if update interval is 0, don't start ticker
	if s.updateInterval <= 0 {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(s.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.loadJobsFromCrontab(); err != nil {
				log.Printf("[WARN] failed to load jobs from crontab: %v", err)
			}
		}
	}
}

// loadJobsFromDB loads existing job history from database
func (s *Server) loadJobsFromDB() {
	jobs, err := s.store.LoadJobs()
	if err != nil {
		log.Printf("[WARN] failed to load jobs from database: %v", err)
		return
	}

	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	for _, job := range jobs {
		// calculate next run from schedule if not set
		if job.NextRun.IsZero() {
			s.updateNextRun(&job)
		}

		s.jobs[job.ID] = job
		log.Printf("[DEBUG] loaded job from DB: %s (%s)", job.Command, job.LastStatus)
	}
}

// loadJobsFromCrontab loads jobs from crontab file
func (s *Server) loadJobsFromCrontab() error {
	if s.jobsProvider == nil {
		return fmt.Errorf("cannot load jobs: JobsProvider not configured")
	}

	specs, err := s.jobsProvider.List()
	if err != nil {
		return fmt.Errorf("failed to load job specifications from provider: %w", err)
	}

	s.jobsMu.Lock()
	// mark all jobs as potentially removed
	oldJobs := make(map[string]bool)
	for id := range s.jobs {
		oldJobs[id] = true
	}

	for idx, spec := range specs {
		id := HashCommand(spec.Command)

		// parse schedule for next run calculation
		schedule, err := s.parser.Parse(spec.Spec)
		if err != nil {
			log.Printf("[WARN] failed to parse schedule %q for command %q: %v", spec.Spec, spec.Command, err)
			continue
		}

		// update or create job
		if job, exists := s.jobs[id]; exists {
			job.Name = spec.Name
			job.Schedule = spec.Spec
			job.NextRun = schedule.Next(time.Now())
			job.UpdatedAt = time.Now()
			job.SortIndex = idx // update sort index in case order changed
			delete(oldJobs, id)
			s.jobs[id] = job
		} else {
			s.jobs[id] = persistence.JobInfo{
				ID:         id,
				Name:       spec.Name,
				Command:    spec.Command,
				Schedule:   spec.Spec,
				NextRun:    schedule.Next(time.Now()),
				LastStatus: enums.JobStatusIdle,
				Enabled:    true,
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				SortIndex:  idx,
			}
		}
	}

	// remove jobs that no longer exist
	for id := range oldJobs {
		delete(s.jobs, id)
	}
	s.jobsMu.Unlock()

	// persist to database (after releasing the lock)
	s.persistJobs()
	return nil
}

// persistJobs saves jobs to database
func (s *Server) persistJobs() {
	s.jobsMu.RLock()
	jobs := make([]persistence.JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		// determine status to save
		status := job.LastStatus
		if job.IsRunning {
			status = enums.JobStatusRunning
		}
		job.LastStatus = status
		jobs = append(jobs, job)
	}
	s.jobsMu.RUnlock()

	if err := s.store.SaveJobs(jobs); err != nil {
		log.Printf("[WARN] failed to persist jobs: %v", err)
	}
}

// processEvents processes job execution events
func (s *Server) processEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.eventChan:
			s.handleJobEvent(event)
			// persist jobs after status changes to ensure database stays in sync
			if event.EventType == enums.EventTypeCompleted || event.EventType == enums.EventTypeFailed {
				s.persistJobs()
			}
		}
	}
}

// activeRun is a run in progress, keyed in Server.active by its start time
type activeRun struct {
	StartedAt       time.Time
	ExecutedCommand string
	Output          func() string // output so far; nil when output capture is disabled
}

// activeRuns returns the job's runs in progress, newest first
func (s *Server) activeRuns(jobID string) []activeRun {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	res := make([]activeRun, 0, len(s.active[jobID]))
	for _, run := range s.active[jobID] {
		res = append(res, run)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].StartedAt.After(res[j].StartedAt) })
	return res
}

// activeRunAt returns the job's run in progress that started at start (unix micro)
func (s *Server) activeRunAt(jobID string, start int64) (activeRun, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	run, ok := s.active[jobID][start]
	return run, ok
}

// handleJobEvent handles a single job event. A job can have several runs in progress at once (no --dedup,
// or a manual run with an edited command), so each is tracked by its start time until it finishes.
func (s *Server) handleJobEvent(event JobEvent) {
	id := HashCommand(event.Command)
	finished := event.EventType == enums.EventTypeCompleted || event.EventType == enums.EventTypeFailed
	runStatus := enums.JobStatusSuccess
	if event.EventType == enums.EventTypeFailed {
		runStatus = enums.JobStatusFailed
	}

	// recorded before the run leaves the active set, so a poll of its output always finds it in one of the two.
	// the database write stays outside the lock to avoid blocking dashboard reads
	if finished {
		s.recordExecutionAndCleanup(id, event, runStatus)
	}

	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job, exists := s.jobs[id]
	if !exists {
		// create new job entry if it doesn't exist
		job = persistence.JobInfo{
			ID:         id,
			Command:    event.Command,
			Schedule:   event.Schedule,
			LastStatus: enums.JobStatusIdle,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Enabled:    true,
		}
		// calculate next run for new job
		s.updateNextRun(&job)
		if !job.NextRun.IsZero() {
			log.Printf("[DEBUG] calculated NextRun for new job %s: %v", event.Command, job.NextRun)
		} else {
			log.Printf("[WARN] failed to parse schedule %q for job %s", event.Schedule, event.Command)
		}
	}

	switch {
	case event.EventType == enums.EventTypeStarted:
		if s.active[id] == nil {
			s.active[id] = make(map[int64]activeRun)
		}
		s.active[id][event.StartedAt.UnixMicro()] = activeRun{StartedAt: event.StartedAt,
			ExecutedCommand: event.ExecutedCommand, Output: event.LiveOutput}
		job.IsRunning = true
		if event.StartedAt.After(job.LastRun) {
			job.LastRun = event.StartedAt
		}
		job.LastStatus = enums.JobStatusRunning
	case finished:
		delete(s.active[id], event.StartedAt.UnixMicro())
		if len(s.active[id]) == 0 {
			delete(s.active, id)
		}
		job.IsRunning = len(s.active[id]) > 0
		job.LastStatus = runStatus
		if job.IsRunning {
			job.LastStatus = enums.JobStatusRunning
		}
		job.LastExitCode = &event.ExitCode
		job.LastDuration = event.FinishedAt.Sub(event.StartedAt)
		if job.Enabled {
			s.updateNextRun(&job)
		}
	}
	job.UpdatedAt = time.Now()
	s.jobs[id] = job
}

// recordExecutionAndCleanup saves execution to database and cleans up old executions
func (s *Server) recordExecutionAndCleanup(jobID string, event JobEvent, status enums.JobStatus) {
	output := event.Output
	if s.logExecMaxLines == 0 {
		output = "" // skip storing output if disabled
	}

	if err := s.store.RecordExecution(request.RecordExecution{
		JobID:           jobID,
		StartedAt:       event.StartedAt,
		FinishedAt:      event.FinishedAt,
		Status:          status,
		ExitCode:        event.ExitCode,
		ExecutedCommand: event.ExecutedCommand,
		Output:          output,
	}); err != nil {
		log.Printf("[WARN] failed to save execution: %v", err)
	}

	// cleanup old executions if limit configured
	if s.logExecMaxHist > 0 {
		if err := s.store.CleanupOldExecutions(jobID, s.logExecMaxHist); err != nil {
			log.Printf("[WARN] failed to cleanup old executions: %v", err)
		}
	}
}

// updateNextRun updates the next run time for a job based on its schedule
func (s *Server) updateNextRun(job *persistence.JobInfo) {
	if job.Schedule == "" {
		return
	}
	if schedule, err := s.parser.Parse(job.Schedule); err == nil {
		job.NextRun = schedule.Next(time.Now())
	}
}

// sortJobs sorts jobs based on the sort mode
func (s *Server) sortJobs(jobs []persistence.JobInfo, sortMode enums.SortMode) {
	switch sortMode {
	case enums.SortModeLastrun:
		// sort by last run time, most recent first
		sort.Slice(jobs, func(i, j int) bool {
			// handle zero times - put them at the end
			if jobs[i].LastRun.IsZero() && jobs[j].LastRun.IsZero() {
				return jobs[i].SortIndex < jobs[j].SortIndex // maintain default order for unrun jobs
			}
			if jobs[i].LastRun.IsZero() {
				return false
			}
			if jobs[j].LastRun.IsZero() {
				return true
			}
			// if times are equal, use SortIndex for stable ordering
			if jobs[i].LastRun.Equal(jobs[j].LastRun) {
				return jobs[i].SortIndex < jobs[j].SortIndex
			}
			return jobs[i].LastRun.After(jobs[j].LastRun)
		})
	case enums.SortModeNextrun:
		// sort by next run time, soonest first
		sort.Slice(jobs, func(i, j int) bool {
			// handle zero times - put them at the end
			if jobs[i].NextRun.IsZero() && jobs[j].NextRun.IsZero() {
				return jobs[i].SortIndex < jobs[j].SortIndex
			}
			if jobs[i].NextRun.IsZero() {
				return false
			}
			if jobs[j].NextRun.IsZero() {
				return true
			}
			// if times are equal, use SortIndex for stable ordering
			if jobs[i].NextRun.Equal(jobs[j].NextRun) {
				return jobs[i].SortIndex < jobs[j].SortIndex
			}
			return jobs[i].NextRun.Before(jobs[j].NextRun)
		})
	default: // "default"
		// sort by original crontab order
		sort.Slice(jobs, func(i, j int) bool {
			return jobs[i].SortIndex < jobs[j].SortIndex
		})
	}
}

// filterJobs filters jobs based on the selected filter mode. Disabled jobs appear only under all and disabled,
// so the result filters answer what will run
func (s *Server) filterJobs(jobs []persistence.JobInfo, filterMode enums.FilterMode) []persistence.JobInfo {
	if filterMode == enums.FilterModeAll {
		return jobs
	}

	filtered := make([]persistence.JobInfo, 0, len(jobs))
	for _, job := range jobs {
		var match bool
		switch filterMode {
		case enums.FilterModeDisabled:
			match = !job.Enabled
		case enums.FilterModeRunning:
			match = job.Enabled && job.IsRunning
		case enums.FilterModeSuccess:
			match = job.Enabled && job.LastStatus == enums.JobStatusSuccess
		case enums.FilterModeFailed:
			match = job.Enabled && job.LastStatus == enums.JobStatusFailed
		case enums.FilterModeIdle:
			match = job.Enabled && job.LastStatus == enums.JobStatusIdle
		}
		if match {
			filtered = append(filtered, job)
		}
	}
	return filtered
}

// searchJobs filters jobs by search term, matching name or command case-insensitively
func (s *Server) searchJobs(jobs []persistence.JobInfo, searchTerm string) []persistence.JobInfo {
	if searchTerm == "" {
		return jobs
	}

	searchLower := strings.ToLower(searchTerm)
	filtered := make([]persistence.JobInfo, 0, len(jobs))
	for _, job := range jobs {
		if strings.Contains(strings.ToLower(job.Command), searchLower) || strings.Contains(strings.ToLower(job.Name), searchLower) {
			filtered = append(filtered, job)
		}
	}
	return filtered
}
