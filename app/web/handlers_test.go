package web

import (
	"errors"
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
	parts := strings.Split(body, `<div class="grid it`)[1:]
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
}

func TestServer_handleIndex(t *testing.T) {
	server := newHandlersTestServer(t, Config{Hostname: "bigmac"})
	addJobs(server, sampleJobs()...)

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()
	server.handleIndex(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "<title>cronn</title>")
	assert.Contains(t, body, `id="rows"`)
	assert.Contains(t, body, `hx-trigger="every 5s [!uiHeld], refresh-jobs from:body"`)
	assert.Contains(t, body, `id="ihead"`)
	assert.Contains(t, body, `id="dialog-slot"`)
	assert.Contains(t, body, `id="filter-mode" name="filter" value="all"`)
	assert.Contains(t, body, `id="sort-rev" name="rev" value="0"`)
	assert.Contains(t, body, "bigmac")
	assert.Contains(t, body, `<em id="count-disabled">1</em>`)
	assert.Contains(t, body, `<em id="count-failed">1</em>`)
	assert.Contains(t, body, "/static/ui.js")
	assert.NotContains(t, body, "hx-boost")
	assert.NotContains(t, body, `id="inspector"`)
	assert.NotContains(t, body, `id="selected-job"`)
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
		assert.Contains(t, body, `<span class="bad">exit 3</span>`)
		assert.Contains(t, body, "Every 1h15m")
		assert.Contains(t, body, "running 47s")
		assert.Contains(t, body, `<div class="grid it off" data-job-id="off"`)
		assert.Contains(t, body, `<span class="faint">disabled</span>`)
		assert.Contains(t, body, `<em id="count-all" hx-swap-oob="innerHTML">5</em>`)
		assert.Contains(t, body, `<span id="match-count" hx-swap-oob="innerHTML">5 jobs</span>`)
		assert.NotContains(t, body, `id="ihead"`, "polling must not replace the sort buttons")
		assert.NotContains(t, body, `id="pills"`, "polling must not replace the pills")
	})

	t.Run("never-ran job shows no exit code or duration", func(t *testing.T) {
		body := get(t, "filter=idle", &http.Cookie{Name: "filter-mode", Value: "idle"})
		assert.Equal(t, []string{"never"}, rowIDs(body))
		assert.Contains(t, body, "never ran")
		assert.NotContains(t, body, "exit ")
	})

	t.Run("row links carry the list state", func(t *testing.T) {
		body := get(t, "filter=all&sort=lastrun&rev=1&search=sync")
		assert.Contains(t, body, `href="/jobs/ok?filter=all&amp;rev=1&amp;search=sync&amp;sort=lastrun"`)
	})

	t.Run("run, stop or no button by job state", func(t *testing.T) {
		body := get(t, "")
		assert.Contains(t, body, `id="run-ok"`)
		assert.Contains(t, body, `id="run-never"`)
		assert.NotContains(t, body, `id="run-run"`, "a running job cannot start again")
		assert.NotContains(t, body, `id="run-off"`, "a disabled job has no run")
		assert.NotContains(t, body, `id="stop-run"`, "stopping needs a stopper")

		withStop := newHandlersTestServer(t, Config{RunStopper: &mocks.RunStopperMock{}})
		addJobs(withStop, sampleJobs()...)
		w := httptest.NewRecorder()
		withStop.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.Contains(t, w.Body.String(), `id="stop-run"`)

		manualOff := newHandlersTestServer(t, Config{DisableManual: true})
		addJobs(manualOff, sampleJobs()...)
		w = httptest.NewRecorder()
		manualOff.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.NotContains(t, w.Body.String(), `id="run-`)
	})

	t.Run("search by name", func(t *testing.T) {
		body := get(t, "search=report")
		assert.Equal(t, []string{"run"}, rowIDs(body))
		assert.Contains(t, body, "1 of 5")
	})

	t.Run("empty search result says what hides the jobs", func(t *testing.T) {
		body := get(t, "search=rsync", &http.Cookie{Name: "filter-mode", Value: "failed"})
		assert.Empty(t, rowIDs(body))
		assert.Contains(t, body, "No failing jobs match “rsync”")
		assert.Contains(t, body, "1 failing job is hidden by the search.")
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
		delete(s.templates, "partials")
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("template execution error", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		s.templates["partials"] = template.Must(template.New("x").Parse(`{{define "grid-rows"}}{{.Missing}}{{end}}`))
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestServer_indexRows(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	start := time.Now().Add(-time.Hour)
	require.NoError(t, server.store.RecordExecution(request.RecordExecution{JobID: "fail", StartedAt: start,
		FinishedAt: start.Add(time.Second), Status: enums.JobStatusFailed, ExitCode: 3}))
	q := jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll}
	jobs := server.getJobsWithStats(q).jobs

	t.Run("strips come from recent runs", func(t *testing.T) {
		rows := server.indexRows(jobs, q)
		require.Len(t, rows, 5)
		assert.Equal(t, "/jobs/ok?filter=all&rev=0&sort=default", rows[0].Link)
		require.Len(t, rows[1].Strip, indexStripSize)
		last := rows[1].Strip[indexStripSize-1]
		assert.Equal(t, "fail", last.Class)
		assert.Contains(t, last.Href, "run=")
		assert.Equal(t, "e", rows[0].Strip[indexStripSize-1].Class, "a job without runs is all padding")
	})

	t.Run("a store error leaves the strips empty", func(t *testing.T) {
		store := server.store
		server.store = &mocks.PersistenceMock{
			RecentRunsFunc: func(int) (map[string][]persistence.RunSummary, error) { return nil, errors.New("db is gone") },
			CloseFunc:      store.Close,
		}
		defer func() { server.store = store }()
		rows := server.indexRows(jobs, q)
		require.Len(t, rows, 5)
		for _, row := range rows {
			for _, bar := range row.Strip {
				assert.Equal(t, "e", bar.Class)
			}
		}
	})
}

func TestJobsQuery_values(t *testing.T) {
	tests := []struct {
		name string
		q    jobsQuery
		want string
	}{
		{name: "defaults", q: jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll}, want: "filter=all&rev=0&sort=default"},
		{name: "everything set", q: jobsQuery{sort: enums.SortModeNextrun, filter: enums.FilterModeFailed, search: "db backup", reverse: true},
			want: "filter=failed&rev=1&search=db+backup&sort=nextrun"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.q.values().Encode())
		})
	}
}

func TestServer_handleSortModeChange(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)

	tbl := []struct {
		form, wantCookie, wantRev string
	}{
		{"sort=lastrun", "lastrun", "0"},
		{"sort=nextrun", "nextrun", "0"},
		{"sort=default", "default", "0"},
		{"sort=bogus", "default", "0"},
		{"sort=lastrun&rev=1", "lastrun", "1"},
		{"sort=nextrun&rev=0", "nextrun", "0"},
	}
	for _, tt := range tbl {
		t.Run(tt.form, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader(tt.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			server.handleSortModeChange(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			cookies := w.Result().Cookies()
			require.Len(t, cookies, 2)
			assert.Equal(t, "sort-mode", cookies[0].Name)
			assert.Equal(t, tt.wantCookie, cookies[0].Value)
			assert.Equal(t, "sort-rev", cookies[1].Name)
			assert.Equal(t, tt.wantRev, cookies[1].Value)
			body := w.Body.String()
			assert.Contains(t, body, `id="ihead" hx-swap-oob="true"`)
			assert.Contains(t, body, `id="list-state" hidden hx-swap-oob="true"`)
			assert.Contains(t, body, `id="pills" aria-label="Filter jobs by state" hx-swap-oob="true"`)
			assert.Contains(t, body, `id="fdd" hx-swap-oob="true"`)
			assert.Contains(t, body, `id="sort-rev" name="rev" value="`+tt.wantRev+`"`)
		})
	}

	t.Run("new order wins over the old cookie on the request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort=lastrun"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "sort-mode", Value: "default"})
		w := httptest.NewRecorder()
		server.handleSortModeChange(w, req)
		assert.Equal(t, []string{"run", "ok", "fail", "off", "never"}, rowIDs(w.Body.String()))
		assert.Contains(t, w.Body.String(), `class="hsort on" id="sort-lastrun"`)
	})

	t.Run("arrow shows the direction and the next click flips it", func(t *testing.T) {
		for _, tt := range []struct{ form, dir, next string }{
			{"sort=lastrun&rev=0", "down", "1"}, {"sort=lastrun&rev=1", "up", "0"},
		} {
			req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader(tt.form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			server.handleSortModeChange(w, req)
			_, rest, found := strings.Cut(w.Body.String(), `id="sort-lastrun"`)
			require.True(t, found, tt.form)
			head, _, found := strings.Cut(rest, "</button>")
			require.True(t, found, tt.form)
			assert.Contains(t, head, `data-dir="`+tt.dir+`"`, tt.form)
			assert.Contains(t, head, `"rev": "`+tt.next+`"`, tt.form)
			assert.Contains(t, w.Body.String(), `{"sort": "nextrun", "rev": "0"}`, "another column starts nearest first")
		}
	})

	t.Run("reverse flips the order", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader("sort=lastrun&rev=1"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		server.handleSortModeChange(w, req)
		assert.Equal(t, []string{"off", "fail", "ok", "run", "never"}, rowIDs(w.Body.String()))
	})

	t.Run("next run keeps the disabled job last", func(t *testing.T) {
		srv := newHandlersTestServer(t, Config{})
		jobs := sampleJobs()
		for i := range jobs {
			jobs[i].NextRun = time.Now().Add(time.Duration(i+1) * time.Hour)
		}
		jobs[4].NextRun = time.Now().Add(time.Minute) // the disabled job has the soonest next time
		addJobs(srv, jobs...)
		for _, form := range []string{"sort=nextrun", "sort=nextrun&rev=1"} {
			req := httptest.NewRequest(http.MethodPost, "/api/sort-mode", strings.NewReader(form))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			srv.handleSortModeChange(w, req)
			ids := rowIDs(w.Body.String())
			require.Len(t, ids, 5, form)
			assert.Equal(t, "off", ids[4], form)
		}
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
			mode := tt.wantCookie
			assert.Contains(t, w.Body.String(), `class="on" id="pill-`+mode+`"`)
		})
	}

	t.Run("keeps the tab's sort over the cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/filter-mode", strings.NewReader("filter=all&sort=lastrun"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: "sort-mode", Value: "default"})
		w := httptest.NewRecorder()
		server.handleFilterModeChange(w, req)
		assert.Equal(t, []string{"run", "ok", "fail", "off", "never"}, rowIDs(w.Body.String()))
		assert.Contains(t, w.Body.String(), `class="hsort on" id="sort-lastrun"`)
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

func TestServer_handleRunConfirm(t *testing.T) {
	post := func(s *Server, id string, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/run-confirm", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleRunConfirm(w, req)
		return w
	}
	newServer := func(t *testing.T, cfg Config) *Server {
		s := newHandlersTestServer(t, cfg)
		addJobs(s, append(sampleJobs(),
			persistence.JobInfo{ID: "tpl", Name: "Templated", Command: "echo {{.YYYYMMDD}}", Schedule: "@daily", Enabled: true})...)
		return s
	}

	t.Run("plain run shows the job command", func(t *testing.T) {
		w := post(newServer(t, Config{}), "ok", url.Values{})
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `<dialog class="cf" id="run-dialog" data-job-id="ok"`)
		assert.Contains(t, body, "Run “Quote sync” now?")
		assert.Contains(t, body, `<div class="cmd" id="cf-cmd">echo sync</div>`)
		assert.Contains(t, body, `<input type="hidden" name="command" value="echo sync">`)
		assert.NotContains(t, body, `id="cf-edited"`)
		assert.NotContains(t, body, `id="cf-date"`)
		assert.Contains(t, body, `hx-post="/api/jobs/ok/run"`)
	})

	t.Run("blank command means the job's own", func(t *testing.T) {
		body := post(newServer(t, Config{}), "ok", url.Values{"command": {"   "}}).Body.String()
		assert.Contains(t, body, `id="cf-cmd">echo sync</div>`)
		assert.NotContains(t, body, `id="cf-edited"`)
	})

	t.Run("templated run with a date", func(t *testing.T) {
		body := post(newServer(t, Config{}), "tpl", url.Values{"date": {" 20260915 "}}).Body.String()
		assert.Contains(t, body, `<b class="mono" id="cf-date">20260915</b>`)
		assert.Contains(t, body, `<input type="hidden" name="date" value="20260915">`)
	})

	t.Run("edited command is marked", func(t *testing.T) {
		body := post(newServer(t, Config{}), "ok", url.Values{"command": {"echo other"}}).Body.String()
		assert.Contains(t, body, `id="cf-cmd">echo other</div>`)
		assert.Contains(t, body, "Command edited for this run only.")
	})

	t.Run("list state is echoed", func(t *testing.T) {
		form := url.Values{"filter": {"failed"}, "sort": {"lastrun"}, "rev": {"1"}, "search": {"feed"}}
		body := post(newServer(t, Config{}), "fail", form).Body.String()
		for _, field := range []string{`name="filter" value="failed"`, `name="sort" value="lastrun"`,
			`name="rev" value="1"`, `name="search" value="feed"`} {
			assert.Contains(t, body, field)
		}
	})

	t.Run("disabled or running job opens nothing and refreshes", func(t *testing.T) {
		s := newServer(t, Config{})
		for _, id := range []string{"off", "run"} {
			w := post(s, id, url.Values{})
			assert.Equal(t, http.StatusNoContent, w.Code, id)
			assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger"), id)
			assert.Empty(t, w.Body.String(), id)
		}
	})

	t.Run("edited command refused when editing is disabled", func(t *testing.T) {
		s := newServer(t, Config{DisableCommandEdit: true})
		assert.Equal(t, http.StatusForbidden, post(s, "ok", url.Values{"command": {"echo other"}}).Code)
		assert.Equal(t, http.StatusOK, post(s, "ok", url.Values{"command": {"echo sync"}}).Code)
	})

	t.Run("manual runs disabled", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, post(newServer(t, Config{DisableManual: true}), "ok", url.Values{}).Code)
	})

	t.Run("unknown job", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, post(newServer(t, Config{}), "nope", url.Values{}).Code)
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

	t.Run("accepted: curl gets 202 text, htmx is redirected to the run", func(t *testing.T) {
		s, ch := newServer(t, Config{}, 2)

		w := post(s, "ok", url.Values{}, false)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "Job triggered", w.Body.String())
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger"))
		req := <-ch
		assert.Equal(t, "echo sync", req.Command)
		assert.Nil(t, req.CustomDate)

		before := time.Now().UnixMicro()
		w = post(s, "tpl", url.Values{"command": {"echo edited {{.YYYYMMDD}}"}, "date": {"20260915"},
			"filter": {"failed"}, "sort": {"bogus"}, "rev": {"1"}, "search": {"a&b"}}, true)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Empty(t, w.Body.String())
		loc, err := url.Parse(w.Header().Get("HX-Redirect"))
		require.NoError(t, err)
		assert.Equal(t, "/jobs/tpl", loc.Path)
		after, err := strconv.ParseInt(loc.Query().Get("after"), 10, 64)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, after, before)
		assert.LessOrEqual(t, after, time.Now().UnixMicro())
		assert.Equal(t, "failed", loc.Query().Get("filter"))
		assert.Equal(t, "default", loc.Query().Get("sort"), "the list state is re-parsed, not copied")
		assert.Equal(t, "1", loc.Query().Get("rev"))
		assert.Equal(t, "a&b", loc.Query().Get("search"))
		req = <-ch
		assert.Equal(t, "echo edited {{.YYYYMMDD}}", req.Command)
		require.NotNil(t, req.CustomDate)
		assert.Equal(t, "2026-09-15 00:00", req.CustomDate.Format("2006-01-02 15:04"))
	})

	t.Run("redirect keeps the base URL", func(t *testing.T) {
		s, ch := newServer(t, Config{BaseURL: "/cronn"}, 1)
		w := post(s, "ok", url.Values{}, true)
		assert.True(t, strings.HasPrefix(w.Header().Get("HX-Redirect"), "/cronn/jobs/ok?"))
		<-ch
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
			assert.Contains(t, body, `id="run-dialog"`)
			assert.Contains(t, body, template.HTMLEscapeString("Not started: "+tt.wantMsg+"."))
			assert.Empty(t, w.Header().Get("HX-Redirect"))
			if cmd := tt.form.Get("command"); cmd != "" {
				assert.Contains(t, body, `name="command" value="`+template.HTMLEscapeString(cmd)+`"`, "edits are kept")
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
		assert.Contains(t, body, `name="command" value="echo edited"`)
		assert.Contains(t, body, `name="date" value="20260915"`, "a submitted date is kept")
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
	toggleHtmx := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/toggle", http.NoBody)
		req.Header.Set("HX-Request", "true")
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

	w = toggleHtmx("t")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
	assert.Contains(t, w.Body.String(), "Schedule disabled: <b>echo toggle</b>")
	w = toggleHtmx("t")
	assert.Contains(t, w.Body.String(), "Schedule enabled: <b>echo toggle</b>")

	assert.Equal(t, http.StatusNotFound, toggle("nope").Code)
	assert.Equal(t, http.StatusBadRequest, toggle("").Code)
}

func TestServer_handleStopForm(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	newServer := func(t *testing.T, cfg Config) (*Server, string) {
		if cfg.RunStopper == nil {
			cfg.RunStopper = &mocks.RunStopperMock{StopRunFunc: func(string, int64) bool { return true }}
		}
		s := newHandlersTestServer(t, cfg)
		s.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: start.Add(-time.Hour)})
		s.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: start, ExecutedCommand: "live cmd --edited"})
		return s, HashCommand("live cmd")
	}
	get := func(s *Server, id, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/stop-form"+query, http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleStopForm(w, req)
		return w
	}
	startParam := strconv.FormatInt(start.UnixMicro(), 10)

	t.Run("without a start the newest run is the target", func(t *testing.T) {
		s, id := newServer(t, Config{})
		w := get(s, id, "")
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `<dialog class="cf" id="stop-dialog" data-job-id="`+id+`"`)
		assert.Contains(t, body, `<input type="hidden" name="start" value="`+startParam+`">`)
		assert.Contains(t, body, "live cmd --edited")
		assert.Contains(t, body, "Ends the run started at "+start.Format("15:04:05"))
	})

	t.Run("a start picks that run", func(t *testing.T) {
		s, id := newServer(t, Config{})
		older := strconv.FormatInt(start.Add(-time.Hour).UnixMicro(), 10)
		body := get(s, id, "?start="+older).Body.String()
		assert.Contains(t, body, `name="start" value="`+older+`"`)
	})

	t.Run("a run no longer in progress closes the dialog and refreshes", func(t *testing.T) {
		s, id := newServer(t, Config{})
		w := get(s, id, "?start=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
		w = get(s, "nope", "")
		assert.Empty(t, w.Body.String())
	})

	t.Run("invalid start", func(t *testing.T) {
		s, id := newServer(t, Config{})
		assert.Equal(t, http.StatusBadRequest, get(s, id, "?start=x").Code)
	})

	t.Run("manual control disabled", func(t *testing.T) {
		s, id := newServer(t, Config{DisableManual: true})
		assert.Equal(t, http.StatusForbidden, get(s, id, "").Code)
	})
}

func TestServer_handleStopRun(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	type call struct {
		command string
		start   int64
	}
	newServer := func(t *testing.T, cfg Config, running bool) (*Server, *mocks.RunStopperMock, string) {
		stopper := &mocks.RunStopperMock{StopRunFunc: func(string, int64) bool { return running }}
		cfg.RunStopper = stopper
		s := newHandlersTestServer(t, cfg)
		s.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted, StartedAt: start})
		return s, stopper, HashCommand("live cmd")
	}
	post := func(s *Server, id, startValue string, htmx bool) *httptest.ResponseRecorder {
		form := url.Values{"start": {startValue}}
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/stop", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleStopRun(w, req)
		return w
	}
	startParam := strconv.FormatInt(start.UnixMicro(), 10)

	t.Run("stopped: curl gets 202, htmx gets the toast and a refresh", func(t *testing.T) {
		s, stopper, id := newServer(t, Config{}, true)
		w := post(s, id, startParam, false)
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "Stop requested", w.Body.String())
		require.Len(t, stopper.StopRunCalls(), 1)
		assert.Equal(t, call{command: "live cmd", start: start.UnixMicro()},
			call{command: stopper.StopRunCalls()[0].Command, start: stopper.StopRunCalls()[0].Start})

		w = post(s, id, startParam, true)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Stop requested: <b>live cmd</b>")
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
	})

	t.Run("run not in progress: curl gets 404, htmx gets an empty slot", func(t *testing.T) {
		s, _, id := newServer(t, Config{}, false)
		assert.Equal(t, http.StatusNotFound, post(s, id, startParam, false).Code)
		w := post(s, id, startParam, true)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Body.String())
		assert.Equal(t, "refresh-jobs", w.Header().Get("HX-Trigger-After-Swap"))
	})

	t.Run("unknown job never reaches the stopper", func(t *testing.T) {
		s, stopper, _ := newServer(t, Config{}, true)
		assert.Equal(t, http.StatusNotFound, post(s, "nope", startParam, false).Code)
		assert.Empty(t, stopper.StopRunCalls())
	})

	t.Run("invalid start", func(t *testing.T) {
		s, stopper, id := newServer(t, Config{}, true)
		assert.Equal(t, http.StatusBadRequest, post(s, id, "x", true).Code)
		assert.Empty(t, stopper.StopRunCalls())
	})

	t.Run("manual control disabled", func(t *testing.T) {
		s, stopper, id := newServer(t, Config{DisableManual: true}, true)
		assert.Equal(t, http.StatusForbidden, post(s, id, startParam, false).Code)
		assert.Empty(t, stopper.StopRunCalls())
	})

	t.Run("no stopper configured", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		assert.Equal(t, http.StatusForbidden, post(s, "any", startParam, false).Code)
	})
}

func TestServer_stopButtons(t *testing.T) {
	start := time.Now().Add(-time.Minute)
	newServer := func(t *testing.T, cfg Config) (*Server, string) {
		cfg.RunStopper = &mocks.RunStopperMock{StopRunFunc: func(string, int64) bool { return true }}
		s := newHandlersTestServer(t, cfg)
		addJobs(s, sampleJobs()...)
		s.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted, StartedAt: start})
		return s, HashCommand("live cmd")
	}
	jobs := func(s *Server) string {
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody))
		return w.Body.String()
	}

	t.Run("a running job shows Stop in place of Run", func(t *testing.T) {
		s, id := newServer(t, Config{})
		body := jobs(s)
		assert.Contains(t, body, `id="stop-`+id+`"`)
		assert.NotContains(t, body, `id="run-`+id+`"`)
		assert.Contains(t, body, `id="run-ok"`, "an idle job keeps Run")
		assert.NotContains(t, body, `id="stop-ok"`)
	})

	t.Run("no Stop without a stopper", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		s.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted, StartedAt: start})
		assert.NotContains(t, jobs(s), `id="stop-`)
	})

	t.Run("no Stop when manual control is disabled", func(t *testing.T) {
		s, _ := newServer(t, Config{DisableManual: true})
		assert.NotContains(t, jobs(s), `id="stop-`)
	})
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
	assert.Contains(t, body, `href="https://github.com/umputun/cronn"`)
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
		{http.MethodGet, "/jobs/ok", http.StatusOK},
		{http.MethodGet, "/jobs/nope", http.StatusNotFound},
		{http.MethodGet, "/api/jobs/ok/facts", http.StatusOK},
		{http.MethodGet, "/api/jobs/ok/runs", http.StatusOK},
		{http.MethodGet, "/api/jobs/jump?q=sync", http.StatusOK},
		{http.MethodGet, "/api/jobs/ok/inspector", http.StatusNotFound},
		{http.MethodGet, "/api/jobs/ok/run-form", http.StatusNotFound},
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
