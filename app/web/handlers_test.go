package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

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
	server.OnJobStart(request.OnJobStart{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime, EndTime: startTime.Add(time.Second), ExitCode: 0, Output: "", Err: nil})
	server.OnJobStart(request.OnJobStart{Command: "echo test2", ExecutedCommand: "echo test2", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour)})
	server.OnJobComplete(request.OnJobComplete{Command: "echo test2", ExecutedCommand: "echo test2", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour), EndTime: startTime.Add(-59 * time.Minute), ExitCode: 1, Output: "", Err: fmt.Errorf("failed")})

	// start event processor
	ctx := t.Context()
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
	ctx := t.Context()
	go server.processEvents(ctx)

	// add test job
	server.OnJobStart(request.OnJobStart{Command: "echo test", ExecutedCommand: "echo test", Schedule: "* * * * *", StartTime: time.Now()})
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
	ctx := t.Context()
	go server.processEvents(ctx)

	// add test jobs
	startTime := time.Now()
	server.OnJobStart(request.OnJobStart{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime, EndTime: startTime.Add(time.Second), ExitCode: 0, Output: "", Err: nil})
	server.OnJobStart(request.OnJobStart{Command: "echo test2", ExecutedCommand: "echo test2", Schedule: "@daily", StartTime: startTime})

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
	// verify view-mode-button IS rendered during polling (for multi-tab sync)
	assert.Contains(t, body, "view-toggle")

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
	// verify view-mode-button IS rendered during polling (for multi-tab sync)
	assert.Contains(t, body, "view-toggle")
}

func TestServer_handleAPIJobs_Search(t *testing.T) {
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
	ctx := t.Context()
	go server.processEvents(ctx)

	// add test jobs with different commands
	startTime := time.Now()
	server.OnJobStart(request.OnJobStart{Command: "echo backup daily", ExecutedCommand: "echo backup daily", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{Command: "echo backup daily", ExecutedCommand: "echo backup daily", Schedule: "* * * * *", StartTime: startTime, EndTime: startTime.Add(time.Second), ExitCode: 0, Output: "", Err: nil})
	server.OnJobStart(request.OnJobStart{Command: "echo cleanup logs", ExecutedCommand: "echo cleanup logs", Schedule: "@daily", StartTime: startTime})
	server.OnJobStart(request.OnJobStart{Command: "python backup.py", ExecutedCommand: "python backup.py", Schedule: "@weekly", StartTime: startTime})

	// wait for all events to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		return len(server.jobs) == 3
	}, time.Second, 10*time.Millisecond)

	// test search for "backup"
	req := httptest.NewRequest("GET", "/api/jobs?search=backup", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w := httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body := w.Body.String()
	assert.Contains(t, body, "backup daily")
	assert.Contains(t, body, "backup.py")
	assert.NotContains(t, body, "cleanup logs")

	// test case-insensitive search
	req = httptest.NewRequest("GET", "/api/jobs?search=BACKUP", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w = httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = w.Body.String()
	assert.Contains(t, body, "backup daily")
	assert.Contains(t, body, "backup.py")
	assert.NotContains(t, body, "cleanup logs")

	// test search for "python"
	req = httptest.NewRequest("GET", "/api/jobs?search=python", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w = httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = w.Body.String()
	assert.Contains(t, body, "backup.py")
	assert.NotContains(t, body, "backup daily")
	assert.NotContains(t, body, "cleanup logs")

	// test empty search returns all jobs
	req = httptest.NewRequest("GET", "/api/jobs?search=", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w = httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = w.Body.String()
	assert.Contains(t, body, "backup daily")
	assert.Contains(t, body, "cleanup logs")
	assert.Contains(t, body, "backup.py")

	// test search with no matches
	req = httptest.NewRequest("GET", "/api/jobs?search=nonexistent", http.NoBody)
	req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
	w = httptest.NewRecorder()

	server.handleJobsPartial(w, req)

	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body = w.Body.String()
	assert.NotContains(t, body, "backup daily")
	assert.NotContains(t, body, "cleanup logs")
	assert.NotContains(t, body, "backup.py")
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
		{"light to dark", "light", "dark"},
		{"dark to light", "dark", "light"},
		{"no cookie - dark default toggles to light", "", "light"},
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
	server.OnJobStart(request.OnJobStart{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime})
	server.OnJobComplete(request.OnJobComplete{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime, EndTime: startTime.Add(time.Second), ExitCode: 0, Output: "", Err: nil})
	server.OnJobStart(request.OnJobStart{Command: "echo test2", ExecutedCommand: "echo test2", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour)})
	server.OnJobComplete(request.OnJobComplete{Command: "echo test2", ExecutedCommand: "echo test2", Schedule: "0 * * * *", StartTime: startTime.Add(-time.Hour), EndTime: startTime.Add(-time.Hour).Add(time.Second), ExitCode: 0, Output: "", Err: nil})

	// start event processor to handle the job events
	ctx := t.Context()
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
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeAll, "")
		assert.Len(t, stats.jobs, 5)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		assert.NotEqual(t, "-", stats.nextRunTime)
	})

	t.Run("filter running", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeRunning, "")
		assert.Len(t, stats.jobs, 2)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		for _, job := range stats.jobs {
			assert.True(t, job.IsRunning)
		}
	})

	t.Run("filter success", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeSuccess, "")
		assert.Len(t, stats.jobs, 2)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		for _, job := range stats.jobs {
			assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)
		}
	})

	t.Run("filter failed", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeDefault, enums.FilterModeFailed, "")
		assert.Len(t, stats.jobs, 1)
		assert.Equal(t, 2, stats.runningCount)
		assert.Equal(t, 5, stats.totalCount)
		assert.Equal(t, enums.JobStatusFailed, stats.jobs[0].LastStatus)
	})

	t.Run("sorting works with filtering", func(t *testing.T) {
		stats := server.getJobsWithStats(enums.SortModeNextrun, enums.FilterModeAll, "")
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
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger"))

		// verify manual trigger was sent
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "test-job-id", trigger.JobID)
			assert.Equal(t, "echo test", trigger.Command)
			assert.Equal(t, "* * * * *", trigger.Schedule)
			assert.Nil(t, trigger.CustomDate)
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

	t.Run("manual trigger with edited command", func(t *testing.T) {
		// create form data with edited command
		form := url.Values{}
		form.Add("command", "echo edited")

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "test-job-id")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		// verify edited command was sent
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "test-job-id", trigger.JobID)
			assert.Equal(t, "echo edited", trigger.Command)
			assert.Equal(t, "* * * * *", trigger.Schedule)
			assert.Nil(t, trigger.CustomDate)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("manual trigger not received")
		}
	})

	t.Run("manual trigger with custom date", func(t *testing.T) {
		// create form data with custom date
		form := url.Values{}
		form.Add("command", "echo test")
		form.Add("date", "20241225")

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "test-job-id")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		// verify custom date was sent in local timezone
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "test-job-id", trigger.JobID)
			assert.Equal(t, "echo test", trigger.Command)
			assert.Equal(t, "* * * * *", trigger.Schedule)
			require.NotNil(t, trigger.CustomDate)
			// verify date components
			assert.Equal(t, 2024, trigger.CustomDate.Year())
			assert.Equal(t, time.December, trigger.CustomDate.Month())
			assert.Equal(t, 25, trigger.CustomDate.Day())
			// verify it's midnight in local timezone, not UTC
			assert.Equal(t, time.Local, trigger.CustomDate.Location())
			assert.Equal(t, 0, trigger.CustomDate.Hour())
			assert.Equal(t, 0, trigger.CustomDate.Minute())
			assert.Equal(t, 0, trigger.CustomDate.Second())
		case <-time.After(100 * time.Millisecond):
			t.Fatal("manual trigger not received")
		}
	})

	t.Run("manual trigger with invalid date format", func(t *testing.T) {
		// create form data with invalid date
		form := url.Values{}
		form.Add("command", "echo test")
		form.Add("date", "invalid")

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "test-job-id")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server.handleRunJob(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid date format")
	})

	t.Run("manual execution disabled", func(t *testing.T) {
		// create server with manual execution disabled
		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test4.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			ManualTrigger:  manualTrigger,
			JobsProvider:   createTestProvider(t, tmpDir),
			DisableManual:  true,
		}

		server4, err := New(cfg)
		require.NoError(t, err)
		defer server4.store.Close()

		// add test job
		server4.jobsMu.Lock()
		server4.jobs[testJob.ID] = testJob
		server4.jobsMu.Unlock()

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server4.handleRunJob(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "Manual job execution is disabled")
	})

	t.Run("command editing disabled", func(t *testing.T) {
		// create server with command editing disabled
		cfg := Config{
			DBPath:             filepath.Join(tmpDir, "test5.db"),
			UpdateInterval:     time.Minute,
			Version:            "test",
			ManualTrigger:      manualTrigger,
			JobsProvider:       createTestProvider(t, tmpDir),
			DisableCommandEdit: true,
		}

		server5, err := New(cfg)
		require.NoError(t, err)
		defer server5.store.Close()

		// add test job
		server5.jobsMu.Lock()
		server5.jobs[testJob.ID] = testJob
		server5.jobsMu.Unlock()

		// try to run with edited command
		form := url.Values{}
		form.Add("command", "echo modified")

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "test-job-id")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server5.handleRunJob(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "Command editing is disabled")
	})

	t.Run("command editing disabled but command unchanged", func(t *testing.T) {
		// create server with command editing disabled
		cfg := Config{
			DBPath:             filepath.Join(tmpDir, "test6.db"),
			UpdateInterval:     time.Minute,
			Version:            "test",
			ManualTrigger:      manualTrigger,
			JobsProvider:       createTestProvider(t, tmpDir),
			DisableCommandEdit: true,
		}

		server6, err := New(cfg)
		require.NoError(t, err)
		defer server6.store.Close()

		// add test job
		server6.jobsMu.Lock()
		server6.jobs[testJob.ID] = testJob
		server6.jobsMu.Unlock()

		// try to run with same command (should succeed)
		form := url.Values{}
		form.Add("command", "echo test")

		req := httptest.NewRequest("POST", "/api/jobs/test-job-id/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "test-job-id")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server6.handleRunJob(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		// verify manual trigger was sent with original command
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "test-job-id", trigger.JobID)
			assert.Equal(t, "echo test", trigger.Command)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("manual trigger not received")
		}
	})

	t.Run("command editing disabled with templated command", func(t *testing.T) {
		// create server with command editing disabled
		cfg := Config{
			DBPath:             filepath.Join(tmpDir, "test7.db"),
			UpdateInterval:     time.Minute,
			Version:            "test",
			ManualTrigger:      manualTrigger,
			JobsProvider:       createTestProvider(t, tmpDir),
			DisableCommandEdit: true,
		}

		server7, err := New(cfg)
		require.NoError(t, err)
		defer server7.store.Close()

		// add test job with template
		templatedJob := persistence.JobInfo{
			ID:         "templated-job",
			Command:    "echo {{.YYYYMMDD}}",
			Schedule:   "* * * * *",
			Enabled:    true,
			IsRunning:  false,
			LastStatus: enums.JobStatusIdle,
		}
		server7.jobsMu.Lock()
		server7.jobs[templatedJob.ID] = templatedJob
		server7.jobsMu.Unlock()

		// try to run with same templated command (should succeed)
		form := url.Values{}
		form.Add("command", "echo {{.YYYYMMDD}}")

		req := httptest.NewRequest("POST", "/api/jobs/templated-job/run", strings.NewReader(form.Encode()))
		req.SetPathValue("id", "templated-job")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		server7.handleRunJob(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		// verify manual trigger was sent with templated command
		select {
		case trigger := <-manualTrigger:
			assert.Equal(t, "templated-job", trigger.JobID)
			assert.Equal(t, "echo {{.YYYYMMDD}}", trigger.Command)
		case <-time.After(100 * time.Millisecond):
			t.Fatal("manual trigger not received")
		}
	})
}

func TestServer_handleJobHistory(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	crontabFile := filepath.Join(tmpDir, "dummy")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{DBPath: dbPath, UpdateInterval: time.Minute, Version: "test", JobsProvider: parser}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	testJob := persistence.JobInfo{ID: "test-job-id", Command: "echo test", Schedule: "* * * * *", Enabled: true, LastStatus: enums.JobStatusIdle}
	server.jobsMu.Lock()
	server.jobs[testJob.ID] = testJob
	server.jobsMu.Unlock()

	t.Run("successful retrieval with executions", func(t *testing.T) {
		baseTime := time.Now()
		err = server.store.RecordExecution(request.RecordExecution{JobID: "test-job-id", StartedAt: baseTime.Add(-5 * time.Minute), FinishedAt: baseTime.Add(-4 * time.Minute), Status: enums.JobStatusSuccess, ExitCode: 0, ExecutedCommand: "echo test1", Output: ""})
		require.NoError(t, err)
		err = server.store.RecordExecution(request.RecordExecution{JobID: "test-job-id", StartedAt: baseTime.Add(-2 * time.Minute), FinishedAt: baseTime.Add(-1 * time.Minute), Status: enums.JobStatusFailed, ExitCode: 1, ExecutedCommand: "echo test2", Output: ""})
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/api/jobs/test-job-id/history", http.NoBody)
		req.SetPathValue("id", "test-job-id")
		w := httptest.NewRecorder()

		server.handleJobHistory(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Execution History")
		assert.Contains(t, body, "echo test")
		assert.Contains(t, body, "Success")
		assert.Contains(t, body, "Failed")
		assert.Contains(t, body, "history-table")
	})

	t.Run("job not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs/nonexistent/history", http.NoBody)
		req.SetPathValue("id", "nonexistent")
		w := httptest.NewRecorder()

		server.handleJobHistory(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Job not found")
	})

	t.Run("empty job id", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs//history", http.NoBody)
		w := httptest.NewRecorder()

		server.handleJobHistory(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Job ID required")
	})

	t.Run("no executions", func(t *testing.T) {
		emptyJob := persistence.JobInfo{ID: "empty-job-id", Command: "echo empty", Schedule: "* * * * *", Enabled: true}
		server.jobsMu.Lock()
		server.jobs[emptyJob.ID] = emptyJob
		server.jobsMu.Unlock()

		req := httptest.NewRequest("GET", "/api/jobs/empty-job-id/history", http.NoBody)
		req.SetPathValue("id", "empty-job-id")
		w := httptest.NewRecorder()

		server.handleJobHistory(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Execution History")
		assert.Contains(t, body, "No execution history available")
	})
}

func TestServer_handleSettingsModal(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	settingsInfo := SettingsInfo{
		Version:             "v1.0.0",
		StartTime:           time.Now().Add(-1 * time.Hour),
		WebEnabled:          true,
		WebAddress:          ":8080",
		WebUpdateInterval:   30 * time.Second,
		AuthEnabled:         true,
		ManualEnabled:       true,
		CommandEditEnabled:  true,
		CrontabPath:         "test-crontab",
		UpdateEnabled:       true,
		UpdateInterval:      10 * time.Second,
		JitterEnabled:       true,
		DeDupEnabled:        true,
		MaxConcurrentChecks: 10,
		ResumeEnabled:       true,
		ResumePath:          "/tmp/resume",
		AltTemplateFormat:   false,
		RepeaterAttempts:    3,
		RepeaterDuration:    5 * time.Second,
		RepeaterFactor:      2.0,
		RepeaterJitter:      true,
		EmailNotifications:  true,
		SlackIntegration:    true,
		SlackChannelCount:   2,
		TelegramIntegration: true,
		TelegramDestCount:   3,
		WebhookCount:        1,
		NotificationTimeout: 10 * time.Second,
		LoggingEnabled:      true,
		DebugMode:           false,
		LogFilePath:         "/var/log/cronn.log",
		LogMaxSize:          100,
		LogMaxAge:           30,
		LogMaxBackups:       7,
	}

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
		Settings:       settingsInfo,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	req := httptest.NewRequest("GET", "/api/settings/modal", http.NoBody)
	w := httptest.NewRecorder()

	server.handleSettingsModal(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, w.Body.String(), "Settings & About")
	assert.Contains(t, w.Body.String(), "v1.0.0")
	assert.Contains(t, w.Body.String(), ":8080")
	assert.Contains(t, w.Body.String(), "test-crontab")
}

func TestServer_handleExecutionLogs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{DBPath: dbPath, UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), ExecMaxLogLines: 100, LogExecMaxHist: 50}

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
	server.OnJobComplete(request.OnJobComplete{Command: "echo test1", ExecutedCommand: "echo test1", Schedule: "* * * * *", StartTime: startTime, EndTime: endTime, ExitCode: 0, Output: "test output line 1\ntest output line 2", Err: nil})

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

	t.Run("valid request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs/"+jobID+"/executions/"+fmt.Sprint(execID)+"/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", fmt.Sprint(execID))
		w := httptest.NewRecorder()

		server.handleExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body := w.Body.String()
		assert.Contains(t, body, "test output line 1")
		assert.Contains(t, body, "test output line 2")
	})

	t.Run("missing job id", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs//executions/"+fmt.Sprint(execID)+"/logs", http.NoBody)
		req.SetPathValue("exec_id", fmt.Sprint(execID))
		w := httptest.NewRecorder()

		server.handleExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid execution id", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs/"+jobID+"/executions/invalid/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", "invalid")
		w := httptest.NewRecorder()

		server.handleExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("non-existent execution", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/jobs/"+jobID+"/executions/99999/logs", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", "99999")
		w := httptest.NewRecorder()

		server.handleExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("execution belongs to different job", func(t *testing.T) {
		wrongJobID := HashCommand("echo test2")
		req := httptest.NewRequest("GET", "/api/jobs/"+wrongJobID+"/executions/"+fmt.Sprint(execID)+"/logs", http.NoBody)
		req.SetPathValue("id", wrongJobID)
		req.SetPathValue("exec_id", fmt.Sprint(execID))
		w := httptest.NewRecorder()

		server.handleExecutionLogs(w, req)

		resp := w.Result()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}
