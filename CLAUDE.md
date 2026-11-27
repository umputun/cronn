# Cronn Project Architecture Knowledge

## Project Overview
Cron job scheduler with YAML/JSON and traditional crontab support, using `github.com/robfig/cron/v3` for scheduling, HTMX v2 for web UI, and SQLite with WAL mode for persistence.

## Key Components Architecture

### Core Packages
- **app/service**: Core scheduler implementation with `DayParser` for business day templates, `LogPrefixer` for command-specific logging
- **app/crontab**: Parser supporting traditional crontab and YAML/JSON with embedded JSON schema validation
- **app/conditions**: Conditional execution with semaphore-based concurrency control (default: 10 concurrent checks)
- **app/web**: Web UI server implementing `JobEventHandler` interface, uses Go templates with `html/template` and `embed`
- **app/web/enums**: Type-safe enums generated via `go-pkgz/enum` for `JobStatus`, `EventType`, `Theme`, `SortMode`, `FilterMode`
- **app/notify**: Notification service for job completion/failures
- **app/persistence**: SQLite persistence with `sqlx` and `modernc.org/sqlite` driver

### Key Dependencies
- **Scheduling**: `github.com/robfig/cron/v3` with standard 5-field parser (not seconds-based)
- **Database**: `github.com/jmoiron/sqlx` with `modernc.org/sqlite` (pure Go driver, native DATETIME support)
- **HTTP**: `github.com/go-pkgz/routegroup` and `github.com/go-pkgz/rest`
- **Rate limiting**: `github.com/didip/tollbooth/v8` for brute-force protection
- **YAML validation**: `github.com/invopop/jsonschema` for config validation
- **Frontend**: HTMX v2 for dynamic updates; the only other script is the small `static/ui.js` for focus and dialogs

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

### Pages and Templates
- Two pages, no hx-boost: the index (`GET /{$}`, `index.html`) and a job page (`GET /jobs/{id}`, `job.html`, 404 page for an unknown id). Each is its own template set: `layout.html` + the page + `partials/*.html`; `s.templates` keys are `index`, `job`, `partials` (fragments for htmx answers) and `login`
- Uses `html/template` with `embed` for static files
- **CRITICAL**: Templates and static files embedded at compile time - **must rebuild after any changes**
- Template helper functions are pure (no dependencies): `humanDuration`, `timeUntil`, `since`, `ago`, `runDuration`, `clock`, `when`, `deref`; `describeSchedule` renders cron specs in plain words via `lnquy/cron` (`app/web/schedule.go`)
- Partials: `grid.html` (index rows, header sort buttons, pills, filter/sort menu, search, jump list, list-state fields), `jobparts.html` (job title, facts, history strip, runs list, output pane, toggle toast), `runform.html` (run confirmation, stop confirmation), `icons.html`, `settings.html`, `neighbors.html`
- The job page code lives in `app/web/jobpage.go`: page, facts, runs list, strip bars, previous/next position, jump list, recorded and live output, and the wait for a started run

### List State in the URL
- The index's filter, sort, sort direction and search travel in the page URL (`filter`, `sort`, `rev`, `search`) and in hidden inputs `#filter-mode`, `#sort-mode`, `#sort-rev` (plus `#list-search` on the job page). `listQuery` reads them, falling back to the cookies, and `jobsQuery.values()` writes them into every link: rows, history bars, previous/next, the back link, the jump list and the run redirect
- Each tab sends its own state with every request, so a change in one tab never leaks into another; the theme, sort-mode and filter-mode cookies (HTTPOnly, 1 year) only seed a fresh page. Every htmx answer of the index list sets `HX-Replace-Url` to the index URL with that state, so a reload, the theme toggle's refresh and a copied address all read it back. `<body hx-history="false">` keeps htmx from snapshotting the page (commands included) into localStorage on each of those URL replacements
- **Sort**: header buttons (`#sort-default`, `#sort-lastrun`, `#sort-nextrun`) post `sort` and `rev`. The first click on a column sorts nearest to now first, a second click on the sorted column flips it; crontab order flips to reversed file order. Jobs without a value (never ran; disabled or no next run) stay last in both directions

### HTMX Integration Patterns
- **Pollers are stable wrappers swapping `innerHTML`**: index `#rows` (`GET /api/jobs`, also answers `counts-oob`), job page `#facts` (`GET /api/jobs/{id}/facts`, with `job-title-oob`, `job-runact-oob` and pill counts out of band), and the live output `#live-out`. Each poll trigger is `every 5s [!uiHeld], refresh-jobs from:body` (the facts poll also on `run-selected`); `#jruns` reloads on `runs-changed` and `run-selected`
- **Poll hold**: `window.uiHeld` is set on a primary-button `pointerdown` and cleared on `pointerup`, `pointercancel`, `contextmenu` and the window's own `blur` (not captured: an element's blur passes window in the capture phase, and a press moves focus). Every `every 5s` filter reads it, so no new poll starts between press and release (a click needs both on the same element, and htmx cannot defer a swap). A poll already in flight at the press can still land and replace the button
- **Selected run**: the run on screen is carried by the hidden `selected-run` input inside the output fragment (a recorded run's id, or `live:<start>` for a run in progress). Output answers send `HX-Trigger-After-Swap: run-selected`, so the facts and the runs list re-render with that selection; the facts and runs requests include it
- **Runs list mark**: the runs list carries a hidden `newest` input holding `runsMark`: the start of every run in progress plus the newest recorded run id. The facts response sends `HX-Trigger: runs-changed` when it differs, so the list reloads only when a run started or ended, an older overlapping run included
- **Ending a poll**: an answer that turns a live or wait poll into something else (the recorded run, a found run) uses `HX-Retarget: #joutput` with `HX-Reswap: innerHTML`, replacing the polling wrapper. 286 (htmx stop-polling) is used only for the wait timeout and for the facts of a removed job
- **Request ordering in the output pane**: clicks that fill `#joutput` sync with `hx-sync="#joutput:replace"`, the live poll with `#joutput:abort`, so a click always wins over a poll in flight. The runs list reload syncs with `#joutput:queue last`: htmx 2.0.3 throws on the answer to a request whose element was replaced in flight, so a run click aborts a pending list reload instead of being replaced by it. Not `drop`: `run-selected` fires inside the output answer's own swap, while that request still counts as in flight, and `drop` would discard the reload. htmx logs every aborted request as a console error
- **OOB from a replaceable element is lost**: htmx 2 resolves OOB targets from the requesting element's root node, so a response to a button a poll has since replaced drops its OOB updates (the main swap still lands). State that must follow a click goes inside the swapped fragment, not out of band. A control inside a polled wrapper whose answer carries OOB state (Clear search, Show all in the index empty state) syncs with `hx-sync="#rows:replace"`, so no poll lands while it is in flight
- **Event coordination**: `refresh-jobs` (toggle, stop, a run that opens nothing) refreshes every poller on the page
- **Lazy-load dropdowns**: Native `<details>/<summary>` with `hx-trigger="toggle once"` for dropdowns that fetch content on first open (used in the host menu)

### Event-Driven Architecture
- Server implements `JobEventHandler` interface to receive job events
- Events processed asynchronously via channel with goroutine
- Job state updated in memory immediately, persisted separately during sync cycles
- **Runs in progress**: `OnJobStart` carries `LiveOutput func() string` (nil when `--log.exec-max-lines=0`), a reader of the run's web capture, which spans all repeater attempts. The server keeps each run in progress in `Server.active` by job and start time, since one job can overlap itself (no `--dedup`, or a manual run with an edited command). `IsRunning` is derived from what is left there, and a finished run is recorded before it leaves the active set, so a poll finds it in one or the other. The job page reads the active snapshot before the history, so a run that finishes between the two reads still shows its output

### CSS Architecture
- **CSS Custom Properties**: light tokens in `:root`, dark under `[data-theme="dark"]`; the login page keeps its own `--color-*` tokens
- `.app` fills the viewport (`100dvh`, overflow hidden); the index list and the job page's runs list and log scroll inside it. Grid items that hold scrolling content need `min-width: 0`, or a `1fr` track grows with the content instead of scrolling
- Breakpoints (media queries): 1180px narrows the grid columns; under 1040px the pills fold into the `#fdd` filter/sort menu; under 900px the schedule column hides; under 760px the job page stacks the runs above the log; under 720px rows become two-line blocks and the grid header hides; under 600px the top bar compacts; under 480px tall the job page scrolls, with at least 240px for the log, since its header alone can fill a landscape phone. `pointer: coarse` raises buttons to 36px and rows to 48px
- The wrap toggle is a checkbox outside the swapped output; `.jlog:has(#wrap:not(:checked))` unwraps the log, so the setting survives run changes
- **Focus ring**: the global `:focus-visible` outline is drawn only after a Tab press and until the next pointer press. `ui.js` keeps that state in `data-keynav` on `<html>`, and `:where([data-keynav="false"]) :focus-visible` removes the outline. A browser treats Esc as keyboard use too, so without the gate a mouse user who closes a dialog with Esc gets a ring on the opener, and again after each poll re-focuses it. The login page loads no `ui.js` and keeps the ring

### Minimal JS (`static/ui.js`)
- Only where htmx has no attribute: the poll hold (`window.uiHeld`), the shared blink phase for running dots (status dots of jobs and runs only; the `dot key` legend dots in the filter pills and menu stay still), dialog open/close with focus restored to the opener by id (`uiDialogSwapped`, `uiDialogClosed`), live log scroll keeping (follow new lines only when at the end, keep the sideways position), the `[data-tip]` tooltip, copying the command (`uiCopyCommand`, toast), edited-command marking (`uiMarkEdited`, relabels Run), closing `<details>` menus on an outside click or Esc, scrolling the selected run into view, the live indicator (`uiPollDone`, scoped to each poller's own request) and the keyboard map
- **Keyboard map**: one capture-phase `keydown` listener; nothing while a dialog is open or in fields (except Esc, Enter in the search field, and the jump list's arrows). Keys focus and click the existing elements, so they send the same requests as the mouse; a handler returning false leaves the key to the browser (the arrows on a job page): `/` and Cmd/Ctrl+K search; on the index `j`/`k` and ↓/↑ move a `.cur` row cursor kept by job id across row swaps (Home/End to the ends, ↓ in the search field moves into the list; a move blurs the focused control so Enter opens the row), Enter opens it (in the search field: the first match, once rows answering the field's current query arrive), `r` opens its run confirmation; on a job page `j`/`k` next/previous job, `[`/`]` newer/older run, `r` Run now, Shift+E the schedule switch, Esc back to the list; `t` theme; `?` the static `#keys-dialog`. In the jump list ↑↓ move, Enter opens, Shift+Enter opens that job's run confirmation; both act only when the list's `data-q` matches the field, so a list of an older query is never acted on. Keys on a focused jump link stay out of the map, and Esc there closes the list
- `#dialog-slot` opens a swapped `<dialog>` on `hx-on::after-settle`, not after-swap: htmx binds the dialog's `hx-on:close` only at settle, and a dialog closed before that stays in the slot. The opener focus returns to is the request's own element (a form's submitter), not `document.activeElement`: Safari does not focus a clicked button

### Endpoint Contracts
- **Run**: `POST /api/jobs/{id}/run-confirm` renders the confirmation with the exact command (an edited one is marked) and the template date; a disabled or running job answers 204 with `HX-Trigger: refresh-jobs`, since Enter in the command field submits the run bar of such a job too. `POST /api/jobs/{id}/run` from htmx answers an acceptance with 202 and `HX-Redirect` to `/jobs/{id}?after=<unix micro>&<list state>`, a rejection with 200 and the re-rendered confirmation carrying the message; callers without `HX-Request` (curl, scripts) keep 202 "Job triggered" and 4xx/5xx text
- **Landing on a started run**: the job page with `after` polls `GET /api/jobs/{id}/live-output?after=` every second and takes the earliest manual run (`ExecutedCommand` set) of the job started at or after that time, in progress or recorded, so an overlapping scheduled run is never taken for it. Once found it answers with `HX-Replace-Url` (the page URL without `after`); none within 10 seconds ends the wait with 286
- **Run links**: a history bar carries `run=<exec id>`, or `run=live:<start>` for a run in progress, which finds the run recorded once it ended
- **Stop**: `GET /api/jobs/{id}/stop-form[?start=<unix micro>]` renders a confirmation bound to one run (the given start, else the newest run in progress); `POST /api/jobs/{id}/stop` with form `start` calls `RunStopper.StopRun(job command, start)`. htmx callers get an empty dialog slot plus `HX-Trigger-After-Swap: refresh-jobs`, with a toast only when the run was stopped, so a stale confirmation never errors and never reaches a newer run; other callers get 202 or 404. Both are 403 under `--web.disable-manual` or without a stopper
- **Expired session**: `authMiddleware` answers htmx requests with 401 and `HX-Redirect` to the login page, so a poll never swaps the login form into a page
- **Removed job**: the facts poll of a job a crontab sync dropped answers 286 with a removed notice, a Removed badge and no Run, so the job page stops polling; a run posted for a removed job re-renders the confirmation with the submitted values

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
- The run confirmation (row Run button, the job page's Run now, `r`, or Shift+Enter in the jump list) sends a request to the `ManualJobRequest` channel
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

### Custom Date and EOD Interaction
- The job page's command bar lets the user edit the command and enter an optional date (YYYYMMDD); any command holding a newline gets a textarea, since a text input drops newlines from its value, and the template puts a newline right after the opening tag because the parser drops one there; the date field is shown only when the command contains `{{...}}` or `[[...]]` templates. The confirmation (`run-confirm` in `partials/runform.html`) shows both before the run starts
- Empty date → `time.Now()` used for template resolution; custom date → that date is parsed at midnight via `ParseInLocation("20060102", ..., time.Local)` (hour 0)
- With a positive `--eod-hour` (default: 17), midnight is before the cutoff, so a custom date of D resolves both EOD templates to the business day before D. With `--eod-hour=0`, they use D if it is a business day, otherwise the most recent business day before D

### Stopping Runs and Shutdown
- Every attempt runs `sh -c` in its own process group (`process_unix.go`; `process_windows.go` kills the shell only; children keep running, and closing the read end cancels the pending read there too). `runCommand` gives the command one `os.Pipe` for stdout and stderr and copies it itself, so `Wait` returns at the shell's exit and the run lasts until the copy reaches EOF, as with exec's own pipes. It watches the run context until both end; on cancel it sends SIGTERM to the group, SIGKILL after `stopGrace` (5s), then closes the read end if the output is still held after another `stopGrace`, since a process that left the group (setsid, GNU timeout) gets no group TERM or KILL
- `runJobWithCommand` registers a cancel-cause per run, keyed by job command and start time (the same values the web hashes into job id and `Server.active` key); `Scheduler.StopRun` cancels with `errStopped`. The run leaves the registry right after its command returns and before the cause is read, so a later `StopRun` finds nothing, and one that reported success is recorded as a stop unless a shutdown canceled the run first. A stopped run is recorded failed with exit -1 (plus a "stopped from the dashboard" output line when output capture is on), is not retried, notifies nobody, and removes its resume marker. The cause decides, not the exit status, since a command may trap TERM and exit 0
- Shutdown (SIGTERM, SIGINT) cancels the scheduler context, which every run context derives from, so running groups are terminated the same way. `runCommand` reports a terminated command as `errCanceled` whatever its exit status, and an interrupted scheduled, manual or resumed run skips notification and keeps its resume marker. `Do` waits for cron's own stop, then `waitRuns` joins scheduled, manual and resumed runs and refuses new ones
- main creates `*service.Scheduler` before the web server, which takes it as `RunStopper`, fills it in, and only then starts serving HTTP

## Job Enable/Disable Toggle

### Toggle Mechanism
- The job page's schedule switch (`POST /api/jobs/{id}/toggle`, or Shift+E) flips `job.Enabled` in memory
- State persisted immediately via `persistJobs()` after toggle
- An htmx toggle answers with a toast swapped into `#toasts` and `HX-Trigger-After-Swap: refresh-jobs`, which refreshes the page
- Disabled rows get `data-state="off"`, no next run and no Run button; the job page shows a note in place of Run and keeps the command editable
- All lists every job, Disabled only disabled ones; Failing, Running, OK and Never list enabled jobs only

## Type-Safe Enum Pattern

### go-pkgz/enum Integration
Enums in `app/web/enums/` provide compile-time type safety:
```go
//go:generate moq -fmt goimports -out enums_string.go . JobStatus EventType Theme SortMode FilterMode

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
- name (the YAML `name`, empty when unset)
- last_status (idle/running/success/failed)
- last_exit_code (nullable: NULL until a finished run is known; -1 is a real signal exit), last_duration
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
- `migrate()` adds missing columns, then `backfillLastResult` fills `last_exit_code`/`last_duration` of jobs whose code is NULL from their latest retained execution. It runs on every start and never touches a known value, so an interrupted upgrade still recovers
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
- Empty file handling: empty files, whitespace-only files, and `jobs: []` return empty list without error (enables zero-jobs update mode)

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

### E2E Test Requirement
**CRITICAL: Every UI change must ship with corresponding e2e tests.** Any change to templates (`app/web/templates/`), static assets (`app/web/static/*.css`, `*.js`), or web handlers that affects rendered output requires an e2e test asserting the new behavior in the browser — unit tests on handlers alone are not sufficient.

- Tests live in `e2e/` as Go + Playwright (`//go:build e2e` tag), organized by feature (`e2e_test.go`, `controls_test.go`, `toggle_test.go`, etc.), not one-file-per-source
- Run with `make e2e` (headless) or `make e2e-ui` (visible browser for debugging)
- `TestMain` builds the binary once and starts a **single shared server + DB** for the whole run; tests share state and run in file/definition order
- **Shared-state pattern**: because other tests mutate job state, assert invariants rather than fixed numbers to avoid order-dependent flakiness. Example: verify `total == running + success + failed + idle` instead of hardcoding each count
- The e2e crontab (`createTestCrontab` in `e2e_test.go`) includes a `*/5` job, so a job may flip to `success` if a run crosses a 5-minute boundary — assert invariants, never an all-idle baseline. Its manual-only "Live tail" job prints six lines a second for 30s, long enough for the four live-output polls its scroll checks need; tests that start it wait for its previous run to end, and job counts in assertions come from `totalJobs`. "Templated job" must stay never-ran (the never-ran assertions use it); runs that need a templated command use "Dated run"
- **Bound every test run by scope and time**: the full e2e suite is slow (~50s per run). Target specific tests with `-run 'TestName'` and always pass `-timeout`; never loop the full suite repeatedly to chase a flake. To confirm a flake fix, run the single affected test, not the whole suite

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
