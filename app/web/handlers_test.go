package web

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/mocks"
	"github.com/umputun/cronn/app/web/persistence"
)

func newHandlersTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.UpdateInterval = time.Minute
	cfg.Version = "test"
	cfg.JobsProvider = createTestProvider(t, tmpDir)
	server, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.store.Close() })
	return server
}

func addJobs(s *Server, jobs ...persistence.JobInfo) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	for i, j := range jobs {
		j.SortIndex = i
		s.jobs[j.ID] = j
	}
}

func sampleJobs() []persistence.JobInfo {
	now := time.Now()
	return []persistence.JobInfo{
		{ID: "ok", Name: "Quote sync", Command: "echo sync", Schedule: "*/1 * * * *", Enabled: true,
			LastStatus: enums.JobStatusSuccess, LastRun: now.Add(-time.Minute), LastExitCode: new(0), LastDuration: 100 * time.Millisecond},
		{ID: "fail", Name: "Vendor feed import", Command: "sh -c 'exit 3'", Schedule: "*/1 * * * *", Enabled: true,
			LastStatus: enums.JobStatusFailed, LastRun: now.Add(-2 * time.Minute), LastExitCode: new(3), LastDuration: 2 * time.Second},
		{ID: "run", Name: "Report rebuild", Command: "sleep 100", Schedule: "*/2 * * * *", Enabled: true, IsRunning: true,
			LastStatus: enums.JobStatusRunning, LastRun: now.Add(-47 * time.Second)},
		{ID: "never", Command: "reindex --full", Schedule: "@every 1h15m", Enabled: true, LastStatus: enums.JobStatusIdle},
		{ID: "off", Name: "Cache cleanup", Command: "find /tmp/cache -delete", Schedule: "@midnight", Enabled: false,
			LastStatus: enums.JobStatusFailed, LastRun: now.Add(-72 * time.Hour), LastExitCode: new(1), LastDuration: time.Second},
	}
}

func rowIDs(body string) []string {
	parts := strings.Split(body, `<tr class="row`)[1:]
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		start := strings.Index(part, `data-job-id="`) + len(`data-job-id="`)
		ids = append(ids, part[start:start+strings.Index(part[start:], `"`)])
	}
	return ids
}

func TestServer_getJobsWithStats(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)

	t.Run("counts leave disabled jobs out of the result buckets", func(t *testing.T) {
		stats := server.getJobsWithStats(jobsQuery{filter: enums.FilterModeAll})
		assert.Equal(t, 5, stats.totalCount)
		assert.Equal(t, 1, stats.successCount)
		assert.Equal(t, 1, stats.failedCount)
		assert.Equal(t, 1, stats.runningCount)
		assert.Equal(t, 1, stats.idleCount)
		assert.Equal(t, 1, stats.disabledCount)
		require.Len(t, stats.failing, 1)
		assert.Equal(t, "fail", stats.failing[0].ID)
		assert.Len(t, stats.jobs, 5)
	})

	tbl := []struct {
		filter enums.FilterMode
		want   []string
	}{
		{enums.FilterModeAll, []string{"ok", "fail", "run", "never", "off"}},
		{enums.FilterModeFailed, []string{"fail"}},
		{enums.FilterModeSuccess, []string{"ok"}},
		{enums.FilterModeRunning, []string{"run"}},
		{enums.FilterModeIdle, []string{"never"}},
		{enums.FilterModeDisabled, []string{"off"}},
	}
	for _, tt := range tbl {
		t.Run("filter "+tt.filter.String(), func(t *testing.T) {
			stats := server.getJobsWithStats(jobsQuery{filter: tt.filter})
			var ids []string
			for _, j := range stats.jobs {
				ids = append(ids, j.ID)
			}
			assert.Equal(t, tt.want, ids)
		})
	}

	t.Run("search matches name and command", func(t *testing.T) {
		stats := server.getJobsWithStats(jobsQuery{filter: enums.FilterModeAll, search: "vendor"})
		require.Len(t, stats.jobs, 1)
		assert.Equal(t, "fail", stats.jobs[0].ID)

		stats = server.getJobsWithStats(jobsQuery{filter: enums.FilterModeAll, search: "REINDEX"})
		require.Len(t, stats.jobs, 1)
		assert.Equal(t, "never", stats.jobs[0].ID)
	})

	t.Run("hidden by search counted only when nothing matches", func(t *testing.T) {
		stats := server.getJobsWithStats(jobsQuery{filter: enums.FilterModeFailed, search: "rsync"})
		assert.Empty(t, stats.jobs)
		assert.Equal(t, 1, stats.hiddenBySearch)

		stats = server.getJobsWithStats(jobsQuery{filter: enums.FilterModeFailed, search: "vendor"})
		assert.Zero(t, stats.hiddenBySearch)
	})

	t.Run("failing ordered by most recent run", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		now := time.Now()
		addJobs(s,
			persistence.JobInfo{ID: "old", Command: "a", Schedule: "@hourly", Enabled: true, LastStatus: enums.JobStatusFailed, LastRun: now.Add(-time.Hour)},
			persistence.JobInfo{ID: "new", Command: "b", Schedule: "@hourly", Enabled: true, LastStatus: enums.JobStatusFailed, LastRun: now},
		)
		stats := s.getJobsWithStats(jobsQuery{})
		require.Len(t, stats.failing, 2)
		assert.Equal(t, "new", stats.failing[0].ID)
	})
}

func TestServer_handleDashboard(t *testing.T) {
	server := newHandlersTestServer(t, Config{Hostname: "bigmac"})
	addJobs(server, sampleJobs()...)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	server.handleDashboard(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<title>Cronn Dashboard</title>")
	assert.Contains(t, body, `id="jobs-container"`)
	assert.Contains(t, body, `id="inspector"`)
	assert.Contains(t, body, `id="dialog-slot"`)
	assert.Contains(t, body, `id="selected-job"`)
	assert.Contains(t, body, "1 job failing")
	assert.Contains(t, body, "Vendor feed import")
	assert.Contains(t, body, `<em id="count-disabled">1</em>`)
	assert.Contains(t, body, "/static/ui.js")
	assert.NotContains(t, body, "app.js")
	assert.Equal(t, []string{"ok", "fail", "run", "never", "off"}, rowIDs(body))
}

func TestServer_handleJobsPartial(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)

	get := func(t *testing.T, query string, cookies ...*http.Cookie) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs?"+query, http.NoBody)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		server.handleJobsPartial(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	t.Run("rows show name, readable schedule and last result", func(t *testing.T) {
		body := get(t, "")
		assert.Contains(t, body, "Quote sync")
		assert.Contains(t, body, "Every minute")
		assert.Contains(t, body, "exit 3 · 2.0s")
		assert.Contains(t, body, "exit 0 · 0.1s")
		assert.Contains(t, body, "Every 1h15m")
		assert.Contains(t, body, "running 47s")
		assert.Contains(t, body, `<span class="tag off">disabled</span>`)
		assert.Contains(t, body, `hx-swap-oob="innerHTML">5</em>`)
		assert.Contains(t, body, `id="failing-alert" hx-swap-oob="innerHTML"`)
		assert.NotContains(t, body, `id="jobs-controls"`, "polling must not replace the open sort select or focused tab")
	})

	t.Run("never-ran job shows no exit code or duration", func(t *testing.T) {
		body := get(t, "filter=idle", &http.Cookie{Name: "filter-mode", Value: "idle"})
		assert.Equal(t, []string{"never"}, rowIDs(body))
		table, _, found := strings.Cut(body, "</table>")
		require.True(t, found)
		assert.Contains(t, table, "never ran")
		assert.NotContains(t, table, "exit ")
	})

	t.Run("selected job row is marked", func(t *testing.T) {
		body := get(t, "selected-job=fail")
		assert.Contains(t, body, `<tr class="row sel" data-job-id="fail"`)
		assert.NotContains(t, body, `<tr class="row sel" data-job-id="ok"`)
	})

	t.Run("search by name", func(t *testing.T) {
		body := get(t, "search=report")
		assert.Equal(t, []string{"run"}, rowIDs(body))
		assert.Contains(t, body, "1 of 5 jobs")
	})

	t.Run("empty search result says what hides the jobs", func(t *testing.T) {
		body := get(t, "search=rsync", &http.Cookie{Name: "filter-mode", Value: "failed"})
		assert.Empty(t, rowIDs(body))
		assert.Contains(t, body, "No failed jobs match “rsync”")
		assert.Contains(t, body, "1 failed job is hidden by the search.")
		assert.Contains(t, body, "Clear search")
		assert.Contains(t, body, "Show all jobs")
	})

	t.Run("tab modes win over cookies changed by another tab", func(t *testing.T) {
		body := get(t, "filter=all&sort=default",
			&http.Cookie{Name: "filter-mode", Value: "failed"}, &http.Cookie{Name: "sort-mode", Value: "lastrun"})
		assert.Equal(t, []string{"ok", "fail", "run", "never", "off"}, rowIDs(body))
	})

	t.Run("cookie applies when the page sends no mode", func(t *testing.T) {
		body := get(t, "", &http.Cookie{Name: "filter-mode", Value: "failed"})
		assert.Equal(t, []string{"fail"}, rowIDs(body))
	})

	t.Run("reset-search clears the search box", func(t *testing.T) {
		body := get(t, "search=&reset-search=1")
		assert.Contains(t, body, `id="search-box" class="search" hx-swap-oob="true"`)
		assert.Len(t, rowIDs(body), 5)
	})

	t.Run("no jobs configured", func(t *testing.T) {
		empty := newHandlersTestServer(t, Config{})
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody)
		w := httptest.NewRecorder()
		empty.handleJobsPartial(w, req)
		assert.Contains(t, w.Body.String(), "No jobs configured")
	})

	t.Run("template missing", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		delete(s.templates, "partials/jobs.html")
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("template execution error", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		s.templates["partials/jobs.html"] = template.Must(template.New("x").Parse(`{{define "jobs-table"}}{{.Missing}}{{end}}`))
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_handleSortModeChange(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)

	tbl := []struct {
		form, wantCookie string
	}{
		{"lastrun", "lastrun"},
		{"nextrun", "nextrun"},
		{"default", "default"},
		{"bogus", "default"},
	}
	for _, tt := range tbl {
		t.Run(tt.form, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort="+tt.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			server.handleSortModeChange(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			cookies := w.Result().Cookies()
			require.Len(t, cookies, 1)
			assert.Equal(t, "sort-mode", cookies[0].Name)
			assert.Equal(t, tt.wantCookie, cookies[0].Value)
			assert.Contains(t, w.Body.String(), `id="jobs-controls" class="bar" hx-swap-oob="innerHTML"`)
		})
	}

	t.Run("new order wins over the old cookie on the request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort=lastrun"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "sort-mode", Value: "default"})
		w := httptest.NewRecorder()
		server.handleSortModeChange(w, req)
		assert.Equal(t, []string{"run", "ok", "fail", "off", "never"}, rowIDs(w.Body.String()))
		assert.Contains(t, w.Body.String(), `<option value="lastrun" selected>`)
	})

	t.Run("keeps the tab's filter over the cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort=nextrun&filter=success"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "filter-mode", Value: "failed"})
		w := httptest.NewRecorder()
		server.handleSortModeChange(w, req)
		assert.Equal(t, []string{"ok"}, rowIDs(w.Body.String()))
		assert.Contains(t, w.Body.String(), `id="filter-mode" name="filter" value="success"`)
	})

	t.Run("keeps filter and search", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort=nextrun&search=sync"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "filter-mode", Value: "success"})
		w := httptest.NewRecorder()
		server.handleSortModeChange(w, req)
		assert.Equal(t, []string{"ok"}, rowIDs(w.Body.String()))
	})
}

func TestServer_handleFilterModeChange(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)

	tbl := []struct {
		filter, wantCookie string
		wantRows           []string
	}{
		{"failed", "failed", []string{"fail"}},
		{"disabled", "disabled", []string{"off"}},
		{"running", "running", []string{"run"}},
		{"all", "all", []string{"ok", "fail", "run", "never", "off"}},
		{"bogus", "all", []string{"ok", "fail", "run", "never", "off"}},
	}
	for _, tt := range tbl {
		t.Run(tt.filter, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/filter-mode", strings.NewReader("filter="+tt.filter))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "filter-mode", Value: "success"})
			w := httptest.NewRecorder()
			server.handleFilterModeChange(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tt.wantCookie, w.Result().Cookies()[0].Value)
			assert.Equal(t, tt.wantRows, rowIDs(w.Body.String()))
			assert.Contains(t, w.Body.String(), `class="tab on"`)
		})
	}

	t.Run("keeps the tab's sort over the cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/filter-mode", strings.NewReader("filter=all&sort=lastrun"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "sort-mode", Value: "default"})
		w := httptest.NewRecorder()
		server.handleFilterModeChange(w, req)
		assert.Equal(t, []string{"run", "ok", "fail", "off", "never"}, rowIDs(w.Body.String()))
		assert.Contains(t, w.Body.String(), `<option value="lastrun" selected>`)
	})

	t.Run("show all resets the search box", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/filter-mode",
			strings.NewReader("filter=all&search=&reset-search=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		server.handleFilterModeChange(w, req)
		assert.Contains(t, w.Body.String(), `id="search-box" class="search" hx-swap-oob="true"`)
	})
}

func TestServer_handleThemeToggle(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	tbl := []struct{ current, want string }{{"dark", "light"}, {"light", "dark"}, {"", "light"}}
	for _, tt := range tbl {
		t.Run("from "+tt.current, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/theme", http.NoBody)
			if tt.current != "" {
				req.AddCookie(&http.Cookie{Name: "theme", Value: tt.current})
			}
			w := httptest.NewRecorder()
			server.handleThemeToggle(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "true", w.Header().Get("HX-Refresh"))
			assert.Equal(t, tt.want, w.Result().Cookies()[0].Value)
		})
	}
}

func TestServer_handleRunForm(t *testing.T) {
	get := func(s *Server, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/run-form", http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleRunForm(w, req)
		return w
	}

	t.Run("plain command has no date field", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		addJobs(s, sampleJobs()...)
		w := get(s, "ok")
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `<dialog class="run-dialog" data-job-id="ok"`)
		assert.Contains(t, body, "Run “Quote sync” now")
		assert.Contains(t, body, "Requests a one-time run.")
		assert.Contains(t, body, ">echo sync</textarea>")
		assert.NotContains(t, body, `name="date"`)
		assert.NotContains(t, body, " readonly")
		assert.Contains(t, body, `<form method="dialog">`)
	})

	t.Run("templated command gets the date field", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		addJobs(s, persistence.JobInfo{ID: "tpl", Command: "echo {{.YYYYMMDD}}", Schedule: "@daily", Enabled: true})
		body := get(s, "tpl").Body.String()
		assert.Contains(t, body, `name="date"`)
		assert.Contains(t, body, "a supplied date is used at 00:00")
	})

	t.Run("command edit disabled makes the command read-only", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{DisableCommandEdit: true})
		addJobs(s, sampleJobs()...)
		body := get(s, "ok").Body.String()
		assert.Contains(t, body, " readonly>")
		assert.NotContains(t, body, "edits apply to this run only")
	})

	t.Run("manual runs disabled", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{DisableManual: true})
		addJobs(s, sampleJobs()...)
		assert.Equal(t, http.StatusForbidden, get(s, "ok").Code)
	})

	t.Run("unknown job", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		assert.Equal(t, http.StatusNotFound, get(s, "nope").Code)
	})
}

func TestServer_handleRunJob(t *testing.T) {
	post := func(s *Server, id string, form url.Values, htmx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/run", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleRunJob(w, req)
		return w
	}

	newServer := func(t *testing.T, cfg Config, buf int) (*Server, chan service.ManualJobRequest) {
		var ch chan service.ManualJobRequest
		if buf >= 0 {
			ch = make(chan service.ManualJobRequest, buf)
			cfg.ManualTrigger = ch
		}
		s := newHandlersTestServer(t, cfg)
		addJobs(s, append(sampleJobs(),
			persistence.JobInfo{ID: "tpl", Name: "Templated", Command: "echo {{.YYYYMMDD}}", Schedule: "@daily", Enabled: true})...)
		return s, ch
	}

	t.Run("accepted: curl gets 202 text, htmx gets 202 with toast", func(t *testing.T) {
		s, ch := newServer(t, Config{}, 2)

		w := post(s, "ok", url.Values{}, false)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "Job triggered", w.Body.String())
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger"))
		req := <-ch
		assert.Equal(t, "echo sync", req.Command)
		assert.Nil(t, req.CustomDate)

		w = post(s, "tpl", url.Values{"command": {"echo edited {{.YYYYMMDD}}"}, "date": {"20260915"}}, true)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
		assert.Contains(t, w.Body.String(), `id="toasts" hx-swap-oob="innerHTML"`)
		assert.Contains(t, w.Body.String(), "Run request accepted: <b>Templated</b>")
		assert.NotContains(t, w.Body.String(), "<dialog")
		req = <-ch
		assert.Equal(t, "echo edited {{.YYYYMMDD}}", req.Command)
		require.NotNil(t, req.CustomDate)
		assert.Equal(t, "2026-09-15 00:00", req.CustomDate.Format("2006-01-02 15:04"))
	})

	tbl := []struct {
		name       string
		cfg        Config
		buf        int
		id         string
		form       url.Values
		wantStatus int
		wantMsg    string
	}{
		{"disabled job", Config{}, 1, "off", url.Values{}, http.StatusBadRequest, "Job is disabled"},
		{"already running", Config{}, 1, "run", url.Values{"command": {"sleep 5"}}, http.StatusConflict, "Job already running"},
		{"trigger not configured", Config{}, -1, "ok", url.Values{}, http.StatusServiceUnavailable, "Manual trigger not configured"},
		{"command edit disabled", Config{DisableCommandEdit: true}, 1, "ok", url.Values{"command": {"rm -rf /"}},
			http.StatusForbidden, "Command editing is disabled"},
		{"impossible date", Config{}, 1, "tpl", url.Values{"command": {"echo mine"}, "date": {"20260231"}},
			http.StatusBadRequest, "Invalid date format, expected YYYYMMDD"},
		{"busy", Config{}, 0, "ok", url.Values{}, http.StatusServiceUnavailable, "System busy, too many manual triggers"},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newServer(t, tt.cfg, tt.buf)

			w := post(s, tt.id, tt.form, false)
			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, tt.wantMsg+"\n", w.Body.String())

			w = post(s, tt.id, tt.form, true)
			assert.Equal(t, http.StatusOK, w.Code, "htmx rejections answer 200 so htmx swaps the form")
			body := w.Body.String()
			assert.Contains(t, body, "<dialog")
			assert.Contains(t, body, template.HTMLEscapeString("Not started: "+tt.wantMsg+"."))
			assert.Empty(t, w.Header().Get("HX-Trigger-After-Swap"))
			assert.NotContains(t, body, "toasts")
			if cmd := tt.form.Get("command"); cmd != "" {
				assert.Contains(t, body, ">"+template.HTMLEscapeString(cmd)+"</textarea>", "edits are kept")
			}
			if d := tt.form.Get("date"); d != "" {
				assert.Contains(t, body, `value="`+d+`"`)
			}
		})
	}

	t.Run("manual runs disabled", func(t *testing.T) {
		s, _ := newServer(t, Config{DisableManual: true}, 1)
		w := post(s, "ok", url.Values{}, true)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("job removed after the dialog opened", func(t *testing.T) {
		s, ch := newServer(t, Config{}, 1)
		form := url.Values{"command": {"echo edited"}, "date": {"20260915"}}

		w := post(s, "gone", form, false)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "Job not found\n", w.Body.String())

		w = post(s, "gone", form, true)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Not started: Job no longer exists in the crontab.")
		assert.Contains(t, body, `hx-post="/api/jobs/gone/run"`)
		assert.Contains(t, body, ">echo edited</textarea>")
		assert.Contains(t, body, `value="20260915"`, "a submitted date keeps its field")
		assert.NotContains(t, body, "toasts")
		assert.Empty(t, ch, "nothing is queued for a missing job")
	})
}

func TestServer_handleToggleJob(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, persistence.JobInfo{ID: "t", Command: "echo toggle", Schedule: "* * * * *", Enabled: true})

	toggle := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/toggle", http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		server.handleToggleJob(w, req)
		return w
	}

	w := toggle("t")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger"))
	assert.True(t, server.IsJobDisabled("t"))

	toggle("t")
	assert.False(t, server.IsJobDisabled("t"))

	assert.Equal(t, http.StatusNotFound, toggle("nope").Code)
	assert.Equal(t, http.StatusBadRequest, toggle("").Code)
}

func TestServer_handleInspector(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	start := time.Date(2026, 9, 29, 16, 50, 0, 0, time.Local)
	record := func(jobID string, at time.Time, status enums.JobStatus, code int, executed, output string) {
		require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: jobID, StartedAt: at,
			FinishedAt: at.Add(2 * time.Second), Status: status, ExitCode: code, ExecutedCommand: executed, Output: output}))
	}
	record("fail", start, enums.JobStatusFailed, 3, "", "fetching\nboom")
	record("fail", start.Add(time.Minute), enums.JobStatusSuccess, 0, "sh -c 'exit 0'", "manual ok")
	record("fail", start.Add(2*time.Minute), enums.JobStatusFailed, 137, "", "")

	get := func(id, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector?"+query, http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		server.handleInspector(w, req)
		return w
	}

	t.Run("full panel selects the latest run", func(t *testing.T) {
		w := get("fail", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
		body := w.Body.String()
		assert.Contains(t, body, `<div class="insp" data-job-id="fail">`)
		assert.Contains(t, body, "Vendor feed import")
		assert.Contains(t, body, "Last run failed · exit 3")
		assert.Contains(t, body, `id="selected-job" name="selected-job" value="fail" hx-swap-oob="true"`)
		assert.Contains(t, body, "newest first · latest 50 shown")
		assert.Contains(t, body, `<span class="tag man">manual</span>`)
		assert.Contains(t, body, "No output captured. The run failed with exit code 137.")
		assert.Contains(t, body, "<b>Job command:</b> sh -c &#39;exit 3&#39;")
		assert.Equal(t, 3, strings.Count(body, `id="exec-`))
		assert.Equal(t, 1, strings.Count(body, `class="run sel"`))
	})

	t.Run("live part keeps the selected run", func(t *testing.T) {
		runs, err := server.store.GetExecutions("fail", 10)
		require.NoError(t, err)
		w := get("fail", "part=live&selected-run="+strconv.Itoa(runs[2].ID))
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `id="insp-live"`)
		assert.NotContains(t, body, "selected-job")
		_, sel, found := strings.Cut(body, `class="run sel"`)
		require.True(t, found)
		assert.True(t, strings.HasPrefix(strings.Join(strings.Fields(sel), " "),
			`id="exec-`+strconv.Itoa(runs[2].ID)+`" aria-pressed="true"`))
		assert.Empty(t, w.Header().Get("HX-Trigger-After-Swap"))
	})

	tbl := []struct {
		id   string
		want []string
	}{
		{"ok", []string{"Last run succeeded", "Enabled", "Run now…", ">Disable<", "No runs recorded yet."}},
		{"never", []string{"Never ran", "reindex --full"}},
		{"off", []string{"Disabled</span>", ">Enable<"}},
		{"run", []string{"Running for 47s", "disabled\n"}},
	}
	for _, tt := range tbl {
		t.Run("state "+tt.id, func(t *testing.T) {
			body := get(tt.id, "").Body.String()
			for _, want := range tt.want {
				assert.Contains(t, body, want)
			}
		})
	}

	t.Run("job without runs shows one empty message", func(t *testing.T) {
		body := get("never", "").Body.String()
		assert.Equal(t, 1, strings.Count(body, "No runs"), "the runs list and the output pane must not both say it")
		assert.Contains(t, body, "No runs recorded yet.")
	})

	t.Run("disabled job has no run button", func(t *testing.T) {
		assert.NotContains(t, get("off", "").Body.String(), "Run now…")
	})

	t.Run("unknown job", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, get("nope", "").Code)
	})

	t.Run("removed job closes the panel for htmx", func(t *testing.T) {
		for _, query := range []string{"", "part=live"} {
			req := httptest.NewRequest(http.MethodGet, "/api/jobs/gone/inspector?"+query, http.NoBody)
			req.SetPathValue("id", "gone")
			req.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			server.handleInspector(w, req)
			require.Equal(t, http.StatusOK, w.Code, "htmx swaps only 2xx, so a 404 would leave the removed job on screen")
			assert.Equal(t, "#inspector", w.Header().Get("HX-Retarget"))
			assert.Equal(t, "innerHTML", w.Header().Get("HX-Reswap"), "the live part swaps outerHTML and would delete the slot")
			body := w.Body.String()
			assert.Contains(t, body, `id="selected-job" name="selected-job" value="" hx-swap-oob="true"`)
			assert.NotContains(t, body, "selected-run", "the run selection goes with the emptied panel")
			assert.NotContains(t, body, `class="insp"`)
		}
	})
}

func TestServer_handleRunOutput(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	now := time.Now()
	require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: "fail", StartedAt: now, FinishedAt: now.Add(1900 * time.Millisecond),
		Status: enums.JobStatusSuccess, ExitCode: 0, ExecutedCommand: "sh -c 'exit 0'", Output: "manual ok"}))
	require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: "ok", StartedAt: now, FinishedAt: now,
		Status: enums.JobStatusSuccess}))
	runs, err := server.store.GetExecutions("fail", 1)
	require.NoError(t, err)
	other, err := server.store.GetExecutions("ok", 1)
	require.NoError(t, err)

	get := func(jobID, execID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", execID)
		w := httptest.NewRecorder()
		server.handleRunOutput(w, req)
		return w
	}

	w := get("fail", strconv.Itoa(runs[0].ID))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "refresh-inspector", w.Header().Get("HX-Trigger-After-Swap"))
	body := w.Body.String()
	assert.Contains(t, body, "<b>Executed:</b> sh -c &#39;exit 0&#39;")
	assert.Contains(t, body, "manual ok")
	assert.Contains(t, body, "exit 0 · 1.9s")
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(runs[0].ID)+`">`)
	assert.NotContains(t, body, "hx-swap-oob", "the selection travels inside the output, not out of band")

	assert.Equal(t, http.StatusNotFound, get("fail", strconv.Itoa(other[0].ID)).Code, "run of another job")
	assert.Equal(t, http.StatusNotFound, get("fail", "99999").Code)
	assert.Equal(t, http.StatusNotFound, get("nope", strconv.Itoa(runs[0].ID)).Code)
	assert.Equal(t, http.StatusBadRequest, get("fail", "abc").Code)

	require.NoError(t, server.store.Close())
	assert.Equal(t, http.StatusInternalServerError, get("fail", strconv.Itoa(runs[0].ID)).Code,
		"a database failure is not reported as a missing run")
}

func TestServer_handleLiveOutput(t *testing.T) {
	server := newHandlersTestServer(t, Config{ExecMaxLogLines: 100})
	id := HashCommand("live cmd")
	startA := time.Now().Add(-time.Minute).Round(time.Microsecond)
	startB := startA.Add(10 * time.Second)
	startEvent := func(at time.Time, output func() string) {
		server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: at, LiveOutput: output})
	}
	startEvent(startA, func() string { return "a one\na two" })
	startEvent(startB, func() string { return "b one" })

	get := func(jobID, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+jobID+"/live-output?"+query, http.NoBody)
		req.SetPathValue("id", jobID)
		w := httptest.NewRecorder()
		server.handleLiveOutput(w, req)
		return w
	}
	micro := func(at time.Time) string { return strconv.FormatInt(at.UnixMicro(), 10) }

	t.Run("run in progress polls itself", func(t *testing.T) {
		w := get(id, "start="+micro(startA))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "refresh-inspector", w.Header().Get("HX-Trigger-After-Swap"), "a selection refreshes the runs list")
		body := w.Body.String()
		assert.Contains(t, body, "a one\na two")
		assert.Contains(t, body, `id="live-out"`)
		assert.Contains(t, body, `hx-trigger="every 5s"`)
		assert.Contains(t, body, `hx-sync="#inspector:abort"`)
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="live:`+micro(startA)+`">`)
		assert.NotContains(t, body, "hx-swap-oob")
	})

	t.Run("poll does not refresh the runs list", func(t *testing.T) {
		w := get(id, "start="+micro(startA)+"&poll=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("HX-Trigger-After-Swap"))
	})

	t.Run("finished run turns into its recorded output", func(t *testing.T) {
		server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeCompleted,
			StartedAt: startA, FinishedAt: startA.Add(3 * time.Second), Output: "a one\na two\na three"})
		w := get(id, "start="+micro(startA)+"&poll=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "refresh-inspector", w.Header().Get("HX-Trigger-After-Swap"))
		body := w.Body.String()
		assert.Contains(t, body, "a one\na two\na three")
		assert.NotContains(t, body, "every 5s")
		assert.NotContains(t, body, "b one", "the newer run in progress must not take the finished run's place")
		runs, err := server.store.GetExecutions(id, 10)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(runs[0].ID)+`">`)
	})

	t.Run("output capture disabled", func(t *testing.T) {
		startC := startB.Add(time.Second)
		startEvent(startC, nil)
		body := get(id, "start="+micro(startC)).Body.String()
		assert.Contains(t, body, "Output capture is disabled.")
		assert.Contains(t, body, `hx-trigger="every 5s"`, "it still polls to turn into the recorded run")
	})

	t.Run("unknown run", func(t *testing.T) {
		w := get(id, "start=12345")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "no longer available")
		assert.NotContains(t, w.Body.String(), "every 5s")
	})

	t.Run("bad requests", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, get(id, "start=abc").Code)
		assert.Equal(t, http.StatusNotFound, get("nope", "start="+micro(startB)).Code)
	})

	t.Run("store error", func(t *testing.T) {
		store := server.store
		server.store = &mocks.PersistenceMock{
			GetExecutionByStartFunc: func(string, int64) (persistence.ExecutionInfo, error) {
				return persistence.ExecutionInfo{}, errors.New("db is gone")
			},
			CloseFunc: store.Close,
		}
		assert.Equal(t, http.StatusInternalServerError, get(id, "start=12345").Code)
	})
}

func TestServer_handleLiveOutputFindsRetainedRunBeyondListLimit(t *testing.T) {
	tbl := []struct {
		hist     int
		wantGone bool
	}{{hist: 0}, {hist: 80}, {hist: 50, wantGone: true}}
	for _, tt := range tbl {
		t.Run(fmt.Sprintf("exec-max-hist %d", tt.hist), func(t *testing.T) {
			server := newHandlersTestServer(t, Config{ExecMaxLogLines: 100, LogExecMaxHist: tt.hist})
			id := HashCommand("live cmd")
			event := func(typ enums.EventType, at time.Time, output string) JobEvent {
				return JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: typ, StartedAt: at,
					FinishedAt: at.Add(time.Second), Output: output}
			}
			old := time.Now().Add(-2 * time.Hour)
			server.handleJobEvent(event(enums.EventTypeStarted, old, ""))
			for i := range 55 {
				at := old.Add(time.Duration(i+1) * time.Minute)
				server.handleJobEvent(event(enums.EventTypeStarted, at, ""))
				server.handleJobEvent(event(enums.EventTypeCompleted, at, "newer run"))
			}
			server.handleJobEvent(event(enums.EventTypeCompleted, old, "old run done"))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.SetPathValue("id", id)
			req.Form = url.Values{"start": {strconv.FormatInt(old.UnixMicro(), 10)}, "poll": {"1"}}
			w := httptest.NewRecorder()
			server.handleLiveOutput(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			if tt.wantGone {
				assert.Contains(t, w.Body.String(), "no longer available", "the default retention prunes the old run")
				return
			}
			assert.Contains(t, w.Body.String(), "old run done", "a retained run outside the newest 50 is still found")
		})
	}
}

func TestServer_handleInspectorFinishedRunListedOnce(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	id := HashCommand("live cmd")
	start := time.Now().Add(-time.Minute)
	server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveOutput: func() string { return "partial" }})
	require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: id, StartedAt: start,
		FinishedAt: start.Add(time.Second), Status: enums.JobStatusSuccess}))
	runs, err := server.store.GetExecutions(id, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector", http.NoBody)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	server.handleInspector(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `id="live-`, "a run already recorded is not listed as in progress too")
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(runs[0].ID)+`">`)
	_, sel, found := strings.Cut(body, `class="run sel"`)
	require.True(t, found)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(sel), `id="exec-`+strconv.Itoa(runs[0].ID)+`"`))
}

func TestServer_handleInspectorRunFinishingDuringHistoryRead(t *testing.T) {
	// regression: a run finishing between the history read and the active snapshot was in neither
	server := newHandlersTestServer(t, Config{})
	id := HashCommand("live cmd")
	older, start := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: id, StartedAt: older,
		FinishedAt: older.Add(time.Second), Status: enums.JobStatusSuccess, Output: "older run output"}))
	server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveOutput: func() string { return "run A output" }})

	store := server.store
	server.store = &mocks.PersistenceMock{
		GetExecutionsFunc: func(jobID string, limit int) ([]persistence.ExecutionInfo, error) {
			runs, err := store.GetExecutions(jobID, limit)
			require.NoError(t, err)
			server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeCompleted,
				StartedAt: start, FinishedAt: time.Now(), Output: "run A output"})
			return runs, nil
		},
		RecordExecutionFunc:      store.RecordExecution,
		CleanupOldExecutionsFunc: store.CleanupOldExecutions,
		CloseFunc:                store.Close,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector", http.NoBody)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	server.handleInspector(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "run A output")
	assert.NotContains(t, body, "older run output")
	token := "live:" + strconv.FormatInt(start.UnixMicro(), 10)
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+token+`">`)
}

func TestServer_handleInspectorRunningJob(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	id := HashCommand("live cmd")
	start := time.Now().Add(-47 * time.Second)
	server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveOutput: func() string { return "tick 1\ntick 2" }})
	token := "live:" + strconv.FormatInt(start.UnixMicro(), 10)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector", http.NoBody)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	server.handleInspector(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Running for 47s")
	assert.Contains(t, body, "tick 1\ntick 2")
	assert.Contains(t, body, `id="live-out"`)
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+token+`">`)
	assert.NotContains(t, body, `id="selected-run"`)
	assert.NotContains(t, body, "No runs recorded yet.", "a run in progress is listed")
	_, sel, found := strings.Cut(body, `class="run sel"`)
	require.True(t, found)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(sel), `id="live-`+strconv.FormatInt(start.UnixMicro(), 10)+`"`))
}

func TestServer_handleSettingsModal(t *testing.T) {
	server := newHandlersTestServer(t, Config{Settings: SettingsInfo{Version: "v1.0.0", StartTime: time.Now().Add(-time.Hour),
		WebEnabled: true, WebAddress: ":8080", CrontabPath: "test-crontab"}})
	w := httptest.NewRecorder()
	server.handleSettingsModal(w, httptest.NewRequest(http.MethodGet, "/api/settings/modal", http.NoBody))
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `<dialog class="settings-modal"`)
	assert.Contains(t, body, "Settings & About")
	assert.Contains(t, body, "v1.0.0")
	assert.Contains(t, body, ":8080")
	assert.Contains(t, body, "test-crontab")
	assert.NotContains(t, body, "onclick")
}

func TestServer_Routes(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	handler := server.routes()

	tbl := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/api/jobs", http.StatusOK},
		{http.MethodGet, "/api/jobs/ok/inspector", http.StatusOK},
		{http.MethodGet, "/api/jobs/ok/run-form", http.StatusOK},
		{http.MethodGet, "/nonexistent", http.StatusNotFound},
		{http.MethodGet, "/api/jobs/ok/modal", http.StatusNotFound},
		{http.MethodGet, "/api/jobs/ok/history", http.StatusNotFound},
		{http.MethodGet, "/api/jobs/ok/executions/1/logs", http.StatusNotFound},
		{http.MethodPost, "/api/view-mode", http.StatusNotFound},
		{http.MethodPost, "/api/sort-toggle", http.StatusNotFound},
		{http.MethodPost, "/api/filter-toggle", http.StatusNotFound},
		{http.MethodGet, "/api/v1/status", http.StatusOK},
		{http.MethodGet, "/static/ui.js", http.StatusOK},
		{http.MethodGet, "/static/app.js", http.StatusNotFound},
	}
	for _, tt := range tbl {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, http.NoBody)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			assert.Equal(t, tt.want, w.Code)
		})
	}
}
