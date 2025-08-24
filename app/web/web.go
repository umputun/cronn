package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite" // SQLite driver

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/web/enums"
)

//go:embed templates/*.html templates/partials/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server represents the web server
type Server struct {
	db             *sql.DB
	templates      map[string]*template.Template
	crontabFile    string
	jobsMu         sync.RWMutex
	jobs           map[string]JobInfo // job id -> job info
	parser         cron.Parser
	eventChan      chan JobEvent
	updateInterval time.Duration
}

// JobInfo represents a cron job in the UI
type JobInfo struct {
	ID         string // SHA256 of command
	Command    string
	Schedule   string
	NextRun    time.Time
	LastRun    time.Time
	LastStatus enums.JobStatus
	IsRunning  bool
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	SortIndex  int // original order in crontab
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
	Jobs        []JobInfo
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
}

// New creates a new web server
func New(cfg Config) (*Server, error) {
	// open database
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// enable WAL mode for better concurrency
	if _, walErr := db.Exec("PRAGMA journal_mode=WAL"); walErr != nil {
		return nil, fmt.Errorf("failed to set WAL mode: %w", walErr)
	}

	// create tables
	if createErr := createTables(db); createErr != nil {
		return nil, fmt.Errorf("failed to create tables: %w", createErr)
	}

	// parse templates
	templates, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	return &Server{
		db:             db,
		templates:      templates,
		crontabFile:    cfg.CrontabFile,
		jobs:           make(map[string]JobInfo),
		parser:         parser,
		eventChan:      make(chan JobEvent, 1000),
		updateInterval: cfg.UpdateInterval,
	}, nil
}

// Run starts the web server
func (s *Server) Run(ctx context.Context, address string) error {
	// load existing job history from database
	s.loadJobsFromDB()

	// start background job sync
	go s.syncJobs(ctx)

	// start event processor
	go s.processEvents(ctx)

	// setup routes
	mux := http.NewServeMux()

	// pages
	mux.HandleFunc("GET /", s.handleDashboard)

	// api endpoints for HTMX
	mux.HandleFunc("GET /api/jobs", s.handleJobsPartial)
	mux.HandleFunc("POST /api/view-mode", s.handleViewModeToggle)
	mux.HandleFunc("POST /api/theme", s.handleThemeToggle)
	mux.HandleFunc("POST /api/sort-mode", s.handleSortModeChange)
	mux.HandleFunc("POST /api/sort-toggle", s.handleSortToggle)

	// static files
	mux.Handle("GET /static/", http.FileServer(http.FS(staticFS)))

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
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
	rows, err := s.db.Query(`
		SELECT id, command, schedule, next_run, last_run, last_status, 
		       enabled, created_at, updated_at 
		FROM jobs`)
	if err != nil {
		log.Printf("[WARN] failed to load jobs from database: %v", err)
		return
	}
	defer rows.Close()

	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	for rows.Next() {
		job := JobInfo{}
		var nextRun, lastRun sql.NullInt64
		var createdAt, updatedAt int64

		err := rows.Scan(&job.ID, &job.Command, &job.Schedule, &nextRun, &lastRun,
			&job.LastStatus, &job.Enabled, &createdAt, &updatedAt)
		if err != nil {
			log.Printf("[WARN] failed to scan job row: %v", err)
			continue
		}

		// convert timestamps
		job.CreatedAt = time.Unix(createdAt, 0)
		job.UpdatedAt = time.Unix(updatedAt, 0)
		if nextRun.Valid {
			job.NextRun = time.Unix(nextRun.Int64, 0)
		}
		if lastRun.Valid {
			job.LastRun = time.Unix(lastRun.Int64, 0)
		}

		// calculate next run from schedule if not set
		if job.NextRun.IsZero() {
			s.updateNextRun(&job)
		}

		s.jobs[job.ID] = job
		log.Printf("[DEBUG] loaded job from DB: %s (%s)", job.Command, job.LastStatus)
	}

	if err := rows.Err(); err != nil {
		log.Printf("[WARN] error iterating job rows: %v", err)
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
	defer s.jobsMu.Unlock()

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
		} else {
			s.jobs[id] = JobInfo{
				ID:        id,
				Command:   spec.Command,
				Schedule:  spec.Spec,
				NextRun:   schedule.Next(time.Now()),
				Enabled:   true,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				SortIndex: idx,
			}
		}
	}

	// remove jobs that no longer exist
	for id := range oldJobs {
		delete(s.jobs, id)
	}

	// persist to database
	s.persistJobs()
}

// persistJobs saves jobs to database
func (s *Server) persistJobs() {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("[WARN] failed to begin transaction: %v", err)
		return
	}
	defer tx.Rollback()

	for _, job := range s.jobs {
		// determine status to save
		status := job.LastStatus
		if job.IsRunning {
			status = enums.JobStatusRunning
		}

		_, err := tx.Exec(`
			INSERT OR REPLACE INTO jobs 
			(id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at, sort_index)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID, job.Command, job.Schedule, job.NextRun.Unix(),
			job.LastRun.Unix(), status, job.Enabled,
			job.CreatedAt.Unix(), job.UpdatedAt.Unix(), job.SortIndex)
		if err != nil {
			log.Printf("[WARN] failed to persist job: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[WARN] failed to commit transaction: %v", err)
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
		job = JobInfo{
			ID:        id,
			Command:   event.Command,
			Schedule:  event.Schedule,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (job_id, started_at, finished_at, status, exit_code)
		VALUES (?, ?, ?, ?, ?)`,
		id, event.StartedAt.Unix(), event.FinishedAt.Unix(),
		job.LastStatus, event.ExitCode)
	if err != nil {
		log.Printf("[WARN] failed to save execution: %v", err)
	}
}

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	viewMode := getViewMode(r)
	theme := getTheme(r)
	sortMode := s.getSortMode(r)

	s.jobsMu.RLock()
	jobs := make([]JobInfo, 0, len(s.jobs))
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
	viewMode := getViewMode(r)
	sortMode := s.getSortMode(r)

	s.jobsMu.RLock()
	jobs := make([]JobInfo, 0, len(s.jobs))
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
	currentMode := getViewMode(r)
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
	currentTheme := getTheme(r)

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
	viewMode := getViewMode(r)
	s.jobsMu.RLock()
	jobs := make([]JobInfo, 0, len(s.jobs))
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
	viewMode := getViewMode(r)

	s.jobsMu.RLock()
	jobs := make([]JobInfo, 0, len(s.jobs))
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
func parseTemplates() (map[string]*template.Template, error) {
	templates := make(map[string]*template.Template)

	funcMap := template.FuncMap{
		"humanTime":     humanTime,
		"humanDuration": humanDuration,
		"truncate":      truncate,
		"timeUntil":     timeUntil,
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

// createTables creates database tables
func createTables(db *sql.DB) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			command TEXT NOT NULL,
			schedule TEXT NOT NULL,
			next_run INTEGER,
			last_run INTEGER,
			last_status TEXT,
			enabled BOOLEAN DEFAULT 1,
			created_at INTEGER,
			updated_at INTEGER,
			sort_index INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT,
			started_at INTEGER,
			finished_at INTEGER,
			status TEXT,
			exit_code INTEGER,
			FOREIGN KEY (job_id) REFERENCES jobs(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_job_id ON executions(job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_started_at ON executions(started_at)`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	return nil
}

// helper functions

// HashCommand generates a SHA256 hash of a command for use as a unique ID
func HashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(h[:])
}

func getViewMode(r *http.Request) enums.ViewMode {
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

func getTheme(r *http.Request) enums.Theme {
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

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	return t.Format("Jan 2, 15:04:05")
}

func humanDuration(d time.Duration) string {
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

func timeUntil(t time.Time) string {
	if t.IsZero() {
		return "Never"
	}
	d := time.Until(t)
	if d < 0 {
		return "Overdue"
	}
	return humanDuration(d)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
func (s *Server) updateNextRun(job *JobInfo) {
	if job.Schedule == "" {
		return
	}
	if schedule, err := s.parser.Parse(job.Schedule); err == nil {
		job.NextRun = schedule.Next(time.Now())
	}
}

// sortJobs sorts jobs based on the sort mode
func (s *Server) sortJobs(jobs []JobInfo, sortMode enums.SortMode) {
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
