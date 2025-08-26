package web

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// createTestProvider creates a dummy JobsProvider for testing
func createTestProvider(t *testing.T, tmpDir string) JobsProvider {
	t.Helper()
	crontabFile := filepath.Join(tmpDir, "test-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	return crontab.New(crontabFile, 0, nil)
}

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	t.Run("default LoginTTL", func(t *testing.T) {
		cfg := Config{
			DBPath:         dbPath,
			UpdateInterval: time.Minute,
			Version:        "test",
			JobsProvider:   createTestProvider(t, tmpDir),
		}

		server, err := New(cfg)
		require.NoError(t, err)
		assert.NotNil(t, server)
		assert.NotNil(t, server.store)
		assert.NotNil(t, server.templates)
		assert.NotNil(t, server.jobs)
		assert.NotNil(t, server.parser)
		assert.NotNil(t, server.eventChan)
		assert.Equal(t, 24*time.Hour, server.loginTTL)

		// verify store is initialized by loading jobs (should not fail)
		jobs, err := server.store.LoadJobs()
		require.NoError(t, err)
		assert.NotNil(t, jobs)

		require.NoError(t, server.store.Close())
	})

	t.Run("custom LoginTTL", func(t *testing.T) {
		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test2.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			JobsProvider:   createTestProvider(t, tmpDir),
			LoginTTL:       12 * time.Hour,
		}

		server, err := New(cfg)
		require.NoError(t, err)
		assert.Equal(t, 12*time.Hour, server.loginTTL)
		require.NoError(t, server.store.Close())
	})
}

func TestHashCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"simple command", "echo hello", "echo hello"},
		{"complex command", "sh -c 'echo test'", "sh -c 'echo test'"},
		{"empty command", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HashCommand(tt.cmd)
			assert.Equal(t, 64, len(got)) // SHA256 is 64 hex chars
			// verify consistency
			assert.Equal(t, got, HashCommand(tt.cmd))
		})
	}
}

func TestServer_sortJobs(t *testing.T) {
	server := &Server{}

	// create test jobs with different attributes
	now := time.Now()
	jobs := []persistence.JobInfo{
		{ID: "1", Command: "cmd1", SortIndex: 2, LastRun: now.Add(-2 * time.Hour), NextRun: now.Add(2 * time.Hour)},
		{ID: "2", Command: "cmd2", SortIndex: 0, LastRun: now.Add(-1 * time.Hour), NextRun: now.Add(1 * time.Hour)},
		{ID: "3", Command: "cmd3", SortIndex: 1, LastRun: now.Add(-3 * time.Hour), NextRun: now.Add(30 * time.Minute)},
		{ID: "4", Command: "cmd4", SortIndex: 3, LastRun: time.Time{}, NextRun: time.Time{}}, // never run
	}

	t.Run("default sort", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeDefault)

		// should be sorted by SortIndex
		assert.Equal(t, "2", sorted[0].ID)
		assert.Equal(t, "3", sorted[1].ID)
		assert.Equal(t, "1", sorted[2].ID)
		assert.Equal(t, "4", sorted[3].ID)
	})

	t.Run("sort by last run", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeLastrun)

		// should be sorted by LastRun, most recent first
		assert.Equal(t, "2", sorted[0].ID, "First should be ID 2 (1 hour ago - most recent)")
		assert.Equal(t, "1", sorted[1].ID, "Second should be ID 1 (2 hours ago)")
		assert.Equal(t, "3", sorted[2].ID, "Third should be ID 3 (3 hours ago)")
		assert.Equal(t, "4", sorted[3].ID, "Fourth should be ID 4 (never run)")

		// verify actual time ordering
		assert.True(t, sorted[0].LastRun.After(sorted[1].LastRun), "First job should have later LastRun than second")
		assert.True(t, sorted[1].LastRun.After(sorted[2].LastRun), "Second job should have later LastRun than third")
		assert.True(t, sorted[3].LastRun.IsZero(), "Last job should have zero LastRun")
	})

	t.Run("sort by next run", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeNextrun)

		// should be sorted by NextRun, soonest first
		assert.Equal(t, "3", sorted[0].ID) // 30 minutes
		assert.Equal(t, "2", sorted[1].ID) // 1 hour
		assert.Equal(t, "1", sorted[2].ID) // 2 hours
		assert.Equal(t, "4", sorted[3].ID) // never (zero time)
	})

	t.Run("stable sort with equal times", func(t *testing.T) {
		// create jobs with some having equal next run times
		sameTime := now.Add(1 * time.Hour)
		sameTime2 := now.Add(2 * time.Hour)
		jobsEqual := []persistence.JobInfo{
			{ID: "A", Command: "cmdA", SortIndex: 0, NextRun: sameTime, LastRun: sameTime2},
			{ID: "B", Command: "cmdB", SortIndex: 1, NextRun: sameTime, LastRun: sameTime2},
			{ID: "C", Command: "cmdC", SortIndex: 2, NextRun: sameTime, LastRun: sameTime2},
			{ID: "D", Command: "cmdD", SortIndex: 3, NextRun: now.Add(30 * time.Minute), LastRun: now},
			{ID: "E", Command: "cmdE", SortIndex: 4, NextRun: sameTime, LastRun: sameTime2},
		}

		// test next run sorting stability
		sortedNext := make([]persistence.JobInfo, len(jobsEqual))
		copy(sortedNext, jobsEqual)
		server.sortJobs(sortedNext, enums.SortModeNextrun)

		// d should be first (30 minutes), then A,B,C,E in original order (all 1 hour)
		assert.Equal(t, "D", sortedNext[0].ID, "D should be first (soonest)")
		assert.Equal(t, "A", sortedNext[1].ID, "A should maintain position relative to B,C,E")
		assert.Equal(t, "B", sortedNext[2].ID, "B should maintain position relative to C,E")
		assert.Equal(t, "C", sortedNext[3].ID, "C should maintain position relative to E")
		assert.Equal(t, "E", sortedNext[4].ID, "E should be last among equal times")

		// test last run sorting stability
		sortedLast := make([]persistence.JobInfo, len(jobsEqual))
		copy(sortedLast, jobsEqual)
		server.sortJobs(sortedLast, enums.SortModeLastrun)

		// a,B,C,E have same LastRun (2 hours from now), D is different (now)
		// since LastRun sorts most recent first, A,B,C,E should come first in original order
		assert.Equal(t, "A", sortedLast[0].ID, "A should maintain position relative to B,C,E")
		assert.Equal(t, "B", sortedLast[1].ID, "B should maintain position relative to C,E")
		assert.Equal(t, "C", sortedLast[2].ID, "C should maintain position relative to E")
		assert.Equal(t, "E", sortedLast[3].ID, "E should be last among equal times")
		assert.Equal(t, "D", sortedLast[4].ID, "D should be last (oldest)")
	})
}

func TestServer_getSortMode(t *testing.T) {
	server := &Server{}

	tests := []struct {
		name      string
		cookieVal string
		wantSort  enums.SortMode
	}{
		{"no cookie", "", enums.SortModeDefault},
		{"default sort", "default", enums.SortModeDefault},
		{"last run sort", "lastrun", enums.SortModeLastrun},
		{"next run sort", "nextrun", enums.SortModeNextrun},
		{"invalid sort", "invalid", enums.SortModeDefault},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", http.NoBody)
			if tt.cookieVal != "" {
				req.AddCookie(&http.Cookie{Name: "sort-mode", Value: tt.cookieVal})
			}

			got := server.getSortMode(req)
			assert.Equal(t, tt.wantSort, got)
		})
	}
}

func TestServer_handleSortModeChange(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	tests := []struct {
		name       string
		formValue  string
		wantCookie string
	}{
		{"default sort", "default", "default"},
		{"last run sort", "lastrun", "lastrun"},
		{"next run sort", "nextrun", "nextrun"},
		{"invalid sort", "invalid", "default"},
		{"empty sort", "", "default"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/sort-mode", strings.NewReader("sort="+tt.formValue))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			server.handleSortModeChange(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "#jobs-container", w.Header().Get("HX-Retarget"))
			assert.Equal(t, "innerHTML", w.Header().Get("HX-Reswap"))

			// check cookie
			cookies := w.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "sort-mode", cookies[0].Name)
			assert.Equal(t, tt.wantCookie, cookies[0].Value)
		})
	}
}

func TestServer_OnJobStart(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	server.OnJobStart("echo test", "* * * * *", startTime)

	// wait for event to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		_, exists := server.jobs[HashCommand("echo test")]
		return exists
	}, time.Second, 10*time.Millisecond)

	// verify job in memory (not in database since persistJobs runs periodically)
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "echo test", job.Command)
	assert.Equal(t, "* * * * *", job.Schedule)
	assert.True(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusRunning.String(), job.LastStatus.String())
	assert.Equal(t, startTime.Unix(), job.LastRun.Unix())
}

func TestServer_OnJobComplete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	endTime := startTime.Add(time.Second)

	// first start the job
	server.OnJobStart("echo test", "* * * * *", startTime)
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastStatus == enums.JobStatusRunning
	}, time.Second, 10*time.Millisecond)

	// then complete it successfully
	server.OnJobComplete("echo test", "* * * * *", startTime, endTime, 0, nil)
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastStatus == enums.JobStatusSuccess
	}, time.Second, 10*time.Millisecond)

	// verify job status was updated in memory
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)

	// test with error
	server.OnJobStart("echo error", "* * * * *", startTime)
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo error")]
		return exists && job.LastStatus == enums.JobStatusRunning
	}, time.Second, 10*time.Millisecond)

	server.OnJobComplete("echo error", "* * * * *", startTime, endTime, 1, fmt.Errorf("test error"))
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo error")]
		return exists && job.LastStatus == enums.JobStatusFailed
	}, time.Second, 10*time.Millisecond)

	server.jobsMu.RLock()
	job2, exists2 := server.jobs[HashCommand("echo error")]
	server.jobsMu.RUnlock()

	assert.True(t, exists2)
	assert.False(t, job2.IsRunning)
	assert.Equal(t, enums.JobStatusFailed, job2.LastStatus)
}

func TestServer_handleViewModeToggle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// add test jobs
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	server.OnJobStart("echo test2", "0 * * * *", startTime.Add(-time.Hour))
	server.OnJobComplete("echo test2", "0 * * * *", startTime.Add(-time.Hour), startTime.Add(-59*time.Minute), 1, fmt.Errorf("failed"))

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// wait for events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		return len(server.jobs) == 2
	}, time.Second, 10*time.Millisecond)

	tests := []struct {
		name             string
		currentCookie    string
		expectedNextMode string
		expectedInBody   []string
	}{
		{
			name:             "cards to list view",
			currentCookie:    "cards",
			expectedNextMode: "list",
			expectedInBody:   []string{"jobs-container list", "jobs-table", "th-schedule", "th-command"},
		},
		{
			name:             "list to cards view",
			currentCookie:    "list",
			expectedNextMode: "cards",
			expectedInBody:   []string{"jobs-container cards", "job-card", "job-schedule", "job-command"},
		},
		{
			name:             "no cookie defaults to list",
			currentCookie:    "",
			expectedNextMode: "list",
			expectedInBody:   []string{"jobs-container list", "jobs-table"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/view-mode", http.NoBody)
			if tt.currentCookie != "" {
				req.AddCookie(&http.Cookie{Name: "view-mode", Value: tt.currentCookie})
			}
			rec := httptest.NewRecorder()

			server.handleViewModeToggle(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)

			// check cookie was set
			cookies := rec.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "view-mode", cookies[0].Name)
			assert.Equal(t, tt.expectedNextMode, cookies[0].Value)

			// check response contains expected content
			body := rec.Body.String()
			for _, expected := range tt.expectedInBody {
				assert.Contains(t, body, expected)
			}

			// verify that stats updates and view mode button are rendered as OOB
			assert.Contains(t, body, "hx-swap-oob")
			assert.Contains(t, body, "view-toggle")
			assert.Contains(t, body, "id=\"running-count\"")
			assert.Contains(t, body, "id=\"next-run\"")
		})
	}

	// test template not found error case
	t.Run("template not found", func(t *testing.T) {
		// temporarily rename template to simulate not found
		originalTemplate := server.templates["partials/jobs.html"]
		delete(server.templates, "partials/jobs.html")
		defer func() {
			server.templates["partials/jobs.html"] = originalTemplate
		}()

		req := httptest.NewRequest("POST", "/api/view-mode", http.NoBody)
		rec := httptest.NewRecorder()

		server.handleViewModeToggle(rec, req)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	// test template execution error
	t.Run("template execution error", func(t *testing.T) {
		// create a template with an error
		badTemplate := template.Must(template.New("partials/jobs.html").Parse(`{{define "jobs-container"}}{{.NonExistentField}}{{end}}`))
		originalTemplate := server.templates["partials/jobs.html"]
		server.templates["partials/jobs.html"] = badTemplate
		defer func() {
			server.templates["partials/jobs.html"] = originalTemplate
		}()

		req := httptest.NewRequest("POST", "/api/view-mode", http.NoBody)
		rec := httptest.NewRecorder()

		server.handleViewModeToggle(rec, req)

		// should still return 200 but log the error
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestServer_OnJobStartEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// test with invalid schedule that will fail to parse
	startTime := time.Now()
	server.OnJobStart("echo test", "invalid schedule", startTime)

	// wait a bit for processing
	time.Sleep(50 * time.Millisecond)

	// job should still be created but NextRun calculation might fail
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "echo test", job.Command)
	assert.Equal(t, "invalid schedule", job.Schedule)
	assert.True(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusRunning, job.LastStatus)

	// test updating an existing job - schedule should NOT change from events
	server.OnJobStart("echo test", "* * * * *", startTime.Add(time.Hour))

	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastRun.Equal(startTime.Add(time.Hour))
	}, time.Second, 10*time.Millisecond)

	server.jobsMu.RLock()
	updatedJob := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	// schedule should remain unchanged - job events don't update schedule
	assert.Equal(t, "invalid schedule", updatedJob.Schedule)
	assert.True(t, updatedJob.IsRunning)
}

func TestServer_syncWithCrontab(t *testing.T) {
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

	// load jobs from crontab
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)

	// persist the jobs to database
	server.persistJobs()

	// verify jobs were synced by loading from store
	jobs, err := server.store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)

	// create map for easier verification
	jobMap := make(map[string]string)
	for _, job := range jobs {
		jobMap[job.Command] = job.Schedule
	}

	assert.Equal(t, "@daily", jobMap["echo daily"])
	assert.Equal(t, "*/5 * * * *", jobMap["echo five-minutes"])
	assert.Equal(t, "0 * * * *", jobMap["echo hourly"])
}

func TestServer_handleDashboard(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// add test job
	server.OnJobStart("echo test", "* * * * *", time.Now())
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		_, exists := server.jobs[HashCommand("echo test")]
		return exists
	}, time.Second, 10*time.Millisecond)

	req := httptest.NewRequest("GET", "/", http.NoBody)
	w := httptest.NewRecorder()

	server.handleDashboard(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	body := w.Body.String()
	assert.Contains(t, body, "Cronn Dashboard")
	// don't check for job content since it's rendered via separate partial
}

func TestServer_handleAPIJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// add test jobs
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	server.OnJobStart("echo test2", "@daily", startTime)

	// wait for all events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		return len(server.jobs) == 2
	}, time.Second, 10*time.Millisecond)

	// test card view
	req := httptest.NewRequest("GET", "/api/jobs", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w := httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := w.Body.String()
	assert.Contains(t, body, "job-card")
	assert.Contains(t, body, "echo test1")
	assert.Contains(t, body, "echo test2")

	// test list view
	req = httptest.NewRequest("GET", "/api/jobs", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "list"})
	w = httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = w.Body.String()
	// list view renders a table
	assert.Contains(t, body, "jobs-table")
	assert.Contains(t, body, "echo test1")
	assert.Contains(t, body, "echo test2")
}

func TestServer_handleToggleTheme(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	tests := []struct {
		name     string
		current  string
		expected string
	}{
		{"auto to light", "auto", "light"},
		{"light to dark", "light", "dark"},
		{"dark to auto", "dark", "auto"},
		{"no cookie defaults to light", "", "light"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/toggle-theme", http.NoBody)
			if tt.current != "" {
				req.AddCookie(&http.Cookie{Name: "theme", Value: tt.current})
			}
			w := httptest.NewRecorder()

			server.handleThemeToggle(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "theme", cookies[0].Name)
			assert.Equal(t, tt.expected, cookies[0].Value)
		})
	}
}

func TestServer_handleToggleView(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	tests := []struct {
		name     string
		current  string
		expected string
	}{
		{"cards to list", "cards", "list"},
		{"list to cards", "list", "cards"},
		{"no cookie defaults to cards", "", "list"}, // no cookie defaults to list when toggled
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/toggle-view", http.NoBody)
			if tt.current != "" {
				req.AddCookie(&http.Cookie{Name: "view-mode", Value: tt.current})
			}
			w := httptest.NewRecorder()

			server.handleViewModeToggle(w, req)

			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)

			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "view-mode", cookies[0].Name)
			assert.Equal(t, tt.expected, cookies[0].Value)
		})
	}
}

func TestServer_parseJobSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab file
	crontabContent := `0 * * * * echo hourly
*/5 * * * * echo five-minutes
@daily echo daily
@every 2h echo every-two-hours`
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

	// load jobs from crontab
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)

	server.jobsMu.RLock()
	specs := make([]crontab.JobSpec, 0, len(server.jobs))
	for _, job := range server.jobs {
		specs = append(specs, crontab.JobSpec{
			Spec:    job.Schedule,
			Command: job.Command,
		})
	}
	server.jobsMu.RUnlock()
	assert.Len(t, specs, 4)

	// verify specs - jobs are in a map so order is not guaranteed
	expectedJobs := map[string]string{
		"echo hourly":          "0 * * * *",
		"echo five-minutes":    "*/5 * * * *",
		"echo daily":           "@daily",
		"echo every-two-hours": "@every 2h",
	}

	for _, spec := range specs {
		expectedSchedule, exists := expectedJobs[spec.Command]
		assert.True(t, exists, "unexpected job command: %s", spec.Command)
		assert.Equal(t, expectedSchedule, spec.Spec, "schedule for %s", spec.Command)
	}
}

func TestServer_loadJobsFromCrontab_NilProvider(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create server with some existing jobs
	crontabFile := filepath.Join(tmpDir, "crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte("0 * * * * echo initial"), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// load initial job
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)
	assert.Len(t, server.jobs, 1)

	// set provider to nil and attempt to load again
	server.jobsProvider = nil
	err = server.loadJobsFromCrontab()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot load jobs")

	// verify that jobs map remains unchanged after error
	assert.Len(t, server.jobs, 1)
	assert.Contains(t, server.jobs, HashCommand("echo initial"))
}

func TestServer_loadJobsFromCrontab_ProviderError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// first create a working crontab to load initial jobs
	crontabFile := filepath.Join(tmpDir, "crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte("0 * * * * echo existing"), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// load initial job successfully
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)
	assert.Len(t, server.jobs, 1)
	initialJobID := HashCommand("echo existing")
	assert.Contains(t, server.jobs, initialJobID)

	// now remove the file to trigger error
	require.NoError(t, os.Remove(crontabFile))

	// test that loadJobsFromCrontab handles provider errors
	err = server.loadJobsFromCrontab()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load job specifications")

	// verify that jobs map remains unchanged after error
	assert.Len(t, server.jobs, 1)
	assert.Contains(t, server.jobs, initialJobID)

	// verify the existing job details remain intact
	job := server.jobs[initialJobID]
	assert.Equal(t, "echo existing", job.Command)
	assert.Equal(t, "0 * * * *", job.Schedule)
}

func TestNew_NilProviderReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// attempt to create server without JobsProvider
	_, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   nil,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "web server initialization failed: JobsProvider is required")
}

func TestServer_Templates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for the test
	crontabFile := filepath.Join(tmpDir, "dummy")
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

	// test that templates are parsed correctly
	// all templates are parsed into the base template
	t.Run("base template exists", func(t *testing.T) {
		tmpl := server.templates["base.html"]
		assert.NotNil(t, tmpl, "base template should be parsed")
	})

	// check that important templates are defined in the base template
	t.Run("check defined templates", func(t *testing.T) {
		tmpl := server.templates["base.html"]
		require.NotNil(t, tmpl)

		// check for key templates
		main := tmpl.Lookup("main")
		assert.NotNil(t, main, "main template should be defined")

		jobsCards := tmpl.Lookup("jobs-cards")
		assert.NotNil(t, jobsCards, "jobs-cards template should be defined")

		jobsList := tmpl.Lookup("jobs-list")
		assert.NotNil(t, jobsList, "jobs-list template should be defined")
	})

	// test template execution
	t.Run("execute jobs-cards template", func(t *testing.T) {
		tmpl := server.templates["base.html"]
		require.NotNil(t, tmpl)
		jobsCards := tmpl.Lookup("jobs-cards")
		require.NotNil(t, jobsCards)

		data := struct {
			Jobs []persistence.JobInfo
		}{
			Jobs: []persistence.JobInfo{
				{
					ID:         "test123",
					Command:    "echo test",
					Schedule:   "* * * * *",
					LastStatus: enums.JobStatusSuccess,
					LastRun:    time.Now(),
					NextRun:    time.Now().Add(time.Minute),
					IsRunning:  false,
					Enabled:    true,
				},
			},
		}

		var buf strings.Builder
		err := jobsCards.Execute(&buf, data)
		require.NoError(t, err)

		output := buf.String()
		assert.Contains(t, output, "echo test")
		assert.Contains(t, output, "* * * * *")
		assert.Contains(t, output, "status-success")
	})
}

func TestServer_handleSortToggle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// add test jobs with different schedules
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	server.OnJobStart("echo test2", "0 * * * *", startTime.Add(-time.Hour))
	server.OnJobComplete("echo test2", "0 * * * *", startTime.Add(-time.Hour), startTime.Add(-time.Hour).Add(time.Second), 0, nil)

	// start event processor to handle the job events
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// wait for events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		return len(server.jobs) == 2
	}, time.Second, 10*time.Millisecond)

	tests := []struct {
		name             string
		currentCookie    string
		expectedNextMode string
		expectedLabel    string
	}{
		{name: "default to lastrun", currentCookie: "default", expectedNextMode: "lastrun", expectedLabel: "Last Run"},
		{name: "lastrun to nextrun", currentCookie: "lastrun", expectedNextMode: "nextrun", expectedLabel: "Next Run"},
		{name: "nextrun to default", currentCookie: "nextrun", expectedNextMode: "default", expectedLabel: "Original Order"},
		{name: "no cookie defaults to lastrun", currentCookie: "", expectedNextMode: "lastrun", expectedLabel: "Last Run"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/sort-toggle", http.NoBody)
			if tt.currentCookie != "" {
				req.AddCookie(&http.Cookie{Name: "sort-mode", Value: tt.currentCookie})
			}
			rec := httptest.NewRecorder()

			server.handleSortToggle(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)

			// check cookie is set correctly
			cookies := rec.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "sort-mode", cookies[0].Name)
			assert.Equal(t, tt.expectedNextMode, cookies[0].Value)
			assert.Equal(t, "/", cookies[0].Path)
			assert.True(t, cookies[0].HttpOnly)

			// check response contains job data and OOB update
			// note: After refactoring to use templates, the sort button is now replaced entirely
			// via outerHTML instead of just updating innerHTML, which is cleaner and more maintainable
			body := rec.Body.String()
			assert.Contains(t, body, "echo test1")
			assert.Contains(t, body, "echo test2")
			assert.Contains(t, body, `hx-swap-oob="outerHTML:.sort-button"`)
			assert.Contains(t, body, tt.expectedLabel)
		})
	}
}

func TestTemplateHelpers(t *testing.T) {
	// create a minimal server instance for testing
	s := &Server{}

	t.Run("humanTime", func(t *testing.T) {
		tests := []struct {
			name     string
			input    time.Time
			expected string
		}{
			{name: "zero time", input: time.Time{}, expected: "Never"},
			{name: "valid time", input: time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC), expected: "Jan 15, 14:30:45"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := s.humanTime(tt.input)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("humanDuration", func(t *testing.T) {
		tests := []struct {
			name     string
			input    time.Duration
			expected string
		}{
			{name: "seconds", input: 30 * time.Second, expected: "30s"},
			{name: "minutes", input: 5 * time.Minute, expected: "5m"},
			{name: "hours", input: 3 * time.Hour, expected: "3h"},
			{name: "days", input: 48 * time.Hour, expected: "2d"},
			{name: "less than minute", input: 45 * time.Second, expected: "45s"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := s.humanDuration(tt.input)
				assert.Equal(t, tt.expected, result)
			})
		}
	})

	t.Run("timeUntil", func(t *testing.T) {
		tests := []struct {
			name     string
			input    time.Time
			expected string
		}{
			{name: "zero time", input: time.Time{}, expected: "Never"},
			{name: "past time", input: time.Now().Add(-time.Hour), expected: "Overdue"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := s.timeUntil(tt.input)
				assert.Equal(t, tt.expected, result)
			})
		}

		// test future time dynamically to avoid timing issues
		t.Run("future time", func(t *testing.T) {
			futureTime := time.Now().Add(5 * time.Minute)
			result := s.timeUntil(futureTime)
			// should be approximately 5m, but allow for slight timing differences
			assert.Contains(t, []string{"5m", "4m"}, result)
		})
	})

	t.Run("truncate", func(t *testing.T) {
		tests := []struct {
			name     string
			input    string
			length   int
			expected string
		}{
			{name: "short string", input: "hello", length: 10, expected: "hello"},
			{name: "exact length", input: "hello", length: 5, expected: "hello"},
			{name: "long string", input: "hello world", length: 5, expected: "hello..."},
			{name: "empty string", input: "", length: 5, expected: ""},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := s.truncate(tt.input, tt.length)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}

func TestServer_render_ErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	t.Run("template not found", func(t *testing.T) {
		rec := httptest.NewRecorder()

		server.render(rec, "nonexistent.html", "test", nil)

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "Template not found")
	})

	t.Run("template execution error", func(t *testing.T) {
		// create a template with invalid data reference
		server.templates["error.html"] = template.Must(template.New("error").Parse(`{{.NonExistentField}}`))

		rec := httptest.NewRecorder()

		server.render(rec, "error.html", "error", struct{}{})

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Contains(t, rec.Body.String(), "Template error")
	})

	t.Run("template with nil data", func(t *testing.T) {
		server.templates["test.html"] = template.Must(template.New("test").Parse(`<div>Test</div>`))

		rec := httptest.NewRecorder()

		server.render(rec, "test.html", "test", nil)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "<div>Test</div>")
	})
}

func TestServer_getViewMode_CookieErrors(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
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

	t.Run("invalid view mode value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "view-mode",
			Value: "invalid-mode",
		})

		mode := server.getViewMode(req)
		assert.Equal(t, enums.ViewModeCards, mode) // should return default
	})

	t.Run("empty cookie value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "view-mode",
			Value: "",
		})

		mode := server.getViewMode(req)
		assert.Equal(t, enums.ViewModeCards, mode) // should return default
	})
}

func TestServer_getTheme_CookieErrors(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
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

	t.Run("invalid theme value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "theme",
			Value: "invalid-theme",
		})

		theme := server.getTheme(req)
		assert.Equal(t, enums.ThemeAuto, theme) // should return default
	})

	t.Run("empty cookie value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "theme",
			Value: "",
		})

		theme := server.getTheme(req)
		assert.Equal(t, enums.ThemeAuto, theme) // should return default
	})
}

func TestServer_getSortMode_CookieErrors(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
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

	t.Run("invalid sort mode value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "sort-mode",
			Value: "invalid-sort",
		})

		mode := server.getSortMode(req)
		assert.Equal(t, enums.SortModeDefault, mode) // should return default
	})

	t.Run("empty cookie value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "sort-mode",
			Value: "",
		})

		mode := server.getSortMode(req)
		assert.Equal(t, enums.SortModeDefault, mode) // should return default
	})
}

func TestServer_getFilterMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create crontab parser as jobs provider
	parser := crontab.New("test-crontab", 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	t.Run("no cookie returns all", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		mode := server.getFilterMode(req)
		assert.Equal(t, enums.FilterModeAll, mode)
	})

	t.Run("valid cookie", func(t *testing.T) {
		tests := []struct {
			cookieValue string
			expected    enums.FilterMode
		}{
			{"all", enums.FilterModeAll},
			{"running", enums.FilterModeRunning},
			{"success", enums.FilterModeSuccess},
			{"failed", enums.FilterModeFailed},
		}

		for _, tc := range tests {
			t.Run(tc.cookieValue, func(t *testing.T) {
				req := httptest.NewRequest("GET", "/", http.NoBody)
				req.AddCookie(&http.Cookie{
					Name:  "filter-mode",
					Value: tc.cookieValue,
				})
				mode := server.getFilterMode(req)
				assert.Equal(t, tc.expected, mode)
			})
		}
	})

	t.Run("invalid cookie returns all", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "filter-mode",
			Value: "invalid",
		})
		mode := server.getFilterMode(req)
		assert.Equal(t, enums.FilterModeAll, mode)
	})
}

func TestServer_filterJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create crontab parser as jobs provider
	parser := crontab.New("test-crontab", 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// create test jobs
	jobs := []persistence.JobInfo{
		{ID: "1", Command: "cmd1", IsRunning: true, LastStatus: enums.JobStatusRunning},
		{ID: "2", Command: "cmd2", IsRunning: false, LastStatus: enums.JobStatusSuccess},
		{ID: "3", Command: "cmd3", IsRunning: false, LastStatus: enums.JobStatusFailed},
		{ID: "4", Command: "cmd4", IsRunning: false, LastStatus: enums.JobStatusSuccess},
		{ID: "5", Command: "cmd5", IsRunning: true, LastStatus: enums.JobStatusRunning},
	}

	t.Run("filter all returns everything", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeAll)
		assert.Len(t, filtered, 5)
	})

	t.Run("filter running", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeRunning)
		assert.Len(t, filtered, 2)
		for _, job := range filtered {
			assert.True(t, job.IsRunning)
		}
	})

	t.Run("filter success", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeSuccess)
		assert.Len(t, filtered, 2)
		for _, job := range filtered {
			assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)
		}
	})

	t.Run("filter failed", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeFailed)
		assert.Len(t, filtered, 1)
		assert.Equal(t, enums.JobStatusFailed, filtered[0].LastStatus)
	})

	t.Run("empty input", func(t *testing.T) {
		filtered := server.filterJobs([]persistence.JobInfo{}, enums.FilterModeRunning)
		assert.Empty(t, filtered)
	})

	t.Run("nil input", func(t *testing.T) {
		filtered := server.filterJobs(nil, enums.FilterModeRunning)
		assert.Empty(t, filtered)
	})
}

func TestServer_handleFilterToggle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create crontab parser as jobs provider
	parser := crontab.New("test-crontab", 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// add test jobs
	server.jobs["1"] = persistence.JobInfo{ID: "1", Command: "cmd1", IsRunning: true, LastStatus: enums.JobStatusRunning}
	server.jobs["2"] = persistence.JobInfo{ID: "2", Command: "cmd2", IsRunning: false, LastStatus: enums.JobStatusSuccess}
	server.jobs["3"] = persistence.JobInfo{ID: "3", Command: "cmd3", IsRunning: false, LastStatus: enums.JobStatusFailed}

	// add minimal templates for testing
	tmpl := template.New("partials")
	tmpl = template.Must(tmpl.New("jobs-cards").Parse(`{{range .Jobs}}{{.Command}}{{end}}`))
	tmpl = template.Must(tmpl.New("jobs-list").Parse(`{{range .Jobs}}{{.Command}}{{end}}`))
	tmpl = template.Must(tmpl.New("filter-button").Parse(`<button><span id="filter-label">{{if eq .FilterMode.String "all"}}All Jobs{{else if eq .FilterMode.String "running"}}Running{{else if eq .FilterMode.String "success"}}Success{{else}}Failed{{end}}</span></button>`))
	tmpl = template.Must(tmpl.New("stats-updates").Parse(`{{if .IsOOB}}<span id="running-count" hx-swap-oob="innerHTML">{{.RunningCount}}</span><span id="next-run" hx-swap-oob="innerHTML">{{.NextRunTime}}</span><span id="total-count" hx-swap-oob="innerHTML">{{.TotalCount}}</span>{{end}}`))
	server.templates = map[string]*template.Template{
		"partials/jobs.html": tmpl,
	}

	tests := []struct {
		currentMode   string
		expectedNext  string
		expectedLabel string
	}{
		{"all", "running", "Running"},
		{"running", "success", "Success"},
		{"success", "failed", "Failed"},
		{"failed", "all", "All Jobs"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_to_%s", tc.currentMode, tc.expectedNext), func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/filter-toggle", http.NoBody)
			if tc.currentMode != "all" {
				req.AddCookie(&http.Cookie{
					Name:  "filter-mode",
					Value: tc.currentMode,
				})
			}

			w := httptest.NewRecorder()
			server.handleFilterToggle(w, req)

			resp := w.Result()
			defer resp.Body.Close()

			// check cookie was set
			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "filter-mode", cookies[0].Name)
			assert.Equal(t, tc.expectedNext, cookies[0].Value)

			// check response contains the filter label
			body := w.Body.String()
			assert.Contains(t, body, tc.expectedLabel)
			assert.Contains(t, body, `id="filter-label"`)
		})
	}

	t.Run("stats template error", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/filter-toggle", http.NoBody)
		w := httptest.NewRecorder()

		// create a template without stats-updates to trigger error
		tmpl := template.New("partials")
		tmpl = template.Must(tmpl.New("jobs-cards").Parse(`{{range .Jobs}}{{.Command}}{{end}}`))
		tmpl = template.Must(tmpl.New("filter-button").Parse(`<button>test</button>`))
		// intentionally missing stats-updates template
		server.templates = map[string]*template.Template{
			"partials/jobs.html": tmpl,
		}

		server.handleFilterToggle(w, req)

		resp := w.Result()
		defer resp.Body.Close()

		// should get error response
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		body := w.Body.String()
		assert.Equal(t, "Failed to render jobs\n", body)
	})
}

func TestServer_getJobsWithStats_WithFilter(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create crontab parser as jobs provider
	parser := crontab.New("test-crontab", 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// add test jobs with different statuses
	now := time.Now()
	server.jobs = map[string]persistence.JobInfo{
		"1": {ID: "1", Command: "running1", IsRunning: true, LastStatus: enums.JobStatusRunning, NextRun: now.Add(time.Hour)},
		"2": {ID: "2", Command: "success1", IsRunning: false, LastStatus: enums.JobStatusSuccess, NextRun: now.Add(2 * time.Hour)},
		"3": {ID: "3", Command: "failed1", IsRunning: false, LastStatus: enums.JobStatusFailed, NextRun: now.Add(3 * time.Hour)},
		"4": {ID: "4", Command: "success2", IsRunning: false, LastStatus: enums.JobStatusSuccess, NextRun: now.Add(4 * time.Hour)},
		"5": {ID: "5", Command: "running2", IsRunning: true, LastStatus: enums.JobStatusRunning, NextRun: now.Add(30 * time.Minute)},
	}

	t.Run("filter all", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeAll)
		assert.Len(t, stats.jobs, 5)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		assert.NotEqual(t, "-", stats.nextRunTime)
	})

	t.Run("filter running", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeRunning)
		assert.Len(t, stats.jobs, 2)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		for _, job := range stats.jobs {
			assert.True(t, job.IsRunning)
		}
	})

	t.Run("filter success", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeSuccess)
		assert.Len(t, stats.jobs, 2)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		for _, job := range stats.jobs {
			assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)
		}
	})

	t.Run("filter failed", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeFailed)
		assert.Len(t, stats.jobs, 1)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		assert.Equal(t, enums.JobStatusFailed, stats.jobs[0].LastStatus)
	})

	t.Run("sorting works with filtering", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeNextrun, enums.FilterModeAll)
		assert.Len(t, stats.jobs, 5)
		// verify sorted by next run time
		for i := 1; i < len(stats.jobs); i++ {
			if !stats.jobs[i-1].NextRun.IsZero() && !stats.jobs[i].NextRun.IsZero() {
				assert.True(t, stats.jobs[i-1].NextRun.Before(stats.jobs[i].NextRun) ||
					stats.jobs[i-1].NextRun.Equal(stats.jobs[i].NextRun))
			}
		}
	})
}

func TestServer_sortJobs_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
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

	now := time.Now()

	t.Run("sort by next run with zero times", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1", NextRun: time.Time{}},
			{ID: "2", Command: "cmd2", NextRun: now.Add(time.Hour)},
			{ID: "3", Command: "cmd3", NextRun: time.Time{}},
			{ID: "4", Command: "cmd4", NextRun: now.Add(-time.Hour)},
		}

		server.sortJobs(jobs, enums.SortModeNextrun)
		sorted := jobs

		// zero times should be sorted last
		assert.Equal(t, "4", sorted[0].ID) // past time first
		assert.Equal(t, "2", sorted[1].ID) // future time second
		assert.Equal(t, "1", sorted[2].ID) // zero times last
		assert.Equal(t, "3", sorted[3].ID) // zero times last
	})

	t.Run("sort by last run with zero times", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1", LastRun: now},
			{ID: "2", Command: "cmd2", LastRun: time.Time{}},
			{ID: "3", Command: "cmd3", LastRun: now.Add(-time.Hour)},
			{ID: "4", Command: "cmd4", LastRun: time.Time{}},
		}

		server.sortJobs(jobs, enums.SortModeLastrun)
		sorted := jobs

		// most recent first, zero times last
		assert.Equal(t, "1", sorted[0].ID) // most recent
		assert.Equal(t, "3", sorted[1].ID) // older
		assert.Equal(t, "2", sorted[2].ID) // zero time
		assert.Equal(t, "4", sorted[3].ID) // zero time
	})

	t.Run("empty job list", func(t *testing.T) {
		jobs := []persistence.JobInfo{}
		server.sortJobs(jobs, enums.SortModeDefault)
		assert.Empty(t, jobs)
	})

	t.Run("single job", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1"},
		}
		server.sortJobs(jobs, enums.SortModeDefault)
		assert.Len(t, jobs, 1)
		assert.Equal(t, "1", jobs[0].ID)
	})

	t.Run("sort by status with all same status", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "zzz", LastStatus: enums.JobStatusSuccess},
			{ID: "2", Command: "aaa", LastStatus: enums.JobStatusSuccess},
			{ID: "3", Command: "mmm", LastStatus: enums.JobStatusSuccess},
		}

		server.sortJobs(jobs, enums.SortModeDefault)
		sorted := jobs

		// when all statuses are same, should maintain original order
		assert.Equal(t, "1", sorted[0].ID)
		assert.Equal(t, "2", sorted[1].ID)
		assert.Equal(t, "3", sorted[2].ID)
	})
}

func TestServer_Run(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab file
	err := os.WriteFile(crontabFile, []byte("* * * * * echo test\n"), 0o600)
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

	// start server in background
	done := make(chan error)
	go func() {
		done <- server.Run(ctx, ":0")
	}()

	// give server time to start
	time.Sleep(100 * time.Millisecond)

	// cancel context to stop server
	cancel()

	// wait for server to stop
	select {
	case err := <-done:
		// server should return http.ErrServerClosed when shut down gracefully
		assert.Equal(t, http.ErrServerClosed, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func TestServer_handleRunJob(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create manual trigger channel for testing
	manualTrigger := make(chan service.ManualJobRequest, 10)

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		ManualTrigger:  manualTrigger,
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// add test job to server
	testJob := persistence.JobInfo{
		ID:         "test-job-id",
		Command:    "echo test",
		Schedule:   "* * * * *",
		Enabled:    true,
		IsRunning:  false,
		LastStatus: enums.JobStatusIdle,
	}
	server.jobsMu.Lock()
	server.jobs[testJob.ID] = testJob
	server.jobsMu.Unlock()

	t.Run("successful manual trigger", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "Job triggered", w.Body.String())

		// verify manual trigger was sent
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "echo test", trigger.Command)
			assert.Equal(t, "* * * * *", trigger.Schedule)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("manual trigger not received")
		}
	})

	t.Run("job not found", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/jobs/nonexistent/run", http.NoBody)
		req.SetPathValue("id", "nonexistent")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Job not found")
	})

	t.Run("job disabled", func(t *testing.T) {
		// add disabled job
		disabledJob := persistence.JobInfo{
			ID:        "disabled-job",
			Command:   "echo disabled",
			Schedule:  "* * * * *",
			Enabled:   false,
			IsRunning: false,
		}
		server.jobsMu.Lock()
		server.jobs[disabledJob.ID] = disabledJob
		server.jobsMu.Unlock()

		req := httptest.NewRequest("POST", "/api/jobs/disabled-job/run", http.NoBody)
		req.SetPathValue("id", "disabled-job")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Job is disabled")
	})

	t.Run("job already running", func(t *testing.T) {
		// add running job
		runningJob := persistence.JobInfo{
			ID:        "running-job",
			Command:   "echo running",
			Schedule:  "* * * * *",
			Enabled:   true,
			IsRunning: true,
		}
		server.jobsMu.Lock()
		server.jobs[runningJob.ID] = runningJob
		server.jobsMu.Unlock()

		req := httptest.NewRequest("POST", "/api/jobs/running-job/run", http.NoBody)
		req.SetPathValue("id", "running-job")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), "Job already running")
	})

	t.Run("manual trigger channel full", func(t *testing.T) {
		// create server with full channel
		fullChannel := make(chan service.ManualJobRequest, 1)
		fullChannel <- service.ManualJobRequest{} // fill the channel

		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test2.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			ManualTrigger:  fullChannel,
			JobsProvider:   createTestProvider(t, tmpDir),
		}

		server2, err := New(cfg)
		require.NoError(t, err)
		defer server2.store.Close()

		// add test job
		server2.jobsMu.Lock()
		server2.jobs[testJob.ID] = testJob
		server2.jobsMu.Unlock()

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server2.handleRunJob(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "System busy")
	})

	t.Run("no manual trigger configured", func(t *testing.T) {
		// create server without manual trigger
		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test3.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			ManualTrigger:  nil,
			JobsProvider:   createTestProvider(t, tmpDir),
		}

		server3, err := New(cfg)
		require.NoError(t, err)
		defer server3.store.Close()

		// add test job
		server3.jobsMu.Lock()
		server3.jobs[testJob.ID] = testJob
		server3.jobsMu.Unlock()

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server3.handleRunJob(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Contains(t, w.Body.String(), "Manual trigger not configured")
	})
}
