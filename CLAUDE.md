# Cronn Project Knowledge

## Project Architecture
- Cron job scheduler with support for YAML/JSON configuration and traditional crontab format
- Service uses `github.com/robfig/cron/v3` for scheduling
- Web UI uses HTMX v2 for dynamic updates without JavaScript
- SQLite with WAL mode for job history persistence

## Key Components
- **app/service**: Core scheduler implementation
- **app/crontab**: Crontab and YAML/JSON parser
- **app/conditions**: Conditional execution logic
- **app/web**: Web UI server with dashboard
- **app/notify**: Notification service for job completion/failures

## Web UI Architecture
- Server implements `JobEventHandler` interface to receive job events
- Uses Go templates with `html/template` and `embed` for static files
- HTMX for 5-second auto-refresh polling
- Cookie-based persistence for theme and view preferences
- SHA256 hashing for job identity tracking

## Testing and Development Workflow

### Restarting Services
**IMPORTANT**: When restarting services (especially web-enabled ones):
1. First kill the old process: `pkill -f cronn`
2. Verify it's down: `curl -s http://localhost:8080` (should fail)
3. Only then start the new instance

This prevents port conflicts and ensures clean restarts.

### Running with Web UI
```bash
./cronn -f test-crontab --web.enabled --web.address=:8080
```

### Important Configuration
- Web update interval defaults to 30s when not specified
- Standard cron parser uses 5 fields (minute hour day month weekday), not 6
- Jobs are identified by SHA256 hash of their command

## Known Issues and Solutions
- NextRun calculation: Must parse schedule and call `schedule.Next(time.Now())` after job events
- Event processing: JobEventHandler must be properly wired in main.go
- Parser configuration: Use standard 5-field cron parser, not seconds-based

## Database Schema
Jobs table stores:
- id (SHA256 hash)
- command, schedule
- next_run, last_run (Unix timestamps)
- last_status (idle/running/success/failed)
- enabled, created_at, updated_at

## Web UI Testing Best Practices
- **Event synchronization**: Use `require.Eventually()` instead of `time.Sleep()` for reliable event processing tests
- **Web test lifecycle**: Tests must start `processEvents()` goroutine with context to handle job events
- **Template error testing**: Create invalid templates manually to test error paths in render methods
- **Test coverage improvement**: Removing useless tests and adding behavior-focused tests improved coverage from 75.2% to 87.8%

## HTMX Integration Patterns
- **Out-of-band updates**: Uses `hx-swap-oob="innerHTML"` for updating multiple elements in single response
- **Auto-refresh polling**: `hx-trigger="load, every 5s"` provides JavaScript-free real-time updates
- **Response coordination**: `HX-Refresh: true` header triggers full page refresh when state changes require it
- **Event coordination**: Custom events like `refresh-jobs` coordinate updates between components

## Web UI Architecture Patterns
- **Cookie-based preferences**: Theme, view-mode, sort-mode stored in HTTPOnly cookies with 1-year expiration
- **Template helper functions**: Pure functions (`humanTime`, `humanDuration`, `timeUntil`, `truncate`) with no dependencies enable easy testing
- **Status parameter anti-pattern**: Method parameters that always receive same value indicate design smell (removed from render method)

## CSS Architecture
- **CSS Custom Properties theming**: Comprehensive light/dark/auto theme system using CSS variables
- **Mobile-first responsive**: `grid-template-columns: repeat(auto-fill, minmax(380px, 1fr))` for adaptive card layouts
- **Component-based design**: Reusable CSS patterns for cards, lists, buttons, and status indicators

## SQLite Persistence Architecture
- **Critical startup sequence**: Server must call `loadJobsFromDB()` BEFORE `loadJobsFromCrontab()` to preserve execution history
- **Smart state merging**: `loadJobsFromCrontab()` preserves database-loaded execution state (LastRun, LastStatus) when updating job definitions  
- **Event persistence timing**: Job events are persisted to database only when `persistJobs()` is called during crontab sync, not immediately after events
- **Database loading patterns**: `loadJobsFromDB()` handles SQL NULL timestamps gracefully and recalculates NextRun for jobs missing schedule calculations

## Testing Architecture Critical Patterns
- **Persistence testing anti-pattern**: Write-only tests that verify database writes but never test reading data back miss critical failures
- **Round-trip verification requirement**: Persistence systems require tests that simulate complete restart cycles (write -> restart -> read -> verify)
- **Async event testing**: Tests processing job events must start `processEvents()` goroutine AND call `persistJobs()` manually since persistence is decoupled from event handling
- **Test failure design**: Tests must use `require.NoError()` for all operations - never ignore errors with `_` in test code

## Database Architecture Patterns
- **Event-driven persistence**: Job execution history is updated in memory via `JobEventHandler` interface but persisted separately during sync cycles
- **State reconciliation**: Database state is loaded first, then merged with crontab definitions to preserve execution history while respecting configuration changes
- **Timestamp handling**: Database stores Unix timestamps, with `sql.NullInt64` for optional last_run values and zero-value handling in business logic