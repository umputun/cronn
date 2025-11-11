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
	"strings"
	"sync"
	"time"

	"github.com/didip/tollbooth/v8"
	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"
	"github.com/go-pkgz/routegroup"
	"github.com/robfig/cron/v3"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

//go:embed templates/*.html templates/partials/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// session represents an active user session
type session struct {
	token     string
	createdAt time.Time
}

// Server represents the web server
type Server struct {
	store          Persistence
	templates      map[string]*template.Template
	jobsMu         sync.RWMutex
	jobs           map[string]persistence.JobInfo // job id -> job info
	parser         cron.Parser                    // for schedule parsing (NextRun calculations)
	jobsProvider   JobsProvider                   // for loading job specifications
	eventChan      chan JobEvent
	updateInterval time.Duration
	version        string
	passwordHash   string                          // bcrypt hash for basic auth
	loginTTL       time.Duration                   // session TTL
	manualTrigger  chan<- service.ManualJobRequest // channel to send manual trigger requests to scheduler
	csrfProtection *http.CrossOriginProtection     // csrf protection for POST endpoints
	sessions       map[string]session              // active user sessions
	sessionsMu     sync.Mutex                      // protects sessions map
}

// JobsProvider loads job specifications from a configured source (e.g., crontab file, YAML/JSON config).
// Implementations should return the complete list of job specifications on each call.
type JobsProvider interface {
	// List returns all job specifications from the configured source.
	// It should return an error if the source cannot be read or parsed.
	List() ([]crontab.JobSpec, error)
}

// Persistence defines storage operations for job management
type Persistence interface {
	LoadJobs() ([]persistence.JobInfo, error)
	SaveJobs(jobs []persistence.JobInfo) error
	RecordExecution(jobID string, started, finished time.Time, status enums.JobStatus, exitCode int, executedCommand string) error
	GetExecutions(jobID string, limit int) ([]persistence.ExecutionInfo, error)
	Close() error
}

// JobEvent represents job execution events
type JobEvent struct {
	JobID           string
	Command         string
	ExecutedCommand string
	Schedule        string
	EventType       enums.EventType
	ExitCode        int
	StartedAt       time.Time
	FinishedAt      time.Time
}

// TemplateData holds data for templates
type TemplateData struct {
	Jobs         []persistence.JobInfo
	CurrentYear  int
	ViewMode     enums.ViewMode
	Theme        enums.Theme
	SortMode     enums.SortMode
	FilterMode   enums.FilterMode
	RunningCount int    // for stats display
	NextRunTime  string // formatted next run time for stats
	TotalCount   int    // total jobs before filtering
	IsOOB        bool   // for OOB template rendering
	AuthEnabled  bool   // whether authentication is enabled
	Version      string // application version (short form)
	FullVersion  string // full application version
}

// jobsStats holds statistics about jobs
type jobsStats struct {
	jobs         []persistence.JobInfo
	runningCount int
	nextRunTime  string
	totalCount   int
}

// Config holds server configuration
type Config struct {
	DBPath         string
	UpdateInterval time.Duration
	Version        string
	ManualTrigger  chan<- service.ManualJobRequest // channel for sending manual trigger requests
	JobsProvider   JobsProvider                    // interface for loading job specifications
	PasswordHash   string                          // bcrypt hash for basic auth (empty to disable)
	LoginTTL       time.Duration                   // session TTL, defaults to 24h if not set
}

// New creates a new web server
func New(cfg Config) (*Server, error) {
	// validate required dependencies
	if cfg.JobsProvider == nil {
		return nil, fmt.Errorf("web server initialization failed: JobsProvider is required")
	}

	// create persistence store (it initializes itself)
	store, err := persistence.NewSQLiteStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("web server initialization failed: failed to create SQLite store at %q: %w", cfg.DBPath, err)
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	// create CSRF protection
	csrfProtection := http.NewCrossOriginProtection()

	// set default LoginTTL if not specified
	loginTTL := cfg.LoginTTL
	if loginTTL == 0 {
		loginTTL = 24 * time.Hour
	}

	s := &Server{
		store:          store,
		jobs:           make(map[string]persistence.JobInfo),
		parser:         parser,
		jobsProvider:   cfg.JobsProvider,
		eventChan:      make(chan JobEvent, 1000),
		updateInterval: cfg.UpdateInterval,
		version:        cfg.Version,
		passwordHash:   cfg.PasswordHash,
		loginTTL:       loginTTL,
		manualTrigger:  cfg.ManualTrigger,
		csrfProtection: csrfProtection,
	}

	// parse templates
	templates, err := s.parseTemplates()
	if err != nil {
		if closeErr := store.Close(); closeErr != nil {
			return nil, fmt.Errorf("web server initialization failed: failed to parse HTML templates: %w (also failed to close store: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("web server initialization failed: failed to parse HTML templates: %w", err)
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
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("web server failed: %w", err)
	}
	return nil
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

	// add auth middleware if password hash is configured
	// must be done before any routes are defined
	if s.passwordHash != "" {
		log.Printf("[INFO] authentication enabled for web UI")
		// custom auth middleware that checks cookie or basic auth
		router.Use(s.authMiddleware)
	}

	// login routes - not protected by auth (Maybe middleware condition returns false for /login)
	if s.passwordHash != "" {
		router.HandleFunc("GET /login", s.handleLoginForm)
		router.With(s.csrfProtection.Handler, tollbooth.HTTPMiddleware(loginLimiter)).HandleFunc("POST /login", s.handleLogin)
		router.HandleFunc("GET /logout", s.handleLogout)
	}

	// dashboard route
	router.HandleFunc("GET /", s.handleDashboard)

	// api routes with grouping
	router.Mount("/api").Route(func(api *routegroup.Bundle) {
		// api-specific middleware
		api.Use(rest.NoCache)             // prevent caching of API responses
		api.Use(s.csrfProtection.Handler) // CSRF protection for POST endpoints

		// HTMX endpoints
		api.HandleFunc("GET /jobs", s.handleJobsPartial)
		api.HandleFunc("POST /view-mode", s.handleViewModeToggle)
		api.HandleFunc("POST /theme", s.handleThemeToggle)
		api.HandleFunc("POST /sort-mode", s.handleSortModeChange)
		api.HandleFunc("POST /sort-toggle", s.handleSortToggle)
		api.HandleFunc("POST /filter-toggle", s.handleFilterToggle)
		api.HandleFunc("POST /jobs/{id}/run", s.handleRunJob)
		api.HandleFunc("GET /jobs/{id}/modal", s.handleJobModal)
		api.HandleFunc("GET /jobs/{id}/history", s.handleJobHistory)
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
func (s *Server) OnJobStart(command, executedCommand, schedule string, startTime time.Time) {
	event := JobEvent{
		JobID:           HashCommand(command),
		Command:         command,
		ExecutedCommand: executedCommand,
		Schedule:        schedule,
		EventType:       enums.EventTypeStarted,
		StartedAt:       startTime,
	}
	select {
	case s.eventChan <- event:
	default:
		log.Printf("[WARN] event channel full, dropping event")
	}
}

// OnJobComplete implements service.JobEventHandler interface
func (s *Server) OnJobComplete(command, executedCommand, schedule string, startTime, endTime time.Time, exitCode int, err error) {
	eventType := enums.EventTypeCompleted
	if err != nil {
		eventType = enums.EventTypeFailed
	}
	event := JobEvent{
		JobID:           HashCommand(command),
		Command:         command,
		ExecutedCommand: executedCommand,
		Schedule:        schedule,
		EventType:       eventType,
		ExitCode:        exitCode,
		StartedAt:       startTime,
		FinishedAt:      endTime,
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

	switch event.EventType {
	case enums.EventTypeStarted:
		job.IsRunning = true
		job.LastRun = event.StartedAt
		job.LastStatus = enums.JobStatusRunning
	case enums.EventTypeCompleted:
		job.IsRunning = false
		job.LastStatus = enums.JobStatusSuccess
		s.updateNextRun(&job)
		// save execution to database
		if err := s.store.RecordExecution(id, event.StartedAt, event.FinishedAt, job.LastStatus, event.ExitCode, event.ExecutedCommand); err != nil {
			log.Printf("[WARN] failed to save execution: %v", err)
		}
	case enums.EventTypeFailed:
		job.IsRunning = false
		job.LastStatus = enums.JobStatusFailed
		s.updateNextRun(&job)
		// save execution to database
		if err := s.store.RecordExecution(id, event.StartedAt, event.FinishedAt, job.LastStatus, event.ExitCode, event.ExecutedCommand); err != nil {
			log.Printf("[WARN] failed to save execution: %v", err)
		}
	}
	job.UpdatedAt = time.Now()

	// store the updated job back in the map
	s.jobs[id] = job
}

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	viewMode := s.getViewMode(r)
	theme := s.getTheme(r)
	sortMode := s.getSortMode(r)
	filterMode := s.getFilterMode(r)

	stats := s.getJobsWithStats(sortMode, filterMode, "")

	data := TemplateData{
		Jobs:         stats.jobs,
		CurrentYear:  time.Now().Year(),
		ViewMode:     viewMode,
		Theme:        theme,
		SortMode:     sortMode,
		FilterMode:   filterMode,
		RunningCount: stats.runningCount,
		NextRunTime:  stats.nextRunTime,
		TotalCount:   stats.totalCount,
		AuthEnabled:  s.passwordHash != "",
		Version:      shortVersion(s.version),
		FullVersion:  s.version,
	}

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
	viewMode := s.getViewMode(r)
	sortMode := s.getSortMode(r)
	filterMode := s.getFilterMode(r)
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(sortMode, filterMode, searchTerm)

	data := TemplateData{
		Jobs:         stats.jobs,
		ViewMode:     viewMode,
		SortMode:     sortMode,
		FilterMode:   filterMode,
		RunningCount: stats.runningCount,
		NextRunTime:  stats.nextRunTime,
		TotalCount:   stats.totalCount,
		IsOOB:        true, // enable OOB for stats updates
	}

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
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// get sorted jobs for the new view mode
	sortMode := s.getSortMode(r)
	filterMode := s.getFilterMode(r)
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(sortMode, filterMode, searchTerm)

	// prepare template data
	theme := s.getTheme(r)
	data := TemplateData{
		Jobs:         stats.jobs,
		ViewMode:     newMode,
		SortMode:     sortMode,
		FilterMode:   filterMode,
		Theme:        theme,
		TotalCount:   stats.totalCount,
		RunningCount: stats.runningCount,
		NextRunTime:  stats.nextRunTime,
		CurrentYear:  time.Now().Year(),
		IsOOB:        true,
	}

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
		Path:     "/",
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
	viewMode := s.getViewMode(r)
	filterMode := s.getFilterMode(r)
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(nextMode, filterMode, searchTerm)

	// prepare template data
	data := TemplateData{
		Jobs:         stats.jobs,
		ViewMode:     viewMode,
		SortMode:     nextMode,
		FilterMode:   filterMode,
		RunningCount: stats.runningCount,
		NextRunTime:  stats.nextRunTime,
		TotalCount:   stats.totalCount,
	}

	// render jobs and send response with OOB updates
	if err := s.renderSortedJobs(w, data); err != nil {
		log.Printf("[ERROR] failed to render sorted jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// cycleSortMode cycles through sort modes: default -> lastrun -> nextrun -> default
func (s *Server) cycleSortMode(current enums.SortMode) enums.SortMode {
	switch current {
	case enums.SortModeDefault:
		return enums.SortModeLastrun
	case enums.SortModeLastrun:
		return enums.SortModeNextrun
	case enums.SortModeNextrun:
		return enums.SortModeDefault
	default:
		return enums.SortModeDefault
	}
}

// setSortCookie sets the sort mode cookie
func (s *Server) setSortCookie(w http.ResponseWriter, mode enums.SortMode) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sort-mode",
		Value:    mode.String(),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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
	viewMode := s.getViewMode(r)
	sortMode := s.getSortMode(r)
	searchTerm := r.FormValue("search")
	stats := s.getJobsWithStats(sortMode, nextMode, searchTerm)

	// prepare template data
	data := TemplateData{
		Jobs:         stats.jobs,
		ViewMode:     viewMode,
		SortMode:     sortMode,
		FilterMode:   nextMode,
		RunningCount: stats.runningCount,
		NextRunTime:  stats.nextRunTime,
		TotalCount:   stats.totalCount,
	}

	// render jobs and send response with OOB updates
	if err := s.renderFilteredJobs(w, data); err != nil {
		log.Printf("[ERROR] failed to render filtered jobs: %v", err)
		http.Error(w, "Failed to render jobs", http.StatusInternalServerError)
	}
}

// cycleFilterMode cycles through filter modes: all -> running -> success -> failed -> all
func (s *Server) cycleFilterMode(current enums.FilterMode) enums.FilterMode {
	switch current {
	case enums.FilterModeAll:
		return enums.FilterModeRunning
	case enums.FilterModeRunning:
		return enums.FilterModeSuccess
	case enums.FilterModeSuccess:
		return enums.FilterModeFailed
	case enums.FilterModeFailed:
		return enums.FilterModeAll
	default:
		return enums.FilterModeAll
	}
}

// setFilterCookie sets the filter mode cookie
func (s *Server) setFilterCookie(w http.ResponseWriter, mode enums.FilterMode) {
	http.SetCookie(w, &http.Cookie{
		Name:     "filter-mode",
		Value:    mode.String(),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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

// handleRunJob handles manual job trigger requests
func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
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
		"templates/base.html", "templates/dashboard.html", "templates/partials/*.html")
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

	// parse login template (standalone, doesn't use base)
	login, err := template.New("login.html").ParseFS(templatesFS, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse login template: %w", err)
	}
	templates["login"] = login

	return templates, nil
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
		return enums.ThemeDark // default to dark when no cookie
	}
	theme, err := enums.ParseTheme(cookie.Value)
	if err != nil {
		log.Printf("[WARN] invalid theme %q: %v", cookie.Value, err)
		return enums.ThemeDark // default on parse error
	}
	return theme
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

// getFilterMode gets the filter mode from cookie or defaults to "all"
func (s *Server) getFilterMode(r *http.Request) enums.FilterMode {
	cookie, err := r.Cookie("filter-mode")
	if err != nil {
		return enums.FilterModeAll // default to all
	}
	mode, err := enums.ParseFilterMode(cookie.Value)
	if err != nil {
		log.Printf("[WARN] invalid filter mode %q: %v", cookie.Value, err)
		return enums.FilterModeAll
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

// filterJobs filters jobs based on the selected filter mode
func (s *Server) filterJobs(jobs []persistence.JobInfo, filterMode enums.FilterMode) []persistence.JobInfo {
	if filterMode == enums.FilterModeAll {
		return jobs
	}

	filtered := make([]persistence.JobInfo, 0, len(jobs))
	for _, job := range jobs {
		switch filterMode {
		case enums.FilterModeRunning:
			if job.IsRunning {
				filtered = append(filtered, job)
			}
		case enums.FilterModeSuccess:
			if job.LastStatus == enums.JobStatusSuccess {
				filtered = append(filtered, job)
			}
		case enums.FilterModeFailed:
			if job.LastStatus == enums.JobStatusFailed {
				filtered = append(filtered, job)
			}
		}
	}
	return filtered
}

// searchJobs filters jobs by search term (case-insensitive command search)
func (s *Server) searchJobs(jobs []persistence.JobInfo, searchTerm string) []persistence.JobInfo {
	if searchTerm == "" {
		return jobs
	}

	searchLower := strings.ToLower(searchTerm)
	filtered := make([]persistence.JobInfo, 0, len(jobs))
	for _, job := range jobs {
		if strings.Contains(strings.ToLower(job.Command), searchLower) {
			filtered = append(filtered, job)
		}
	}
	return filtered
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

// helper functions

// HashCommand generates a SHA256 hash of a command for use as a unique ID
func HashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(h[:])
}

// shortVersion extracts a short version string from full version
// for version like "v1.7.0-abc1234-20241225", returns "v1.7.0"
// for version like "v1.7.0", returns "v1.7.0"
func shortVersion(fullVer string) string {
	if fullVer == "" || fullVer == "unknown" {
		return fullVer
	}
	// extract version before first dash
	if idx := strings.Index(fullVer, "-"); idx > 0 {
		return fullVer[:idx]
	}
	return fullVer
}
