package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/mocks"
	"github.com/umputun/cronn/app/web/persistence"
)

func getJob(id, query string, handler func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/jobs/"+id+"?"+query, http.NoBody)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func recordRun(t *testing.T, s *Server, rec request.RecordExecution) int {
	t.Helper()
	if rec.FinishedAt.IsZero() {
		rec.FinishedAt = rec.StartedAt.Add(2 * time.Second)
	}
	require.NoError(t, s.store.RecordExecution(rec))
	run, err := s.store.GetExecutionByStart(rec.JobID, rec.StartedAt.UnixMicro())
	require.NoError(t, err)
	return run.ID
}

func TestServer_handleJobPage(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, append(sampleJobs(),
		persistence.JobInfo{ID: "tpl", Name: "Templated", Command: "echo {{.YYYYMMDD}}", Schedule: "@daily", Enabled: true},
		persistence.JobInfo{ID: "multi", Name: "Two step", Command: "echo one\necho two\n", Schedule: "@daily", Enabled: true},
		persistence.JobInfo{ID: "long", Name: "Long script", Command: "a\nb\nc\nd\ne\nf\ng\nh", Schedule: "@daily", Enabled: true},
		persistence.JobInfo{ID: "trail", Name: "Block", Command: "echo once\n", Schedule: "@daily", Enabled: true},
		persistence.JobInfo{ID: "lead", Name: "Blank first", Command: "\nfirst\nsecond", Schedule: "@daily", Enabled: true})...)
	start := time.Date(2026, 9, 29, 16, 50, 0, 0, time.Local)
	first := recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: start, Status: enums.JobStatusFailed,
		ExitCode: 3, Output: "fetching\nERROR boom\nfatal: gave up\n"})
	recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: start.Add(time.Minute), Status: enums.JobStatusSuccess,
		ExecutedCommand: "sh -c 'exit 0'", Output: "manual ok"})
	latest := recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: start.Add(2 * time.Minute),
		Status: enums.JobStatusFailed, ExitCode: 137})
	other := recordRun(t, server, request.RecordExecution{JobID: "ok", StartedAt: start, Status: enums.JobStatusSuccess})

	page := func(id, query string) *httptest.ResponseRecorder { return getJob(id, query, server.handleJobPage) }

	t.Run("failing job opens its latest run", func(t *testing.T) {
		w := page("fail", "")
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "<title>Vendor feed import · cronn</title>")
		assert.Contains(t, body, `<h1 class="" id="job-heading">Vendor feed import</h1>`)
		assert.Contains(t, body, `<span class="badge fail" id="job-badge">Failing</span>`)
		assert.Contains(t, body, `value="sh -c &#39;exit 3&#39;"`)
		assert.Contains(t, body, `id="run-now"`)
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(latest)+`">`)
		assert.Contains(t, body, "No output captured. The run failed with exit code 137.")
		assert.Equal(t, 3, strings.Count(body, `<button type="button" class="jr`))
		assert.Equal(t, 1, strings.Count(body, `class="jr sel"`))
		assert.Contains(t, body, `<input type="hidden" name="newest" value="`+strconv.Itoa(latest)+`">`)
		assert.Contains(t, body, `hx-trigger="every 5s [!uiHeld], refresh-jobs from:body, run-selected from:body"`)
		assert.Contains(t, body, `hx-include="#joutput [name='selected-run'], #jruns [name='newest'], #filter-mode, #sort-mode, #sort-rev, #list-search"`,
			"the facts poll keeps the list state in its links")
		assert.NotContains(t, body, `id="pill-all" aria-pressed`, "pills link back to the index on a job page")
		assert.Contains(t, body, `class="off" id="pill-failed" href="/?filter=failed&amp;rev=0&amp;sort=default"`)
		assert.NotContains(t, body, `id="datein"`, "no date without templates")
	})

	t.Run("a multi-line command keeps its lines in a textarea", func(t *testing.T) {
		body := page("multi", "").Body.String()
		assert.Contains(t, body, "<textarea class=\"cmdin\" id=\"cmdin\" name=\"command\" rows=\"2\" data-orig=\"echo one\necho two\n\"")
		assert.Contains(t, body, ">\necho one\necho two\n</textarea>")
		assert.Contains(t, page("long", "").Body.String(), `rows="6"`, "a long script stops growing at six rows")
		assert.Contains(t, page("ok", "").Body.String(), `<input class="cmdin" id="cmdin" name="command" value="echo sync"`)
		trail := page("trail", "").Body.String()
		assert.Contains(t, trail, `<textarea class="cmdin" id="cmdin" name="command" rows="1"`, "a trailing newline needs a textarea too")
		assert.Contains(t, trail, ">\necho once\n</textarea>")
		assert.Contains(t, page("lead", "").Body.String(), ">\n\nfirst\nsecond</textarea>",
			"the parser drops the newline after the tag, not the command's own")
	})

	t.Run("run= opens that run with numbered lines and error marks", func(t *testing.T) {
		body := page("fail", "run="+strconv.Itoa(first)).Body.String()
		assert.Contains(t, body, `name="selected-run" value="`+strconv.Itoa(first)+`"`)
		assert.Contains(t, body, `<div class="log"><div>fetching</div><div class="err">ERROR boom</div><div class="err">fatal: gave up</div></div>`)
		assert.Contains(t, body, `class="fail sel"`, "the strip marks the selected run")
	})

	t.Run("run= of another job is ignored", func(t *testing.T) {
		body := page("fail", "run="+strconv.Itoa(other)).Body.String()
		assert.Contains(t, body, `name="selected-run" value="`+strconv.Itoa(latest)+`"`)
	})

	t.Run("after= waits for the started run", func(t *testing.T) {
		body := page("fail", "after=1795763722418424").Body.String()
		assert.Contains(t, body, "Starting…")
		assert.Contains(t, body, `live-output?after=1795763722418424"`)
		assert.Contains(t, body, `hx-trigger="every 1s [!uiHeld]"`)
		assert.NotContains(t, body, `name="selected-run"`)
	})

	t.Run("list state goes to the back link and the previous and next links", func(t *testing.T) {
		body := page("fail", "filter=all&sort=lastrun&rev=1").Body.String()
		assert.Contains(t, body, `id="back-link" href="/?filter=all&amp;rev=1&amp;sort=lastrun"`)
		assert.Contains(t, body, `id="filter-mode" name="filter" value="all"`)
		assert.Contains(t, body, `id="sort-rev" name="rev" value="1"`)
		assert.Contains(t, body, `<span id="jpos">`)
	})

	tbl := []struct {
		id   string
		want []string
		not  []string
	}{
		{"ok", []string{`id="job-badge">Healthy`, `id="run-now"`, "Enabled</button>"}, nil},
		{"never", []string{`class="cmd" id="job-heading">reindex --full</h1>`, `id="job-badge">Never ran`, "No runs yet."}, nil},
		{"off", []string{`id="job-badge">Disabled`, "Enable the schedule to run this job", "Disabled</button>"},
			[]string{`id="run-now"`, " readonly"}},
		{"run", []string{`id="job-badge">Running`, "running 47s"}, []string{`id="run-now"`, `id="stop-run"`}},
		{"tpl", []string{`id="datein"`, `class="runbar dated"`}, nil},
	}
	for _, tt := range tbl {
		t.Run("state "+tt.id, func(t *testing.T) {
			w := page(tt.id, "")
			require.Equal(t, http.StatusOK, w.Code)
			for _, want := range tt.want {
				assert.Contains(t, w.Body.String(), want)
			}
			for _, not := range tt.not {
				assert.NotContains(t, w.Body.String(), not)
			}
		})
	}

	t.Run("unknown job is a 404 page", func(t *testing.T) {
		w := page("nope", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "Job not found")
		assert.Contains(t, w.Body.String(), `<a href="/?filter=all&amp;rev=0&amp;sort=default" id="back-link">All jobs</a>`)
	})

	t.Run("command is read-only without editing or manual runs", func(t *testing.T) {
		for _, cfg := range []Config{{DisableCommandEdit: true}, {DisableManual: true}} {
			s := newHandlersTestServer(t, cfg)
			addJobs(s, sampleJobs()...)
			body := getJob("ok", "", s.handleJobPage).Body.String()
			assert.Contains(t, body, "readonly")
		}
	})

	t.Run("history read failure", func(t *testing.T) {
		s := newHandlersTestServer(t, Config{})
		addJobs(s, sampleJobs()...)
		store := s.store
		s.store = &mocks.PersistenceMock{
			GetExecutionsFunc: func(string, int) ([]persistence.ExecutionInfo, error) { return nil, errors.New("db is gone") },
			CloseFunc:         store.Close,
		}
		assert.Equal(t, http.StatusInternalServerError, getJob("ok", "", s.handleJobPage).Code)
	})
}

func TestServer_handleJobPageRunInProgress(t *testing.T) {
	server := newHandlersTestServer(t, Config{RunStopper: &mocks.RunStopperMock{}})
	id := HashCommand("live cmd")
	start := time.Now().Add(-47 * time.Second)
	server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveOutput: func() string { return "tick 1\ntick 2" }})
	token := "live:" + strconv.FormatInt(start.UnixMicro(), 10)

	body := getJob(id, "", server.handleJobPage).Body.String()
	assert.Contains(t, body, `id="live-out"`)
	assert.Contains(t, body, `<div>tick 1</div><div class=" cursor">tick 2</div>`)
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+token+`">`)
	assert.Contains(t, body, `class="jr sel" data-run="`+token+`"`)
	assert.Contains(t, body, `class="run sel" href=`)
	assert.Contains(t, body, `/stop-form?start=`+strconv.FormatInt(start.UnixMicro(), 10)+`"`)
	assert.NotContains(t, body, "No runs yet.", "a run in progress is listed")
}

func TestServer_handleJobPageLiveRunLink(t *testing.T) {
	server := newHandlersTestServer(t, Config{ExecMaxLogLines: 100})
	id := HashCommand("live cmd")
	older, newer := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	for _, at := range []time.Time{older, newer} {
		server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: at, LiveOutput: func() string { return "out " + at.String() }})
	}
	micro := func(at time.Time) string { return strconv.FormatInt(at.UnixMicro(), 10) }

	t.Run("the older of two runs in progress", func(t *testing.T) {
		body := getJob(id, "run=live:"+micro(older), server.handleJobPage).Body.String()
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="live:`+micro(older)+`">`)
	})

	t.Run("a run recorded since the link was drawn", func(t *testing.T) {
		server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeCompleted,
			StartedAt: older, FinishedAt: older.Add(time.Second), Output: "older done"})
		runs, err := server.store.GetExecutions(id, 10)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		body := getJob(id, "run=live:"+micro(older), server.handleJobPage).Body.String()
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(runs[0].ID)+`">`)
		assert.Contains(t, body, "older done")
	})

	t.Run("an unknown start falls back to the newest run in progress", func(t *testing.T) {
		body := getJob(id, "run=live:12345", server.handleJobPage).Body.String()
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="live:`+micro(newer)+`">`)
	})
}

func TestServer_handleJobPageFinishedRunListedOnce(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	id := HashCommand("live cmd")
	start := time.Now().Add(-time.Minute)
	server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveOutput: func() string { return "partial" }})
	run := recordRun(t, server, request.RecordExecution{JobID: id, StartedAt: start, FinishedAt: start.Add(time.Second),
		Status: enums.JobStatusSuccess})

	body := getJob(id, "", server.handleJobPage).Body.String()
	assert.NotContains(t, body, `data-run="live:`, "a run already recorded is not listed as in progress too")
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(run)+`">`)
	assert.Contains(t, body, `class="jr sel" data-run="`+strconv.Itoa(run)+`"`)
}

func TestServer_handleJobPageRunFinishingDuringHistoryRead(t *testing.T) {
	// regression: a run finishing between the history read and the active snapshot was in neither
	server := newHandlersTestServer(t, Config{})
	id := HashCommand("live cmd")
	older, start := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
	recordRun(t, server, request.RecordExecution{JobID: id, StartedAt: older, FinishedAt: older.Add(time.Second),
		Status: enums.JobStatusSuccess, Output: "older run output"})
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
		JobRunsFunc:              store.JobRuns,
		RecordExecutionFunc:      store.RecordExecution,
		CleanupOldExecutionsFunc: store.CleanupOldExecutions,
		CloseFunc:                store.Close,
	}

	body := getJob(id, "", server.handleJobPage).Body.String()
	assert.Contains(t, body, "run A output")
	assert.NotContains(t, body, "older run output")
	token := "live:" + strconv.FormatInt(start.UnixMicro(), 10)
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+token+`">`)
}

func TestServer_jobPosition(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	link := func(id, query string) string { return "/jobs/" + id + "?" + query }

	tbl := []struct {
		name string
		id   string
		q    jobsQuery
		want jobPosition
	}{
		{"crontab order", "fail", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll},
			jobPosition{Pos: 2, Total: 5, Prev: link("ok", "filter=all&rev=0&sort=default"), Next: link("run", "filter=all&rev=0&sort=default")}},
		{"first has no previous", "ok", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll},
			jobPosition{Pos: 1, Total: 5, Next: link("fail", "filter=all&rev=0&sort=default")}},
		{"reversed", "ok", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll, reverse: true},
			jobPosition{Pos: 5, Total: 5, Prev: link("fail", "filter=all&rev=1&sort=default")}},
		{"last run sort", "ok", jobsQuery{sort: enums.SortModeLastrun, filter: enums.FilterModeAll},
			jobPosition{Pos: 2, Total: 5, Prev: link("run", "filter=all&rev=0&sort=lastrun"), Next: link("fail", "filter=all&rev=0&sort=lastrun")}},
		{"filtered", "ok", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeSuccess},
			jobPosition{Pos: 1, Total: 1}},
		{"searched", "never", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll, search: "re"},
			jobPosition{Pos: 2, Total: 2, Prev: link("run", "filter=all&rev=0&search=re&sort=default")}},
		{"not in the list falls back to all jobs", "off", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeFailed},
			jobPosition{Pos: 5, Total: 5, Prev: link("never", "filter=failed&rev=0&sort=default")}},
		{"unknown job", "nope", jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll}, jobPosition{}},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, server.jobPosition(tt.id, tt.q))
		})
	}
}

func TestServer_stripBars(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	start := time.Date(2026, 9, 29, 16, 50, 0, 0, time.Local)
	runs := []persistence.RunSummary{ // newest first
		{ID: 3, StartedAt: start.Add(2 * time.Minute), FinishedAt: start.Add(2*time.Minute + time.Second), Status: enums.JobStatusSuccess, Manual: true},
		{ID: 2, StartedAt: start.Add(time.Minute), FinishedAt: start.Add(time.Minute + 4*time.Second), Status: enums.JobStatusSuccess},
		{ID: 1, StartedAt: start, FinishedAt: start.Add(time.Second), Status: enums.JobStatusFailed, ExitCode: 3},
	}
	q := jobsQuery{sort: enums.SortModeLastrun, filter: enums.FilterModeAll, search: "x y"}
	job := persistence.JobInfo{ID: "j1", Command: "cmd"}

	t.Run("padding, order, heights, tips and links", func(t *testing.T) {
		bars := server.stripBars(stripReq{Job: job, Runs: runs, Size: 5, Selected: "2"}, q)
		require.Len(t, bars, 5)
		assert.Equal(t, stripBar{Class: "e", Height: 25}, bars[0])
		assert.Equal(t, stripBar{Class: "e", Height: 25}, bars[1])
		assert.Equal(t, []string{"1", "2", "3"}, []string{bars[2].Key, bars[3].Key, bars[4].Key}, "oldest to newest")
		assert.Equal(t, "fail", bars[2].Class)
		assert.Equal(t, 100, bars[2].Height, "a failure is full height")
		assert.Equal(t, 100, bars[3].Height, "the longest run is full height")
		assert.Equal(t, 35+65/4, bars[4].Height)
		assert.Equal(t, "Sep 29 16:50:00 · exit 3 · 1.0s", bars[2].Tip)
		assert.Equal(t, "Sep 29 16:52:00 · ok · 1.0s · manual", bars[4].Tip)
		assert.True(t, bars[3].Sel)
		assert.False(t, bars[4].Sel)
		assert.Equal(t, "/jobs/j1?filter=all&rev=0&run=2&search=x+y&sort=lastrun", bars[3].Href)
		assert.Equal(t, "/api/jobs/j1/executions/2/output", bars[3].Out)
	})

	t.Run("size limits the runs shown", func(t *testing.T) {
		bars := server.stripBars(stripReq{Job: job, Runs: runs, Size: 2}, q)
		require.Len(t, bars, 2)
		assert.Equal(t, "2", bars[0].Key)
		assert.Equal(t, "3", bars[1].Key)
	})

	t.Run("runs in progress come last, recorded ones are not repeated", func(t *testing.T) {
		live := start.Add(3 * time.Minute)
		server.handleJobEvent(JobEvent{Command: "live", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: live, ExecutedCommand: "live --x"})
		server.handleJobEvent(JobEvent{Command: "live", Schedule: "* * * * *", EventType: enums.EventTypeStarted,
			StartedAt: runs[0].StartedAt})
		liveJob, ok := server.jobByID(HashCommand("live"))
		require.True(t, ok)
		require.True(t, liveJob.IsRunning)
		token := "live:" + strconv.FormatInt(live.UnixMicro(), 10)
		bars := server.stripBars(stripReq{Job: liveJob, Runs: runs, Size: 5, Selected: token}, q)
		require.Len(t, bars, 5)
		last := bars[4]
		assert.Equal(t, token, last.Key)
		assert.Equal(t, "run", last.Class)
		assert.True(t, last.Sel)
		assert.True(t, strings.HasPrefix(last.Tip, "Sep 29 16:53:00 · running "))
		assert.True(t, strings.HasSuffix(last.Tip, " · manual"))
		assert.Equal(t, "/api/jobs/"+liveJob.ID+"/live-output?start="+strconv.FormatInt(live.UnixMicro(), 10), last.Out)
		assert.Equal(t, "3", bars[3].Key, "the active run already recorded is drawn once")
	})

	t.Run("no runs at all", func(t *testing.T) {
		bars := server.stripBars(stripReq{Job: job, Size: 3}, q)
		assert.Equal(t, []stripBar{{Class: "e", Height: 25}, {Class: "e", Height: 25}, {Class: "e", Height: 25}}, bars)
	})
}

func TestServer_handleJobFacts(t *testing.T) {
	newServer := func(t *testing.T, cfg Config) *Server {
		s := newHandlersTestServer(t, cfg)
		addJobs(s, sampleJobs()...)
		return s
	}
	facts := func(s *Server, id, query string) *httptest.ResponseRecorder {
		return getJob(id, query, s.handleJobFacts)
	}

	t.Run("facts with the title, run action and counts out of band", func(t *testing.T) {
		s := newServer(t, Config{})
		run := recordRun(t, s, request.RecordExecution{JobID: "ok", StartedAt: time.Now().Add(-time.Minute), Status: enums.JobStatusSuccess})
		w := facts(s, "ok", "newest="+strconv.Itoa(run)+"&selected-run="+strconv.Itoa(run))
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Every minute")
		assert.Contains(t, body, `id="toggle-ok"`)
		assert.Contains(t, body, `class="ok sel"`)
		assert.Contains(t, body, `<span class="jtitle" id="jtitle" hx-swap-oob="true">`)
		assert.Contains(t, body, `<span class="runact" id="runact" hx-swap-oob="true">`)
		assert.Contains(t, body, `id="run-now"`)
		assert.Contains(t, body, `<em id="count-all" hx-swap-oob="innerHTML">5</em>`)
		assert.NotContains(t, body, `id="match-count"`, "the job page has no match count")
		assert.Empty(t, w.Header().Get("HX-Trigger"), "the runs list is current")
	})

	t.Run("a newer run asks the runs list to reload", func(t *testing.T) {
		s := newServer(t, Config{})
		run := recordRun(t, s, request.RecordExecution{JobID: "ok", StartedAt: time.Now().Add(-time.Minute), Status: enums.JobStatusSuccess})
		w := facts(s, "ok", "newest="+strconv.Itoa(run-1))
		assert.Equal(t, "runs-changed", w.Header().Get("HX-Trigger"))
		assert.NotContains(t, w.Body.String(), `class="ok sel"`, "nothing selected, nothing marked")
	})

	t.Run("run action by state", func(t *testing.T) {
		stopper := &mocks.RunStopperMock{}
		tbl := []struct {
			name string
			cfg  Config
			id   string
			want string
			not  []string
		}{
			{"idle runs", Config{}, "ok", `id="run-now"`, []string{`id="stop-run"`}},
			{"disabled", Config{}, "off", "Enable the schedule to run this job", []string{`id="run-now"`}},
			{"running with a stopper", Config{RunStopper: stopper}, "run", `id="stop-run"`, []string{`id="run-now"`}},
			{"running without a stopper", Config{}, "run", `id="runact" hx-swap-oob="true">`, []string{`id="run-now"`, `id="stop-run"`}},
			{"manual disabled", Config{DisableManual: true}, "ok", `id="runact" hx-swap-oob="true">`, []string{`id="run-now"`, `id="stop-run"`}},
		}
		for _, tt := range tbl {
			t.Run(tt.name, func(t *testing.T) {
				body := facts(newServer(t, tt.cfg), tt.id, "").Body.String()
				assert.Contains(t, body, tt.want)
				for _, not := range tt.not {
					assert.NotContains(t, body, not)
				}
			})
		}
	})

	t.Run("a disabled job keeps Stop for its run in progress", func(t *testing.T) {
		s := newServer(t, Config{RunStopper: &mocks.RunStopperMock{}})
		addJobs(s, persistence.JobInfo{ID: "offrun", Name: "Paused", Command: "sleep 100", Schedule: "@daily", IsRunning: true})
		body := facts(s, "offrun", "").Body.String()
		assert.Contains(t, body, `id="stop-run"`)
		assert.NotContains(t, body, "Enable the schedule to run this job")
	})

	t.Run("stop follows the selected run in progress", func(t *testing.T) {
		s := newServer(t, Config{RunStopper: &mocks.RunStopperMock{}})
		older, newer := time.Now().Add(-time.Hour), time.Now().Add(-time.Minute)
		for _, at := range []time.Time{older, newer} {
			s.handleJobEvent(JobEvent{Command: "live", Schedule: "* * * * *", EventType: enums.EventTypeStarted, StartedAt: at})
		}
		id := HashCommand("live")
		micro := func(at time.Time) string { return strconv.FormatInt(at.UnixMicro(), 10) }
		assert.Contains(t, facts(s, id, "").Body.String(), "stop-form?start="+micro(newer)+`"`)
		assert.Contains(t, facts(s, id, "selected-run=live:"+micro(older)).Body.String(), "stop-form?start="+micro(older)+`"`)
	})

	t.Run("removed job stops the poll", func(t *testing.T) {
		w := facts(newServer(t, Config{}), "gone", "")
		assert.Equal(t, 286, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "This job was removed from the crontab.")
		assert.Contains(t, body, "Removed")
		assert.NotContains(t, body, `id="run-now"`)
	})
}

func TestServer_handleJobRuns(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	start := time.Now().Add(-time.Hour)
	first := recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: start, Status: enums.JobStatusFailed, ExitCode: 3})
	second := recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: start.Add(time.Minute),
		Status: enums.JobStatusSuccess, ExecutedCommand: "x"})

	w := getJob("fail", "selected-run="+strconv.Itoa(first), server.handleJobRuns)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `<input type="hidden" name="newest" value="`+strconv.Itoa(second)+`">`)
	assert.Contains(t, body, `class="jr sel" data-run="`+strconv.Itoa(first)+`"`)
	assert.Contains(t, body, `class="jr" data-run="`+strconv.Itoa(second)+`"`)
	assert.Contains(t, body, `<span class="bad">exit 3</span>`)
	assert.Contains(t, body, "<small>manual</small>")
	assert.Contains(t, body, "<small>scheduled</small>")
	assert.Contains(t, body, `hx-get="/api/jobs/fail/executions/`+strconv.Itoa(first)+`/output"`)

	assert.Contains(t, getJob("never", "", server.handleJobRuns).Body.String(), "No runs yet.")
	assert.Equal(t, http.StatusNotFound, getJob("nope", "", server.handleJobRuns).Code)
}

func TestServer_handleJump(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	jobs := sampleJobs()
	for i := range 12 {
		jobs = append(jobs, persistence.JobInfo{ID: fmt.Sprintf("bulk%d", i), Name: fmt.Sprintf("Bulk job %d", i),
			Command: "echo bulk", Schedule: "@daily", Enabled: true})
	}
	addJobs(server, jobs...)
	jump := func(query string) string {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/jump?"+query, http.NoBody)
		w := httptest.NewRecorder()
		server.handleJump(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	t.Run("every word must match name or command", func(t *testing.T) {
		body := jump("q=" + url.QueryEscape("FEED vendor"))
		assert.Equal(t, 1, strings.Count(body, `class="ji"`))
		assert.Contains(t, body, "Vendor feed import")
		body = jump("q=" + url.QueryEscape("vendor rsync"))
		assert.Contains(t, body, "No job matches")
		body = jump("q=reindex")
		assert.Contains(t, body, `data-job-id="never"`)
	})

	t.Run("first ten in crontab order", func(t *testing.T) {
		body := jump("q=")
		assert.Equal(t, 10, strings.Count(body, `class="ji"`))
		assert.Less(t, strings.Index(body, `data-job-id="ok"`), strings.Index(body, `data-job-id="fail"`))
	})

	t.Run("only a job that can start carries its run confirmation", func(t *testing.T) {
		body := jump("q=")
		assert.Contains(t, body, `data-job-id="ok"
   data-confirm="/api/jobs/ok/run-confirm"`)
		for _, id := range []string{"off", "run"} {
			assert.Contains(t, body, `data-job-id="`+id+`"
   >`, id)
		}
	})

	t.Run("links carry the list state", func(t *testing.T) {
		body := jump("q=sync&filter=failed&sort=lastrun&rev=1&search=a")
		assert.Contains(t, body, `href="/jobs/ok?filter=failed&amp;rev=1&amp;search=a&amp;sort=lastrun"`)
	})
}

func TestServer_handleRunOutput(t *testing.T) {
	server := newHandlersTestServer(t, Config{})
	addJobs(server, sampleJobs()...)
	now := time.Now()
	run := recordRun(t, server, request.RecordExecution{JobID: "fail", StartedAt: now, FinishedAt: now.Add(1900 * time.Millisecond),
		Status: enums.JobStatusSuccess, ExecutedCommand: "sh -c 'exit 0'", Output: "manual ok"})
	other := recordRun(t, server, request.RecordExecution{JobID: "ok", StartedAt: now, FinishedAt: now, Status: enums.JobStatusSuccess})

	get := func(jobID, execID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.SetPathValue("id", jobID)
		req.SetPathValue("exec_id", execID)
		w := httptest.NewRecorder()
		server.handleRunOutput(w, req)
		return w
	}

	w := get("fail", strconv.Itoa(run))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "run-selected", w.Header().Get("HX-Trigger-After-Swap"))
	assert.Equal(t, "#joutput", w.Header().Get("HX-Retarget"))
	body := w.Body.String()
	assert.Contains(t, body, `<b>Executed:</b> <span class="mono">sh -c &#39;exit 0&#39;</span>`)
	assert.Contains(t, body, "<div>manual ok</div>")
	assert.Contains(t, body, "1.9s")
	assert.NotContains(t, body, "exit 0 ·", "a success shows no exit code")
	assert.Contains(t, body, `<input type="hidden" name="selected-run" value="`+strconv.Itoa(run)+`">`)
	assert.NotContains(t, body, "hx-swap-oob", "the selection travels inside the output, not out of band")

	assert.Equal(t, http.StatusNotFound, get("fail", strconv.Itoa(other)).Code, "run of another job")
	assert.Equal(t, http.StatusNotFound, get("fail", "99999").Code)
	assert.Equal(t, http.StatusNotFound, get("nope", strconv.Itoa(run)).Code)
	assert.Equal(t, http.StatusBadRequest, get("fail", "abc").Code)

	require.NoError(t, server.store.Close())
	assert.Equal(t, http.StatusInternalServerError, get("fail", strconv.Itoa(run)).Code,
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
		return getJob(jobID, query, server.handleLiveOutput)
	}
	micro := func(at time.Time) string { return strconv.FormatInt(at.UnixMicro(), 10) }

	t.Run("a selected run in progress fills the pane and polls itself", func(t *testing.T) {
		w := get(id, "start="+micro(startA))
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "run-selected", w.Header().Get("HX-Trigger-After-Swap"))
		assert.Equal(t, "#joutput", w.Header().Get("HX-Retarget"))
		body := w.Body.String()
		assert.Contains(t, body, `<div>a one</div><div class=" cursor">a two</div>`)
		assert.Contains(t, body, `id="live-out"`)
		assert.Contains(t, body, `hx-trigger="every 5s [!uiHeld], refresh-jobs from:body"`)
		assert.Contains(t, body, `hx-sync="#joutput:abort"`)
		assert.Contains(t, body, `<input type="hidden" name="selected-run" value="live:`+micro(startA)+`">`)
		assert.NotContains(t, body, "hx-swap-oob")
	})

	t.Run("a poll gets only the new body", func(t *testing.T) {
		w := get(id, "start="+micro(startA)+"&poll=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, w.Header().Get("HX-Trigger-After-Swap"))
		assert.Empty(t, w.Header().Get("HX-Retarget"))
		assert.NotContains(t, w.Body.String(), `id="live-out"`)
		assert.Contains(t, w.Body.String(), "a two")
	})

	t.Run("finished run turns into its recorded output", func(t *testing.T) {
		server.handleJobEvent(JobEvent{Command: "live cmd", Schedule: "* * * * *", EventType: enums.EventTypeCompleted,
			StartedAt: startA, FinishedAt: startA.Add(3 * time.Second), Output: "a one\na two\na three"})
		w := get(id, "start="+micro(startA)+"&poll=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "run-selected", w.Header().Get("HX-Trigger-After-Swap"))
		assert.Equal(t, "#joutput", w.Header().Get("HX-Retarget"), "the recorded run replaces the polling wrapper")
		assert.Equal(t, "innerHTML", w.Header().Get("HX-Reswap"))
		body := w.Body.String()
		assert.Contains(t, body, "<div>a three</div>")
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
		assert.Contains(t, body, "every 5s", "it still polls to turn into the recorded run")
	})

	t.Run("unknown run", func(t *testing.T) {
		w := get(id, "start=12345")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "no longer available")
		assert.NotContains(t, w.Body.String(), "every 5s")
	})

	t.Run("bad requests", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, get(id, "start=abc").Code)
		assert.Equal(t, http.StatusBadRequest, get(id, "after=abc").Code)
		assert.Equal(t, http.StatusNotFound, get("nope", "start="+micro(startB)).Code)
		assert.Equal(t, http.StatusNotFound, get("nope", "after="+micro(startB)).Code)
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

			w := getJob(id, "start="+strconv.FormatInt(old.UnixMicro(), 10)+"&poll=1", server.handleLiveOutput)
			require.Equal(t, http.StatusOK, w.Code)
			if tt.wantGone {
				assert.Contains(t, w.Body.String(), "no longer available", "the default retention prunes the old run")
				return
			}
			assert.Contains(t, w.Body.String(), "old run done", "a retained run outside the newest 50 is still found")
		})
	}
}

func TestServer_handleLiveOutputAfter(t *testing.T) {
	const cmd = "work cmd"
	id := HashCommand(cmd)
	newServer := func(t *testing.T) *Server {
		s := newHandlersTestServer(t, Config{ExecMaxLogLines: 100})
		addJobs(s, persistence.JobInfo{ID: id, Name: "Work", Command: cmd, Schedule: "@daily", Enabled: true})
		return s
	}
	started := func(s *Server, at time.Time, executed string) {
		s.handleJobEvent(JobEvent{Command: cmd, ExecutedCommand: executed, Schedule: "@daily", EventType: enums.EventTypeStarted,
			StartedAt: at, LiveOutput: func() string { return "output of " + executed }})
	}
	finished := func(s *Server, at time.Time, executed string) {
		s.handleJobEvent(JobEvent{Command: cmd, ExecutedCommand: executed, Schedule: "@daily", EventType: enums.EventTypeCompleted,
			StartedAt: at, FinishedAt: at.Add(time.Second), Output: "done " + executed})
	}
	wait := func(s *Server, after time.Time, extra string) *httptest.ResponseRecorder {
		return getJob(id, "after="+strconv.FormatInt(after.UnixMicro(), 10)+extra, s.handleLiveOutput)
	}

	t.Run("starting", func(t *testing.T) {
		w := wait(newServer(t), time.Now(), "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "Starting…")
		assert.Empty(t, w.Header().Get("HX-Retarget"))
		assert.Empty(t, w.Header().Get("HX-Replace-Url"))
	})

	t.Run("did not start within the limit", func(t *testing.T) {
		w := wait(newServer(t), time.Now().Add(-11*time.Second), "")
		assert.Equal(t, 286, w.Code)
		assert.Contains(t, w.Body.String(), "did not start within 10 seconds")
		assert.Contains(t, w.Body.String(), "cronn log")
	})

	t.Run("found in progress", func(t *testing.T) {
		s := newServer(t)
		after := time.Now().Add(-time.Second)
		started(s, after.Add(time.Millisecond), "work cmd --edited")
		w := wait(s, after, "&filter=failed&sort=lastrun&rev=1")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "#joutput", w.Header().Get("HX-Retarget"))
		assert.Equal(t, "run-selected", w.Header().Get("HX-Trigger-After-Swap"))
		assert.Equal(t, "/jobs/"+id+"?filter=failed&rev=1&sort=lastrun", w.Header().Get("HX-Replace-Url"))
		assert.Contains(t, w.Body.String(), `id="live-out"`)
		assert.Contains(t, w.Body.String(), "output of work cmd --edited")
	})

	t.Run("finished before the first poll", func(t *testing.T) {
		s := newServer(t)
		after := time.Now().Add(-time.Second)
		started(s, after.Add(time.Millisecond), cmd)
		finished(s, after.Add(time.Millisecond), cmd)
		w := wait(s, after, "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "<div>done work cmd</div>")
		assert.NotContains(t, w.Body.String(), `id="live-out"`)
		assert.NotEmpty(t, w.Header().Get("HX-Replace-Url"))
	})

	t.Run("an overlapping scheduled run is not the started one", func(t *testing.T) {
		s := newServer(t)
		after := time.Now().Add(-time.Second)
		started(s, after.Add(time.Millisecond), "")
		w := wait(s, after, "")
		assert.Contains(t, w.Body.String(), "Starting…")
		assert.Empty(t, w.Header().Get("HX-Retarget"))
	})

	t.Run("the manual run finished while a scheduled one runs", func(t *testing.T) {
		s := newServer(t)
		after := time.Now().Add(-time.Second)
		started(s, after.Add(time.Millisecond), cmd)
		finished(s, after.Add(time.Millisecond), cmd)
		started(s, after.Add(2*time.Millisecond), "")
		w := wait(s, after, "")
		assert.Contains(t, w.Body.String(), "<div>done work cmd</div>")
	})

	t.Run("the earliest manual run after the trigger wins", func(t *testing.T) {
		s := newServer(t)
		after := time.Now().Add(-time.Second)
		started(s, after.Add(-time.Millisecond), "earlier")
		started(s, after.Add(5*time.Millisecond), "second")
		started(s, after.Add(time.Millisecond), "first")
		w := wait(s, after, "")
		assert.Contains(t, w.Body.String(), "output of first")
	})
}

func TestServer_logLines(t *testing.T) {
	s := &Server{}
	assert.Nil(t, s.logLines(""))
	assert.Equal(t, []logLine{{Text: "one"}, {Text: ""}, {Text: "Error: two", Err: true}, {Text: "FATAL x", Err: true}},
		s.logLines("one\n\nError: two\nFATAL x\n"))
}
