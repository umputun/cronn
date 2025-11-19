package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	theme := s.getTheme(r)
	stats := s.getJobsWithStats(s.getSortMode(r), s.getFilterMode(r), "")

	data := s.newTemplateData(r)
	data.Jobs = stats.jobs
	data.CurrentYear = time.Now().Year()
	data.Theme = theme
	data.RunningCount = stats.runningCount
	data.NextRunTime = stats.nextRunTime
	data.TotalCount = stats.totalCount
	data.AuthEnabled = s.passwordHash != ""
	data.Version = shortVersion(s.version)
	data.FullVersion = s.version

	s.render(w, "base.html", "base", data)
}

// getJobsWithStats retrieves jobs with calculated stats and applies filtering
func (s *Server) getJobsWithStats(sortMode enums.SortMode, filterMode enums.FilterMode, searchTerm string) jobsStats {
	s.jobsMu.RLock()
	allJobs := make([]persistence.JobInfo, 0, len(s.jobs))
	runningCount := 0
	var nearestNextRun *time.Time

	// first collect all jobs and calculate stats
	for _, job := range s.jobs {
		// work with a copy, recalculate next run times
		jobCopy := job
		s.updateNextRun(&jobCopy)
		allJobs = append(allJobs, jobCopy)

		// count running jobs
		if jobCopy.IsRunning {
			runningCount++
		}

		// find nearest next run
		if !jobCopy.NextRun.IsZero() {
			if nearestNextRun == nil || jobCopy.NextRun.Before(*nearestNextRun) {
				nearestNextRun = &jobCopy.NextRun
			}
		}
	}
	s.jobsMu.RUnlock()

	// store total count before filtering
	totalCount := len(allJobs)

	// apply search first, then status filtering
	jobs := s.searchJobs(allJobs, searchTerm)
	jobs = s.filterJobs(jobs, filterMode)

	// sort jobs based on selected mode
	s.sortJobs(jobs, sortMode)

	// format next run time
	nextRunTime := "-"
	if nearestNextRun != nil {
		nextRunTime = s.humanTime(*nearestNextRun)
	}

	return jobsStats{
		jobs:         jobs,
		runningCount: runningCount,
		nextRunTime:  nextRunTime,
		totalCount:   totalCount,
	}
}

// handleJobsPartial returns the jobs list partial for HTMX polling
func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(s.getSortMode(r), s.getFilterMode(r), searchTerm)

	data := s.newTemplateData(r)
	data.Jobs = stats.jobs
	data.RunningCount = stats.runningCount
	data.NextRunTime = stats.nextRunTime
	data.TotalCount = stats.totalCount
	data.IsOOB = true // enable OOB for stats updates

	// render jobs partial with stats updates
	if err := s.renderJobsWithStats(w, data); err != nil {
		log.Printf("[ERROR] failed to render jobs partial: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// renderJobsWithStats renders the jobs template with OOB stats updates
func (s *Server) renderJobsWithStats(w http.ResponseWriter, data TemplateData) error {
	// determine template name based on view mode
	tmplName := "jobs-cards"
	if data.ViewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// get the template
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		return fmt.Errorf("partials template not found")
	}

	// render the jobs template
	var jobsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&jobsHTML, tmplName, data); err != nil {
		return fmt.Errorf("failed to render jobs template: %w", err)
	}

	// render stats updates template
	var statsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&statsHTML, "stats-updates", data); err != nil {
		return fmt.Errorf("failed to render stats updates: %w", err)
	}

	// render view mode button if OOB (for multi-tab sync during polling)
	var buttonHTML bytes.Buffer
	if data.IsOOB {
		if err := tmpl.ExecuteTemplate(&buttonHTML, "view-mode-button", data); err != nil {
			return fmt.Errorf("failed to render view mode button: %w", err)
		}
	}

	// write response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(jobsHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write jobs HTML: %v", err)
	}
	if _, err := w.Write(statsHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write stats HTML: %v", err)
	}
	if buttonHTML.Len() > 0 {
		if _, err := w.Write(buttonHTML.Bytes()); err != nil {
			log.Printf("[ERROR] failed to write button HTML: %v", err)
		}
	}

	return nil
}

// handleViewModeToggle toggles between card and list view
func (s *Server) handleViewModeToggle(w http.ResponseWriter, r *http.Request) {
	currentMode := s.getViewMode(r)
	newMode := enums.ViewModeList
	if currentMode == enums.ViewModeList {
		newMode = enums.ViewModeCards
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "view-mode",
		Value:    newMode.String(),
		Path:     s.cookiePath(),
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// get sorted jobs for the new view mode
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(s.getSortMode(r), s.getFilterMode(r), searchTerm)

	// prepare template data
	data := s.newTemplateData(r)
	data.Jobs = stats.jobs
	data.ViewMode = newMode
	data.Theme = s.getTheme(r)
	data.TotalCount = stats.totalCount
	data.RunningCount = stats.runningCount
	data.NextRunTime = stats.nextRunTime
	data.CurrentYear = time.Now().Year()
	data.IsOOB = true

	// get the template
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		log.Printf("[WARN] partials template not found")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// render the entire jobs container with correct class
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// render the container with the new view mode class
	if err := tmpl.ExecuteTemplate(w, "jobs-container", data); err != nil {
		log.Printf("[WARN] Failed to render view mode toggle response: %v", err)
		return
	}

	// render stats updates as OOB
	if err := tmpl.ExecuteTemplate(w, "stats-updates", data); err != nil {
		log.Printf("[WARN] Failed to render stats updates: %v", err)
		return
	}

	// render view mode button as OOB
	if err := tmpl.ExecuteTemplate(w, "view-mode-button", data); err != nil {
		log.Printf("[WARN] Failed to render view mode button: %v", err)
		return
	}
}

// handleThemeToggle toggles the theme
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	currentTheme := s.getTheme(r)

	// toggle: light <-> dark
	var nextTheme enums.Theme
	if currentTheme == enums.ThemeLight {
		nextTheme = enums.ThemeDark
	} else {
		nextTheme = enums.ThemeLight
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "theme",
		Value:    nextTheme.String(),
		Path:     s.cookiePath(),
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// trigger full page refresh for theme change
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// handleSortToggle toggles between sort modes
func (s *Server) handleSortToggle(w http.ResponseWriter, r *http.Request) {
	currentMode := s.getSortMode(r)
	nextMode := s.cycleSortMode(currentMode)
	s.setSortCookie(w, nextMode)

	// get sorted jobs for the new mode
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(nextMode, s.getFilterMode(r), searchTerm)

	// prepare template data
	data := s.newTemplateData(r)
	data.Jobs = stats.jobs
	data.SortMode = nextMode
	data.RunningCount = stats.runningCount
	data.NextRunTime = stats.nextRunTime
	data.TotalCount = stats.totalCount

	// render jobs and send response with OOB updates
	if err := s.renderSortedJobs(w, data); err != nil {
		log.Printf("[ERROR] failed to render sorted jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// renderSortedJobs renders the jobs template with OOB updates for sort button
func (s *Server) renderSortedJobs(w http.ResponseWriter, data TemplateData) error {
	// determine template name based on view mode
	tmplName := "jobs-cards"
	if data.ViewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// get the template
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		return fmt.Errorf("partials template not found")
	}

	// render the jobs template
	var jobsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&jobsHTML, tmplName, data); err != nil {
		return fmt.Errorf("failed to render jobs template: %w", err)
	}

	// prepare data for sort button with OOB flag
	buttonData := data
	buttonData.IsOOB = true

	// render sort button with OOB
	var sortButtonHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&sortButtonHTML, "sort-button", buttonData); err != nil {
		return fmt.Errorf("failed to render sort button template: %w", err)
	}

	// write response with all OOB updates
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(jobsHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write jobs HTML: %v", err)
	}
	if _, err := w.Write(sortButtonHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write sort button HTML: %v", err)
	}

	return nil
}

// handleFilterToggle cycles through filter modes
func (s *Server) handleFilterToggle(w http.ResponseWriter, r *http.Request) {
	currentMode := s.getFilterMode(r)
	nextMode := s.cycleFilterMode(currentMode)
	s.setFilterCookie(w, nextMode)

	// get filtered jobs for the new mode
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(s.getSortMode(r), nextMode, searchTerm)

	// prepare template data
	data := s.newTemplateData(r)
	data.Jobs = stats.jobs
	data.FilterMode = nextMode
	data.RunningCount = stats.runningCount
	data.NextRunTime = stats.nextRunTime
	data.TotalCount = stats.totalCount

	// render jobs and send response with OOB updates
	if err := s.renderFilteredJobs(w, data); err != nil {
		log.Printf("[ERROR] failed to render filtered jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// renderFilteredJobs renders the jobs template with OOB updates
func (s *Server) renderFilteredJobs(w http.ResponseWriter, data TemplateData) error {
	// determine template name based on view mode
	tmplName := "jobs-cards"
	if data.ViewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// get the template
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		return fmt.Errorf("partials template not found")
	}

	// render the jobs template
	var jobsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&jobsHTML, tmplName, data); err != nil {
		return fmt.Errorf("failed to render jobs template: %w", err)
	}

	// prepare data for filter button with OOB flag
	buttonData := data
	buttonData.IsOOB = true

	// render filter button with OOB
	var filterButtonHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&filterButtonHTML, "filter-button", buttonData); err != nil {
		return fmt.Errorf("failed to render filter button template: %w", err)
	}

	// render stats updates with OOB
	statsData := TemplateData{
		RunningCount: data.RunningCount,
		NextRunTime:  data.NextRunTime,
		TotalCount:   data.TotalCount,
		IsOOB:        true,
	}
	var statsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&statsHTML, "stats-updates", statsData); err != nil {
		return fmt.Errorf("failed to render stats updates template: %w", err)
	}

	// write response with all OOB updates
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(jobsHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write jobs HTML: %v", err)
	}
	if _, err := w.Write(filterButtonHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write filter button HTML: %v", err)
	}
	if _, err := w.Write(statsHTML.Bytes()); err != nil {
		log.Printf("[ERROR] failed to write stats HTML: %v", err)
	}

	return nil
}

// handleSortModeChange changes the sort mode
func (s *Server) handleSortModeChange(w http.ResponseWriter, r *http.Request) {
	sortModeStr := r.FormValue("sort")

	// parse and validate sort mode
	sortMode, err := enums.ParseSortMode(sortModeStr)
	if err != nil {
		log.Printf("[WARN] invalid sort mode %q: %v", sortModeStr, err)
		sortMode = enums.SortModeDefault
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sort-mode",
		Value:    sortMode.String(),
		Path:     s.cookiePath(),
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// return the jobs partial with sorted jobs
	s.jobsMu.RLock()
	jobs := make([]persistence.JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		// work with a copy, recalculate next run times
		jobCopy := job
		s.updateNextRun(&jobCopy)
		jobs = append(jobs, jobCopy)
	}
	s.jobsMu.RUnlock()

	// sort jobs based on selected mode
	s.sortJobs(jobs, sortMode)

	data := s.newTemplateData(r)
	data.Jobs = jobs
	data.SortMode = sortMode

	tmplName := "jobs-cards"
	if data.ViewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// set header to tell HTMX to swap the jobs container
	w.Header().Set("HX-Retarget", "#jobs-container")
	w.Header().Set("HX-Reswap", "innerHTML")

	s.render(w, "partials/jobs.html", tmplName, data)
}

// handleRunJob handles manual job trigger requests
func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	// check if manual job execution is disabled
	if s.disableManual {
		http.Error(w, "Manual job execution is disabled", http.StatusForbidden)
		return
	}

	s.jobsMu.RLock()
	job, exists := s.jobs[jobID]
	s.jobsMu.RUnlock()

	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	if !job.Enabled {
		http.Error(w, "Job is disabled", http.StatusBadRequest)
		return
	}

	if job.IsRunning {
		http.Error(w, "Job already running", http.StatusConflict)
		return
	}

	// check if manual trigger channel is available
	if s.manualTrigger == nil {
		http.Error(w, "Manual trigger not configured", http.StatusServiceUnavailable)
		return
	}

	// parse form data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// get command from form, fallback to job command if not provided
	command := r.FormValue("command")
	if command == "" {
		command = job.Command
	}

	// check if command editing is disabled and command was modified
	if s.disableCommandEdit && command != job.Command {
		http.Error(w, "Command editing is disabled", http.StatusForbidden)
		return
	}

	// parse custom date if provided
	var customDate *time.Time
	dateStr := r.FormValue("date")
	if dateStr != "" {
		parsedDate, err := time.ParseInLocation("20060102", dateStr, time.Local)
		if err != nil {
			http.Error(w, "Invalid date format, expected YYYYMMDD", http.StatusBadRequest)
			return
		}
		customDate = &parsedDate
	}

	// use request context with timeout for sending
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	select {
	case <-ctx.Done():
		http.Error(w, "Request canceled", http.StatusRequestTimeout)
		return
	case s.manualTrigger <- service.ManualJobRequest{
		JobID:      jobID,
		Command:    command,
		Schedule:   job.Schedule,
		CustomDate: customDate,
	}:
		log.Printf("[INFO] manual trigger sent for job %s: %s", jobID, command)
		w.Header().Set("HX-Trigger", "refresh-jobs")
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("Job triggered")); err != nil {
			log.Printf("[ERROR] failed to write response: %v", err)
		}
	default:
		// non-blocking send failed, channel full
		http.Error(w, "System busy, too many manual triggers", http.StatusServiceUnavailable)
	}
}

// handleJobModal handles job details modal requests
func (s *Server) handleJobModal(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	s.jobsMu.RLock()
	job, exists := s.jobs[jobID]
	s.jobsMu.RUnlock()

	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// create a copy and update next run time
	jobCopy := job
	s.updateNextRun(&jobCopy)

	s.render(w, "partials/jobs.html", "job-modal", jobCopy)
}

// handleJobHistory handles job execution history modal requests
func (s *Server) handleJobHistory(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	s.jobsMu.RLock()
	job, exists := s.jobs[jobID]
	s.jobsMu.RUnlock()

	if !exists {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	// get execution history from database
	executions, err := s.store.GetExecutions(jobID, 50)
	if err != nil {
		log.Printf("[ERROR] failed to get executions for job %s: %v", jobID, err)
		http.Error(w, "Failed to load execution history", http.StatusInternalServerError)
		return
	}

	// prepare data for template
	data := struct {
		Job        persistence.JobInfo
		Executions []persistence.ExecutionInfo
	}{
		Job:        job,
		Executions: executions,
	}

	s.render(w, "partials/jobs.html", "history-modal", data)
}

// handleSettingsModal handles settings/about modal requests
func (s *Server) handleSettingsModal(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "partials/jobs.html", "settings-modal", s.settingsInfo)
}

// handleExecutionLogs handles requests for execution log output
func (s *Server) handleExecutionLogs(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	execIDStr := r.PathValue("exec_id")

	if jobID == "" || execIDStr == "" {
		http.Error(w, "Job ID and Execution ID required", http.StatusBadRequest)
		return
	}

	// parse execution ID
	execID, err := strconv.Atoi(execIDStr)
	if err != nil {
		http.Error(w, "Invalid execution ID", http.StatusBadRequest)
		return
	}

	// get execution from database
	execution, err := s.store.GetExecutionByID(execID)
	if err != nil {
		log.Printf("[ERROR] failed to get execution %d: %v", execID, err)
		http.Error(w, "Execution not found", http.StatusNotFound)
		return
	}

	// verify execution belongs to the requested job
	if execution.JobID != jobID {
		http.Error(w, "Execution does not belong to this job", http.StatusForbidden)
		return
	}

	// prepare data for template
	data := struct {
		Execution persistence.ExecutionInfo
		JobID     string
	}{
		Execution: execution,
		JobID:     jobID,
	}

	s.render(w, "partials/jobs.html", "logs-modal", data)
}
