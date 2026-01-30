// Package web implements the web server for cronn application
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
	"github.com/umputun/cronn/app/service/request"
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
	store              Persistence
	templates          map[string]*template.Template
	jobsMu             sync.RWMutex
	jobs               map[string]persistence.JobInfo // job id -> job info
	parser             cron.Parser                    // for schedule parsing (NextRun calculations)
	jobsProvider       JobsProvider                   // for loading job specifications
	eventChan          chan JobEvent
	updateInterval     time.Duration
	baseURL            string // base URL path for reverse proxy (e.g., /cronn), empty for root
	hostname           string // hostname to display in UI
	version            string
	passwordHash       string                          // bcrypt hash for basic auth
	loginTTL           time.Duration                   // session TTL
	manualTrigger      chan<- service.ManualJobRequest // channel to send manual trigger requests to scheduler
	csrfProtection     *http.CrossOriginProtection     // csrf protection for POST endpoints
	sessions           map[string]session              // active user sessions
	sessionsMu         sync.Mutex                      // protects sessions map
	disableManual      bool                            // disable manual job execution
	disableCommandEdit bool                            // disable command editing in manual run dialog
	settingsInfo       SettingsInfo                    // runtime configuration for settings/about modal
	logExecMaxLines    int                             // max log lines to store per execution (0 = disabled)
	logExecMaxHist     int                             // max executions to keep per job
	neighborsURL       string                          // URL to fetch neighbor instances JSON
	neighborsMu        sync.RWMutex                    // protects neighbors cache
	neighborsCache     []NeighborInstance              // cached neighbor instances
	neighborsCacheTime time.Time                       // when neighbors were last fetched
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
	RecordExecution(req request.RecordExecution) error
	GetExecutions(jobID string, limit int) ([]persistence.ExecutionInfo, error)
	GetExecutionByID(execID int) (persistence.ExecutionInfo, error)
	CleanupOldExecutions(jobID string, limit int) error
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
	Output          string
	StartedAt       time.Time
	FinishedAt      time.Time
}

// TemplateData holds data for templates
type TemplateData struct {
	Jobs               []persistence.JobInfo
	CurrentYear        int
	BaseURL            string // base URL path for reverse proxy (e.g., /cronn)
	Hostname           string // hostname to display in UI
	ViewMode           enums.ViewMode
	Theme              enums.Theme
	SortMode           enums.SortMode
	FilterMode         enums.FilterMode
	RunningCount       int    // for stats display
	NextRunTime        string // formatted next run time for stats
	TotalCount         int    // total jobs before filtering
	IsOOB              bool   // for OOB template rendering
	AuthEnabled        bool   // whether authentication is enabled
	Version            string // application version (short form)
	FullVersion        string // full application version
	ManualDisabled     bool   // whether manual job execution is disabled
	CommandEditEnabled bool   // whether command editing is enabled in manual run dialog
	NeighborsEnabled   bool   // whether neighbor instances selector is enabled
}

// newTemplateData creates a TemplateData with common fields populated from request
func (s *Server) newTemplateData(r *http.Request) TemplateData {
	return TemplateData{
		BaseURL:            s.baseURL,
		Hostname:           s.hostname,
		ViewMode:           s.getViewMode(r),
		SortMode:           s.getSortMode(r),
		FilterMode:         s.getFilterMode(r),
		ManualDisabled:     s.disableManual,
		CommandEditEnabled: !s.disableCommandEdit,
		NeighborsEnabled:   s.neighborsURL != "",
	}
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
	DBPath             string
	UpdateInterval     time.Duration
	BaseURL            string // base URL path for reverse proxy (e.g., /cronn), empty for root
	Hostname           string // hostname to display in UI
	Version            string
	ManualTrigger      chan<- service.ManualJobRequest // channel for sending manual trigger requests
	JobsProvider       JobsProvider                    // interface for loading job specifications
	PasswordHash       string                          // bcrypt hash for basic auth (empty to disable)
	LoginTTL           time.Duration                   // session TTL, defaults to 24h if not set
	DisableManual      bool                            // disable manual job execution
	DisableCommandEdit bool                            // disable command editing in manual run dialog
	Settings           SettingsInfo                    // runtime configuration for settings/about modal
	ExecMaxLogLines    int                             // max log lines to store per execution (0 = disabled)
	LogExecMaxHist     int                             // max executions to keep per job
	NeighborsURL       string                          // URL to fetch neighbor instances JSON
}

// SettingsInfo holds safe-to-display runtime configuration for settings/about modal
type SettingsInfo struct {
	// version & build info
	Version   string
	StartTime time.Time

	// web settings
	WebEnabled         bool
	WebAddress         string
	WebHostname        string
	WebUpdateInterval  time.Duration
	AuthEnabled        bool
	ManualEnabled      bool
	CommandEditEnabled bool

	// crontab/scheduler settings
	CrontabPath         string // or "command mode" if using --command
	UpdateEnabled       bool
	UpdateInterval      time.Duration
	JitterEnabled       bool
	DeDupEnabled        bool
	MaxConcurrentChecks int

	// advanced features
	ResumeEnabled     bool
	ResumePath        string
	AltTemplateFormat bool

	// repeater defaults
	RepeaterAttempts int
	RepeaterDuration time.Duration
	RepeaterFactor   float64
	RepeaterJitter   bool

	// notification summary (counts, no secrets)
	EmailNotifications  bool
	SlackIntegration    bool
	SlackChannelCount   int
	TelegramIntegration bool
	TelegramDestCount   int
	WebhookCount        int
	NotificationTimeout time.Duration

	// logging settings
	LoggingEnabled bool
	DebugMode      bool
	LogFilePath    string
	LogMaxSize     int
	LogMaxAge      int
	LogMaxBackups  int
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
		store:              store,
		jobs:               make(map[string]persistence.JobInfo),
		parser:             parser,
		jobsProvider:       cfg.JobsProvider,
		eventChan:          make(chan JobEvent, 1000),
		updateInterval:     cfg.UpdateInterval,
		baseURL:            cfg.BaseURL,
		hostname:           cfg.Hostname,
		version:            cfg.Version,
		passwordHash:       cfg.PasswordHash,
		loginTTL:           loginTTL,
		manualTrigger:      cfg.ManualTrigger,
		csrfProtection:     csrfProtection,
		disableManual:      cfg.DisableManual,
		disableCommandEdit: cfg.DisableCommandEdit,
		settingsInfo:       cfg.Settings,
		logExecMaxLines:    cfg.ExecMaxLogLines,
		logExecMaxHist:     cfg.LogExecMaxHist,
		neighborsURL:       cfg.NeighborsURL,
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
		Handler:           s.handler(),
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

// handler returns the http.Handler with base URL wrapping applied
func (s *Server) handler() http.Handler {
	routes := s.routes()
	if s.baseURL == "" {
		return routes
	}

	// create a mux that handles the redirect and then the stripped routes
	mux := http.NewServeMux()
	// handle base URL without trailing slash - redirect to with trailing slash
	mux.HandleFunc(s.baseURL, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.baseURL+"/", http.StatusMovedPermanently)
	})
	// handle all other routes under base URL with StripPrefix
	mux.Handle(s.baseURL+"/", http.StripPrefix(s.baseURL, routes))
	return mux
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

	// api routes with grouping (HTMX endpoints)
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
		api.HandleFunc("GET /settings/modal", s.handleSettingsModal)
		api.HandleFunc("GET /jobs/{id}/executions/{exec_id}/logs", s.handleExecutionLogs)
		api.HandleFunc("GET /neighbors", s.handleNeighbors)
	})

	// JSON API for CLI/programmatic access
	router.Mount("/api/v1").Route(func(api *routegroup.Bundle) {
		api.Use(rest.NoCache)
		api.HandleFunc("GET /status", s.handleAPIStatus)
		api.HandleFunc("GET /jobs/{id}/history", s.handleAPIJobHistory)
		api.HandleFunc("GET /jobs/{id}/executions/{exec_id}/logs", s.handleAPIExecutionLogs)
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
		"since":         s.since,
		"url":           s.url,
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
	login, err := template.New("login.html").Funcs(funcMap).ParseFS(templatesFS, "templates/login.html")
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
		Path:     s.cookiePath(),
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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
		Path:     s.cookiePath(),
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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

func (s *Server) since(t time.Time) time.Duration {
	return time.Since(t)
}

func (s *Server) truncate(str string, n int) string {
	if len(str) <= n {
		return str
	}
	return str[:n] + "..."
}

// url prepends the base URL to a path for reverse proxy support
func (s *Server) url(path string) string {
	return s.baseURL + path
}

// cookiePath returns the cookie path with base URL support
func (s *Server) cookiePath() string {
	if s.baseURL == "" {
		return "/"
	}
	return s.baseURL + "/"
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
