# JSON API for History and Logs Implementation Plan

## Overview

Add JSON API endpoints for job execution history and logs to enable CLI/programmatic access. Mirrors existing HTMX endpoints with JSON responses.

## Context

- Files involved: `app/web/handlers.go`, `app/web/web.go`
- New files: `app/web/api.go`, `app/web/api_test.go`
- Related patterns: existing `handleAPIStatus` in handlers.go
- Dependencies: `github.com/go-pkgz/rest` for JSON rendering

## Tasks

### 1. Create api.go with JSON API code

**Files:**
- Create: `app/web/api.go`
- Modify: `app/web/handlers.go` (remove API types and handler)

- [x] Create `app/web/api.go` with response types:
  - Move `APIStatusResponse`, `APIJob`, `APIStats` from handlers.go
  - Add `APIExecution` (execution without output field)
  - Add `APIHistoryResponse` (job info + executions list)
  - Add `APILogsResponse` (single execution with output)
- [x] Move `handleAPIStatus` handler from handlers.go to api.go
- [x] Add helper function `toAPIExecution` for conversion
- [x] Add `handleAPIJobHistory` handler:
  - Validate job ID, check job exists
  - Fetch executions from store (limit 50)
  - Convert to API format, return JSON
- [x] Add `handleAPIExecutionLogs` handler:
  - Validate job ID and execution ID
  - Fetch execution, verify belongs to job
  - Return full execution with output as JSON
- [x] Remove moved code from handlers.go

### 2. Wire routes

**Files:**
- Modify: `app/web/web.go`

- [x] Add route `GET /jobs/{id}/history` to `/api/v1` mount
- [x] Add route `GET /jobs/{id}/executions/{exec_id}/logs` to `/api/v1` mount
- [x] Verify tests pass

### 3. Add tests

**Files:**
- Create: `app/web/api_test.go`

- [ ] Add `TestHandleAPIStatus` - verify existing endpoint still works
- [ ] Add `TestHandleAPIJobHistory`:
  - Success case with executions
  - Job not found (404)
  - Empty history (empty array)
- [ ] Add `TestHandleAPIExecutionLogs`:
  - Success case with output
  - Execution not found (404)
  - Job ID mismatch (403)
- [ ] Verify all tests pass

### 4. Final Validation

- [ ] Run full test suite: `go test ./...`
- [ ] Run linter: `golangci-lint run`
- [ ] Move plan to `docs/plans/completed/`
