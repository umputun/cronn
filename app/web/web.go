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
	"path"
	"strings"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite"

	"github.com/umputun/cronn/app/crontab"
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
	jobs           map[string]*JobInfo // job id -> job info
	parser         *cron.Parser
	eventChan      chan JobEvent
	updateInterval time.Duration
}

// JobInfo represents a cron job in the UI
type JobInfo struct {
	ID          string    // SHA256 of command
	Command     string
	Schedule    string
	NextRun     time.Time
	LastRun     time.Time
	LastStatus  string // "success", "failed", "running", ""
	IsRunning   bool
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// JobEvent represents job execution events
type JobEvent struct {
	JobID      string
	Command    string
	Schedule   string
	EventType  string // "started", "completed", "failed"
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
}

// TemplateData holds data for templates
type TemplateData struct {
	Jobs        []*JobInfo
	CurrentYear int
	ViewMode    string // "cards" or "list"
	Theme       string // "light", "dark", "auto"
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
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	// create tables
	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	// parse templates
	templates, err := parseTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	parser := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	return &Server{
		db:             db,
		templates:      templates,
		crontabFile:    cfg.CrontabFile,
		jobs:           make(map[string]*JobInfo),
		parser:         parser,
		eventChan:      make(chan JobEvent, 100),
		updateInterval: cfg.UpdateInterval,
	}, nil
}

// Run starts the web server
func (s *Server) Run(ctx context.Context, address string) error {
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
			log.Printf("[ERROR] failed to shutdown server: %v", err)
		}
	}()

	log.Printf("[INFO] starting web server on %s", address)
	return server.ListenAndServe()
}

// SendEvent sends a job event to the web server
func (s *Server) SendEvent(event JobEvent) {
	select {
	case s.eventChan <- event:
	default:
		log.Printf("[WARN] event channel full, dropping event")
	}
}

// syncJobs syncs jobs from crontab file
func (s *Server) syncJobs(ctx context.Context) {
	ticker := time.NewTicker(s.updateInterval)
	defer ticker.Stop()

	// initial sync
	s.loadJobsFromCrontab()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.loadJobsFromCrontab()
		}
	}
}

// loadJobsFromCrontab loads jobs from crontab file
func (s *Server) loadJobsFromCrontab() {
	parser := crontab.New(s.crontabFile, crontab.UpdateInterval(0), crontab.HostName(""), crontab.Once(true))
	specs, err := parser.List()
	if err != nil {
		log.Printf("[ERROR] failed to parse crontab: %v", err)
		return
	}

	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	// mark all jobs as potentially removed
	oldJobs := make(map[string]bool)
	for id := range s.jobs {
		oldJobs[id] = true
	}

	for _, spec := range specs {
		id := hashCommand(spec.Command)
		
		// parse schedule for next run calculation
		schedule, err := s.parser.Parse(spec.Schedule)
		if err != nil {
			log.Printf("[WARN] failed to parse schedule %q: %v", spec.Schedule, err)
			continue
		}

		// update or create job
		if job, exists := s.jobs[id]; exists {
			job.Schedule = spec.Schedule
			job.NextRun = schedule.Next(time.Now())
			job.UpdatedAt = time.Now()
			delete(oldJobs, id)
		} else {
			s.jobs[id] = &JobInfo{
				ID:        id,
				Command:   spec.Command,
				Schedule:  spec.Schedule,
				NextRun:   schedule.Next(time.Now()),
				Enabled:   true,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
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
		log.Printf("[ERROR] failed to begin transaction: %v", err)
		return
	}
	defer tx.Rollback()

	for _, job := range s.jobs {
		_, err := tx.Exec(`
			INSERT OR REPLACE INTO jobs 
			(id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID, job.Command, job.Schedule, job.NextRun.Unix(),
			job.LastRun.Unix(), job.LastStatus, job.Enabled,
			job.CreatedAt.Unix(), job.UpdatedAt.Unix())
		if err != nil {
			log.Printf("[ERROR] failed to persist job: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("[ERROR] failed to commit transaction: %v", err)
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
	id := hashCommand(event.Command)
	
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()

	job, exists := s.jobs[id]
	if !exists {
		// create new job entry if it doesn't exist
		job = &JobInfo{
			ID:        id,
			Command:   event.Command,
			Schedule:  event.Schedule,
			CreatedAt: time.Now(),
		}
		s.jobs[id] = job
	}

	switch event.EventType {
	case "started":
		job.IsRunning = true
		job.LastRun = event.StartedAt
		job.LastStatus = "running"
	case "completed":
		job.IsRunning = false
		job.LastStatus = "success"
	case "failed":
		job.IsRunning = false
		job.LastStatus = "failed"
	}
	job.UpdatedAt = time.Now()

	// save execution to database
	_, err := s.db.Exec(`
		INSERT INTO executions (job_id, started_at, finished_at, status, exit_code)
		VALUES (?, ?, ?, ?, ?)`,
		id, event.StartedAt.Unix(), event.FinishedAt.Unix(),
		job.LastStatus, event.ExitCode)
	if err != nil {
		log.Printf("[ERROR] failed to save execution: %v", err)
	}
}

// handleDashboard renders the main dashboard
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	viewMode := getViewMode(r)
	theme := getTheme(r)

	s.jobsMu.RLock()
	jobs := make([]*JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	s.jobsMu.RUnlock()

	data := TemplateData{
		Jobs:        jobs,
		CurrentYear: time.Now().Year(),
		ViewMode:    viewMode,
		Theme:       theme,
	}

	s.render(w, http.StatusOK, "base.html", "base", data)
}

// handleJobsPartial returns the jobs list partial for HTMX polling
func (s *Server) handleJobsPartial(w http.ResponseWriter, r *http.Request) {
	viewMode := getViewMode(r)

	s.jobsMu.RLock()
	jobs := make([]*JobInfo, 0, len(s.jobs))
	for _, job := range s.jobs {
		// recalculate next run times
		if schedule, err := s.parser.Parse(job.Schedule); err == nil {
			job.NextRun = schedule.Next(time.Now())
		}
		jobs = append(jobs, job)
	}
	s.jobsMu.RUnlock()

	data := TemplateData{
		Jobs:     jobs,
		ViewMode: viewMode,
	}

	tmplName := "jobs-cards"
	if viewMode == "list" {
		tmplName = "jobs-list"
	}

	s.render(w, http.StatusOK, "partials/jobs.html", tmplName, data)
}

// handleViewModeToggle toggles between card and list view
func (s *Server) handleViewModeToggle(w http.ResponseWriter, r *http.Request) {
	currentMode := getViewMode(r)
	newMode := "list"
	if currentMode == "list" {
		newMode = "cards"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "view-mode",
		Value:    newMode,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	// trigger HTMX to refresh the jobs list
	w.Header().Set("HX-Trigger", "refresh-jobs")
	w.WriteHeader(http.StatusOK)
}

// handleThemeToggle toggles the theme
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	currentTheme := getTheme(r)
	
	// cycle: light -> dark -> auto -> light
	nextTheme := "light"
	switch currentTheme {
	case "light":
		nextTheme = "dark"
	case "dark":
		nextTheme = "auto"
	case "auto":
		nextTheme = "light"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "theme",
		Value:    nextTheme,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: false, // allow JS to read for immediate update
		SameSite: http.SameSiteLaxMode,
	})

	// trigger full page refresh for theme change
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// render renders a template
func (s *Server) render(w http.ResponseWriter, status int, page, tmplName string, data any) {
	tmpl, ok := s.templates[page]
	if !ok {
		log.Printf("[ERROR] template %s not found", page)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	buf := new(bytes.Buffer)
	if err := tmpl.ExecuteTemplate(buf, tmplName, data); err != nil {
		log.Printf("[ERROR] failed to execute template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[ERROR] failed to write response: %v", err)
	}
}

// parseTemplates parses all templates
func parseTemplates() (map[string]*template.Template, error) {
	templates := make(map[string]*template.Template)

	funcMap := template.FuncMap{
		"humanTime": humanTime,
		"humanDuration": humanDuration,
		"cronDescription": cronDescription,
		"truncate": truncate,
		"timeUntil": timeUntil,
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
			updated_at INTEGER
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

func hashCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(h[:])
}

func getViewMode(r *http.Request) string {
	cookie, err := r.Cookie("view-mode")
	if err != nil {
		return "cards" // default
	}
	if cookie.Value == "list" {
		return "list"
	}
	return "cards"
}

func getTheme(r *http.Request) string {
	cookie, err := r.Cookie("theme")
	if err != nil {
		return "auto" // default
	}
	switch cookie.Value {
	case "light", "dark", "auto":
		return cookie.Value
	default:
		return "auto"
	}
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

func cronDescription(schedule string) string {
	// simple human-readable descriptions for common patterns
	switch schedule {
	case "* * * * *":
		return "Every minute"
	case "*/5 * * * *":
		return "Every 5 minutes"
	case "*/10 * * * *":
		return "Every 10 minutes"
	case "*/15 * * * *":
		return "Every 15 minutes"
	case "*/30 * * * *":
		return "Every 30 minutes"
	case "0 * * * *":
		return "Every hour"
	case "0 */2 * * *":
		return "Every 2 hours"
	case "0 0 * * *":
		return "Daily at midnight"
	case "0 2 * * *":
		return "Daily at 2:00 AM"
	case "0 0 * * 0":
		return "Weekly on Sunday"
	case "0 0 1 * *":
		return "Monthly on the 1st"
	default:
		// for complex expressions, return as-is with a hint
		if strings.Contains(schedule, "@") {
			return schedule // special keywords like @daily, @hourly
		}
		return fmt.Sprintf("Custom: %s", schedule)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}