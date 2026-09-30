package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// jobsQuery selects which jobs the dashboard shows and in what order
type jobsQuery struct {
	sort   enums.SortMode
	filter enums.FilterMode
	search string
}

// inspectorData is the template data of the job inspector panel
type inspectorData struct {
	Job            persistence.JobInfo
	Runs           []persistence.ExecutionInfo
	SelectedRun    int
	ManualDisabled bool
	Output         *runOutputData // output of the selected run, nil when the job has no runs
}

// runOutputData is the template data of one run's output in the inspector
type runOutputData struct {
	JobID        string
	Run          persistence.ExecutionInfo
	Command      string
	CommandLabel string
}

// runForm is the template data of the manual run dialog
type runForm struct {
	JobID           string
	Name            string
	Command         string
	Date            string
	HasTemplates    bool
	CommandEditable bool
	Error           string
}

// filterTab is one filter tab of the dashboard
type filterTab struct {
	Mode   string
	Label  string
	Dot    string
	Count  int
	Active bool
}

// runError is a rejected manual run with the status code and text curl callers get
type runError struct {
	status int
	msg    string
}

// inspectorRuns is how many recent runs the inspector lists
const inspectorRuns = 50

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	q := jobsQuery{sort: s.getSortMode(r), filter: s.getFilterMode(r)}
	data := s.jobsTemplateData(r, q)
	data.CurrentYear = time.Now().Year()
	data.Theme = s.getTheme(r)
	data.AuthEnabled = s.passwordHash != ""
	data.Version = shortVersion(s.version)
	data.FullVersion = s.version
	s.render(w, "base.html", "base", data)
}

// getJobsWithStats retrieves jobs with counts and applies search, filter and sort
func (s *Server) getJobsWithStats(q jobsQuery) jobsStats {
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

	stats := jobsStats{totalCount: len(allJobs)}
	for _, job := range allJobs {
		if !job.Enabled {
			stats.disabledCount++
			continue
		}
		if job.IsRunning {
			stats.runningCount++
		}
		switch job.LastStatus {
		case enums.JobStatusSuccess:
			stats.successCount++
		case enums.JobStatusFailed:
			stats.failedCount++
			stats.failing = append(stats.failing, job)
		case enums.JobStatusIdle:
			stats.idleCount++
		}
	}
	s.sortJobs(stats.failing, enums.SortModeLastrun)

	searched := s.searchJobs(allJobs, q.search)
	stats.jobs = s.filterJobs(searched, q.filter)
	if len(stats.jobs) == 0 && q.search != "" {
		stats.hiddenBySearch = len(s.filterJobs(allJobs, q.filter))
	}
	s.sortJobs(stats.jobs, q.sort)
	return stats
}

// jobsTemplateData builds template data for the job list from the query and the request's selection state
func (s *Server) jobsTemplateData(r *http.Request, q jobsQuery) TemplateData {
	stats := s.getJobsWithStats(q)
	data := s.newTemplateData(r)
	data.SortMode = q.sort
	data.FilterMode = q.filter
	data.Search = q.search
	data.Jobs = stats.jobs
	data.Failing = stats.failing
	data.TotalCount = stats.totalCount
	data.RunningCount = stats.runningCount
	data.SuccessCount = stats.successCount
	data.FailedCount = stats.failedCount
	data.IdleCount = stats.idleCount
	data.DisabledCount = stats.disabledCount
	data.MatchCount = len(stats.jobs)
	data.HiddenBySearch = stats.hiddenBySearch
	data.SelectedJob = r.FormValue("selected-job")
	data.Tabs = []filterTab{
		{Mode: "all", Label: "All", Count: stats.totalCount},
		{Mode: "failed", Label: "Failed", Dot: "fail", Count: stats.failedCount},
		{Mode: "running", Label: "Running", Dot: "running", Count: stats.runningCount},
		{Mode: "success", Label: "Succeeded", Dot: "ok", Count: stats.successCount},
		{Mode: "idle", Label: "Never ran", Dot: "never", Count: stats.idleCount},
		{Mode: "disabled", Label: "Disabled", Dot: "off", Count: stats.disabledCount},
	}
	for i := range data.Tabs {
		data.Tabs[i].Active = data.Tabs[i].Mode == q.filter.String()
	}
	return data
}

// renderJobs is the one render path for the job table. It writes the table and out-of-band updates for the
// counts and the failing-jobs alert; a filter or sort change also re-renders the controls with the new mode
func (s *Server) renderJobs(w http.ResponseWriter, r *http.Request, q jobsQuery) error {
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		return fmt.Errorf("partials template not found")
	}
	data := s.jobsTemplateData(r, q)

	parts := []string{"jobs-table", "failing-alert-oob", "jobs-counts-oob"}
	if r.Method == http.MethodPost {
		parts[2] = "jobs-controls-oob" // the controls carry the counts and the newly active tab and sort
	}
	if r.FormValue("reset-search") != "" {
		parts = append(parts, "search-input-oob")
	}

	var buf bytes.Buffer
	for _, name := range parts {
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			return fmt.Errorf("failed to render %s: %w", name, err)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write jobs response: %v", err)
	}
	return nil
}

// handleJobsPartial returns the job table for polling and search
func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	q := jobsQuery{sort: s.getSortMode(r), filter: s.getFilterMode(r), search: r.FormValue("search")}
	if err := s.renderJobs(w, r, q); err != nil {
		log.Printf("[ERROR] failed to render jobs partial: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// handleThemeToggle toggles the theme
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	nextTheme := enums.ThemeLight
	if s.getTheme(r) == enums.ThemeLight {
		nextTheme = enums.ThemeDark
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

// handleFilterModeChange sets the filter mode chosen from the filter tabs
func (s *Server) handleFilterModeChange(w http.ResponseWriter, r *http.Request) {
	filterMode, err := enums.ParseFilterMode(r.FormValue("filter"))
	if err != nil {
		log.Printf("[WARN] invalid filter mode %q: %v", r.FormValue("filter"), err)
		filterMode = enums.FilterModeAll
	}
	s.setFilterCookie(w, filterMode)

	q := jobsQuery{sort: s.getSortMode(r), filter: filterMode, search: r.FormValue("search")}
	if err := s.renderJobs(w, r, q); err != nil {
		log.Printf("[ERROR] failed to render filtered jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// handleSortModeChange sets the sort mode chosen from the sort select
func (s *Server) handleSortModeChange(w http.ResponseWriter, r *http.Request) {
	sortMode, err := enums.ParseSortMode(r.FormValue("sort"))
	if err != nil {
		log.Printf("[WARN] invalid sort mode %q: %v", r.FormValue("sort"), err)
		sortMode = enums.SortModeDefault
	}
	s.setSortCookie(w, sortMode)

	q := jobsQuery{sort: sortMode, filter: s.getFilterMode(r), search: r.FormValue("search")}
	if err := s.renderJobs(w, r, q); err != nil {
		log.Printf("[ERROR] failed to render sorted jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// handleRunForm renders the manual run dialog for a job
func (s *Server) handleRunForm(w http.ResponseWriter, r *http.Request) {
	if s.disableManual {
		http.Error(w, "Manual job execution is disabled", http.StatusForbidden)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	s.render(w, "partials/jobs.html", "run-form", s.newRunForm(job, job.Command, ""))
}

// newRunForm builds the run dialog data for a job with the given command and date values
func (s *Server) newRunForm(job persistence.JobInfo, command, date string) runForm {
	return runForm{
		JobID:           job.ID,
		Name:            job.Name,
		Command:         command,
		Date:            date,
		HasTemplates:    strings.Contains(job.Command, "{{") || strings.Contains(job.Command, "[["),
		CommandEditable: !s.disableCommandEdit,
	}
}

// handleRunJob handles manual job trigger requests. htmx requests get the run dialog back (with the error on
// a rejection, empty with a toast on acceptance); other callers get the status code and plain text
func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	if s.disableManual {
		http.Error(w, "Manual job execution is disabled", http.StatusForbidden)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		s.writeRunError(w, r, runError{status: http.StatusNotFound, msg: "Job not found"})
		return
	}

	req, rerr := s.checkRun(r, job)
	if rerr != nil {
		s.writeRunError(w, r, *rerr)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	select {
	case <-ctx.Done():
		s.writeRunError(w, r, runError{status: http.StatusRequestTimeout, msg: "Request canceled"})
		return
	case s.manualTrigger <- req:
	default:
		s.writeRunError(w, r, runError{status: http.StatusServiceUnavailable, msg: "System busy, too many manual triggers"})
		return
	}

	log.Printf("[INFO] manual trigger sent for job %s: %s", job.ID, req.Command)
	if r.Header.Get("HX-Request") != "true" {
		w.Header().Set("HX-Trigger", "refresh-jobs")
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("Job triggered")); err != nil {
			log.Printf("[ERROR] failed to write response: %v", err)
		}
		return
	}

	w.Header().Set("HX-Trigger-After-Swap", "refresh-jobs")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	tmpl := s.templates["partials/jobs.html"]
	if err := tmpl.ExecuteTemplate(w, "run-accepted-oob", job); err != nil {
		log.Printf("[ERROR] failed to render run toast: %v", err)
	}
}

// checkRun validates a manual run request and builds the scheduler request.
// The command and date are read first so every rejection can show them back in the form
func (s *Server) checkRun(r *http.Request, job persistence.JobInfo) (service.ManualJobRequest, *runError) {
	if err := r.ParseForm(); err != nil {
		return service.ManualJobRequest{}, &runError{status: http.StatusBadRequest, msg: "Invalid form data"}
	}
	command := r.FormValue("command")
	if command == "" {
		command = job.Command
	}
	dateStr := r.FormValue("date")

	switch {
	case !job.Enabled:
		return service.ManualJobRequest{}, &runError{status: http.StatusBadRequest, msg: "Job is disabled"}
	case job.IsRunning:
		return service.ManualJobRequest{}, &runError{status: http.StatusConflict, msg: "Job already running"}
	case s.manualTrigger == nil:
		return service.ManualJobRequest{}, &runError{status: http.StatusServiceUnavailable, msg: "Manual trigger not configured"}
	case s.disableCommandEdit && command != job.Command:
		return service.ManualJobRequest{}, &runError{status: http.StatusForbidden, msg: "Command editing is disabled"}
	}

	req := service.ManualJobRequest{JobID: job.ID, Command: command, Schedule: job.Schedule}
	if dateStr != "" {
		parsed, err := time.ParseInLocation("20060102", dateStr, time.Local)
		if err != nil {
			return service.ManualJobRequest{}, &runError{status: http.StatusBadRequest,
				msg: "Invalid date format, expected YYYYMMDD"}
		}
		req.CustomDate = &parsed
	}
	return req, nil
}

// writeRunError answers a rejected run: htmx gets the dialog re-rendered with the error and the submitted
// values (200, so htmx swaps it), other callers get the status code and text as before
func (s *Server) writeRunError(w http.ResponseWriter, r *http.Request, e runError) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, e.msg, e.status)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		// the definition is gone, so the form keeps the request's own id and values
		job = persistence.JobInfo{ID: r.PathValue("id"), Command: r.FormValue("command")}
		e.msg = "Job no longer exists in the crontab"
	}
	form := s.newRunForm(job, r.FormValue("command"), r.FormValue("date"))
	form.HasTemplates = form.HasTemplates || form.Date != ""
	if form.Command == "" {
		form.Command = job.Command
	}
	form.Error = e.msg
	s.render(w, "partials/jobs.html", "run-form", form)
}

// handleToggleJob toggles the enabled state of a job
func (s *Server) handleToggleJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		http.Error(w, "Job ID required", http.StatusBadRequest)
		return
	}

	s.jobsMu.Lock()
	job, exists := s.jobs[jobID]
	if !exists {
		s.jobsMu.Unlock()
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	job.Enabled = !job.Enabled
	job.UpdatedAt = time.Now()
	s.jobs[jobID] = job
	s.jobsMu.Unlock()

	s.persistJobs()
	w.Header().Set("HX-Trigger", "refresh-jobs")
	w.WriteHeader(http.StatusOK)
}

// handleInspector renders the job inspector. part=live renders only the refreshable part (status, runs) for
// the inspector's own polling; otherwise the whole panel plus the selection inputs and the latest run's output
func (s *Server) handleInspector(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok && r.Header.Get("HX-Request") == "true" {
		// a crontab sync removed the job: close the panel, since htmx would keep showing it on a 404
		w.Header().Set("HX-Retarget", "#inspector")
		w.Header().Set("HX-Reswap", "innerHTML")
		s.render(w, "partials/jobs.html", "inspector-closed", nil)
		return
	}
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	if job.Enabled {
		s.updateNextRun(&job)
	}

	runs, err := s.store.GetExecutions(job.ID, inspectorRuns)
	if err != nil {
		log.Printf("[ERROR] failed to get executions for job %s: %v", job.ID, err)
		http.Error(w, "Failed to load execution history", http.StatusInternalServerError)
		return
	}
	data := inspectorData{Job: job, Runs: runs, ManualDisabled: s.disableManual}

	if r.FormValue("part") == "live" {
		data.SelectedRun, _ = strconv.Atoi(r.FormValue("selected-run"))
		s.render(w, "partials/jobs.html", "inspector-live", data)
		return
	}

	if len(runs) > 0 {
		data.SelectedRun = runs[0].ID
		out := s.newRunOutput(job, runs[0])
		data.Output = &out
	}
	tmpl := s.templates["partials/jobs.html"]
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "inspector", data); err != nil {
		log.Printf("[ERROR] failed to render inspector: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger-After-Swap", "refresh-jobs")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write inspector: %v", err)
	}
}

// handleRunOutput renders one run's output in the inspector, with its command and exit code
func (s *Server) handleRunOutput(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	execID, err := strconv.Atoi(r.PathValue("exec_id"))
	if err != nil {
		http.Error(w, "Invalid execution ID", http.StatusBadRequest)
		return
	}
	job, ok := s.jobByID(jobID)
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	run, err := s.store.GetExecutionByID(execID)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		log.Printf("[ERROR] failed to get execution %d: %v", execID, err)
		http.Error(w, "Failed to load execution", http.StatusInternalServerError)
		return
	}
	if err != nil || run.JobID != jobID {
		http.Error(w, "Execution not found", http.StatusNotFound)
		return
	}

	w.Header().Set("HX-Trigger-After-Swap", "refresh-inspector")
	s.render(w, "partials/jobs.html", "run-output-response", s.newRunOutput(job, run))
}

// newRunOutput pairs a run with the command it ran: the executed command of a manual run, the job command
// otherwise, since scheduled runs do not record their resolved command
func (s *Server) newRunOutput(job persistence.JobInfo, run persistence.ExecutionInfo) runOutputData {
	data := runOutputData{JobID: job.ID, Run: run, Command: job.Command, CommandLabel: "Job command"}
	if run.ExecutedCommand != "" {
		data.Command, data.CommandLabel = run.ExecutedCommand, "Executed"
	}
	return data
}

// handleSettingsModal renders the settings and about dialog
func (s *Server) handleSettingsModal(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "partials/jobs.html", "settings-modal", s.settingsInfo)
}

// jobByID returns a copy of the job with the given id
func (s *Server) jobByID(id string) (persistence.JobInfo, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}
