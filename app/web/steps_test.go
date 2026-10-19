package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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

const stepsJobCommand = "extract.sh {{.YYYYMMDD}} && transform.sh && load.sh"

// newStepsServer makes a server whose crontab has one job with three steps, loaded as on startup
func newStepsServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	crontabFile := filepath.Join(tmpDir, "crontab.yml")
	require.NoError(t, os.WriteFile(crontabFile, []byte(`jobs:
  - spec: "0 3 * * *"
    name: "Pipeline"
    steps:
      - name: "Extract"
        command: "extract.sh {{.YYYYMMDD}}"
      - command: "transform.sh"
      - command: "load.sh"
`), 0o600))
	cfg.DBPath = filepath.Join(tmpDir, "test.db")
	cfg.UpdateInterval = time.Minute
	cfg.Version = "test"
	cfg.JobsProvider = crontab.New(crontabFile, 0, nil)
	server, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.store.Close() })
	require.NoError(t, server.loadJobsFromCrontab())
	return server
}

func stepStates(statuses ...enums.StepStatus) []request.StepState {
	cmds := []string{"extract.sh 20261016", "transform.sh", "load.sh"}
	start := time.Date(2026, 10, 16, 3, 0, 0, 0, time.Local)
	res := make([]request.StepState, len(statuses))
	for i, st := range statuses {
		res[i] = request.StepState{Index: i + 1, Command: cmds[i], Status: st}
		if st != enums.StepStatusPending && st != enums.StepStatusSkipped {
			res[i].StartedAt = start.Add(time.Duration(i) * time.Minute)
			res[i].Attempts = 1
			res[i].Output = "out " + strconv.Itoa(i+1)
		}
		if st == enums.StepStatusSuccess || st == enums.StepStatusFailed {
			res[i].FinishedAt = res[i].StartedAt.Add(48 * time.Second)
		}
		if st == enums.StepStatusFailed {
			res[i].ExitCode = 3
			res[i].Attempts = 3
		}
	}
	if len(res) > 0 {
		res[0].Name = "Extract"
	}
	return res
}

func TestStepHelpers(t *testing.T) {
	t.Run("job steps from the crontab", func(t *testing.T) {
		assert.Nil(t, toJobSteps(nil))
		assert.Equal(t, []persistence.JobStep{{Name: "a", Command: "x"}, {Command: "y"}},
			toJobSteps([]crontab.StepSpec{{Name: "a", Command: "x"}, {Command: "y"}}))
	})

	t.Run("last and current step", func(t *testing.T) {
		failed := stepStates(enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped)
		assert.Equal(t, 2, lastStep(failed))
		running := stepStates(enums.StepStatusSuccess, enums.StepStatusRunning, enums.StepStatusPending)
		assert.Equal(t, 2, currentStep(running))
		between := stepStates(enums.StepStatusSuccess, enums.StepStatusPending, enums.StepStatusPending)
		assert.Equal(t, 1, currentStep(between))
		assert.Zero(t, lastStep(nil))
		assert.Zero(t, currentStep(nil))
	})

	t.Run("views open failed and running steps", func(t *testing.T) {
		states := stepStates(enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped)
		views := stateViews(states, time.Now())
		require.Len(t, views, 3)
		assert.False(t, views[0].Open)
		assert.Equal(t, 48*time.Second, views[0].Duration)
		assert.True(t, views[1].Open)
		assert.Equal(t, "failed", views[1].Status)
		assert.Zero(t, views[2].Duration)

		running := stepStates(enums.StepStatusRunning)
		now := running[0].StartedAt.Add(12 * time.Second)
		v := stateViews(running, now)[0]
		assert.True(t, v.Open)
		assert.Equal(t, 12*time.Second, v.Duration)

		info := infoViews([]persistence.StepInfo{{Index: 2, Command: "c", Status: enums.StepStatusSuccess,
			StartedAt: now, FinishedAt: now.Add(time.Second)}})
		assert.Equal(t, stepView{Index: 2, Command: "c", Status: "success", Duration: time.Second}, info[0])
	})

	t.Run("step labels", func(t *testing.T) {
		steps := []persistence.JobStep{{Name: "Extract", Command: "x"}, {Command: "y"}}
		assert.Equal(t, "Extract", stepAt(steps, 1))
		assert.Equal(t, "y", stepAt(steps, 2))
		assert.Empty(t, stepAt(steps, 3))
		assert.Empty(t, stepAt(steps, 0))
	})

	t.Run("run progress of the runs list", func(t *testing.T) {
		run := persistence.ExecutionInfo{StepFirst: 2, StepLast: 3, Status: enums.JobStatusFailed}
		assert.Equal(t, runStepsView{First: 2, Last: 3, Total: 3, Failed: true}, runStepsOf(run, 3))
	})
}

func TestServer_stepsJobLoadedFromCrontab(t *testing.T) {
	s := newStepsServer(t, Config{})
	job, ok := s.jobByID(HashCommand(stepsJobCommand))
	require.True(t, ok)
	assert.Equal(t, "Pipeline", job.Name)
	assert.Equal(t, []persistence.JobStep{{Name: "Extract", Command: "extract.sh {{.YYYYMMDD}}"},
		{Command: "transform.sh"}, {Command: "load.sh"}}, job.Steps)
}

func TestServer_stepsRunInProgress(t *testing.T) {
	s := newStepsServer(t, Config{ExecMaxLogLines: 100})
	id := HashCommand(stepsJobCommand)
	start := time.Now().Add(-time.Minute).Round(time.Microsecond)
	states := stepStates(enums.StepStatusSuccess, enums.StepStatusRunning, enums.StepStatusPending)
	s.handleJobEvent(JobEvent{Command: stepsJobCommand, Schedule: "0 3 * * *", EventType: enums.EventTypeStarted,
		StartedAt: start, LiveSteps: func() []request.StepState { return states }})

	t.Run("table shows the running step", func(t *testing.T) {
		stats := s.getJobsWithStats(jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll})
		require.Len(t, stats.jobs, 1)
		assert.Equal(t, 2, stats.jobs[0].RunningStep)

		req := httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody)
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, req)
		body := w.Body.String()
		assert.Contains(t, body, `<span class="tag steps">3 steps</span>`)
		assert.Contains(t, body, "Extract → transform.sh → load.sh")
		assert.Contains(t, body, `<span class="meta step-at">step 2/3 transform.sh</span>`)
		assert.Contains(t, body, "· 2/3</span>")
	})

	t.Run("inspector lists the run at its step", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector", http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleInspector(w, req)
		body := w.Body.String()
		assert.Contains(t, body, `<ol class="cmdbox steps-def">`)
		assert.Contains(t, body, "<li><b>Extract</b> <span>extract.sh {{.YYYYMMDD}}</span></li>")
		assert.NotContains(t, body, "<div class=\"cmdlbl\">Job command</div>")
		assert.Contains(t, body, `<div class="runs with-steps">`)
		assert.Contains(t, body, `<span class="ex">running · 2/3</span>`)
		assert.Contains(t, body, `id="live-out"`)
		assert.Contains(t, body, `<details class="step running" data-step="2" open>`)
		assert.Contains(t, body, `<details class="step success" data-step="1">`)
		assert.Contains(t, body, `<div class="step pending" data-step="3">`)
		assert.Contains(t, body, "<pre>out 2</pre>")
		assert.NotContains(t, body, "Output capture is disabled.")
	})

	t.Run("finished run records its steps", func(t *testing.T) {
		final := stepStates(enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped)
		s.handleJobEvent(JobEvent{Command: stepsJobCommand, Schedule: "0 3 * * *", EventType: enums.EventTypeFailed,
			StartedAt: start, FinishedAt: start.Add(2 * time.Minute), ExitCode: 3, Steps: final})

		job, ok := s.jobByID(id)
		require.True(t, ok)
		assert.Equal(t, 2, job.LastStep)
		assert.False(t, job.IsRunning)

		runs, err := s.store.GetExecutions(id, 10)
		require.NoError(t, err)
		require.Len(t, runs, 1)
		assert.Equal(t, 1, runs[0].StepFirst)
		assert.Equal(t, 2, runs[0].StepLast)
		steps, err := s.store.GetExecutionSteps(runs[0].ID)
		require.NoError(t, err)
		require.Len(t, steps, 3)
		assert.Equal(t, "out 2", steps[1].Output)

		req := httptest.NewRequest(http.MethodGet, "/api/jobs", http.NoBody)
		w := httptest.NewRecorder()
		s.handleJobsPartial(w, req)
		assert.Contains(t, w.Body.String(), "step 2/3 · exit 3")
	})
}

func TestServer_stepsRecordedRunOutput(t *testing.T) {
	s := newStepsServer(t, Config{ExecMaxLogLines: 100})
	id := HashCommand(stepsJobCommand)
	start := time.Date(2026, 10, 16, 3, 0, 0, 0, time.Local)
	require.NoError(t, s.store.RecordExecution(request.RecordExecution{JobID: id, StartedAt: start,
		FinishedAt: start.Add(2 * time.Minute), Status: enums.JobStatusFailed, ExitCode: 3,
		Steps: stepStates(enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped)}))
	later := start.Add(time.Hour)
	ok := stepStates(enums.StepStatusSuccess, enums.StepStatusSuccess)
	ok[0].Index, ok[1].Index = 2, 3
	require.NoError(t, s.store.RecordExecution(request.RecordExecution{JobID: id, StartedAt: later,
		FinishedAt: later.Add(time.Minute), Status: enums.JobStatusSuccess, ExecutedCommand: "transform.sh && load.sh",
		Steps: ok}))
	runs, err := s.store.GetExecutions(id, 10)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	failedID := runs[1].ID

	output := func(srv *Server, execID int) string {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.SetPathValue("id", id)
		req.SetPathValue("exec_id", strconv.Itoa(execID))
		w := httptest.NewRecorder()
		srv.handleRunOutput(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	t.Run("failed run shows its steps and the re-run button", func(t *testing.T) {
		body := output(s, failedID)
		assert.Contains(t, body, `<details class="step success" data-step="1">`)
		assert.Contains(t, body, `<details class="step failed" data-step="2" open>`)
		assert.Contains(t, body, `<div class="step skipped" data-step="3">`)
		assert.Contains(t, body, "exit 3 · 48.0s · 3 tries")
		assert.Contains(t, body, "<b>Extract</b>")
		assert.NotContains(t, body, "<b>Job command:</b>")
		assert.Contains(t, body, `/run-form?from=2&run=`+strconv.Itoa(failedID)+`"`)
		assert.Contains(t, body, "▶ Re-run from step 2…")
	})

	t.Run("no re-run for a successful run", func(t *testing.T) {
		assert.NotContains(t, output(s, runs[0].ID), "Re-run from step")
	})

	t.Run("no re-run when manual runs are disabled", func(t *testing.T) {
		s.disableManual = true
		t.Cleanup(func() { s.disableManual = false })
		assert.NotContains(t, output(s, failedID), "Re-run from step")
	})

	t.Run("runs list shows how far each run got", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/inspector", http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleInspector(w, req)
		body := w.Body.String()
		assert.Contains(t, body, `<span class="ex bad">step 2 · exit 3</span>`)
		assert.Contains(t, body, `<span class="ex">from 2 · 3/3 · exit 0</span>`)
	})
}

func TestServer_stepsRunForm(t *testing.T) {
	get := func(s *Server, query string) string {
		id := HashCommand(stepsJobCommand)
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+id+"/run-form?"+query, http.NoBody)
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.handleRunForm(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	t.Run("whole chain by default", func(t *testing.T) {
		body := get(newStepsServer(t, Config{}), "")
		assert.Contains(t, body, `<select id="rf-from" name="from"`)
		assert.Contains(t, body, `<option value="1" selected>1 · Extract</option>`)
		assert.Contains(t, body, `<option value="2">2 · transform.sh</option>`)
		assert.Contains(t, body, `name="step-1"`)
		assert.Contains(t, body, ">extract.sh {{.YYYYMMDD}}</textarea>")
		assert.Contains(t, body, `name="step-3"`)
		assert.Contains(t, body, `name="date"`, "a step with templates gets the date field")
		assert.NotContains(t, body, `name="command"`)
	})

	t.Run("later start keeps the dialog's edits", func(t *testing.T) {
		body := get(newStepsServer(t, Config{}), url.Values{"from": {"2"}, "step-1": {"ignored"}, "step-2": {"transform.sh --fast"},
			"date": {"20261016"}}.Encode())
		assert.Contains(t, body, `<option value="2" selected>`)
		assert.NotContains(t, body, `name="step-1"`)
		assert.Contains(t, body, ">transform.sh --fast</textarea>")
		assert.Contains(t, body, ">load.sh</textarea>")
		assert.Contains(t, body, `value="20261016"`, "the date is kept even without templates")
	})

	t.Run("re-run prefills the commands the run executed", func(t *testing.T) {
		s := newStepsServer(t, Config{})
		id := HashCommand(stepsJobCommand)
		start := time.Now()
		require.NoError(t, s.store.RecordExecution(request.RecordExecution{JobID: id, StartedAt: start,
			FinishedAt: start, Status: enums.JobStatusFailed, ExitCode: 3,
			Steps: stepStates(enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped)}))
		runs, err := s.store.GetExecutions(id, 1)
		require.NoError(t, err)

		body := get(s, "from=1&run="+strconv.Itoa(runs[0].ID))
		assert.Contains(t, body, ">extract.sh 20261016</textarea>")
		assert.Contains(t, body, ">load.sh</textarea>", "a skipped step keeps its own command")
		assert.NotContains(t, body, `name="date"`, "resolved commands need no date")

		s.disableCommandEdit = true
		body = get(s, "from=1&run="+strconv.Itoa(runs[0].ID))
		assert.Contains(t, body, ">extract.sh {{.YYYYMMDD}}</textarea>", "read-only steps keep the job's commands")
		assert.Contains(t, body, " readonly>")
	})

	t.Run("re-run of another job's run is ignored", func(t *testing.T) {
		s := newStepsServer(t, Config{})
		require.NoError(t, s.store.RecordExecution(request.RecordExecution{JobID: "other", StartedAt: time.Now(),
			Status: enums.JobStatusFailed, Steps: []request.StepState{{Index: 1, Command: "rm -rf /", Status: enums.StepStatusFailed}}}))
		runs, err := s.store.GetExecutions("other", 1)
		require.NoError(t, err)
		body := get(s, "run="+strconv.Itoa(runs[0].ID))
		assert.NotContains(t, body, "rm -rf")
		assert.Contains(t, body, ">extract.sh {{.YYYYMMDD}}</textarea>")
	})

	t.Run("out of range start falls back to the first step", func(t *testing.T) {
		body := get(newStepsServer(t, Config{}), "from=9")
		assert.Contains(t, body, `<option value="1" selected>`)
		assert.Contains(t, body, `name="step-1"`)
	})
}

func TestServer_stepsRunJob(t *testing.T) {
	post := func(s *Server, form url.Values, htmx bool) *httptest.ResponseRecorder {
		id := HashCommand(stepsJobCommand)
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
	newServer := func(t *testing.T, cfg Config) (*Server, chan service.ManualJobRequest) {
		ch := make(chan service.ManualJobRequest, 2)
		cfg.ManualTrigger = ch
		return newStepsServer(t, cfg), ch
	}

	t.Run("without fields the whole chain runs", func(t *testing.T) {
		s, ch := newServer(t, Config{})
		w := post(s, url.Values{}, false)
		require.Equal(t, http.StatusAccepted, w.Code)
		req := <-ch
		assert.Equal(t, 1, req.FromStep)
		assert.Equal(t, []string{"extract.sh {{.YYYYMMDD}}", "transform.sh", "load.sh"}, req.Steps)
	})

	t.Run("from a later step with edits", func(t *testing.T) {
		s, ch := newServer(t, Config{})
		w := post(s, url.Values{"from": {"2"}, "step-2": {"transform.sh --fast"}, "step-3": {"load.sh"},
			"date": {"20261016"}}, true)
		require.Equal(t, http.StatusAccepted, w.Code)
		req := <-ch
		assert.Equal(t, 2, req.FromStep)
		assert.Equal(t, []string{"transform.sh --fast", "load.sh"}, req.Steps)
		require.NotNil(t, req.CustomDate)
		assert.Equal(t, "20261016", req.CustomDate.Format("20060102"))
	})

	t.Run("invalid start step", func(t *testing.T) {
		s, _ := newServer(t, Config{})
		assert.Equal(t, http.StatusBadRequest, post(s, url.Values{"from": {"4"}}, false).Code)
		w := post(s, url.Values{"from": {"x"}, "step-2": {"kept edit"}}, true)
		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "Not started: Invalid start step.")
		assert.Contains(t, body, ">kept edit</textarea>")
	})

	t.Run("empty step", func(t *testing.T) {
		s, _ := newServer(t, Config{})
		w := post(s, url.Values{"from": {"2"}, "step-2": {" "}}, false)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Step 2 has no command")
	})

	t.Run("edits rejected when command editing is disabled", func(t *testing.T) {
		s, ch := newServer(t, Config{DisableCommandEdit: true})
		assert.Equal(t, http.StatusForbidden, post(s, url.Values{"step-3": {"load.sh --all"}}, false).Code)
		require.Equal(t, http.StatusAccepted, post(s, url.Values{"from": {"3"}, "step-3": {"load.sh"}}, false).Code)
		assert.Equal(t, []string{"load.sh"}, (<-ch).Steps)
	})
}

func TestServer_stepsOutputNotStoredWhenDisabled(t *testing.T) {
	s := newStepsServer(t, Config{ExecMaxLogLines: 0})
	id := HashCommand(stepsJobCommand)
	start := time.Now()
	final := stepStates(enums.StepStatusSuccess, enums.StepStatusSuccess, enums.StepStatusSuccess)
	s.handleJobEvent(JobEvent{Command: stepsJobCommand, Schedule: "0 3 * * *", EventType: enums.EventTypeCompleted,
		StartedAt: start, FinishedAt: start.Add(time.Minute), Steps: final})

	runs, err := s.store.GetExecutions(id, 1)
	require.NoError(t, err)
	steps, err := s.store.GetExecutionSteps(runs[0].ID)
	require.NoError(t, err)
	require.Len(t, steps, 3)
	assert.Empty(t, steps[0].Output)
	assert.Equal(t, "out 1", final[0].Output, "the event's own states are left as they are")
}
