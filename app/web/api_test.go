package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

func TestHandleAPIStatus(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{DBPath: dbPath, UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir)}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx := t.Context()
	go server.processEvents(ctx)

	// add test jobs with different statuses
	startTime := time.Now()

	// job 1: success
	server.OnJobStart(request.OnJobStart{Command: "echo success", ExecutedCommand: "echo success", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{Command: "echo success", ExecutedCommand: "echo success", Schedule: "* * * * *", StartTime: startTime, EndTime: startTime.Add(time.Second), ExitCode: 0, Output: "", Err: nil})

	// job 2: failed
	server.OnJobStart(request.OnJobStart{Command: "echo failed", ExecutedCommand: "echo failed", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour)})
	server.OnJobComplete(request.OnJobComplete{Command: "echo failed", ExecutedCommand: "echo failed", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour), EndTime: startTime.Add(-time.Hour + time.Second), ExitCode: 1, Output: "", Err: fmt.Errorf("failed")})

	// job 3: running
	server.OnJobStart(request.OnJobStart{Command: "echo running", ExecutedCommand: "echo running", Schedule: "@daily", StartTime: startTime})

	// wait for events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		return len(server.jobs) == 3
	}, time.Second, 10*time.Millisecond)

	t.Run("returns valid json with jobs and stats", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/status", http.NoBody)
		w := httptest.NewRecorder()

		server.handleAPIStatus(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var apiResp APIStatusResponse
		err := json.NewDecoder(w.Body).Decode(&apiResp)
		require.NoError(t, err)

		// verify jobs
		assert.Len(t, apiResp.Jobs, 3)

		// verify stats
		assert.Equal(t, 3, apiResp.Stats.Total)
		assert.Equal(t, 1, apiResp.Stats.Running)
		assert.Equal(t, 1, apiResp.Stats.Success)
		assert.Equal(t, 1, apiResp.Stats.Failed)
		assert.Equal(t, 0, apiResp.Stats.Idle)

		// verify timestamp is recent
		assert.WithinDuration(t, time.Now(), apiResp.Timestamp, time.Second)

		// verify job data
		jobMap := make(map[string]APIJob)
		for _, j := range apiResp.Jobs {
			jobMap[j.Command] = j
		}

		successJob := jobMap["echo success"]
		assert.Equal(t, "success", successJob.LastStatus)
		assert.False(t, successJob.IsRunning)
		assert.True(t, successJob.Enabled)
		assert.Equal(t, "* * * * *", successJob.Schedule)

		failedJob := jobMap["echo failed"]
		assert.Equal(t, "failed", failedJob.LastStatus)
		assert.False(t, failedJob.IsRunning)

		runningJob := jobMap["echo running"]
		assert.Equal(t, "running", runningJob.LastStatus)
		assert.True(t, runningJob.IsRunning)
	})

	t.Run("empty jobs returns empty array not null", func(t *testing.T) {
		// create new server with no jobs
		emptyServer, err := New(Config{
			DBPath:         filepath.Join(tmpDir, "empty.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			JobsProvider:   createTestProvider(t, tmpDir),
		})
		require.NoError(t, err)
		defer emptyServer.store.Close()

		req := httptest.NewRequest("GET", "/api/v1/status", http.NoBody)
		w := httptest.NewRecorder()

		emptyServer.handleAPIStatus(w, req)

		var apiResp APIStatusResponse
		err = json.NewDecoder(w.Body).Decode(&apiResp)
		require.NoError(t, err)

		assert.NotNil(t, apiResp.Jobs)
		assert.Empty(t, apiResp.Jobs)
		assert.Equal(t, 0, apiResp.Stats.Total)
	})
}

func TestHandleAPIJobHistory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{DBPath: dbPath, UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir)}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// add test job
	testJob := persistence.JobInfo{
		ID:         "test-job-id",
		Command:    "echo test",
		Schedule:   "* * * * *",
		Enabled:    true,
		LastStatus: enums.JobStatusIdle,
	}
	server.jobsMu.Lock()
	server.jobs[testJob.ID] = testJob
	server.jobsMu.Unlock()

	t.Run("success case with executions", func(t *testing.T) {
		baseTime := time.Now()

		// record executions
		err = server.store.RecordExecution(request.RecordExecution{
			JobID: "test-job-id", StartedAt: baseTime.Add(-5 * time.Minute), FinishedAt: baseTime.Add(-4 * time.Minute),
			Status: enums.JobStatusSuccess, ExitCode: 0, ExecutedCommand: "echo test1", Output: "output1",
		})
		require.NoError(t, err)

		err = server.store.RecordExecution(request.RecordExecution{
			JobID: "test-job-id", StartedAt: baseTime.Add(-2 * time.Minute), FinishedAt: baseTime.Add(-1 * time.Minute),
			Status: enums.JobStatusFailed, ExitCode: 1, ExecutedCommand: "echo test2", Output: "output2",
		})
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/v1/jobs/test-job-id/history", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server.handleAPIJobHistory(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var apiResp APIHistoryResponse
		err = json.NewDecoder(w.Body).Decode(&apiResp)
		require.NoError(t, err)

		// verify job info
		assert.Equal(t, "test-job-id", apiResp.Job.ID)
		assert.Equal(t, "echo test", apiResp.Job.Command)
		assert.Equal(t, "* * * * *", apiResp.Job.Schedule)
		assert.True(t, apiResp.Job.Enabled)

		// verify executions
		assert.Len(t, apiResp.Executions, 2)

		// executions should be ordered by started_at DESC (most recent first)
		assert.Equal(t, "failed", apiResp.Executions[0].Status)
		assert.Equal(t, 1, apiResp.Executions[0].ExitCode)
		assert.Equal(t, "echo test2", apiResp.Executions[0].ExecutedCommand)

		assert.Equal(t, "success", apiResp.Executions[1].Status)
		assert.Equal(t, 0, apiResp.Executions[1].ExitCode)
		assert.Equal(t, "echo test1", apiResp.Executions[1].ExecutedCommand)
	})

	t.Run("job not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs/nonexistent/history", http.NoBody)
		req.SetPathValue("id", "nonexistent")
		w := httptest.NewRecorder()

		server.handleAPIJobHistory(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "job not found", errResp["error"])
	})

	t.Run("empty history returns empty array", func(t *testing.T) {
		// add job without executions
		emptyJob := persistence.JobInfo{
			ID:         "empty-job-id",
			Command:    "echo empty",
			Schedule:   "0 * * * *",
			Enabled:    true,
			LastStatus: enums.JobStatusIdle,
		}
		server.jobsMu.Lock()
		server.jobs[emptyJob.ID] = emptyJob
		server.jobsMu.Unlock()

		req := httptest.NewRequest("GET", "/api/v1/jobs/empty-job-id/history", http.NoBody)
		req.SetPathValue("id", "empty-job-id")
		w := httptest.NewRecorder()

		server.handleAPIJobHistory(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var apiResp APIHistoryResponse
		err = json.NewDecoder(w.Body).Decode(&apiResp)
		require.NoError(t, err)

		assert.Equal(t, "empty-job-id", apiResp.Job.ID)
		assert.NotNil(t, apiResp.Executions)
		assert.Empty(t, apiResp.Executions)
	})

	t.Run("missing job id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs//history", http.NoBody)
		// note: not setting path value simulates empty job ID
		w := httptest.NewRecorder()

		server.handleAPIJobHistory(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "job ID required", errResp["error"])
	})
}

func TestHandleAPIExecutionLogs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:          dbPath,
		UpdateInterval:  time.Minute,
		Version:         "test",
		JobsProvider:    createTestProvider(t, tmpDir),
		ExecMaxLogLines: 100,
		LogExecMaxHist:  50,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx := t.Context()
	go server.processEvents(ctx)

	// create test execution records
	jobID := HashCommand("echo test1")
	startTime := time.Now().Add(-1 * time.Minute)
	endTime := time.Now()

	server.OnJobStart(request.OnJobStart{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{
		Command: "echo test1", ExecutedCommand: "echo test1 expanded", Schedule: "* * * * *",
		StartTime: startTime, EndTime: endTime, ExitCode: 0, Output: "test output line 1\ntest output line 2", Err: nil,
	})

	// wait for event processing
	require.Eventually(t, func() bool {
		execs, errGet := server.store.GetExecutions(jobID, 10)
		return errGet == nil && len(execs) == 1
	}, time.Second, 10*time.Millisecond)

	// get execution ID from database
	executions, err := server.store.GetExecutions(jobID, 10)
	require.NoError(t, err)
	require.Len(t, executions, 1)
	execID := executions[0].ID

	t.Run("success case with output", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs/"+jobID+"/executions/"+fmt.Sprint(execID)+"/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", fmt.Sprint(execID))
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		var apiResp APILogsResponse
		err = json.NewDecoder(w.Body).Decode(&apiResp)
		require.NoError(t, err)

		assert.Equal(t, execID, apiResp.ID)
		assert.Equal(t, jobID, apiResp.JobID)
		assert.Equal(t, "success", apiResp.Status)
		assert.Equal(t, 0, apiResp.ExitCode)
		assert.Equal(t, "echo test1 expanded", apiResp.ExecutedCommand)
		assert.Equal(t, "test output line 1\ntest output line 2", apiResp.Output)
		assert.False(t, apiResp.StartedAt.IsZero())
		assert.False(t, apiResp.FinishedAt.IsZero())
	})

	t.Run("execution not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs/"+jobID+"/executions/99999/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", "99999")
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "execution not found", errResp["error"])
	})

	t.Run("job ID mismatch returns 404", func(t *testing.T) {
		wrongJobID := HashCommand("echo wrong")

		req := httptest.NewRequest("GET", "/api/v1/jobs/"+wrongJobID+"/executions/"+fmt.Sprint(execID)+"/logs", http.NoBody)
		req.SetPathValue("id", wrongJobID)
		req.SetPathValue("exec_id", fmt.Sprint(execID))
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "execution not found", errResp["error"])
	})

	t.Run("missing job id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs//executions/1/logs", http.NoBody)
		req.SetPathValue("exec_id", "1")
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "job ID and execution ID required", errResp["error"])
	})

	t.Run("missing execution id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs/"+jobID+"/executions//logs", http.NoBody)
		req.SetPathValue("id", jobID)
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "job ID and execution ID required", errResp["error"])
	})

	t.Run("invalid execution id returns 400", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/jobs/"+jobID+"/executions/invalid/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", "invalid")
		w := httptest.NewRecorder()

		server.handleAPIExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp map[string]string
		err = json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Equal(t, "invalid execution ID", errResp["error"])
	})
}
