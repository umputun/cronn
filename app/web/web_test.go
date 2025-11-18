package web

import (
	"context"
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
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

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
			assert.Len(t, got, 64) // SHA256 is 64 hex chars
			// verify consistency
			assert.Equal(t, got, HashCommand(tt.cmd))
		})
	}
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
			Jobs           []persistence.JobInfo
			ManualDisabled bool
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
			ManualDisabled: false,
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
		assert.Equal(t, enums.ThemeDark, theme) // should return default
	})

	t.Run("empty cookie value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.AddCookie(&http.Cookie{
			Name:  "theme",
			Value: "",
		})

		theme := server.getTheme(req)
		assert.Equal(t, enums.ThemeDark, theme) // should return default
	})

	t.Run("no cookie", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", http.NoBody)
		// no cookie set

		theme := server.getTheme(req)
		assert.Equal(t, enums.ThemeDark, theme) // should return dark default
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
		// server returns nil when shut down gracefully (ErrServerClosed is filtered out)
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time")
	}
}

func Test_shortVersion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full version with commit and date",
			input:    "v1.7.0-abc1234-20241225",
			expected: "v1.7.0",
		},
		{
			name:     "full version with dev and date",
			input:    "v1.7.0-dev-20251111",
			expected: "v1.7.0",
		},
		{
			name:     "simple version without extras",
			input:    "v1.7.0",
			expected: "v1.7.0",
		},
		{
			name:     "unknown version",
			input:    "unknown",
			expected: "unknown",
		},
		{
			name:     "empty version",
			input:    "",
			expected: "",
		},
		{
			name:     "version with single dash",
			input:    "v2.0.0-beta",
			expected: "v2.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shortVersion(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestServer_since(t *testing.T) {
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

	now := time.Now()
	past := now.Add(-1 * time.Hour)

	duration := server.since(past)

	assert.InDelta(t, time.Hour.Seconds(), duration.Seconds(), 1.0)
}

func TestServer_BaseURL(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	t.Run("url helper without baseURL", func(t *testing.T) {
		cfg := Config{DBPath: dbPath, UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: ""}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		assert.Equal(t, "/", server.url("/"))
		assert.Equal(t, "/api/jobs", server.url("/api/jobs"))
		assert.Equal(t, "/static/style.css", server.url("/static/style.css"))
	})

	t.Run("url helper with baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test2.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/cronn"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		assert.Equal(t, "/cronn/", server.url("/"))
		assert.Equal(t, "/cronn/api/jobs", server.url("/api/jobs"))
		assert.Equal(t, "/cronn/static/style.css", server.url("/static/style.css"))
	})

	t.Run("cookiePath without baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test3.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: ""}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		assert.Equal(t, "/", server.cookiePath())
	})

	t.Run("cookiePath with baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test4.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/cronn"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		assert.Equal(t, "/cronn/", server.cookiePath())
	})

	t.Run("routing with baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test5.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/cronn"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		// start server to test routing
		srv := &http.Server{Addr: ":0", Handler: server.routes(), ReadHeaderTimeout: time.Second}
		handler := srv.Handler
		if server.baseURL != "" {
			handler = http.StripPrefix(server.baseURL, handler)
		}

		// test request to /cronn/api/jobs should work
		req := httptest.NewRequest("GET", "/cronn/api/jobs", http.NoBody)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		// test request to /api/jobs (without prefix) should fail with baseURL
		req = httptest.NewRequest("GET", "/api/jobs", http.NoBody)
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("cookie paths with baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test6.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/cronn"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		// test view mode toggle sets cookie with correct path
		req := httptest.NewRequest("POST", "/api/view-mode", http.NoBody)
		req.AddCookie(&http.Cookie{Name: "view-mode", Value: "cards"})
		rec := httptest.NewRecorder()
		server.handleViewModeToggle(rec, req)

		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		assert.Equal(t, "view-mode", cookies[0].Name)
		assert.Equal(t, "/cronn/", cookies[0].Path)
	})

	t.Run("redirects with baseURL", func(t *testing.T) {
		passwordHash := "$2a$10$test" // bcrypt hash placeholder
		cfg := Config{
			DBPath:         filepath.Join(tmpDir, "test7.db"),
			UpdateInterval: time.Minute,
			Version:        "test",
			JobsProvider:   createTestProvider(t, tmpDir),
			BaseURL:        "/cronn",
			PasswordHash:   passwordHash,
		}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		// test auth redirect to login uses correct base URL
		req := httptest.NewRequest("GET", "/", http.NoBody)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusSeeOther, rec.Code)
		location := rec.Header().Get("Location")
		assert.Equal(t, "/cronn/login", location)
	})

	t.Run("template data includes baseURL", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test8.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/myapp"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		req := httptest.NewRequest("GET", "/", http.NoBody)
		data := server.newTemplateData(req)

		assert.Equal(t, "/myapp", data.BaseURL)
	})

	t.Run("base URL with multiple segments", func(t *testing.T) {
		cfg := Config{DBPath: filepath.Join(tmpDir, "test9.db"), UpdateInterval: time.Minute, Version: "test", JobsProvider: createTestProvider(t, tmpDir), BaseURL: "/app/cronn"}
		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		assert.Equal(t, "/app/cronn/api/jobs", server.url("/api/jobs"))
		assert.Equal(t, "/app/cronn/", server.cookiePath())
	})
}

// createTestProvider creates a dummy JobsProvider for testing
func createTestProvider(t *testing.T, tmpDir string) JobsProvider {
	t.Helper()
	crontabFile := filepath.Join(tmpDir, "test-crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	return crontab.New(crontabFile, 0, nil)
}
