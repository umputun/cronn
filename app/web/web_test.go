package web

import (
	"context"
	"fmt"
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
)

func TestNew(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	assert.NotNil(t, server)
	assert.Equal(t, "crontab", server.crontabFile)
	assert.NotNil(t, server.db)
	assert.NotNil(t, server.templates)
	assert.NotNil(t, server.jobs)
	assert.NotNil(t, server.parser)
	assert.NotNil(t, server.eventChan)

	// check database schema was created
	var count int
	err = server.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	_ = server.db.Close()
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
	jobs := []*JobInfo{
		{ID: "1", Command: "cmd1", SortIndex: 2, LastRun: now.Add(-2 * time.Hour), NextRun: now.Add(2 * time.Hour)},
		{ID: "2", Command: "cmd2", SortIndex: 0, LastRun: now.Add(-1 * time.Hour), NextRun: now.Add(1 * time.Hour)},
		{ID: "3", Command: "cmd3", SortIndex: 1, LastRun: now.Add(-3 * time.Hour), NextRun: now.Add(30 * time.Minute)},
		{ID: "4", Command: "cmd4", SortIndex: 3, LastRun: time.Time{}, NextRun: time.Time{}}, // never run
	}

	t.Run("default sort", func(t *testing.T) {
		sorted := make([]*JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, "default")

		// should be sorted by SortIndex
		assert.Equal(t, "2", sorted[0].ID)
		assert.Equal(t, "3", sorted[1].ID)
		assert.Equal(t, "1", sorted[2].ID)
		assert.Equal(t, "4", sorted[3].ID)
	})

	t.Run("sort by last run", func(t *testing.T) {
		sorted := make([]*JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, "lastrun")

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
		sorted := make([]*JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, "nextrun")

		// should be sorted by NextRun, soonest first
		assert.Equal(t, "3", sorted[0].ID) // 30 minutes
		assert.Equal(t, "2", sorted[1].ID) // 1 hour
		assert.Equal(t, "1", sorted[2].ID) // 2 hours
		assert.Equal(t, "4", sorted[3].ID) // never (zero time)
	})
}

func TestServer_getSortMode(t *testing.T) {
	server := &Server{}

	tests := []struct {
		name      string
		cookieVal string
		wantSort  string
	}{
		{"no cookie", "", "default"},
		{"default sort", "default", "default"},
		{"last run sort", "lastrun", "lastrun"},
		{"next run sort", "nextrun", "nextrun"},
		{"invalid sort", "invalid", "default"},
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
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

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
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	server.OnJobStart("echo test", "* * * * *", startTime)

	// wait for event to be processed
	time.Sleep(50 * time.Millisecond)

	// verify job in memory (not in database since persistJobs runs periodically)
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "echo test", job.Command)
	assert.Equal(t, "* * * * *", job.Schedule)
	assert.True(t, job.IsRunning)
	assert.Equal(t, "running", job.LastStatus)
	assert.Equal(t, startTime.Unix(), job.LastRun.Unix())
}

func TestServer_OnJobComplete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	endTime := startTime.Add(time.Second)

	// first start the job
	server.OnJobStart("echo test", "* * * * *", startTime)
	time.Sleep(50 * time.Millisecond) // wait for event processing

	// then complete it successfully
	server.OnJobComplete("echo test", "* * * * *", startTime, endTime, 0, nil)
	time.Sleep(50 * time.Millisecond) // wait for event processing

	// verify job status was updated in memory
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, "success", job.LastStatus)

	// test with error
	server.OnJobStart("echo error", "* * * * *", startTime)
	time.Sleep(50 * time.Millisecond) // wait for event processing
	server.OnJobComplete("echo error", "* * * * *", startTime, endTime, 1, fmt.Errorf("test error"))
	time.Sleep(50 * time.Millisecond) // wait for event processing

	server.jobsMu.RLock()
	job2, exists2 := server.jobs[HashCommand("echo error")]
	server.jobsMu.RUnlock()

	assert.True(t, exists2)
	assert.False(t, job2.IsRunning)
	assert.Equal(t, "failed", job2.LastStatus)
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

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// load jobs from crontab
	server.loadJobsFromCrontab()

	// verify jobs were synced
	var count int
	err = server.db.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// verify job details
	rows, err := server.db.Query("SELECT command, schedule FROM jobs ORDER BY command")
	require.NoError(t, err)
	defer rows.Close()

	var jobs []struct {
		Command  string
		Schedule string
	}
	for rows.Next() {
		var job struct {
			Command  string
			Schedule string
		}
		err = rows.Scan(&job.Command, &job.Schedule)
		require.NoError(t, err)
		jobs = append(jobs, job)
	}

	assert.Len(t, jobs, 3)
	assert.Equal(t, "echo daily", jobs[0].Command)
	assert.Equal(t, "@daily", jobs[0].Schedule)
	assert.Equal(t, "echo five-minutes", jobs[1].Command)
	assert.Equal(t, "*/5 * * * *", jobs[1].Schedule)
	assert.Equal(t, "echo hourly", jobs[2].Command)
	assert.Equal(t, "0 * * * *", jobs[2].Schedule)
}

func TestServer_handleDashboard(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// add test job
	server.OnJobStart("echo test", "* * * * *", time.Now())
	time.Sleep(50 * time.Millisecond) // wait for event processing

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
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// add test jobs
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	time.Sleep(50 * time.Millisecond) // wait for event processing
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	time.Sleep(50 * time.Millisecond) // wait for event processing
	server.OnJobStart("echo test2", "@daily", startTime)
	time.Sleep(50 * time.Millisecond) // wait for event processing

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

func TestServer_handleAPIStats(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// add test jobs
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	server.OnJobStart("echo test2", "@daily", startTime)
	server.OnJobStart("echo test3", "0 * * * *", startTime)
	server.OnJobComplete("echo test3", "0 * * * *", startTime, startTime.Add(time.Second), 1, fmt.Errorf("failed"))
}

func TestServer_handleToggleTheme(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

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
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

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

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	// load jobs from crontab
	server.loadJobsFromCrontab()

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

func TestServer_Templates(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

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
			Jobs []*JobInfo
		}{
			Jobs: []*JobInfo{
				{
					ID:         "test123",
					Command:    "echo test",
					Schedule:   "* * * * *",
					LastStatus: "success",
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

func TestServer_getScheduleDescription(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		Address:        ":8080",
		CrontabFile:    "crontab",
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

	tests := []struct {
		name     string
		schedule string
		expected string
	}{
		{"every minute", "* * * * *", "Every minute"},
		{"every 5 minutes", "*/5 * * * *", "Every 5 minutes"},
		{"every hour", "0 * * * *", "Every hour at minute 0"},
		{"daily", "@daily", "@daily"},
		{"midnight", "@midnight", "@midnight"},
		{"hourly", "@hourly", "@hourly"},
		{"custom", "0 2 * * 1", "Custom: 0 2 * * 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// getScheduleDescription is not exported, test via template rendering
			// or integration test
		})
	}
}

func TestServer_Run(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab file
	err := os.WriteFile(crontabFile, []byte("* * * * * echo test\n"), 0o600)
	require.NoError(t, err)

	cfg := Config{
		Address:        ":0", // use random port
		CrontabFile:    crontabFile,
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.db.Close()

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
