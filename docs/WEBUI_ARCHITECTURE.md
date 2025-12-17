# Cronn Web UI Architecture

## Overview

The web UI provides a browser-based interface for managing cron jobs without touching the command line. Users can view, create, edit, and monitor scheduled tasks through a clean, responsive interface.

## Features

### Job Management
- **View all jobs** - See all scheduled jobs with their cron expressions and commands
- **Add new jobs** - Create jobs using a simple form with cron expression validation
- **Edit existing jobs** - Modify schedule or command for any job
- **Delete jobs** - Remove jobs with confirmation
- **Enable/disable jobs** - Toggle jobs on/off without deletion
- **Manual execution** - Trigger any job to run immediately with a "Run Now" button

### Real-time Monitoring
- **Live status updates** - See which jobs are currently running, completed, or failed
- **Execution history** - View recent job runs with timestamps, duration, and exit status
- **Live output streaming** - Watch stdout/stderr in real-time as jobs execute
- **Output logs** - Click on any past execution to see its complete output
- **Success/failure indicators** - Visual status with color coding (green/red/blue)

### Live Output Streaming
When a job is running:

- Click on the running job to open a live console view
- See stdout and stderr as they happen (color-coded)
- Output auto-scrolls but can be paused to read
- Shows elapsed time and current status
- Terminal-like appearance with monospace font

### Navigation
- **Dashboard** - Overview of all jobs with their last run status
- **Jobs page** - Detailed job management interface
- **History page** - Complete execution history with filtering options

## User Experience

### Main Dashboard
Shows a table with:

- Cron expression (e.g., "*/5 * * * *") with human-readable tooltip on hover
  - Shows "Every 5 minutes" or "At 2:30 AM on weekdays"
- Command (e.g., "backup.sh /data")
- Status (enabled/disabled)
- Last run time
- Last run status (success/failed/running)
- Next scheduled run
- First seen date (how long job has existed)
- Actions (Run Now, Enable/Disable, Edit, Delete)

### Adding a Job
Simple form with:

- Schedule field with examples (tooltips showing common patterns)
- Live preview showing human-readable schedule as you type
  - "Invalid expression" → "Every day at midnight" → "Every hour on weekends"
- Command field with template variables support
- Test button to validate cron expression
- Save button

### Real-time Updates
- Jobs currently running show a pulsing blue indicator
- When a job completes, the status updates without page refresh
- New executions appear in history automatically
- Error messages display inline when jobs fail

## Technical Approach

### Minimal Structure
```
app/
├── web/
│   ├── server.go      # Everything in one file
│   └── templates/     # HTML templates
└── main.go           # Add --web flag
```

### Architecture
- Single `server.go` file containing all server logic
- Embedded HTML templates using `embed`
- SQLite database for persistent execution history
- HTMX for dynamic updates without JavaScript

### Key Design Decisions

**No WebSockets/SSE**: Simple HTMX polling is sufficient:

- Job status refreshes every 5 seconds
- Live output refreshes every 1 second when viewing
- Reduces complexity significantly

**SQLite for Persistence**: Execution history must survive restarts:

- Single file database (`cronn.db`)
- WAL mode for better concurrency
- Configurable retention policy (days/size)
- Output written incrementally during execution

**File Locking**: Prevent crontab corruption:

- Exclusive lock when writing crontab file
- Read-modify-write in atomic operation
- Prevents race conditions with CLI edits

**Minimal Frontend**: Pure HTMX, no custom JavaScript:

- All interactions via HTMX attributes
- Server returns HTML fragments
- One inline onclick for UI toggle (output viewer)

### Integration Points

**With Scheduler**: 
- Add `JobEventHandler` callback to receive job events (start/output/complete)
- Events update both memory cache and database

**With CLI**:

- Both read/write same crontab file
- Web UI changes trigger file reload
- No breaking changes to existing CLI usage

### HTTP Endpoints

Pages (return full HTML):

- `GET /` - Dashboard
- `GET /jobs` - Job management
- `GET /history` - Execution history

HTMX partials (return HTML fragments):

- `GET /partials/jobs` - Job list table
- `POST /partials/jobs` - Create job
- `DELETE /partials/jobs/{id}` - Delete job
- `POST /partials/jobs/{id}/run` - Trigger manual run
- `GET /partials/jobs/{id}/output` - Get job output (live or historical)

### Data Flow

#### Job Event Flow
1. Job execution triggers event in service.Scheduler
2. Event handler writes to SQLite incrementally
3. Output accumulates in database as job runs
4. Connected browsers poll for updates via HTMX

#### State Management
- **Job Specifications**: Synchronized with crontab file
- **Job Identity**: Tracked in SQLite based on command hash
- **Execution History**: Linked to job ID, survives schedule changes
- **Enable/Disable**: Commented/uncommented lines in crontab
- **Real-time Status**: Queried from database on each poll
- **Sync Process**: On crontab change:
  1. Parse all jobs from file
  2. Match to existing jobs by command hash
  3. Update schedules/line numbers for matches
  4. Create new job records for new commands
  5. Mark missing jobs as removed

### HTMX Patterns

#### Auto-refreshing Lists
- Job list polls `/partials/jobs` every 5 seconds
- Replaces table content with updated HTML

#### Live Output Viewing
- Click "View Output" toggles console view
- Console div polls `/partials/jobs/{id}/output` every second
- Returns pre-formatted HTML with current output

#### Form Submissions
- Create job form posts to `/partials/jobs`
- Returns updated job list HTML
- Form resets on successful submission

#### Error Handling
- Authentication failures return 401 with `HX-Redirect: /login` header
- Failed partials show inline error messages
- Network errors trigger exponential backoff
- Background tabs reduce polling frequency automatically

### Security

#### Authentication
- Basic authentication enabled by default when web UI is on
- Opt-out with explicit --web.no-auth flag
- Initial implementation: HTTP Basic Auth with multiple users
  - Users defined via CLI flags or environment variables
  - Format: `--web.users="admin:pass1,user2:pass2"`
  - Passwords hashed with bcrypt in memory
  - No sessions initially, just Basic Auth on each request
- Future: Session-based authentication with secure cookies
- HTMX-aware authentication (return HX-Redirect header on 401)

#### CSRF Protection
- Token-based CSRF protection for state-changing operations
- Tokens transmitted via X-CSRF-Token header

#### Input Validation
- Server-side validation for all inputs
- Cron expression validation
- Rate limiting on manual job execution

#### Output Safety
- Tail-like output buffer (configurable, default 1MB)
- Keeps most recent output, discards old
- Prevents memory/database exhaustion

### Persistence Strategy
- **SQLite database** - Single file `cronn.db` stores execution history and job tracking
- **WAL mode** - Better concurrent access between web and CLI
- **Retention policy** - Configurable by days (default 30) or size (default 100MB)
- **Automatic cleanup** - Runs on startup and daily
- **Audit trail** - Built-in table for tracking all changes:
  - User actions (add/edit/delete/enable/disable jobs)
  - Authentication attempts (success/failure)
  - Manual job executions
  - Timestamp, user, action, details, IP address

### Job Identity Management
- **Job ID** - SHA256 hash of command (what it does, not when)
- **Identity principle** - Command defines the job, schedule is just a property
- **Tracking table** - Stores job metadata separate from executions:
  - ID (command hash)
  - Command string
  - Current schedule
  - Current line number in crontab
  - First seen / last seen timestamps
  - Enabled/disabled state
- **Schedule changes** - Same job, updated property
- **Command changes** - New job with new identity
- **External edit detection** - Sync on file change, detect additions/removals/updates

### Output Capture
- Output written to database incrementally as job runs
- Survives crashes with partial output preserved
- Keeps last N bytes of output (default 1MB, configurable)
- Works like `tail` - old output discarded, recent output kept
- `/api/jobs/{id}/output` queries database directly
- Output viewer polls every second while job is running

### Crontab File Safety
- Exclusive file lock for all write operations
- Atomic read-modify-write pattern
- Prevents corruption from concurrent edits
- Compatible with CLI file watching
- **Comment preservation** - All user comments maintained:
  - Section headers (e.g., `# Backup jobs`)
  - Inline comments (e.g., `command # explanation`)
  - Blank lines for readability
  - Only job-specific comments modified for enable/disable

### Change Hooks (Optional)

#### Post-Change Hook
- Execute custom script after any crontab modification
- Configurable via flag: `--web.change-hook=/path/to/script.sh`
- Script receives context via environment variables and templates
- Template variables available:
  - `{{.Action}}` - add/edit/delete/enable/disable
  - `{{.Details}}` - Human-readable description of change
  - `{{.User}}` - Username who made the change (if auth enabled)
  - `{{.Timestamp}}` - When the change occurred
  - `{{.JobSpec}}` - Cron expression
  - `{{.JobCommand}}` - Command being scheduled
  - `{{.CrontabPath}}` - Path to the crontab file
  - `{{.OldValue}}` / `{{.NewValue}}` - For edit actions

#### Sync Hook  
- Execute custom script periodically to sync external changes
- Configurable via flags:
  - `--web.sync-hook=/path/to/sync.sh` - Script to run
  - `--web.sync-interval=5m` - How often to run (default: 5m)
- Template variables available:
  - `{{.CrontabPath}}` - Path to the crontab file
  - `{{.Timestamp}}` - Current time
- Return code handling:
  - 0 = Success, reload crontab if changed
  - Non-zero = Log error but continue

#### Example Hook Scripts
- **Git sync**: `cd {{dir .CrontabPath}} && git pull`
- **Mercurial sync**: `cd {{dir .CrontabPath}} && hg pull -u`
- **S3 sync**: `aws s3 cp s3://bucket/crontab {{.CrontabPath}}`
- **Central config**: `curl -o {{.CrontabPath}} https://config.internal/crontab`
- **Git push (post-change)**: `git add {{.CrontabPath}} && git commit -m "{{.Details}}" && git push`
- **Audit log (post-change)**: `echo "{{.Timestamp}} {{.User}}: {{.Details}}" >> /var/log/cronn-changes.log`

## Deployment

### Configuration
```bash
cronn --web.enabled \
      --web.address=:8080 \
      --web.users="admin:secret,viewer:readonly" \
      --web.retention-days=30 \
      --web.max-output-size=1048576
```

### Environment Variables
- `CRONN_WEB_ENABLED` - Enable web UI
- `CRONN_WEB_ADDRESS` - Bind address (default: :8080)
- `CRONN_WEB_NO_AUTH` - Disable authentication (not recommended)
- `CRONN_WEB_USERS` - Users in format "user1:pass1,user2:pass2"
- `CRONN_WEB_RETENTION_DAYS` - History retention in days (default: 30)
- `CRONN_WEB_RETENTION_SIZE_MB` - Max database size in MB (default: 100)
- `CRONN_WEB_MAX_OUTPUT_SIZE` - Max output per job in bytes (default: 1048576)
- `CRONN_WEB_CHANGE_HOOK` - Script to execute after crontab changes
- `CRONN_WEB_SYNC_HOOK` - Script to execute periodically for syncing
- `CRONN_WEB_SYNC_INTERVAL` - How often to run sync hook (default: 5m)

## Performance Considerations

- In-memory cache for active jobs reduces database queries
- Efficient HTMX polling with minimal data transfer
- Throttling middleware to prevent abuse
- Lightweight HTML fragments for updates

## Benefits

### For Users
- **No more crontab syntax errors** - Visual form with validation
- **See what's running** - Real-time visibility into job execution
- **Quick troubleshooting** - Immediate access to logs and error messages
- **Easy job management** - Add, edit, delete without editing files
- **Test runs** - Execute jobs manually to verify they work

### For Operations
- **Central monitoring** - See all scheduled tasks in one place
- **Persistent history** - Execution history survives restarts and crashes
- **Historical analysis** - Track success rates and failures over days/weeks
- **Quick fixes** - Disable or modify failing jobs instantly
- **No SSH needed** - Manage jobs from any browser

## Integration with CLI

The web UI works alongside the existing CLI:

- Both read/write the same crontab file
- Changes in web UI are visible to CLI users
- Auto-reload ensures both stay in sync
- No breaking changes to existing workflows

## Implementation Notes

### Critical Safety Features
1. **File locking is mandatory** - Without it, concurrent edits will corrupt crontab
2. **Authentication on by default** - Explicit opt-out required for security
3. **Output limits enforced** - Prevent memory/disk exhaustion
4. **Database retention** - Automatic cleanup prevents unbounded growth
5. **Job identity tracking** - Commands define jobs, schedules are mutable properties

### Simple But Safe
The architecture stays minimal while addressing all critical concerns:

- No SSE/WebSockets complexity
- No external dependencies beyond SQLite
- Single server.go file keeps it maintainable
- Safety features don't add significant complexity

### Cron Expression Translation
- Use robfig/cron's built-in parser (already a dependency)
- Generate human-readable descriptions server-side
- Return in HTML title attribute for hover tooltips
- No JavaScript needed - pure HTML/CSS solution

## Future Enhancements

- Job categories/tags for organization
- Job templates for common tasks
- Export/import job configurations
- Success rate metrics per job (leveraging job identity)
- Command preview with template expansion
- Job history timeline (schedule changes over time)
- Similar command detection for deduplication
- Database backup/restore functionality
- Resource limits (max concurrent viewers, rate limiting)
- Session-based authentication with secure cookies
- Advanced user management and roles
- Execution anomaly detection (alert when job runs longer/shorter than usual)
- Group run mode - bulk execute selected jobs with parallelism control
- Visual cron builder/helper for easier expression creation
- Execution time prediction showing next 5-10 runs
- Search and filter capabilities for job management
- Job duplication for quick similar job creation
- Timeline/calendar view of scheduled jobs