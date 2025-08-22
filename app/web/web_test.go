package web

import (
	"context"
	"database/sql"
	"encoding/json"
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
	assert.Equal(t, ":8080", server.address)
	assert.Equal(t, "crontab", server.crontabFile)
	assert.NotNil(t, server.db)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.templates)
	
	// check database schema was created
	var count int
	err = server.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	
	server.db.Close()
}

func TestHashCommand(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"simple command", "echo hello", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"complex command", "sh -c 'echo test'", "e0a7b1e5e77e8fe3e6e0e0f4e8db5bb1a7e6f8c5f3f76c5c3bb7bb6f9a6dc3e0"},
		{"empty command", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HashCommand(tt.cmd)
			assert.Equal(t, 8, len(got))
			// verify consistency
			assert.Equal(t, got, HashCommand(tt.cmd))
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
	
	startTime := time.Now()
	server.OnJobStart("echo test", "* * * * *", startTime)
	
	// verify job was inserted
	var count int
	err = server.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE command = ?", "echo test").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	
	// verify job details
	var job struct {
		Status    string
		StartTime sql.NullTime
		EndTime   sql.NullTime
	}
	err = server.db.QueryRow(
		"SELECT status, start_time, end_time FROM jobs WHERE command = ?",
		"echo test",
	).Scan(&job.Status, &job.StartTime, &job.EndTime)
	require.NoError(t, err)
	assert.Equal(t, "running", job.Status)
	assert.True(t, job.StartTime.Valid)
	assert.False(t, job.EndTime.Valid)
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
	
	startTime := time.Now()
	endTime := startTime.Add(time.Second)
	
	// first start the job
	server.OnJobStart("echo test", "* * * * *", startTime)
	
	// then complete it successfully
	server.OnJobComplete("echo test", "* * * * *", startTime, endTime, 0, nil)
	
	// verify job status was updated
	var job struct {
		Status    string
		ExitCode  sql.NullInt64
		Error     sql.NullString
		StartTime sql.NullTime
		EndTime   sql.NullTime
	}
	err = server.db.QueryRow(
		"SELECT status, exit_code, error, start_time, end_time FROM jobs WHERE command = ?",
		"echo test",
	).Scan(&job.Status, &job.ExitCode, &job.Error, &job.StartTime, &job.EndTime)
	require.NoError(t, err)
	assert.Equal(t, "success", job.Status)
	assert.True(t, job.ExitCode.Valid)
	assert.Equal(t, int64(0), job.ExitCode.Int64)
	assert.False(t, job.Error.Valid)
	assert.True(t, job.EndTime.Valid)
	
	// test with error
	server.OnJobStart("echo error", "* * * * *", startTime)
	server.OnJobComplete("echo error", "* * * * *", startTime, endTime, 1, fmt.Errorf("test error"))
	
	err = server.db.QueryRow(
		"SELECT status, exit_code, error FROM jobs WHERE command = ?",
		"echo error",
	).Scan(&job.Status, &job.ExitCode, &job.Error)
	require.NoError(t, err)
	assert.Equal(t, "error", job.Status)
	assert.Equal(t, int64(1), job.ExitCode.Int64)
	assert.True(t, job.Error.Valid)
	assert.Equal(t, "test error", job.Error.String)
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
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0644)
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
	
	err = server.syncWithCrontab()
	require.NoError(t, err)
	
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
	
	// add test job
	server.OnJobStart("echo test", "* * * * *", time.Now())
	
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	
	server.handleDashboard(w, req)
	
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	
	body := w.Body.String()
	assert.Contains(t, body, "Cronn Dashboard")
	assert.Contains(t, body, "echo test")
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
	
	// add test jobs
	startTime := time.Now()
	server.OnJobStart("echo test1", "* * * * *", startTime)
	server.OnJobComplete("echo test1", "* * * * *", startTime, startTime.Add(time.Second), 0, nil)
	server.OnJobStart("echo test2", "@daily", startTime)
	
	// test card view
	req := httptest.NewRequest("GET", "/api/jobs", nil)
	req.AddCookie(&http.Cookie{Name: "view_mode", Value: "cards"})
	w := httptest.NewRecorder()
	
	server.handleAPIJobs(w, req)
	
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	
	body := w.Body.String()
	assert.Contains(t, body, "job-card")
	assert.Contains(t, body, "echo test1")
	assert.Contains(t, body, "echo test2")
	assert.Contains(t, body, "status-success")
	assert.Contains(t, body, "status-running")
	
	// test list view
	req = httptest.NewRequest("GET", "/api/jobs", nil)
	req.AddCookie(&http.Cookie{Name: "view_mode", Value: "list"})
	w = httptest.NewRecorder()
	
	server.handleAPIJobs(w, req)
	
	resp = w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	
	body = w.Body.String()
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
	
	req := httptest.NewRequest("GET", "/api/stats", nil)
	w := httptest.NewRecorder()
	
	server.handleAPIStats(w, req)
	
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	
	var stats struct {
		Total    int    `json:"total"`
		Running  int    `json:"running"`
		Success  int    `json:"success"`
		Error    int    `json:"error"`
		NextRun  string `json:"next_run"`
	}
	err = json.NewDecoder(resp.Body).Decode(&stats)
	require.NoError(t, err)
	
	assert.Equal(t, 3, stats.Total)
	assert.Equal(t, 1, stats.Running)
	assert.Equal(t, 1, stats.Success)
	assert.Equal(t, 1, stats.Error)
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
		name       string
		current    string
		expected   string
	}{
		{"auto to light", "auto", "light"},
		{"light to dark", "light", "dark"},
		{"dark to auto", "dark", "auto"},
		{"no cookie defaults to light", "", "light"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/toggle-theme", nil)
			if tt.current != "" {
				req.AddCookie(&http.Cookie{Name: "theme", Value: tt.current})
			}
			w := httptest.NewRecorder()
			
			server.handleToggleTheme(w, req)
			
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
		name       string
		current    string
		expected   string
	}{
		{"cards to list", "cards", "list"},
		{"list to cards", "list", "cards"},
		{"no cookie defaults to list", "", "list"},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/toggle-view", nil)
			if tt.current != "" {
				req.AddCookie(&http.Cookie{Name: "view_mode", Value: tt.current})
			}
			w := httptest.NewRecorder()
			
			server.handleToggleView(w, req)
			
			resp := w.Result()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			
			cookies := resp.Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "view_mode", cookies[0].Name)
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
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0644)
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
	
	specs, err := server.parseJobSpecs()
	require.NoError(t, err)
	assert.Len(t, specs, 4)
	
	// verify specs
	expected := []crontab.JobSpec{
		{Spec: "0 * * * *", Command: "echo hourly"},
		{Spec: "*/5 * * * *", Command: "echo five-minutes"},
		{Spec: "@daily", Command: "echo daily"},
		{Spec: "@every 2h", Command: "echo every-two-hours"},
	}
	
	for i, spec := range specs {
		assert.Equal(t, expected[i].Spec, spec.Spec)
		assert.Equal(t, expected[i].Command, spec.Command)
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
	
	// test that all templates are parsed correctly
	templates := []string{
		"base.html",
		"dashboard.html",
		"jobs-cards",
		"jobs-list",
	}
	
	for _, name := range templates {
		t.Run(name, func(t *testing.T) {
			tmpl := server.templates.Lookup(name)
			assert.NotNil(t, tmpl, "template %s should be parsed", name)
		})
	}
	
	// test template execution
	t.Run("execute jobs-cards template", func(t *testing.T) {
		tmpl := server.templates.Lookup("jobs-cards")
		require.NotNil(t, tmpl)
		
		data := struct {
			Jobs []map[string]interface{}
		}{
			Jobs: []map[string]interface{}{
				{
					"hash":         "test123",
					"command":      "echo test",
					"schedule":     "* * * * *",
					"status":       "success",
					"last_run":     time.Now().Format("Jan 02, 15:04:05"),
					"next_run_rel": "1m",
				},
			},
		}
		
		var buf strings.Builder
		err := tmpl.Execute(&buf, data)
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
			got := server.getScheduleDescription(tt.schedule)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestServer_Run(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	
	cfg := Config{
		Address:        ":0", // use random port
		CrontabFile:    "crontab",
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
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}