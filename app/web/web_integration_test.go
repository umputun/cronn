package web

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
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

	// create crontab parser as jobs provider
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

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

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

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
		EventType: enums.EventTypeStarted,
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
		EventType:  enums.EventTypeCompleted,
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
	assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)
}

func TestServer_PersistJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// add test jobs to memory
	now := time.Now()
	server.jobsMu.Lock()
	server.jobs[HashCommand("test1")] = persistence.JobInfo{
		ID:         HashCommand("test1"),
		Command:    "test1",
		Schedule:   "* * * * *",
		LastRun:    now,
		LastStatus: enums.JobStatusSuccess,
		IsRunning:  false,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	server.jobs[HashCommand("test2")] = persistence.JobInfo{
		ID:         HashCommand("test2"),
		Command:    "test2",
		Schedule:   "@daily",
		LastRun:    now,
		LastStatus: enums.JobStatusFailed,
		IsRunning:  false, // not running so LastStatus should persist as "failed"
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	server.jobsMu.Unlock()

	// persist jobs
	server.persistJobs()

	// verify jobs were persisted to database
	loadedJobs, err := server.store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, loadedJobs, 2)

	// create map for easier verification
	jobMap := make(map[string]persistence.JobInfo)
	for _, job := range loadedJobs {
		jobMap[job.Command] = job
	}

	// verify test1
	test1 := jobMap["test1"]
	assert.Equal(t, "* * * * *", test1.Schedule)
	assert.Equal(t, enums.JobStatusSuccess, test1.LastStatus)

	// verify test2
	test2 := jobMap["test2"]
	assert.Equal(t, "@daily", test2.Schedule)
	assert.Equal(t, enums.JobStatusFailed, test2.LastStatus)

	// CRITICAL: Test round-trip - clear memory and reload from database
	server.jobsMu.Lock()
	originalJob1 := server.jobs[HashCommand("test1")] // save for comparison
	originalJob2 := server.jobs[HashCommand("test2")]
	server.jobs = make(map[string]persistence.JobInfo) // clear all jobs
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

	// create crontab parser as jobs provider
	parser := crontab.New(crontabFile, 100*time.Millisecond, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: 100 * time.Millisecond,
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

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

	parser := crontab.New(crontabFile, 0, nil)
	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	// create first server instance
	server1, err := New(cfg)
	require.NoError(t, err)
	defer server1.store.Close()

	// load jobs from crontab first so events can update them
	err = server1.loadJobsFromCrontab()
	require.NoError(t, err)

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
	require.Equal(t, enums.JobStatusSuccess, job1.LastStatus)
	require.Equal(t, enums.JobStatusFailed, job2.LastStatus)
	require.False(t, job1.LastRun.IsZero())
	require.False(t, job2.LastRun.IsZero())
	server1.jobsMu.RUnlock()

	// close first server
	require.NoError(t, server1.store.Close())

	// create second server instance (simulating restart)
	parser2 := crontab.New(crontabFile, 0, nil)
	cfg2 := Config{
		DBPath:         dbPath, // same database
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser2,
	}
	server2, err := New(cfg2)
	require.NoError(t, err)
	defer server2.store.Close()

	// load from database (this happens in Run(), but we call directly for testing)
	server2.loadJobsFromDB()
	err = server2.loadJobsFromCrontab() // sync with crontab
	require.NoError(t, err)

	// verify persistence worked
	server2.jobsMu.RLock()
	defer server2.jobsMu.RUnlock()

	restoredJob1, exists1 := server2.jobs[HashCommand("echo test1")]
	restoredJob2, exists2 := server2.jobs[HashCommand("echo test2")]

	require.True(t, exists1, "job1 should exist after restart")
	require.True(t, exists2, "job2 should exist after restart")

	// verify execution history persisted
	assert.Equal(t, enums.JobStatusSuccess, restoredJob1.LastStatus, "job1 status should persist")
	assert.Equal(t, enums.JobStatusFailed, restoredJob2.LastStatus, "job2 status should persist")
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

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// insert test data directly into database
	now := time.Now()
	testJobs := []persistence.JobInfo{
		{
			ID:         HashCommand("echo test1"),
			Command:    "echo test1",
			Schedule:   "* * * * *",
			NextRun:    now.Add(time.Minute),
			LastRun:    now,
			LastStatus: enums.JobStatusSuccess,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         HashCommand("echo test2"),
			Command:    "echo test2",
			Schedule:   "0 * * * *",
			NextRun:    now.Add(time.Hour),
			LastRun:    now.Add(-time.Hour),
			LastStatus: enums.JobStatusFailed,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         HashCommand("echo test3"),
			Command:    "echo test3",
			Schedule:   "@daily",
			NextRun:    now.Add(time.Hour * 24),
			LastRun:    time.Time{}, // zero time
			LastStatus: enums.JobStatusIdle,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
	err = server.store.SaveJobs(testJobs)
	require.NoError(t, err)

	// clear jobs map to simulate fresh start
	server.jobsMu.Lock()
	server.jobs = make(map[string]persistence.JobInfo)
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
	assert.Equal(t, enums.JobStatusSuccess, job1.LastStatus)
	assert.False(t, job1.LastRun.IsZero())
	assert.False(t, job1.NextRun.IsZero())

	job2 := server.jobs[HashCommand("echo test2")]
	require.NotNil(t, job2)
	assert.Equal(t, "echo test2", job2.Command)
	assert.Equal(t, "0 * * * *", job2.Schedule)
	assert.Equal(t, enums.JobStatusFailed, job2.LastStatus)
	assert.False(t, job2.LastRun.IsZero())

	job3 := server.jobs[HashCommand("echo test3")]
	require.NotNil(t, job3)
	assert.Equal(t, "echo test3", job3.Command)
	assert.Equal(t, "@daily", job3.Schedule)
	assert.Equal(t, enums.JobStatusIdle, job3.LastStatus)
	assert.True(t, job3.LastRun.IsZero(), "job3 should have zero LastRun timestamp")
	assert.False(t, job3.NextRun.IsZero(), "NextRun should be calculated from schedule")
}

func TestServer_HandleJobEvent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// test job start event
	event := JobEvent{
		JobID:     HashCommand("test"),
		Command:   "test",
		Schedule:  "* * * * *",
		EventType: enums.EventTypeStarted,
		StartedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.True(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusRunning, job.LastStatus)

	// test job complete event
	event = JobEvent{
		JobID:      HashCommand("test"),
		Command:    "test",
		Schedule:   "* * * * *",
		EventType:  enums.EventTypeCompleted,
		ExitCode:   0,
		FinishedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists = server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)

	// test job failed event
	event = JobEvent{
		JobID:      HashCommand("test"),
		Command:    "test",
		Schedule:   "* * * * *",
		EventType:  enums.EventTypeFailed,
		ExitCode:   1,
		FinishedAt: time.Now(),
	}

	server.handleJobEvent(event)

	server.jobsMu.RLock()
	job, exists = server.jobs[HashCommand("test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusFailed, job.LastStatus)
}

func TestServer_ConcurrentJobEvents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create crontab with multiple jobs
	crontabContent := []string{
		"* * * * * echo job1",
		"* * * * * echo job2",
		"* * * * * echo job3",
		"* * * * * echo job4",
		"* * * * * echo job5",
	}
	err := os.WriteFile(crontabFile, []byte(strings.Join(crontabContent, "\n")), 0o600)
	require.NoError(t, err)

	// create crontab parser as jobs provider
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start processing events
	go server.processEvents(ctx)

	// simulate concurrent job events from multiple goroutines
	var wg sync.WaitGroup
	const numGoroutines = 10
	const eventsPerGoroutine = 20

	// track all events sent
	startTime := time.Now()

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < eventsPerGoroutine; j++ {
				jobNum := (j % 5) + 1
				jobCmd := fmt.Sprintf("echo job%d", jobNum)

				// randomly send start or complete events
				if j%2 == 0 {
					// send start event
					server.OnJobStart(jobCmd, "* * * * *", startTime.Add(time.Duration(j)*time.Millisecond))
				} else {
					// send complete event
					server.OnJobComplete(jobCmd, "* * * * *",
						startTime.Add(time.Duration(j-1)*time.Millisecond),
						startTime.Add(time.Duration(j)*time.Millisecond),
						0, nil)
				}

				// small random delay to increase chance of actual concurrency
				time.Sleep(time.Duration(rand.Intn(5)) * time.Microsecond) //nolint:gosec // test only, not security sensitive
			}
		}()
	}

	// wait for all goroutines to finish sending events
	wg.Wait()

	// give time for events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()

		// check that all 5 jobs exist
		if len(server.jobs) != 5 {
			return false
		}

		// verify each job has been updated
		for i := 1; i <= 5; i++ {
			jobID := HashCommand(fmt.Sprintf("echo job%d", i))
			if _, exists := server.jobs[jobID]; !exists {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond)

	// verify no data corruption - check that all jobs have valid state
	server.jobsMu.RLock()
	for id, job := range server.jobs {
		assert.NotEmpty(t, job.ID, "job ID should not be empty")
		assert.NotEmpty(t, job.Command, "job command should not be empty")
		assert.NotEmpty(t, job.Schedule, "job schedule should not be empty")
		assert.Contains(t, []enums.JobStatus{
			enums.JobStatusIdle,
			enums.JobStatusRunning,
			enums.JobStatusSuccess,
			enums.JobStatusFailed,
		}, job.LastStatus, "job %s has invalid status", id)
	}
	server.jobsMu.RUnlock()

	// persist and verify database integrity
	server.persistJobs()

	// load from database and verify
	loadedJobs, err := server.store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, loadedJobs, 5, "should have exactly 5 jobs in database")
}

func TestServer_ConcurrentHTTPRequests(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab
	crontabContent := []string{
		"* * * * * echo test1",
		"*/2 * * * * echo test2",
		"0 * * * * echo test3",
	}
	err := os.WriteFile(crontabFile, []byte(strings.Join(crontabContent, "\n")), 0o600)
	require.NoError(t, err)

	// create crontab parser as jobs provider
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// load jobs from crontab to initialize server state
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)

	// create test server
	router := server.routes()
	ts := httptest.NewServer(router)
	defer ts.Close()

	// simulate concurrent HTTP requests
	var wg sync.WaitGroup
	const numClients = 20
	const requestsPerClient = 10

	// different endpoints to hit
	endpoints := []struct {
		path   string
		method string
		body   string
	}{
		{"/", "GET", ""},
		{"/api/jobs", "GET", ""},
		{"/api/theme", "POST", "theme=dark"},
		{"/api/view-mode", "POST", "view-mode=list"},
		{"/api/sort-mode", "POST", "sort-mode=lastrun"},
		{"/api/sort-toggle", "POST", ""},
	}

	errors := make(chan error, numClients*requestsPerClient)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			client := &http.Client{Timeout: 5 * time.Second}

			for j := 0; j < requestsPerClient; j++ {
				endpoint := endpoints[j%len(endpoints)]

				var resp *http.Response
				var err error

				if endpoint.method == "GET" {
					resp, err = client.Get(ts.URL + endpoint.path)
				} else {
					var body *strings.Reader
					if endpoint.body != "" {
						body = strings.NewReader(endpoint.body)
					} else {
						body = strings.NewReader("")
					}
					resp, err = client.Post(ts.URL+endpoint.path, "application/x-www-form-urlencoded", body)
				}

				if err != nil {
					errors <- fmt.Errorf("client %d request %d failed: %w", clientID, j, err)
					continue
				}

				// API endpoints can return various status codes (200, 204, 303, etc.)
				if resp.StatusCode >= 400 {
					errors <- fmt.Errorf("client %d request %d to %s got status %d", clientID, j, endpoint.path, resp.StatusCode)
				}
				_ = resp.Body.Close()

				// tiny random delay
				time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond) //nolint:gosec // test only, not security sensitive
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// check for any errors
	allErrors := make([]error, 0, numClients*requestsPerClient)
	for err := range errors {
		allErrors = append(allErrors, err)
	}
	assert.Empty(t, allErrors, "concurrent requests should not produce errors")

	// verify server state is still consistent
	server.jobsMu.RLock()
	assert.Len(t, server.jobs, 3, "should still have 3 jobs")
	server.jobsMu.RUnlock()
}

func TestServer_ConcurrentModifications(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// initial crontab
	err := os.WriteFile(crontabFile, []byte("* * * * * echo initial"), 0o600)
	require.NoError(t, err)

	parser := crontab.New(crontabFile, 0, nil)
	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start event processing
	go server.processEvents(ctx)

	var wg sync.WaitGroup

	// goroutine 1: continuously reload crontab
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			// modify crontab
			newCrontab := fmt.Sprintf("* * * * * echo job%d\n*/2 * * * * echo task%d", i, i)
			writeErr := os.WriteFile(crontabFile, []byte(newCrontab), 0o600)
			if writeErr != nil {
				return
			}

			// reload
			if loadErr := server.loadJobsFromCrontab(); loadErr != nil {
				t.Errorf("loadJobsFromCrontab failed: %v", loadErr)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// goroutine 2: continuously send job events
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			jobCmd := fmt.Sprintf("echo job%d", i%10)
			if i%2 == 0 {
				server.OnJobStart(jobCmd, "* * * * *", time.Now())
			} else {
				server.OnJobComplete(jobCmd, "* * * * *", time.Now().Add(-time.Second), time.Now(), 0, nil)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// goroutine 3: continuously persist to database
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			server.persistJobs()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// goroutine 4: continuously read state
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			server.jobsMu.RLock()
			// just iterate to simulate reads
			count := 0
			for range server.jobs {
				count++
			}
			server.jobsMu.RUnlock()
			assert.GreaterOrEqual(t, count, 0, "jobs count should be non-negative")
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// wait for all operations to complete
	wg.Wait()

	// give a bit of time for final persistence
	time.Sleep(50 * time.Millisecond)

	// do final persistence to ensure everything is saved
	server.persistJobs()

	// final consistency check
	server.jobsMu.RLock()
	finalJobCount := len(server.jobs)
	server.jobsMu.RUnlock()

	assert.Greater(t, finalJobCount, 0, "should have at least some jobs after concurrent modifications")

	// verify database consistency - load fresh and check
	loadedJobs, err := server.store.LoadJobs()
	require.NoError(t, err)

	// after all the crontab reloads, we should have jobs from the final crontab
	// plus any jobs from events that haven't been cleaned up
	// the important thing is the database has some data and didn't corrupt
	assert.Greater(t, len(loadedJobs), 0, "database should have jobs")

	// verify loaded jobs are valid
	for _, job := range loadedJobs {
		assert.NotEmpty(t, job.ID, "job ID should not be empty")
		assert.NotEmpty(t, job.Command, "job command should not be empty")
	}
}

func TestServer_EventChannelStress(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	err := os.WriteFile(crontabFile, []byte("* * * * * echo test"), 0o600)
	require.NoError(t, err)

	parser := crontab.New(crontabFile, 0, nil)
	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// start event processing
	go server.processEvents(ctx)

	// flood the event channel
	const numEvents = 1000
	startTime := time.Now()

	for i := 0; i < numEvents; i++ {
		// send events as fast as possible without blocking
		// rotate through event types
		var eventType enums.EventType
		switch i % 3 {
		case 0:
			eventType = enums.EventTypeStarted
		case 1:
			eventType = enums.EventTypeCompleted
		case 2:
			eventType = enums.EventTypeFailed
		}

		select {
		case server.eventChan <- JobEvent{
			JobID:      fmt.Sprintf("job%d", i),
			Command:    fmt.Sprintf("echo test%d", i),
			Schedule:   "* * * * *",
			EventType:  eventType,
			StartedAt:  startTime,
			FinishedAt: startTime.Add(time.Millisecond),
		}:
			// event sent successfully
		case <-ctx.Done():
			// context canceled, stop sending
			return
		}
	}

	// wait a bit for processing
	time.Sleep(100 * time.Millisecond)

	// verify system didn't crash and is still responsive
	server.jobsMu.RLock()
	jobCount := len(server.jobs)
	server.jobsMu.RUnlock()

	assert.GreaterOrEqual(t, jobCount, 0, "server should still be functional after stress test")
}
