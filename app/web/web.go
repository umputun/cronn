package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"
	"github.com/go-pkgz/routegroup"
	"github.com/robfig/cron/v3"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

//go:embed templates/*.html templates/partials/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Persistence defines storage operations for job management
type Persistence interface {
	Initialize() error
	LoadJobs() ([]persistence.JobInfo, error)
	SaveJobs(jobs []persistence.JobInfo) error
	RecordExecution(jobID string, started, finished time.Time, status enums.JobStatus, exitCode int) error
	Close() error
}

// Server represents the web server
type Server struct {
	store          Persistence
	templates      map[string]*template.Template
	crontabFile    string
	jobsMu         sync.RWMutex
	jobs           map[string]persistence.JobInfo // job id -> job info
	parser         cron.Parser
	eventChan      chan JobEvent
	updateInterval time.Duration
	version        string
}

// JobEvent represents job execution events
type JobEvent struct {
	JobID      string
	Command    string
	Schedule   string
	EventType  enums.EventType
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}

// TemplateData holds data for templates
type TemplateData struct {
	Jobs        []persistence.JobInfo
	CurrentYear int
	ViewMode    enums.ViewMode
	Theme       enums.Theme
	SortMode    enums.SortMode
}

// Config holds server configuration
type Config struct {
	Address        string
	CrontabFile    string
	DBPath         string
	UpdateInterval time.Duration
	Version        string
}

// New creates a new web server
func New(cfg Config) (*Server, error) {
	// create persistence store
	store, err := persistence.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	// initialize database schema
	if initErr := store.Initialize(); initErr != nil {
		if closeErr := store.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to initialize database: %w (also failed to close: %v)", initErr, closeErr)
		}
		return nil, fmt.Errorf("failed to initialize database: %w", initErr)
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	s := &Server{
		store:          store,
		crontabFile:    cfg.CrontabFile,
		jobs:           make(map[string]persistence.JobInfo),
		parser:         parser,
		eventChan:      make(chan JobEvent, 1000),
		updateInterval: cfg.UpdateInterval,
		version:        cfg.Version,
	}

	// parse templates
	templates, err := s.parseTemplates()
	if err != nil {
		if closeErr := store.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to parse templates: %w (also failed to close: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}
	s.templates = templates

	return s, nil
}

// Run starts the web server
func (s *Server) Run(ctx context.Context, address string) error {
	// load existing job history from database
	s.loadJobsFromDB()

	// start background job sync
	go s.syncJobs(ctx)

	// start event processor
	go s.processEvents(ctx)

	server := &http.Server{
		Addr:              address,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("[WARN] failed to shutdown server: %v", err)
		}
	}()

	log.Printf("[INFO] starting web server on %s", address)
	return server.ListenAndServe()
}

// routes returns the http.Handler with all routes configured
func (s *Server) routes() http.Handler {
	router := routegroup.New(http.NewServeMux())

	// global middleware - applied to all routes
	router.Use(
		rest.RealIP,
		rest.Recoverer(log.Default()),
		rest.Throttle(1000),
		rest.AppInfo("cronn", "umputun", s.version),
		rest.Ping,
		rest.Trace,
		rest.SizeLimit(64*1024), // 64KB max request size
		logger.New(logger.Log(log.Default()), logger.Prefix("[DEBUG]")).Handler,
	)

	// dashboard route
	router.HandleFunc("GET /", s.handleDashboard)

	// api routes with grouping
	router.Mount("/api").Route(func(api *routegroup.Bundle) {
		// api-specific middleware
		api.Use(rest.NoCache) // prevent caching of API responses

		// HTMX endpoints
		api.HandleFunc("GET /jobs", s.handleJobsPartial)
		api.HandleFunc("POST /view-mode", s.handleViewModeToggle)
		api.HandleFunc("POST /theme", s.handleThemeToggle)
		api.HandleFunc("POST /sort-mode", s.handleSortModeChange)
		api.HandleFunc("POST /sort-toggle", s.handleSortToggle)
	})

	// static files with proper error handling
	fsys, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Printf("[ERROR] failed to create static file system: %v", err)
		// fallback to direct FileServer if Sub fails
		router.Handle("GET /static/", http.FileServer(http.FS(staticFS)))
	} else {
		router.HandleFiles("/static/", http.FS(fsys))
	}

	return router
}

// OnJobStart implements service.JobEventHandler interface
func (s *Server) OnJobStart(command, schedule string, startTime time.Time) {
	event := JobEvent{
		JobID:     HashCommand(command),
		Command:   command,
		Schedule:  schedule,
		EventType: enums.EventTypeStarted,
		StartedAt: startTime,
	}
	select {
	case s.eventChan <- event:
	default:
		log.Printf("[WARN] event channel full, dropping event")
	}
}

// OnJobComplete implements service.JobEventHandler interface
func (s *Server) OnJobComplete(command, schedule string, startTime, endTime time.Time, exitCode int, err error) {
	eventType := enums.EventTypeCompleted
	if err != nil {
		eventType = enums.EventTypeFailed
	}
	event := JobEvent{
		JobID:      HashCommand(command),
		Command:    command,
		Schedule:   schedule,
		EventType:  eventType,
		ExitCode:   exitCode,
		StartedAt:  startTime,
		FinishedAt: endTime,
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
	s.loadJobsFromCrontab()

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
			s.loadJobsFromCrontab()
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
func (s *Server) loadJobsFromCrontab() {
	parser := crontab.New(s.crontabFile, 0, nil)
	specs, err := parser.List()
	if err != nil {
		log.Printf("[WARN] failed to parse crontab: %v", err)
		return
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
			log.Printf("[WARN] failed to parse schedule %q: %v", spec.Spec, err)
			continue
		}

		// update or create job
		if job, exists := s.jobs[id]; exists {
			job.Schedule = spec.Spec
			job.NextRun = schedule.Next(time.Now())
			job.UpdatedAt = time.Now()
			job.SortIndex = idx // update sort index in case order changed
			delete(oldJobs, id)
			s.jobs[id] = job
		} else {
			s.jobs[id] = persistence.JobInfo{
				ID:         id,
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
		}
	}
}

// handleJobEvent handles a single job event
func (s *Server) handleJobEvent(event JobEvent) {
	id := HashCommand(event.Command)

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
			Enabled:   true,
		}
		// calculate next run for new job
		s.updateNextRun(&job)
		if !job.NextRun.IsZero() {
			log.Printf("[DEBUG] calculated NextRun for new job %s: %v", event.Command, job.NextRun)
		} else {
			log.Printf("[WARN] failed to parse schedule %q for job %s", event.Schedule, event.Command)
		}
	}

	switch event.EventType {
	case enums.EventTypeStarted:
		job.IsRunning = true
		job.LastRun = event.StartedAt
		job.LastStatus = enums.JobStatusRunning
	case enums.EventTypeCompleted:
		job.IsRunning = false
		job.LastStatus = enums.JobStatusSuccess
		s.updateNextRun(&job)
	case enums.EventTypeFailed:
		job.IsRunning = false
		job.LastStatus = enums.JobStatusFailed
		s.updateNextRun(&job)
	}
	job.UpdatedAt = time.Now()

	// store the updated job back in the map
	s.jobs[id] = job

	// save execution to database
	if err := s.store.RecordExecution(id, event.StartedAt, event.FinishedAt, job.LastStatus, event.ExitCode); err != nil {
		log.Printf("[WARN] failed to save execution: %v", err)
	}
}

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	viewMode := s.getViewMode(r)
	theme := s.getTheme(r)
	sortMode := s.getSortMode(r)

	s.jobsMu.RLock()
	jobs := make([]persistence.JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		// work with a copy, recalculate next run times before sorting
		jobCopy := job
		s.updateNextRun(&jobCopy)
		jobs = append(jobs, jobCopy)
	}
	s.jobsMu.RUnlock()

	// sort jobs based on selected mode
	s.sortJobs(jobs, sortMode)

	data := TemplateData{
		Jobs:        jobs,
		CurrentYear: time.Now().Year(),
		ViewMode:    viewMode,
		Theme:       theme,
		SortMode:    sortMode,
	}

	s.render(w, "base.html", "base", data)
}

// handleJobsPartial returns the jobs list partial for HTMX polling
func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	viewMode := s.getViewMode(r)
	sortMode := s.getSortMode(r)

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

	data := TemplateData{
		Jobs:     jobs,
		ViewMode: viewMode,
		SortMode: sortMode,
	}

	tmplName := "jobs-cards"
	if viewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	s.render(w, "partials/jobs.html", tmplName, data)
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
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// trigger full page refresh to update the toggle button icon
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// handleThemeToggle toggles the theme
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	currentTheme := s.getTheme(r)

	// cycle: light -> dark -> auto -> light
	var nextTheme enums.Theme
	switch currentTheme {
	case enums.ThemeLight:
		nextTheme = enums.ThemeDark
	case enums.ThemeDark:
		nextTheme = enums.ThemeAuto
	case enums.ThemeAuto:
		nextTheme = enums.ThemeLight
	default:
		nextTheme = enums.ThemeLight
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "theme",
		Value:    nextTheme.String(),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: false,              // allow JS to read for immediate update
		SameSite: http.SameSiteLaxMode,
	})

	// trigger full page refresh for theme change
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// handleSortToggle toggles between sort modes
func (s *Server) handleSortToggle(w http.ResponseWriter, r *http.Request) {
	currentMode := s.getSortMode(r)

	// cycle: default -> lastrun -> nextrun -> default
	var nextMode enums.SortMode
	switch currentMode {
	case enums.SortModeDefault:
		nextMode = enums.SortModeLastrun
	case enums.SortModeLastrun:
		nextMode = enums.SortModeNextrun
	case enums.SortModeNextrun:
		nextMode = enums.SortModeDefault
	default:
		// if somehow we get an unexpected value, default to default sort
		nextMode = enums.SortModeDefault
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "sort-mode",
		Value:    nextMode.String(),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// get sorted jobs for the new mode
	viewMode := s.getViewMode(r)
	s.jobsMu.RLock()
	jobs := make([]persistence.JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		// work with a copy, recalculate next run times before sorting
		jobCopy := job
		s.updateNextRun(&jobCopy)
		jobs = append(jobs, jobCopy)
	}
	s.jobsMu.RUnlock()

	// sort jobs based on new mode
	s.sortJobs(jobs, nextMode)

	// prepare template data
	data := TemplateData{
		Jobs:     jobs,
		ViewMode: viewMode,
		SortMode: nextMode,
	}

	// determine template name
	tmplName := "jobs-cards"
	if viewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// get the template and render jobs HTML
	tmpl, ok := s.templates["partials/jobs.html"]
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	var jobsHTML bytes.Buffer
	if err := tmpl.ExecuteTemplate(&jobsHTML, tmplName, data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	// get sort button label
	sortLabel := "Original Order"
	switch nextMode {
	case enums.SortModeLastrun:
		sortLabel = "Last Run"
	case enums.SortModeNextrun:
		sortLabel = "Next Run"
	}

	// return jobs with OOB update for sort button
	fmt.Fprintf(w, `%s
<span class="sort-label" hx-swap-oob="innerHTML" id="sort-label">%s</span>`,
		jobsHTML.String(), sortLabel)
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
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// return the jobs partial with sorted jobs
	viewMode := s.getViewMode(r)

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

	data := TemplateData{
		Jobs:     jobs,
		ViewMode: viewMode,
		SortMode: sortMode,
	}

	tmplName := "jobs-cards"
	if viewMode == enums.ViewModeList {
		tmplName = "jobs-list"
	}

	// set header to tell HTMX to swap the jobs container
	w.Header().Set("HX-Retarget", "#jobs-container")
	w.Header().Set("HX-Reswap", "innerHTML")

	s.render(w, "partials/jobs.html", tmplName, data)
}

// render renders a template
func (s *Server) render(w http.ResponseWriter, page, tmplName string, data any) {
	tmpl, ok := s.templates[page]
	if !ok {
		log.Printf("[WARN] template %s not found", page)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	buf := new(bytes.Buffer)
	if err := tmpl.ExecuteTemplate(buf, tmplName, data); err != nil {
		log.Printf("[WARN] failed to execute template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write response: %v", err)
	}
}

// parseTemplates parses all templates
func (s *Server) parseTemplates() (map[string]*template.Template, error) {
	templates := make(map[string]*template.Template)

	funcMap := template.FuncMap{
		"humanTime":     s.humanTime,
		"humanDuration": s.humanDuration,
		"truncate":      s.truncate,
		"timeUntil":     s.timeUntil,
	}

	// parse base template with all partials
	base, err := template.New("base.html").Funcs(funcMap).ParseFS(templatesFS,
		"templates/base.html",
		"templates/dashboard.html",
		"templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse base template: %w", err)
	}
	templates["base.html"] = base

	// parse partials separately for HTMX requests
	partials, err := template.New("jobs.html").Funcs(funcMap).ParseFS(templatesFS,
		"templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse partials: %w", err)
	}
	templates["partials/jobs.html"] = partials

	return templates, nil
}

// helper functions

// HashCommand generates a SHA256 hash of a command for use as a unique ID
func HashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(h[:])
}

func (s *Server) getViewMode(r *http.Request) enums.ViewMode {
	cookie, err := r.Cookie("view-mode")
	if err != nil {
		return enums.ViewModeCards // default
	}
	mode, err := enums.ParseViewMode(cookie.Value)
	if err != nil {
		log.Printf("[WARN] invalid view mode %q: %v", cookie.Value, err)
		return enums.ViewModeCards // default on parse error
	}
	return mode
}

func (s *Server) getTheme(r *http.Request) enums.Theme {
	cookie, err := r.Cookie("theme")
	if err != nil {
		return enums.ThemeAuto // default
	}
	theme, err := enums.ParseTheme(cookie.Value)
	if err != nil {
		log.Printf("[WARN] invalid theme %q: %v", cookie.Value, err)
		return enums.ThemeAuto // default on parse error
	}
	return theme
}

// template helper functions

func (s *Server) humanTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return t.Format("Jan 2, 15:04:05")
}

func (s *Server) humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (s *Server) timeUntil(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	d := time.Until(t)
	if d < 0 {
		return "Overdue"
	}
	return s.humanDuration(d)
}

func (s *Server) truncate(str string, n int) string {
	if len(str) <= n {
		return str
	}
	return str[:n] + "..."
}

// getSortMode gets the sort mode from cookie or defaults to "default"
func (s *Server) getSortMode(r *http.Request) enums.SortMode {
	cookie, err := r.Cookie("sort-mode")
	if err != nil || cookie.Value == "" {
		return enums.SortModeDefault
	}
	mode, err := enums.ParseSortMode(cookie.Value)
	if err != nil {
		log.Printf("[WARN] invalid sort mode cookie %q: %v", cookie.Value, err)
		return enums.SortModeDefault
	}
	return mode
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
