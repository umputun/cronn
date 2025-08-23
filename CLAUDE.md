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