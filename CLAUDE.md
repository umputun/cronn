# Cronn Project Architecture Knowledge

## Project Overview
Cron job scheduler with YAML/JSON and traditional crontab support, using `github.com/robfig/cron/v3` for scheduling, HTMX v2 for web UI, and SQLite with WAL mode for persistence.

## Key Components Architecture

### Core Packages
- **app/service**: Core scheduler implementation with `DayParser` for business day templates, `LogPrefixer` for command-specific logging
- **app/crontab**: Parser supporting traditional crontab and YAML/JSON with embedded JSON schema validation
- **app/conditions**: Conditional execution with semaphore-based concurrency control (default: 10 concurrent checks)
- **app/web**: Web UI server implementing `JobEventHandler` interface, uses Go templates with `html/template` and `embed`
- **app/web/enums**: Type-safe enums generated via `go-pkgz/enum` for `JobStatus`, `EventType`, `ViewMode`, `Theme`, `SortMode`, `FilterMode`
- **app/notify**: Notification service for job completion/failures
- **app/persistence**: SQLite persistence with `sqlx` and `modernc.org/sqlite` driver

### Key Dependencies
- **Scheduling**: `github.com/robfig/cron/v3` with standard 5-field parser (not seconds-based)
- **Database**: `github.com/jmoiron/sqlx` with `modernc.org/sqlite` (pure Go driver, native DATETIME support)
- **HTTP**: `github.com/go-pkgz/routegroup` and `github.com/go-pkgz/rest`
- **Rate limiting**: `github.com/didip/tollbooth/v8` for brute-force protection
- **YAML validation**: `github.com/invopop/jsonschema` for config validation
- **Frontend**: HTMX v2 for JavaScript-free dynamic updates

## Interface Design Patterns

### Consumer-Side Interfaces
Interfaces defined in consumer packages, not provider packages (Go idiom):
```go
// web/server.go defines what it needs from persistence
type Persistence interface {
    SaveJob(job Job) error
    LoadJobs() ([]Job, error)
}

// persistence/sqlite.go returns concrete type
func NewSQLiteStore(...) *SQLiteStore { ... }
```

### Minimal Interface Principle
Interfaces expose only operations needed by consumers, hide internal details like initialization:
```go
// Bad: exposing implementation details
type Persistence interface {
    Init() error  // don't expose
    SaveJob(job Job) error
}

// Good: initialization hidden in constructor
type Persistence interface {
    SaveJob(job Job) error
}
```

### Return Concrete Types, Accept Interfaces
Functions return concrete `*SQLiteStore` while accepting `Persistence` interface for flexibility.

## Web UI Architecture

### Template System
- Uses `html/template` with `embed` for static files
- **CRITICAL**: Templates and static files embedded at compile time - **must rebuild after any changes**
- Template helper functions are pure (no dependencies): `humanTime`, `humanDuration`, `timeUntil`, `truncate`

### HTMX Integration Patterns
- **Out-of-band updates**: `hx-swap-oob="innerHTML"` for updating multiple elements in single response
- **Auto-refresh polling**: `hx-trigger="load, every 5s"` for JavaScript-free real-time updates
- **Response coordination**: `HX-Refresh: true` header triggers full page refresh when needed
- **Event coordination**: Custom events like `refresh-jobs` coordinate updates between components

### Event-Driven Architecture
- Server implements `JobEventHandler` interface to receive job events
- Events processed asynchronously via channel with goroutine
- Job state updated in memory immediately, persisted separately during sync cycles

### Cookie-Based Preferences
Theme, view-mode, sort-mode, filter-mode stored in HTTPOnly cookies with 1-year expiration.

### CSS Architecture
- **CSS Custom Properties**: Comprehensive light/dark/auto theme system using CSS variables
- **Mobile-first responsive**: `grid-template-columns: repeat(auto-fill, minmax(380px, 1fr))` for adaptive layouts
- **Component-based design**: Reusable patterns for cards, lists, buttons, status indicators

## Authentication Architecture

### Password Authentication
- **Bcrypt hashing**: Password stored as bcrypt hash via `--web.password-hash` flag
- **Fixed username**: Always "cronn" (hardcoded)
- **Session management**: Cookie-based with `__Host-` prefix on HTTPS, configurable TTL (`--web.login-ttl`, default 24h)
- **CSRF protection**: Uses Go 1.25's built-in cross-origin protection
- **Brute-force protection**: Rate limiting via tollbooth (5 attempts per minute per IP)
- **Custom login form**: Styled login page matching dashboard theme
- **Fallback**: HTTP Basic Auth for API clients
- **Static resources**: No authentication required

## Manual Job Execution Architecture

### Trigger Mechanism
- Web UI "Run Now" button sends request to `ManualJobRequest` channel
- Channel wired to scheduler's `ManualTrigger` channel
- Background listener in scheduler processes triggers immediately
- No persistence of manual executions (runs on-demand only)

### Implementation Pattern
```go
// server creates channel
manualJobChan := make(chan string, 10)

// wired to service
svc := service.New(..., service.WithManualTrigger(manualJobChan))

// web UI sends job ID to channel
manualJobChan <- jobID
```

## Type-Safe Enum Pattern

### go-pkgz/enum Integration
Enums in `app/web/enums/` provide compile-time type safety:
```go
//go:generate moq -fmt goimports -out enums_string.go . JobStatus EventType ViewMode Theme SortMode FilterMode

type JobStatus int
//go:generate enumer -type=JobStatus -json -text -sql -values
const (
    JobStatusIdle JobStatus = iota
    JobStatusRunning
    // ...
)
```

Features:
- String conversion (String(), MarshalText(), UnmarshalText())
- JSON marshaling/unmarshaling
- SQL database compatibility (Scan(), Value())
- Values() for iteration
- Generated via: `go generate ./app/web/enums`

## Advanced Template System Architecture

### DayParser Design
Located in `app/service/day.go`, handles business day calculation:
```go
type DayParser struct {
    timeZone       *time.Location
    tmpl           tmpl
    eodHour        int  // default: 17
    skipWeekDays   []time.Weekday  // default: Sat, Sun
    holidayChecker HolidayChecker
    altTemplate    bool
}
```

### Template Variables
Standard templates: `YYYYMMDD`, `YYYY`, `YYYYMM`, `YYMMDD`, `YY`, `MM`, `DD`, `ISODATE`, `UNIX`, `UNIXMSEC`
EOD templates: `YYYYMMDDEOD`, `WYYYYMMDDEOD`
Weekday templates (W-prefix): `WYYYYMMDD`, `WYYYY`, `WYYYYMM`, `WYYMMDD`, `WISODATE`, `WYY`, `WMM`, `WDD`

### Business Day Logic
- EOD threshold (default 17:00): if time < EOD, use previous business day; else use current
- Weekday backward: skip Saturday/Sunday (configurable via `SkipWeekDays`)
- Holiday checker: interface for custom holiday calendars via `HolidayChecker`
- Alternative delimiters: `[[.YYYYMMDD]]` instead of `{{.YYYYMMDD}}` via `AltTemplateFormat` option

## SQLite Persistence Architecture

### Database Design
**Jobs table:**
- id (SHA256 hash of command), command, schedule
- next_run, last_run (DATETIME format)
- last_status (idle/running/success/failed)
- enabled, created_at, updated_at
- sort_index (for preserving job order)

**Executions table:**
- job_id, started_at, finished_at, status, exit_code, output
- Index on (job_id, started_at) for query performance

### SQLx Integration
- Uses `github.com/jmoiron/sqlx` for struct scanning and named parameters
- Pure Go `modernc.org/sqlite` driver handles DATETIME natively with time.Time
- Busy timeout: 5000ms for concurrent access
- WAL mode for better concurrency

### Single-Phase Initialization
SQLiteStore initializes tables automatically in constructor (no two-phase init anti-pattern):
```go
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
    // open DB, create tables, configure WAL - all in constructor
}
```

### Critical Startup Sequence
Server **must** call `loadJobsFromDB()` BEFORE `loadJobsFromCrontab()` to preserve execution history:
1. `loadJobsFromDB()` loads jobs with execution state (LastRun, LastStatus)
2. `loadJobsFromCrontab()` merges crontab definitions, preserving execution state from step 1
3. Smart merging: crontab updates job definition but keeps execution history

### Event Persistence Timing
Job events update memory immediately via `JobEventHandler`, but persist only when `persistJobs()` called during crontab sync (not real-time).

### Database Loading Patterns
- `loadJobsFromDB()` handles empty timestamps gracefully
- Recalculates NextRun for jobs missing schedule calculations
- Uses `schedule.Next(time.Now())` to compute next execution

## YAML Configuration Architecture

### JSON Schema Validation
- Schema embedded at compile time in `app/crontab/schema.json`
- Generated via `go generate ./app/crontab`
- Validates: job structure, schedule formats, condition thresholds
- Repeater limits: attempts (1-100), factor (1-10), duration (1ms-1h)
- Prevents `spec`/`sched` conflicts (mutually exclusive)
- Invalid configs prevent startup

### Parser Features
- Auto-detects format by file extension (.yml/.yaml vs others)
- Inline comment support in crontab lines
- Per-job repeater configuration overrides global settings
- Conditional execution via `conditions` field (YAML only)

## Testing Architecture Critical Patterns

### Persistence Testing Anti-Patterns
**Bad**: Write-only tests that verify database writes but never read back
```go
store.SaveJob(job)
// test ends - never verified job can be loaded
```

**Good**: Round-trip verification simulating restart cycles
```go
store.SaveJob(job)
loaded := store.LoadJobs()
require.Equal(t, job, loaded[0])
```

### Async Event Testing
Tests processing job events must:
1. Start `processEvents()` goroutine with context
2. Send event to channel
3. Call `persistJobs()` manually (persistence decoupled from event handling)
4. Use `require.Eventually()` instead of `time.Sleep()` for synchronization

### Test Failure Design
- Use `require.NoError()` for all operations
- **Never ignore errors** with `_` in test code
- Test must fail fast on unexpected errors

### Error Path Testing
Drop database tables after initialization to test error conditions (don't try to skip initialization).

### Web Test Lifecycle
Web tests must:
1. Start `processEvents()` goroutine with context
2. Wire event handlers properly
3. Test template error paths by creating invalid templates manually

### Event Synchronization
Use `require.Eventually()` for reliable event processing:
```go
require.Eventually(t, func() bool {
    return job.LastStatus == StatusSuccess
}, time.Second, 10*time.Millisecond)
```

## Testing and Development Workflow

### Restarting Services
**IMPORTANT**: When restarting services (especially web-enabled):
1. Kill old process: `pkill -f cronn`
2. Verify down: `curl -s http://localhost:8080` (should fail)
3. Start new instance

This prevents port conflicts and ensures clean restarts.

### Running with Web UI
```bash
./cronn -f test-crontab --web.enabled --web.address=:8080
```

### Test Coverage Best Practices
- Removing useless tests and adding behavior-focused tests improved coverage from 75.2% to 87.8%
- Template helper functions are pure (no dependencies) enabling easy testing
- Let t.Run() be self-documenting (skip comments before subtests)

## Known Issues and Solutions

### NextRun Calculation
Must parse schedule and call `schedule.Next(time.Now())` after job events to update next run time.

### Event Processing
JobEventHandler must be properly wired in main.go for events to flow from scheduler to web UI.

### Parser Configuration
Use standard 5-field cron parser (minute hour day month weekday), not 6-field seconds-based parser.

### Job Identity
Jobs identified by SHA256 hash of command - changes to command create new job identity.

## Database Architecture Patterns

### Event-Driven Persistence
Job execution history updated in memory via `JobEventHandler` interface but persisted separately during sync cycles (not real-time).

### State Reconciliation
Database state loaded first, then merged with crontab definitions to preserve execution history while respecting configuration changes.

### Timestamp Handling
Database stores DATETIME format, handled transparently by modernc.org/sqlite driver with time.Time types.

## Conditions Package Architecture

### Concurrency Control
- Semaphore pattern limits concurrent condition checks (default: 10)
- Configurable via `--max-concurrent-checks` flag or `CRONN_MAX_CONCURRENT_CHECKS` env var
- Prevents resource exhaustion when multiple jobs check conditions simultaneously

### Threshold Validation
- CPU/memory/disk thresholds validated at config parse time
- Custom script timeout default: 30s (configurable via `custom_timeout`)
- Instant measurements for system metrics (minimal performance impact)

### Postponement Logic
- Without `max_postpone`: job skipped if conditions not met
- With `max_postpone`: waits up to deadline, checking at `check_interval` (default: 30s)
- Deadline reached: job executes regardless of conditions

## Service Architecture Details

### Log Prefixer
`log_prefixer` component adds command-specific prefix to all log output for job identification.

### Error Writer
`err_writer` component handles error output from job execution separately from stdout.

### Notify Timeout
Notification operations timeout configurable via `NotifyTimeout` (default: 10s).

## Resumer Architecture

### Flag File Pattern
- Flag file naming: `<timestamp>-<sequence>.cronn`
- Content: command line of running job
- Location: `--resume=<directory>` or `$CRONN_RESUME`
- Cleanup: 24-hour cleanup of old resume files
- Atomic sequence counter ensures uniqueness
- Concurrent resume execution via `--resume-concur` flag (default: 1)

### Resume Execution Flow
1. At startup, discover all `.cronn` flag files
2. Execute sequentially or in parallel based on concurrency setting
3. Remove flag file on completion
4. Non-blocking: doesn't prevent scheduled tasks from running

## DeDup Implementation

### Thread-Safe Design
- Thread-safe map tracks active jobs by command hash
- Timestamp tracking for start time
- Mutex-based synchronization
- Jobs identified by SHA256 hash of command line
