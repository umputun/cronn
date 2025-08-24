# WEBUI_TODO.md - Implementation Plan

## Current Status: Minimal Implementation Complete

## Phase 0: Project Setup ✅

### 0.1 Dependencies ✅
- [x] Add SQLite driver (modernc.org/sqlite) to go.mod
- [x] Add file locking library if needed
- [x] Create app/web directory structure
- [x] Update .gitignore for cronn.db and test databases

## Phase 1: Foundation & Security 🟦

### 1.1 Basic Server Structure ✅
- [x] Create `app/web/server.go` with basic HTTP server
- [x] Add `--web.enabled` and `--web.address` flags to main.go
- [x] Add `--web.no-auth` flag (default: true for minimal version) to main.go
- [x] Write tests for server startup/shutdown
- [x] Test flags integration and defaults

### 1.2 Basic Authentication ⬜ (Skipped for minimal version)
- [ ] Add `--web.users` flag parsing (format: "user1:pass1,user2:pass2")
- [ ] Implement HTTP Basic Auth middleware
- [ ] Hash passwords with bcrypt in memory
- [ ] Test auth with multiple users
- [ ] Test auth bypass with --web.no-auth flag

### 1.3 Database Schema ✅
- [x] Design SQLite schema for jobs, executions tables
- [x] Create database initialization in server.go
- [x] Add WAL mode configuration
- [x] Write schema tests
- [x] Test concurrent access patterns

### 1.4 Template System ✅
- [x] Create `app/web/templates/` directory structure
- [x] Implement base layout template with HTMX v2
- [x] Add template embedding with go:embed
- [x] Write template rendering tests
- [x] Test error page rendering

## Phase 2: Job Viewing ✅

### 2.1 Crontab File Integration ✅
- [x] Add file watcher for crontab changes (via syncJobs)
- [x] Implement crontab parser integration
- [x] Create job identity tracking (SHA256 of command)
- [x] Implement state sync logic (crontab → memory → SQLite)
- [x] Test concurrent file access (CLI + Web)
- [x] Test job identity with commands
- [x] Test sync logic for add/remove/schedule changes

### 2.2 Dashboard Page ✅
- [x] Create dashboard template with job list (cards and list views)
- [x] Implement `GET /` handler
- [x] Add job status indicators (running/success/failed/idle)
- [x] Show schedule and next run time
- [x] Add human-readable cron expressions (server-side)
- [x] Write dashboard rendering tests
- [x] Test with various crontab formats
- [x] Add view mode toggle (cards/list)
- [x] Add theme toggle (light/dark/auto)

### 2.3 HTMX Polling ✅
- [x] Implement `GET /api/jobs` endpoint (partials endpoint)
- [x] Add 5-second polling to dashboard
- [x] Handle empty job list gracefully
- [x] Add proper HTMX error fragment handling
- [x] Test partial updates
- [x] Test polling behavior

## Phase 3: Job History ✅

### 3.1 Event Handler Integration ✅
- [x] Add JobEventHandler interface to service.Scheduler
- [x] Capture job start/complete events
- [x] Store events in memory and persist to SQLite
- [x] Test event capture
- [x] Integrate with main.go

### 3.2 History Page 🟦
- [x] Store execution history in database
- [ ] Create history template
- [ ] Implement `GET /history` handler
- [ ] Add execution list with status/duration
- [ ] Link to output viewing
- [ ] Test history rendering
- [ ] Test with large datasets

### 3.3 Live Status Updates ✅
- [x] Add running job indicators
- [x] Update job status via HTMX polling
- [x] Show last run time per job
- [x] Test status transitions
- [x] Test multiple running jobs

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

### Completed Testing ✅
- Unit tests for web server initialization
- Unit tests for JobEventHandler implementation
- Unit tests for template parsing
- Unit tests for database operations
- Integration tests for HTTP handlers
- Manual testing with Playwright

### Remaining Testing ⬜
- Load testing with 100+ jobs
- Edge case testing for malformed crontab
- Concurrent modification testing
- Performance profiling

## Status Key
- ⬜ Not started
- 🟦 In progress  
- ✅ Completed
- ❌ Blocked

## Implementation Notes

### What was implemented:
1. **Web Server** (`app/web/web.go`):
   - HTTP server with embedded templates and static files
   - JobEventHandler interface implementation for scheduler integration
   - SQLite database for job persistence
   - Event-driven architecture with channels
   - Cookie-based preferences (theme, view mode)

2. **Templates** (HTMX v2):
   - Base layout with theme and view mode toggles
   - Dashboard with stats bar
   - Jobs display (cards and list views)
   - Auto-refresh every 5 seconds via HTMX polling

3. **Integration**:
   - Direct implementation of JobEventHandler (no adapter pattern)
   - Seamless integration with existing scheduler
   - Crontab file synchronization
   - Real-time job status updates

4. **Testing**:
   - Comprehensive unit tests
   - Integration tests with httptest
   - Playwright UI testing

### Architecture Decisions:
- Used direct JobEventHandler implementation instead of adapter pattern (simpler)
- Event-driven updates via channels for real-time status
- Cookie-based preferences for user settings
- Standard cron parser (5 fields) instead of seconds-based (6 fields)
- SHA256 hashing for job identity tracking

### Next Steps for Full Implementation:
1. Add authentication and CSRF protection
2. Implement job management (create/edit/delete)
3. Add job output capture and viewing
4. Implement audit trail
5. Add hooks system for external integrations