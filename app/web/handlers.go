package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	sort    enums.SortMode
	filter  enums.FilterMode
	search  string
	reverse bool
}

// values returns the list state as URL query values, so links can carry it between pages
func (q jobsQuery) values() url.Values {
	v := url.Values{}
	v.Set("filter", q.filter.String())
	v.Set("sort", q.sort.String())
	v.Set("rev", "0")
	if q.reverse {
		v.Set("rev", "1")
	}
	if q.search != "" {
		v.Set("search", q.search)
	}
	return v
}

// indexRow is one job of the index grid
type indexRow struct {
	Job   persistence.JobInfo
	Strip []stripBar
	Link  string // job page URL carrying the list state
}

// filterTab is one filter pill of the top bar
type filterTab struct {
	Mode   string
	Label  string
	Dot    string
	Count  int
	Active bool
	Href   string // index link with this filter, for the pills on a job page
}

// sortCol is one sortable column. A click on the sorted column flips it, a click on another column sorts
// it nearest to now first
type sortCol struct {
	Mode    enums.SortMode
	Label   string // grid header text
	Menu    string // filter menu text
	On      bool
	Reverse bool   // sorted and flipped
	NextRev string // rev value a click sends
	Href    string // index link with this sort, for the menu on a job page
}

// runConfirm is the template data of the run confirmation dialog
type runConfirm struct {
	JobID   string
	Name    string
	Command string
	Date    string
	Edited  bool
	Error   string
	List    url.Values // list state, echoed so the page the run lands on keeps it
}

// stopForm is the template data of the stop confirmation dialog, bound to one run by its start time
type stopForm struct {
	JobID     string
	Name      string
	Command   string
	Start     int64
	StartedAt time.Time
}

// runError is a rejected manual run with the status code and text curl callers get
type runError struct {
	status int
	msg    string
}

// indexStripSize is how many recent runs an index row shows
const indexStripSize = 20

// handleIndex renders the job list page
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	q := s.listQuery(r)
	stats := s.getJobsWithStats(q)
	data := s.listData(r, q, stats)
	data.Rows = s.indexRows(stats.jobs, q)
	s.render(w, "index", "layout", data)
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
		case enums.JobStatusIdle:
			stats.idleCount++
		}
	}

	searched := s.searchJobs(allJobs, q.search)
	stats.jobs = s.filterJobs(searched, q.filter)
	if len(stats.jobs) == 0 && q.search != "" {
		stats.hiddenBySearch = len(s.filterJobs(allJobs, q.filter))
	}
	s.sortJobs(stats.jobs, q.sort, q.reverse)
	return stats
}

// listData builds the template data every page shares: the top bar, the list state, the counts and the
// pills, menus and sort columns built from them
func (s *Server) listData(r *http.Request, q jobsQuery, stats jobsStats) TemplateData {
	data := s.newTemplateData(r)
	data.Theme = s.getTheme(r)
	data.AuthEnabled = s.passwordHash != ""
	data.SortMode = q.sort
	data.FilterMode = q.filter
	data.Reverse = q.reverse
	data.Search = q.search
	data.IndexLink = s.indexLink(q)
	data.TotalCount = stats.totalCount
	data.RunningCount = stats.runningCount
	data.SuccessCount = stats.successCount
	data.FailedCount = stats.failedCount
	data.IdleCount = stats.idleCount
	data.DisabledCount = stats.disabledCount
	data.MatchCount = len(stats.jobs)
	data.HiddenBySearch = stats.hiddenBySearch
	data.Tabs = []filterTab{
		{Mode: "all", Label: "All", Count: stats.totalCount},
		{Mode: "failed", Label: "Failing", Dot: "fail", Count: stats.failedCount},
		{Mode: "running", Label: "Running", Dot: "running", Count: stats.runningCount},
		{Mode: "success", Label: "OK", Dot: "ok", Count: stats.successCount},
		{Mode: "idle", Label: "Never", Dot: "never", Count: stats.idleCount},
		{Mode: "disabled", Label: "Disabled", Dot: "off", Count: stats.disabledCount},
	}
	for i := range data.Tabs {
		tab := &data.Tabs[i]
		tab.Active = tab.Mode == q.filter.String()
		mode, err := enums.ParseFilterMode(tab.Mode)
		if err != nil {
			continue
		}
		link := q
		link.filter = mode
		tab.Href = s.indexLink(link)
	}
	data.SortCols = []sortCol{
		{Mode: enums.SortModeDefault, Label: "Job", Menu: "Crontab order"},
		{Mode: enums.SortModeLastrun, Label: "Last run", Menu: "Last run"},
		{Mode: enums.SortModeNextrun, Label: "Next", Menu: "Next run"},
	}
	for i := range data.SortCols {
		col := &data.SortCols[i]
		col.On = col.Mode == q.sort
		col.Reverse = col.On && q.reverse
		link := q
		link.sort, link.reverse = col.Mode, col.On && !q.reverse
		col.NextRev = "0"
		if link.reverse {
			col.NextRev = "1"
		}
		col.Href = s.indexLink(link)
	}
	return data
}

// indexRows pairs each job with its run history strip and its page link. A failed history read leaves the
// strips empty rather than failing the list
func (s *Server) indexRows(jobs []persistence.JobInfo, q jobsQuery) []indexRow {
	runs, err := s.store.RecentRuns(indexStripSize)
	if err != nil {
		log.Printf("[WARN] failed to load recent runs: %v", err)
	}
	rows := make([]indexRow, 0, len(jobs))
	for _, job := range jobs {
		bars := s.stripBars(stripReq{Job: job, Runs: runs[job.ID], Size: indexStripSize}, q)
		rows = append(rows, indexRow{Job: job, Strip: bars, Link: s.jobLink(job.ID, q)})
	}
	return rows
}

// indexLink is the index URL with the list state
func (s *Server) indexLink(q jobsQuery) string {
	return s.url("/") + "?" + q.values().Encode()
}

// jobLink is a job page URL with the list state
func (s *Server) jobLink(id string, q jobsQuery) string {
	return s.url("/jobs/"+id) + "?" + q.values().Encode()
}

// renderJobs is the one render path for the index rows. A poll gets the rows and the counts out of band; a
// filter or sort change also re-renders the grid header, the list state, the pills and the filter menu
func (s *Server) renderJobs(w http.ResponseWriter, r *http.Request, q jobsQuery) error {
	tmpl, ok := s.templates["partials"]
	if !ok {
		return fmt.Errorf("partials template not found")
	}
	stats := s.getJobsWithStats(q)
	data := s.listData(r, q, stats)
	data.Rows = s.indexRows(stats.jobs, q)

	parts := []string{"grid-rows", "counts-oob"}
	if r.Method == http.MethodPost {
		parts = []string{"grid-rows", "grid-head-oob", "list-state-oob", "pills-oob", "fdd-oob"}
	}
	if r.FormValue("reset-search") != "" {
		parts = append(parts, "search-oob")
	}

	var buf bytes.Buffer
	for _, name := range parts {
		if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
			return fmt.Errorf("failed to render %s: %w", name, err)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		// a reload, the theme toggle's refresh and a copied link all read the list state from the address
		w.Header().Set("HX-Replace-Url", s.indexLink(q))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write jobs response: %v", err)
	}
	return nil
}

// handleJobsPartial returns the index rows for polling and search
func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	if err := s.renderJobs(w, r, s.listQuery(r)); err != nil {
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

// handleFilterModeChange sets the filter mode chosen from the pills or the filter menu
func (s *Server) handleFilterModeChange(w http.ResponseWriter, r *http.Request) {
	filterMode, err := enums.ParseFilterMode(r.FormValue("filter"))
	if err != nil {
		log.Printf("[WARN] invalid filter mode %q: %v", r.FormValue("filter"), err)
		filterMode = enums.FilterModeAll
	}
	s.setFilterCookie(w, filterMode)

	q := s.listQuery(r)
	q.filter = filterMode
	if err := s.renderJobs(w, r, q); err != nil {
		log.Printf("[ERROR] failed to render filtered jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// handleSortModeChange sets the sort column and direction chosen from the grid header or the filter menu
func (s *Server) handleSortModeChange(w http.ResponseWriter, r *http.Request) {
	sortMode, err := enums.ParseSortMode(r.FormValue("sort"))
	if err != nil {
		log.Printf("[WARN] invalid sort mode %q: %v", r.FormValue("sort"), err)
		sortMode = enums.SortModeDefault
	}
	q := s.listQuery(r)
	q.sort = sortMode
	s.setSortCookie(w, sortMode, q.reverse)

	if err := s.renderJobs(w, r, q); err != nil {
		log.Printf("[ERROR] failed to render sorted jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// handleRunConfirm renders the confirmation of a manual run with the exact command and date it will use.
// It is a POST so an edited command stays out of URLs, which request logs and proxies record
func (s *Server) handleRunConfirm(w http.ResponseWriter, r *http.Request) {
	if s.disableManual {
		http.Error(w, "Manual job execution is disabled", http.StatusForbidden)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	// Enter in the command field submits the run bar of a disabled or running job too, and a row's Run can be
	// stale: nothing opens, and the page refreshes to show why
	if !job.Enabled || job.IsRunning {
		w.Header().Set("HX-Trigger", "refresh-jobs")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	form := s.newRunConfirm(job, r)
	if s.disableCommandEdit && form.Edited {
		http.Error(w, "Command editing is disabled", http.StatusForbidden)
		return
	}
	s.render(w, "partials", "run-confirm", form)
}

// newRunConfirm builds the confirmation from the submitted command and date; a blank command means the
// job's own
func (s *Server) newRunConfirm(job persistence.JobInfo, r *http.Request) runConfirm {
	command := r.FormValue("command")
	if strings.TrimSpace(command) == "" {
		command = job.Command
	}
	return runConfirm{JobID: job.ID, Name: job.Name, Command: command, Date: strings.TrimSpace(r.FormValue("date")),
		Edited: command != job.Command, List: s.listQuery(r).values()}
}

// handleStopForm renders the stop confirmation for the run that started at start, or for the job's newest
// run in progress when start is absent. A run that has already ended closes the dialog and refreshes the page.
func (s *Server) handleStopForm(w http.ResponseWriter, r *http.Request) {
	if s.disableManual || s.runStopper == nil {
		http.Error(w, "Stopping runs is disabled", http.StatusForbidden)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		s.writeStopGone(w)
		return
	}
	active := s.activeRuns(job.ID)
	run, found := activeRun{}, len(active) > 0
	if found {
		run = active[0]
	}
	if raw := r.FormValue("start"); raw != "" {
		start, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			http.Error(w, "Invalid run start", http.StatusBadRequest)
			return
		}
		run, found = s.activeRunAt(job.ID, start)
	}
	if !found {
		s.writeStopGone(w)
		return
	}
	command := job.Command
	if run.ExecutedCommand != "" {
		command = run.ExecutedCommand
	}
	s.render(w, "partials", "stop-form", stopForm{JobID: job.ID, Name: job.Name, Command: command,
		Start: run.StartedAt.UnixMicro(), StartedAt: run.StartedAt})
}

// handleStopRun stops the run that started at the posted start time. htmx requests get an empty dialog slot,
// with a toast when the run was stopped; other callers get 202, or 404 when that run is not in progress
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	if s.disableManual || s.runStopper == nil {
		http.Error(w, "Stopping runs is disabled", http.StatusForbidden)
		return
	}
	start, err := strconv.ParseInt(r.FormValue("start"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid run start", http.StatusBadRequest)
		return
	}
	htmx := r.Header.Get("HX-Request") == "true"
	job, ok := s.jobByID(r.PathValue("id"))
	stopped := ok && s.runStopper.StopRun(job.Command, start)
	if stopped {
		log.Printf("[INFO] stop requested for job %s, run started %s", job.ID, time.UnixMicro(start).Format(time.RFC3339))
	}
	if !htmx {
		if !stopped {
			http.Error(w, "Run not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("Stop requested")); err != nil {
			log.Printf("[ERROR] failed to write response: %v", err)
		}
		return
	}
	if !stopped {
		s.writeStopGone(w)
		return
	}
	w.Header().Set("HX-Trigger-After-Swap", "refresh-jobs")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates["partials"].ExecuteTemplate(w, "stop-accepted-oob", job); err != nil {
		log.Printf("[ERROR] failed to render stop toast: %v", err)
	}
}

// writeStopGone empties the dialog slot and refreshes the page when the run to stop is no longer in progress
func (s *Server) writeStopGone(w http.ResponseWriter) {
	w.Header().Set("HX-Trigger-After-Swap", "refresh-jobs")
	w.WriteHeader(http.StatusOK)
}

// handleRunJob handles manual job trigger requests. An accepted htmx request is redirected to the job page,
// which waits for the run that started after the trigger; a rejected one gets the confirmation back with
// the error. Other callers get the status code and plain text
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
	after := time.Now().UnixMicro() // the run starts after the send, so its start time is at least this
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

	v := s.listQuery(r).values()
	v.Set("after", strconv.FormatInt(after, 10))
	w.Header().Set("HX-Redirect", s.url("/jobs/"+job.ID)+"?"+v.Encode())
	w.WriteHeader(http.StatusAccepted)
}

// checkRun validates a manual run request and builds the scheduler request
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

// writeRunError answers a rejected run: htmx gets the confirmation re-rendered with the error and the
// submitted values (200, so htmx swaps it), other callers get the status code and text
func (s *Server) writeRunError(w http.ResponseWriter, r *http.Request, e runError) {
	if r.Header.Get("HX-Request") != "true" {
		http.Error(w, e.msg, e.status)
		return
	}
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		// the definition is gone, so the dialog keeps the request's own id and command
		job = persistence.JobInfo{ID: r.PathValue("id"), Command: r.FormValue("command")}
		e.msg = "Job no longer exists in the crontab"
	}
	form := s.newRunConfirm(job, r)
	form.Error = e.msg
	s.render(w, "partials", "run-confirm", form)
}

// handleToggleJob toggles the enabled state of a job. htmx requests get a toast; every caller gets the
// page refreshed through refresh-jobs
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
	if r.Header.Get("HX-Request") != "true" {
		w.Header().Set("HX-Trigger", "refresh-jobs")
		w.WriteHeader(http.StatusOK)
		return
	}
	// the switch sits in the polled facts, so the refresh rides After-Swap, which htmx sends from the body
	// when the switch was replaced before the response came
	w.Header().Set("HX-Trigger-After-Swap", "refresh-jobs")
	s.render(w, "partials", "toggle-toast", job)
}

// handleSettingsModal renders the settings and about dialog
func (s *Server) handleSettingsModal(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "partials", "settings-modal", s.settingsInfo)
}

// jobByID returns a copy of the job with the given id
func (s *Server) jobByID(id string) (persistence.JobInfo, bool) {
	s.jobsMu.RLock()
	defer s.jobsMu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}
