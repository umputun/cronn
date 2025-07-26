# WEBUI_TODO.md - Implementation Plan

## Phase 0: Project Setup ⬜

### 0.1 Dependencies ⬜
- [ ] Add SQLite driver (modernc.org/sqlite) to go.mod
- [ ] Add bcrypt (golang.org/x/crypto/bcrypt) to go.mod
- [ ] Add file locking library if needed
- [ ] Create app/web directory structure
- [ ] Update .gitignore for cronn.db and test databases

## Phase 1: Foundation & Security ⬜

### 1.1 Basic Server Structure ⬜
- [ ] Create `app/web/server.go` with basic HTTP server
- [ ] Add `--web.enabled` and `--web.address` flags to main.go
- [ ] Add `--web.no-auth` flag (default: false) to main.go
- [ ] Write tests for server startup/shutdown
- [ ] Test flags integration and defaults

### 1.2 Basic Authentication ⬜
- [ ] Add `--web.users` flag parsing (format: "user1:pass1,user2:pass2")
- [ ] Implement HTTP Basic Auth middleware
- [ ] Hash passwords with bcrypt in memory
- [ ] Test auth with multiple users
- [ ] Test auth bypass with --web.no-auth flag

### 1.3 Database Schema ⬜
- [ ] Design SQLite schema for jobs, executions, and audit tables
- [ ] Create database initialization in server.go
- [ ] Add WAL mode configuration
- [ ] Write schema tests
- [ ] Test concurrent access patterns

### 1.4 Template System ⬜
- [ ] Create `app/web/templates/` directory structure
- [ ] Implement base layout template with HTMX
- [ ] Add template embedding with go:embed
- [ ] Add 401 handler with HX-Redirect header
- [ ] Write template rendering tests
- [ ] Test error page rendering

## Phase 2: Job Viewing ⬜

### 2.1 Crontab File Integration ⬜
- [ ] Add file watcher for crontab changes
- [ ] Implement crontab parser integration
- [ ] Create job identity tracking (SHA256 of unresolved command with templates)
- [ ] Write file locking mechanism with proper OS support
- [ ] Implement state sync logic (crontab → SQLite)
- [ ] Test concurrent file access (CLI + Web)
- [ ] Test job identity with template commands
- [ ] Test sync logic for add/remove/schedule changes
- [ ] Test malformed crontab handling

### 2.2 Dashboard Page ⬜
- [ ] Create dashboard template with job list
- [ ] Implement `GET /` handler
- [ ] Add job status indicators (enabled/disabled)
- [ ] Show schedule and next run time
- [ ] Add human-readable cron expressions (server-side)
- [ ] Write dashboard rendering tests
- [ ] Test with various crontab formats
- [ ] Test with 100+ jobs

### 2.3 HTMX Polling ⬜
- [ ] Implement `GET /partials/jobs` endpoint
- [ ] Add 5-second polling to dashboard
- [ ] Handle empty job list gracefully
- [ ] Add proper HTMX error fragment handling
- [ ] Test partial updates
- [ ] Test polling behavior
- [ ] Test error scenarios return correct fragments

## Phase 3: Job History ⬜

### 3.1 Event Handler Integration ⬜
- [ ] Add JobEventHandler to service.Scheduler
- [ ] Capture job start/output/complete events
- [ ] Store events in SQLite incrementally
- [ ] Implement output buffering (tail-like, keep last N bytes)
- [ ] Test event capture
- [ ] Test output size limits (1MB default)
- [ ] Test output truncation preserves newest lines
- [ ] Test performance with large outputs

### 3.2 History Page ⬜
- [ ] Create history template
- [ ] Implement `GET /history` handler
- [ ] Add execution list with status/duration
- [ ] Link to output viewing
- [ ] Test history rendering
- [ ] Test with large datasets

### 3.3 Live Status Updates ⬜
- [ ] Add running job indicators
- [ ] Update job status via HTMX polling
- [ ] Show execution count per job
- [ ] Test status transitions
- [ ] Test multiple running jobs

## Phase 4: Output Viewing ⬜

### 4.1 Output Storage ⬜
- [ ] Implement incremental output writing
- [ ] Add configurable size limits
- [ ] Handle partial output on crashes
- [ ] Test output persistence
- [ ] Test size limit enforcement

### 4.2 Output Viewer ⬜
- [ ] Create output viewing template
- [ ] Implement `GET /partials/jobs/{id}/output`
- [ ] Add terminal-style formatting
- [ ] Support live streaming (1s polls)
- [ ] Test output rendering
- [ ] Test live updates

## Phase 5: Job Management ⬜

### 5.1 CSRF Protection ⬜
- [ ] Generate CSRF tokens for sessions
- [ ] Add X-CSRF-Token header validation
- [ ] Include tokens in all forms
- [ ] Test token validation on all POST endpoints
- [ ] Test HTMX integration with CSRF

### 5.2 Job Creation ⬜
- [ ] Create job form template
- [ ] Implement `POST /partials/jobs` with CSRF
- [ ] Add cron expression validation
- [ ] Update crontab file safely with format preservation
- [ ] Test form submission
- [ ] Test validation errors
- [ ] Test file locking
- [ ] Test comment preservation in crontab

### 5.3 Job Editing ⬜
- [ ] Add edit form/modal
- [ ] Implement update endpoint with CSRF
- [ ] Implement line-by-line crontab rewriting
- [ ] Preserve all comments, blank lines, and formatting
- [ ] Test schedule changes (same job ID)
- [ ] Test command changes (new job ID)
- [ ] Test identity tracking
- [ ] Test complex crontab preservation

### 5.4 Job Actions ⬜
- [ ] Implement enable/disable toggle (comment/uncomment)
- [ ] Add delete with confirmation
- [ ] Implement "Run Now" button
- [ ] Add rate limiting on manual job execution
- [ ] Test all actions preserve crontab formatting
- [ ] Test concurrent modifications
- [ ] Test rate limiting behavior

## Phase 6: Audit Trail ⬜

### 6.1 Audit Implementation ⬜
- [ ] Create audit log table in SQLite
- [ ] Log all modifications (add/edit/delete/enable/disable)
- [ ] Track user actions with timestamps
- [ ] Store IP addresses
- [ ] Test audit logging completeness
- [ ] Test with auth disabled (logs "anonymous")

## Phase 7: Enhanced Features ⬜

### 7.1 Cron Expression Live Preview ⬜
- [ ] Add live preview to job creation form
- [ ] Show human-readable text as user types
- [ ] Handle invalid expressions gracefully
- [ ] Test various expressions
- [ ] Test real-time updates

### 7.2 Database Maintenance ⬜
- [ ] Implement retention policies
- [ ] Add cleanup on startup
- [ ] Schedule daily cleanup
- [ ] Test retention logic
- [ ] Test size-based cleanup

### 7.3 Error Handling ⬜
- [ ] Add inline error messages
- [ ] Implement exponential backoff
- [ ] Handle network failures gracefully
- [ ] Test error scenarios
- [ ] Test recovery behavior

## Phase 8: Hooks System ⬜

### 8.1 Change Hook ⬜
- [ ] Parse `--web.change-hook` flag
- [ ] Implement template variables
- [ ] Execute after modifications
- [ ] Test hook execution
- [ ] Test template rendering
- [ ] Test error handling

### 8.2 Sync Hook ⬜
- [ ] Parse sync hook flags
- [ ] Implement periodic execution
- [ ] Reload on changes
- [ ] Test sync behavior
- [ ] Test various intervals

## Phase 9: Polish & Optimization ⬜

### 9.1 Performance ⬜
- [ ] Add job caching layer
- [ ] Optimize database queries
- [ ] Implement request throttling
- [ ] Test under load
- [ ] Profile memory usage

### 9.2 UI Refinements ⬜
- [ ] Improve responsive design
- [ ] Add loading indicators
- [ ] Enhance error messages
- [ ] Test on mobile
- [ ] Test accessibility

### 9.3 Documentation ⬜
- [ ] Update README with web UI section
- [ ] Document all flags
- [ ] Add example configurations
- [ ] Create troubleshooting guide

## Testing Strategy

Each task requires:
1. Unit tests for the specific functionality
2. Integration tests with the full system
3. Manual testing checklist completion
4. Update this document with ✅ when done

## Status Key
- ⬜ Not started
- 🟦 In progress  
- ✅ Completed
- ❌ Blocked

## Notes
- Each phase delivers working functionality
- Later phases can be reordered based on needs
- Keep server.go under 1000 lines if possible
- All database operations must be tested with WAL mode
- File operations must be tested for concurrency
- Job identity = SHA256 hash of unresolved command (with templates)
- Crontab manipulation must preserve all formatting, comments, blank lines