package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_IntegrationHandlers(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab file
	crontabContent := `# test crontab
0 * * * * echo hourly
*/5 * * * * echo five-minutes
@daily echo daily`
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0o600)
	require.NoError(t, err)

	cfg := Config{
		Address:        ":0",
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// start server in background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if runErr := server.Run(ctx, ":0"); runErr != nil && runErr != http.ErrServerClosed {
			t.Errorf("server run error: %v", runErr)
		}
	}()

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	// simulate some job events
	server.OnJobStart("echo hourly", "0 * * * *", time.Now())
	time.Sleep(10 * time.Millisecond)
	server.OnJobComplete("echo hourly", "0 * * * *", time.Now().Add(-time.Second), time.Now(), 0, nil)

	server.OnJobStart("echo five-minutes", "*/5 * * * *", time.Now())
	time.Sleep(10 * time.Millisecond)
	server.OnJobComplete("echo five-minutes", "*/5 * * * *", time.Now().Add(-time.Second), time.Now(), 1, fmt.Errorf("test error"))

	t.Run("dashboard", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		w := httptest.NewRecorder()

		server.handleDashboard(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

		body := w.Body.String()
		assert.Contains(t, body, "Cronn Dashboard")
		assert.Contains(t, body, "htmx")
		assert.Contains(t, body, "hx-get=\"/api/jobs\"")
	})

	t.Run("jobs partial - cards view", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
		w := httptest.NewRecorder()

		server.handleJobsPartial(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := w.Body.String()
		assert.Contains(t, body, "job-card")
		assert.Contains(t, body, "echo hourly")
		assert.Contains(t, body, "echo five-minutes")
		assert.Contains(t, body, "echo daily")
	})

	t.Run("jobs partial - list view", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "view-mode", Value: "list"})
		w := httptest.NewRecorder()

		server.handleJobsPartial(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body := w.Body.String()
		// list view renders a table
		assert.Contains(t, body, "jobs-table")
		assert.Contains(t, body, "echo hourly")
		assert.Contains(t, body, "echo five-minutes")
		assert.Contains(t, body, "echo daily")
	})

	t.Run("theme toggle", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/toggle-theme", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "theme", Value: "auto"})
		w := httptest.NewRecorder()

		server.handleThemeToggle(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		cookies := resp.Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "theme", cookies[0].Name)
		assert.Equal(t, "light", cookies[0].Value)
	})

	t.Run("view mode toggle", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/toggle-view", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
		w := httptest.NewRecorder()

		server.handleViewModeToggle(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		cookies := resp.Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "view-mode", cookies[0].Name)
		assert.Equal(t, "list", cookies[0].Value)
	})
}

func TestServer_ProcessEvents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":0",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// test event processing
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start event processor
	go server.processEvents(ctx)

	// send job start event
	server.eventChan <- JobEvent{
		JobID:     HashCommand("test command"),
		Command:   "test command",
		Schedule:  "* * * * *",
		EventType: "started",
		StartedAt: time.Now(),
	}

	// give time to process
	time.Sleep(50 * time.Millisecond)

	// verify job was added
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("test command")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "test command", job.Command)
	assert.True(t, job.IsRunning)

	// send job complete event
	server.eventChan <- JobEvent{
		JobID:      HashCommand("test command"),
		Command:    "test command",
		Schedule:   "* * * * *",
		EventType:  "completed",
		ExitCode:   0,
		FinishedAt: time.Now(),
	}

	// give time to process
	time.Sleep(50 * time.Millisecond)

	// verify job was updated
	server.jobsMu.RLock()
	job, exists = server.jobs[HashCommand("test command")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, "success", job.LastStatus)
}

func TestServer_PersistJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":0",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// add test jobs to memory
	now := time.Now()
	server.jobsMu.Lock()
	server.jobs[HashCommand("test1")] = &JobInfo{
		ID:         HashCommand("test1"),
		Command:    "test1",
		Schedule:   "* * * * *",
		LastRun:    now,
		LastStatus: "success",
		IsRunning:  false,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	server.jobs[HashCommand("test2")] = &JobInfo{
		ID:         HashCommand("test2"),
		Command:    "test2",
		Schedule:   "@daily",
		LastRun:    now,
		LastStatus: "failed",
		IsRunning:  false, // not running so LastStatus should persist as "failed"
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	server.jobsMu.Unlock()

	// persist jobs
	server.persistJobs()

	// verify jobs were persisted to database
	rows, err := server.db.Query("SELECT command, schedule, last_status FROM jobs ORDER BY command")
	require.NoError(t, err)
	defer rows.Close()

	var jobs []struct {
		Command  string
		Schedule string
		Status   string
	}
	for rows.Next() {
		var job struct {
			Command  string
			Schedule string
			Status   string
		}
		err = rows.Scan(&job.Command, &job.Schedule, &job.Status)
		require.NoError(t, err)
		jobs = append(jobs, job)
	}

	assert.Len(t, jobs, 2)
	assert.Equal(t, "test1", jobs[0].Command)
	assert.Equal(t, "* * * * *", jobs[0].Schedule)
	assert.Equal(t, "success", jobs[0].Status)
	assert.Equal(t, "test2", jobs[1].Command)
	assert.Equal(t, "@daily", jobs[1].Schedule)
	assert.Equal(t, "failed", jobs[1].Status)

	// CRITICAL: Test round-trip - clear memory and reload from database
	server.jobsMu.Lock()
	originalJob1 := *server.jobs[HashCommand("test1")] // save for comparison
	originalJob2 := *server.jobs[HashCommand("test2")]
	server.jobs = make(map[string]*JobInfo) // clear all jobs
	server.jobsMu.Unlock()

	// load jobs back from database
	server.loadJobsFromDB()

	// verify round-trip persistence worked
	server.jobsMu.RLock()
	defer server.jobsMu.RUnlock()

	require.Len(t, server.jobs, 2, "should restore 2 jobs from database")

	restoredJob1, exists1 := server.jobs[HashCommand("test1")]
	require.True(t, exists1, "test1 should exist after database reload")
	assert.Equal(t, originalJob1.Command, restoredJob1.Command)
	assert.Equal(t, originalJob1.Schedule, restoredJob1.Schedule)
	assert.Equal(t, originalJob1.LastStatus, restoredJob1.LastStatus)
	assert.Equal(t, originalJob1.LastRun.Unix(), restoredJob1.LastRun.Unix())

	restoredJob2, exists2 := server.jobs[HashCommand("test2")]
	require.True(t, exists2, "test2 should exist after database reload")
	assert.Equal(t, originalJob2.Command, restoredJob2.Command)
	assert.Equal(t, originalJob2.Schedule, restoredJob2.Schedule)
	assert.Equal(t, originalJob2.LastStatus, restoredJob2.LastStatus)
	assert.Equal(t, originalJob2.LastRun.Unix(), restoredJob2.LastRun.Unix())
}

func TestServer_SyncJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create initial crontab file
	crontabContent := `0 * * * * echo hourly`
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0o600)
	require.NoError(t, err)

	cfg := Config{
		Address:        ":0",
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: 100 * time.Millisecond,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start sync in background
	go server.syncJobs(ctx)

	// give initial sync time
	time.Sleep(150 * time.Millisecond)

	// verify initial job was loaded
	server.jobsMu.RLock()
	assert.Len(t, server.jobs, 1)
	server.jobsMu.RUnlock()

	// update crontab file
	crontabContent = `0 * * * * echo hourly
*/5 * * * * echo five-minutes`
	err = os.WriteFile(crontabFile, []byte(crontabContent), 0o600)
	require.NoError(t, err)

	// wait for sync
	time.Sleep(200 * time.Millisecond)

	// verify jobs were updated
	server.jobsMu.RLock()
	assert.Len(t, server.jobs, 2)
	server.jobsMu.RUnlock()
}

func TestServer_PersistenceRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab
	crontabContent := `* * * * * echo test1
0 * * * * echo test2`
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0o600)
	require.NoError(t, err)

	cfg := Config{
		Address:        ":0",
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	// create first server instance
	server1, err := New(cfg)
	require.NoError(t, err)
	defer server1.db.Close()

	// load jobs from crontab first so events can update them
	server1.loadJobsFromCrontab()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server1.processEvents(ctx)

	// simulate job execution events
	now := time.Now()
	server1.OnJobStart("echo test1", "* * * * *", now)
	server1.OnJobComplete("echo test1", "* * * * *", now, now.Add(time.Second), 0, nil)

	server1.OnJobStart("echo test2", "0 * * * *", now.Add(-time.Hour))
	server1.OnJobComplete("echo test2", "0 * * * *", now.Add(-time.Hour), now.Add(-time.Hour).Add(time.Second), 1, fmt.Errorf("test error"))

	// wait for events to be processed
	require.Eventually(t, func() bool {
		server1.jobsMu.RLock()
		defer server1.jobsMu.RUnlock()
		job1, exists1 := server1.jobs[HashCommand("echo test1")]
		job2, exists2 := server1.jobs[HashCommand("echo test2")]
		return exists1 && exists2 && !job1.IsRunning && !job2.IsRunning
	}, time.Second, 10*time.Millisecond)

	// persist job changes to database
	server1.persistJobs()

	// verify data in first server
	server1.jobsMu.RLock()
	job1 := server1.jobs[HashCommand("echo test1")]
	job2 := server1.jobs[HashCommand("echo test2")]
	require.Equal(t, "success", job1.LastStatus)
	require.Equal(t, "failed", job2.LastStatus)
	require.False(t, job1.LastRun.IsZero())
	require.False(t, job2.LastRun.IsZero())
	server1.jobsMu.RUnlock()

	// close first server
	require.NoError(t, server1.db.Close())

	// create second server instance (simulating restart)
	server2, err := New(cfg)
	require.NoError(t, err)
	defer server2.db.Close()

	// load from database (this happens in Run(), but we call directly for testing)
	server2.loadJobsFromDB()
	server2.loadJobsFromCrontab() // sync with crontab

	// verify persistence worked
	server2.jobsMu.RLock()
	defer server2.jobsMu.RUnlock()

	restoredJob1, exists1 := server2.jobs[HashCommand("echo test1")]
	restoredJob2, exists2 := server2.jobs[HashCommand("echo test2")]

	require.True(t, exists1, "job1 should exist after restart")
	require.True(t, exists2, "job2 should exist after restart")

	// verify execution history persisted
	assert.Equal(t, "success", restoredJob1.LastStatus, "job1 status should persist")
	assert.Equal(t, "failed", restoredJob2.LastStatus, "job2 status should persist")
	assert.False(t, restoredJob1.LastRun.IsZero(), "job1 last run should persist")
	assert.False(t, restoredJob2.LastRun.IsZero(), "job2 last run should persist")

	// verify crontab data is still correct
	assert.Equal(t, "echo test1", restoredJob1.Command)
	assert.Equal(t, "echo test2", restoredJob2.Command)
	assert.Equal(t, "* * * * *", restoredJob1.Schedule)
	assert.Equal(t, "0 * * * *", restoredJob2.Schedule)
}

func TestServer_LoadJobsFromDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":0",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// insert test data directly into database
	now := time.Now()
	_, err = server.db.Exec(`
		INSERT INTO jobs (id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at)
		VALUES 
			(?, 'echo test1', '* * * * *', ?, ?, 'success', 1, ?, ?),
			(?, 'echo test2', '0 * * * *', ?, ?, 'failed', 1, ?, ?),
			(?, 'echo test3', '@daily', ?, ?, '', 1, ?, ?)
	`,
		HashCommand("echo test1"), now.Add(time.Minute).Unix(), now.Unix(), now.Unix(), now.Unix(),
		HashCommand("echo test2"), now.Add(time.Hour).Unix(), now.Add(-time.Hour).Unix(), now.Unix(), now.Unix(),
		HashCommand("echo test3"), now.Add(time.Hour*24).Unix(), 0, now.Unix(), now.Unix(),
	)
	require.NoError(t, err)

	// clear jobs map to simulate fresh start
	server.jobsMu.Lock()
	server.jobs = make(map[string]*JobInfo)
	server.jobsMu.Unlock()

	// load jobs from database
	server.loadJobsFromDB()

	// verify jobs were loaded correctly
	server.jobsMu.RLock()
	defer server.jobsMu.RUnlock()

	require.Len(t, server.jobs, 3, "should load 3 jobs from database")

	job1 := server.jobs[HashCommand("echo test1")]
	require.NotNil(t, job1)
	assert.Equal(t, "echo test1", job1.Command)
	assert.Equal(t, "* * * * *", job1.Schedule)
	assert.Equal(t, "success", job1.LastStatus)
	assert.False(t, job1.LastRun.IsZero())
	assert.False(t, job1.NextRun.IsZero())

	job2 := server.jobs[HashCommand("echo test2")]
	require.NotNil(t, job2)
	assert.Equal(t, "echo test2", job2.Command)
	assert.Equal(t, "0 * * * *", job2.Schedule)
	assert.Equal(t, "failed", job2.LastStatus)
	assert.False(t, job2.LastRun.IsZero())

	job3 := server.jobs[HashCommand("echo test3")]
	require.NotNil(t, job3)
	assert.Equal(t, "echo test3", job3.Command)
	assert.Equal(t, "@daily", job3.Schedule)
	assert.Equal(t, "", job3.LastStatus)
	assert.Equal(t, int64(0), job3.LastRun.Unix(), "job3 should have zero LastRun timestamp")
	assert.False(t, job3.NextRun.IsZero(), "NextRun should be calculated from schedule")
}

func TestServer_HandleJobEvent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":0",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// test job start event
	event := JobEvent{
		JobID:     HashCommand("test"),
		Command:   "test",
		Schedule:  "* * * * *",
		EventType: "started",
		StartedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.True(t, job.IsRunning)
	assert.Equal(t, "running", job.LastStatus)

	// test job complete event
	event = JobEvent{
		JobID:      HashCommand("test"),
		Command:    "test",
		Schedule:   "* * * * *",
		EventType:  "completed",
		ExitCode:   0,
		FinishedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists = server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, "success", job.LastStatus)

	// test job failed event
	event = JobEvent{
		JobID:      HashCommand("test"),
		Command:    "test",
		Schedule:   "* * * * *",
		EventType:  "failed",
		ExitCode:   1,
		FinishedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists = server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, "failed", job.LastStatus)
}
