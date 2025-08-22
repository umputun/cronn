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
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0644)
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
		server.Run(ctx, ":0")
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
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		
		server.handleDashboard(w, req)
		
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
		
		body := w.Body.String()
		assert.Contains(t, body, "Cronn Dashboard")
		assert.Contains(t, body, "htmx")
		assert.Contains(t, body, "hx-get=\"/partials/jobs\"")
	})
	
	t.Run("jobs partial - cards view", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/partials/jobs", nil)
		req.AddCookie(&http.Cookie{Name: "view_mode", Value: "cards"})
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
		req := httptest.NewRequest("GET", "/partials/jobs", nil)
		req.AddCookie(&http.Cookie{Name: "view_mode", Value: "list"})
		w := httptest.NewRecorder()
		
		server.handleJobsPartial(w, req)
		
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		
		body := w.Body.String()
		assert.Contains(t, body, "jobs-table")
		assert.Contains(t, body, "echo hourly")
		assert.Contains(t, body, "echo five-minutes")
		assert.Contains(t, body, "echo daily")
	})
	
	t.Run("theme toggle", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/toggle-theme", nil)
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
		req := httptest.NewRequest("POST", "/toggle-view", nil)
		req.AddCookie(&http.Cookie{Name: "view_mode", Value: "cards"})
		w := httptest.NewRecorder()
		
		server.handleViewModeToggle(w, req)
		
		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		
		cookies := resp.Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "view_mode", cookies[0].Name)
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
		IsRunning:  true,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	server.jobsMu.Unlock()
	
	// persist jobs
	server.persistJobs()
	
	// verify jobs were persisted to database
	rows, err := server.db.Query("SELECT command, schedule, status FROM jobs ORDER BY command")
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
	assert.Equal(t, "running", jobs[1].Status)
}

func TestServer_SyncJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")
	
	// create initial crontab file
	crontabContent := `0 * * * * echo hourly`
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0644)
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
	err = os.WriteFile(crontabFile, []byte(crontabContent), 0644)
	require.NoError(t, err)
	
	// wait for sync
	time.Sleep(200 * time.Millisecond)
	
	// verify jobs were updated
	server.jobsMu.RLock()
	assert.Len(t, server.jobs, 2)
	server.jobsMu.RUnlock()
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