package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

const (
	jobStripSize   = 30               // runs in the job page's history strip
	jobRunsLimit   = 50               // runs in the job page's runs list
	jumpLimit      = 10               // jobs in the jump list
	maxCommandRows = 6                // rows a multi-line command field grows to before it scrolls
	startTimeout   = 10 * time.Second // how long a started manual run may take to show up
)

// errLine marks log lines shown as errors
var errLine = regexp.MustCompile(`(?i)error|fatal`)

// stripBar is one bar of a run history strip, newest on the right
type stripBar struct {
	Key    string // a recorded run's execution id, or live:<start> for a run in progress; empty for padding
	Class  string // ok, fail, run, or e for padding
	Height int    // percent of the strip height
	Tip    string
	Href   string // job page with this run selected
	Out    string // the run's output fragment, for bars on the job page
	Sel    bool
}

// stripReq is what stripBars draws: the job, its recorded runs newest first, the bar count and the
// selected run's key
type stripReq struct {
	Job      persistence.JobInfo
	Runs     []persistence.RunSummary
	Size     int
	Selected string
}

// jobPosition is a job's place in the list the user came from, with links to the jobs before and after it
type jobPosition struct {
	Pos   int
	Total int
	Prev  string
	Next  string
}

// logLine is one line of a run's output
type logLine struct {
	Text   string
	Err    bool
	Cursor bool // last line of a run in progress
}

// liveRunView is a run in progress as the runs list shows it
type liveRunView struct {
	Start           int64 // start time in unix microseconds, the run's identity while it is in progress
	Token           string
	StartedAt       time.Time
	ExecutedCommand string
}

// runOutputData is one recorded run's output with its command
type runOutputData struct {
	JobID        string
	Run          persistence.ExecutionInfo
	Command      string
	CommandLabel string
	Lines        []logLine
}

// liveOutputData is the output of a run in progress so far
type liveOutputData struct {
	liveRunView
	JobID        string
	Command      string
	CommandLabel string
	Capture      bool // false when output capture is disabled
	Lines        []logLine
}

// jobOutput is what the job page's output pane shows: a recorded run, a run in progress, the wait for a
// run just started (After), or nothing
type jobOutput struct {
	JobID   string
	Run     *runOutputData
	Live    *liveOutputData
	After   int64
	NoStart bool // the started run did not show up in time
	Gone    bool // the selected run is no longer retained
}

// jobPageData is the template data of the job page and its fragments
type jobPageData struct {
	TemplateData
	jobPosition
	Job          persistence.JobInfo
	Found        bool
	Strip        []stripBar
	Active       []liveRunView // runs in progress not yet recorded, newest first
	Runs         []persistence.ExecutionInfo
	Newest       string // key of the newest run, so a poll can tell when the runs list is stale
	Selected     string
	StopStart    int64 // the run the Stop button stops: the selected run in progress, else the newest
	BackLink     string
	HasTemplates bool
	CommandRows  int // rows of the command field for a multi-line command, 0 for a single line
	Output       jobOutput
}

// handleJobPage renders a job's page. run selects a recorded run of this job; after waits for the manual
// run started after that time (unix micro), as a run accepted from the dashboard lands here
func (s *Server) handleJobPage(w http.ResponseWriter, r *http.Request) {
	q := s.listQuery(r)
	stats := s.getJobsWithStats(q)
	data := jobPageData{TemplateData: s.listData(r, q, stats)}
	data.OnJob = true
	data.BackLink = data.IndexLink
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		s.renderJobNotFound(w, data)
		return
	}
	if job.Enabled {
		s.updateNextRun(&job)
	}
	data.Job, data.Found = job, true
	data.PageTitle = job.Name
	if data.PageTitle == "" {
		data.PageTitle = job.Command
	}
	data.HasTemplates = strings.Contains(job.Command, "{{") || strings.Contains(job.Command, "[[")
	// a text input drops every newline from its value, a trailing one included, so any command holding
	// one needs a textarea to run as written
	if strings.Contains(job.Command, "\n") {
		data.CommandRows = min(strings.Count(strings.TrimRight(job.Command, "\n"), "\n")+1, maxCommandRows)
	}
	data.jobPosition = s.jobPosition(job.ID, q)

	active := s.activeRuns(job.ID)
	runs, err := s.store.GetExecutions(job.ID, jobRunsLimit)
	if err != nil {
		log.Printf("[ERROR] failed to get executions for job %s: %v", job.ID, err)
		http.Error(w, "Failed to load execution history", http.StatusInternalServerError)
		return
	}
	pending := s.pendingRuns(active, runs)
	data.Runs = runs
	for _, run := range pending {
		data.Active = append(data.Active, run.view())
	}
	data.Output = s.pageOutput(r, &data, pending)
	data.Selected = data.Output.selected()
	s.fillFacts(&data)
	s.render(w, "job", "layout", data)
}

// renderJobNotFound answers an unknown job id with the page shell, a notice and 404
func (s *Server) renderJobNotFound(w http.ResponseWriter, data jobPageData) {
	var buf bytes.Buffer
	if err := s.templates["job"].ExecuteTemplate(&buf, "layout", data); err != nil {
		log.Printf("[WARN] failed to execute template: %v", err)
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write response: %v", err)
	}
}

// pageOutput picks what the output pane opens with: the wait for a started run, the run named by run= when
// it belongs to the job, the newest run in progress, or the newest recorded run of data.Runs. pending is the
// snapshot read before the history, so a run that finished since still shows its output. A run= of
// live:<start> comes from a bar drawn while the run was in progress and finds it recorded once it ended
func (s *Server) pageOutput(r *http.Request, data *jobPageData, pending []activeRun) jobOutput {
	job := data.Job
	out := jobOutput{JobID: job.ID}
	if after, err := strconv.ParseInt(r.FormValue("after"), 10, 64); err == nil && after > 0 {
		out.After = after
		return out
	}
	if token, ok := strings.CutPrefix(r.FormValue("run"), "live:"); ok {
		if linked, found := s.liveLinkOutput(job, pending, token); found {
			return linked
		}
	}
	if id, err := strconv.Atoi(r.FormValue("run")); err == nil {
		run, err := s.store.GetExecutionByID(id)
		if err == nil && run.JobID == job.ID {
			o := s.newRunOutput(job, run)
			out.Run = &o
			return out
		}
	}
	if len(pending) > 0 {
		live := s.newLiveOutput(job, pending[0])
		out.Live = &live
		return out
	}
	if len(data.Runs) > 0 {
		o := s.newRunOutput(job, data.Runs[0])
		out.Run = &o
	}
	return out
}

// liveLinkOutput finds the run a live:<start> link names among the runs in progress, or recorded since
func (s *Server) liveLinkOutput(job persistence.JobInfo, pending []activeRun, token string) (jobOutput, bool) {
	out := jobOutput{JobID: job.ID}
	start, err := strconv.ParseInt(token, 10, 64)
	if err != nil {
		return out, false
	}
	for _, run := range pending {
		if run.StartedAt.UnixMicro() == start {
			live := s.newLiveOutput(job, run)
			out.Live = &live
			return out, true
		}
	}
	run, err := s.store.GetExecutionByStart(job.ID, start)
	if err != nil {
		return out, false
	}
	o := s.newRunOutput(job, run)
	out.Run = &o
	return out, true
}

// selected is the key of the run the output pane shows
func (o jobOutput) selected() string {
	switch {
	case o.Run != nil:
		return strconv.Itoa(o.Run.Run.ID)
	case o.Live != nil:
		return o.Live.Token
	}
	return ""
}

// fillFacts adds the facts strip, the newest-run key and the run the Stop button targets
func (s *Server) fillFacts(data *jobPageData) {
	summaries, err := s.store.JobRuns(data.Job.ID, jobStripSize)
	if err != nil {
		log.Printf("[WARN] failed to load runs of job %s: %v", data.Job.ID, err)
	}
	q := jobsQuery{sort: data.SortMode, filter: data.FilterMode, search: data.Search, reverse: data.Reverse}
	data.Strip = s.stripBars(stripReq{Job: data.Job, Runs: summaries, Size: jobStripSize, Selected: data.Selected}, q)
	if last := data.Strip[len(data.Strip)-1]; last.Key != "" {
		data.Newest = last.Key
	}
	active := s.activeRuns(data.Job.ID)
	for _, run := range active {
		if run.view().Token == data.Selected {
			data.StopStart = run.StartedAt.UnixMicro()
			return
		}
	}
	if len(active) > 0 {
		data.StopStart = active[0].StartedAt.UnixMicro()
	}
}

// handleJobFacts renders the polled part of the job page header, with the title, the run action and the
// pill counts out of band. runs-changed tells the runs list to reload when the newest run the page knows of
// is not the newest one. A job gone from the crontab gets a notice and 286, which stops the poll
func (s *Server) handleJobFacts(w http.ResponseWriter, r *http.Request) {
	q := s.listQuery(r)
	data := jobPageData{TemplateData: s.listData(r, q, s.getJobsWithStats(q))}
	data.OnJob = true
	job, ok := s.jobByID(r.PathValue("id"))
	status := http.StatusOK
	if ok {
		if job.Enabled {
			s.updateNextRun(&job)
		}
		data.Job, data.Found = job, true
		data.Selected = r.FormValue("selected-run")
		s.fillFacts(&data)
		if data.Newest != r.FormValue("newest") {
			w.Header().Set("HX-Trigger", "runs-changed")
		}
	} else {
		status = 286 // htmx stops polling on this code
	}

	var buf bytes.Buffer
	for _, name := range []string{"job-facts", "job-title-oob", "job-runact-oob", "pill-counts-oob"} {
		if err := s.templates["partials"].ExecuteTemplate(&buf, name, data); err != nil {
			log.Printf("[ERROR] failed to render %s: %v", name, err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("[WARN] failed to write facts: %v", err)
	}
}

// handleJobRuns renders the job page's runs list with the selected run marked
func (s *Server) handleJobRuns(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobByID(r.PathValue("id"))
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	// active runs are read before history: a run finishing in between is then in both, and its
	// recorded row wins, where the reverse order would leave it in neither
	active := s.activeRuns(job.ID)
	runs, err := s.store.GetExecutions(job.ID, jobRunsLimit)
	if err != nil {
		log.Printf("[ERROR] failed to get executions for job %s: %v", job.ID, err)
		http.Error(w, "Failed to load execution history", http.StatusInternalServerError)
		return
	}
	data := jobPageData{Job: job, Found: true, Runs: runs, Selected: r.FormValue("selected-run")}
	for _, run := range s.pendingRuns(active, runs) {
		data.Active = append(data.Active, run.view())
	}
	switch {
	case len(data.Active) > 0:
		data.Newest = data.Active[0].Token
	case len(runs) > 0:
		data.Newest = strconv.Itoa(runs[0].ID)
	}
	s.render(w, "partials", "job-runs", data)
}

// pendingRuns keeps the runs in progress that are not recorded yet, newest first
func (s *Server) pendingRuns(active []activeRun, runs []persistence.ExecutionInfo) []activeRun {
	return slices.DeleteFunc(slices.Clone(active), func(a activeRun) bool {
		return slices.ContainsFunc(runs, func(r persistence.ExecutionInfo) bool {
			return r.StartedAt.UnixMicro() == a.StartedAt.UnixMicro()
		})
	})
}

// stripBars draws a run history strip: padding on the left, then recorded runs oldest to newest, then the
// runs in progress. Heights scale with duration against the longest shown run, failed runs are full height
func (s *Server) stripBars(req stripReq, q jobsQuery) []stripBar {
	var live []activeRun
	if req.Job.IsRunning {
		for _, a := range s.activeRuns(req.Job.ID) {
			recorded := slices.ContainsFunc(req.Runs, func(r persistence.RunSummary) bool {
				return r.StartedAt.UnixMicro() == a.StartedAt.UnixMicro()
			})
			if !recorded {
				live = append(live, a)
			}
		}
	}
	if len(live) > req.Size {
		live = live[:req.Size]
	}
	runs := req.Runs
	if n := req.Size - len(live); len(runs) > n {
		runs = runs[:n]
	}

	var longest time.Duration
	for _, r := range runs {
		longest = max(longest, r.FinishedAt.Sub(r.StartedAt))
	}

	bars := make([]stripBar, 0, req.Size)
	for range req.Size - len(runs) - len(live) {
		bars = append(bars, stripBar{Class: "e", Height: 25})
	}
	for _, r := range slices.Backward(runs) {
		dur := r.FinishedAt.Sub(r.StartedAt)
		bar := stripBar{Key: strconv.Itoa(r.ID), Class: "ok", Height: 100}
		result := "ok"
		if r.Status == enums.JobStatusFailed {
			bar.Class, result = "fail", fmt.Sprintf("exit %d", r.ExitCode)
		}
		if bar.Class == "ok" && longest > 0 {
			bar.Height = 35 + int(65*dur/longest)
		}
		bar.Tip = r.StartedAt.Format("Jan 2 15:04:05") + " · " + result + " · " + s.runDuration(dur)
		if r.Manual {
			bar.Tip += " · manual"
		}
		bar.Out = s.url(fmt.Sprintf("/api/jobs/%s/executions/%d/output", req.Job.ID, r.ID))
		bars = append(bars, bar)
	}
	for _, a := range slices.Backward(live) {
		v := a.view()
		bar := stripBar{Key: v.Token, Class: "run", Height: 100,
			Tip: a.StartedAt.Format("Jan 2 15:04:05") + " · running " + s.humanDuration(time.Since(a.StartedAt)),
			Out: s.url(fmt.Sprintf("/api/jobs/%s/live-output?start=%d", req.Job.ID, v.Start))}
		if a.ExecutedCommand != "" {
			bar.Tip += " · manual"
		}
		bars = append(bars, bar)
	}
	for i := range bars {
		if bars[i].Key == "" {
			continue
		}
		bars[i].Sel = bars[i].Key == req.Selected
		v := q.values()
		v.Set("run", bars[i].Key)
		bars[i].Href = s.url("/jobs/"+req.Job.ID) + "?" + v.Encode()
	}
	return bars
}

// jobPosition finds the job in the list the user came from (search, filter and sort as on the index), or
// among all jobs in crontab order when that list does not hold it
func (s *Server) jobPosition(id string, q jobsQuery) jobPosition {
	list := s.getJobsWithStats(q).jobs
	idx := slices.IndexFunc(list, func(j persistence.JobInfo) bool { return j.ID == id })
	if idx < 0 {
		list = s.getJobsWithStats(jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll}).jobs
		idx = slices.IndexFunc(list, func(j persistence.JobInfo) bool { return j.ID == id })
	}
	if idx < 0 {
		return jobPosition{}
	}
	pos := jobPosition{Pos: idx + 1, Total: len(list)}
	if idx > 0 {
		pos.Prev = s.jobLink(list[idx-1].ID, q)
	}
	if idx < len(list)-1 {
		pos.Next = s.jobLink(list[idx+1].ID, q)
	}
	return pos
}

// handleJump renders the jump list of the job page search: jobs whose name or command holds every word of
// q, in crontab order
func (s *Server) handleJump(w http.ResponseWriter, r *http.Request) {
	q := s.listQuery(r)
	words := strings.Fields(strings.ToLower(r.FormValue("q")))
	all := s.getJobsWithStats(jobsQuery{sort: enums.SortModeDefault, filter: enums.FilterModeAll}).jobs
	rows := make([]indexRow, 0, jumpLimit)
	for _, job := range all {
		text := strings.ToLower(job.Name + " " + job.Command)
		if !slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) }) {
			rows = append(rows, indexRow{Job: job, Link: s.jobLink(job.ID, q)})
		}
		if len(rows) == jumpLimit {
			break
		}
	}
	s.render(w, "partials", "jump-list", rows)
}

// handleRunOutput renders one recorded run's output into the job page's output pane
func (s *Server) handleRunOutput(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	execID, err := strconv.Atoi(r.PathValue("exec_id"))
	if err != nil {
		http.Error(w, "Invalid execution ID", http.StatusBadRequest)
		return
	}
	job, ok := s.jobByID(jobID)
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}
	run, err := s.store.GetExecutionByID(execID)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		log.Printf("[ERROR] failed to get execution %d: %v", execID, err)
		http.Error(w, "Failed to load execution", http.StatusInternalServerError)
		return
	}
	if err != nil || run.JobID != jobID {
		http.Error(w, "Execution not found", http.StatusNotFound)
		return
	}
	out := s.newRunOutput(job, run)
	s.writeOutput(w, jobOutput{JobID: job.ID, Run: &out})
}

// handleLiveOutput renders a run in progress identified by its start (unix micro), or, with after, waits
// for the manual run started after that time.
// A poll of a run in progress gets only the new body of its fragment. Every other answer replaces the whole
// output pane: the run's recorded output once it finished, matched by start time so a newer run never takes
// its place, or the found run of a wait
func (s *Server) handleLiveOutput(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobByID(r.PathValue("id"))
	if r.FormValue("after") != "" {
		if !ok {
			http.Error(w, "Job not found", http.StatusNotFound)
			return
		}
		s.waitForRun(w, r, job)
		return
	}
	start, err := strconv.ParseInt(r.FormValue("start"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid run start", http.StatusBadRequest)
		return
	}
	if !ok {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	if run, ok := s.activeRunAt(job.ID, start); ok {
		live := s.newLiveOutput(job, run)
		if r.FormValue("poll") != "" {
			s.render(w, "partials", "live-body", live)
			return
		}
		s.writeOutput(w, jobOutput{JobID: job.ID, Live: &live})
		return
	}

	run, err := s.store.GetExecutionByStart(job.ID, start)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		log.Printf("[ERROR] failed to get execution of job %s started at %d: %v", job.ID, start, err)
		http.Error(w, "Failed to load execution", http.StatusInternalServerError)
		return
	}
	if err != nil {
		s.writeOutput(w, jobOutput{JobID: job.ID, Gone: true})
		return
	}
	out := s.newRunOutput(job, run)
	s.writeOutput(w, jobOutput{JobID: job.ID, Run: &out})
}

// waitForRun answers the wait for a started manual run: the earliest manual run of the job that started at
// or after after, in progress or already recorded. Only manual runs count, so a scheduled run overlapping the
// wait is never taken for it. A run found replaces the pane and drops after from the page URL; none within
// startTimeout ends the wait with 286
func (s *Server) waitForRun(w http.ResponseWriter, r *http.Request, job persistence.JobInfo) {
	after, err := strconv.ParseInt(r.FormValue("after"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid run start", http.StatusBadRequest)
		return
	}
	var found *activeRun
	for _, a := range s.activeRuns(job.ID) {
		if a.ExecutedCommand != "" && a.StartedAt.UnixMicro() >= after && (found == nil || a.StartedAt.Before(found.StartedAt)) {
			found = &a
		}
	}
	runs, err := s.store.JobRuns(job.ID, jobStripSize)
	if err != nil {
		log.Printf("[WARN] failed to load runs of job %s: %v", job.ID, err)
	}
	var recorded *persistence.RunSummary
	for i, rs := range runs {
		if rs.Manual && rs.StartedAt.UnixMicro() >= after && (recorded == nil || rs.StartedAt.Before(recorded.StartedAt)) {
			recorded = &runs[i]
		}
	}

	w.Header().Set("HX-Replace-Url", s.jobLink(job.ID, s.listQuery(r)))
	switch {
	case recorded != nil && (found == nil || !found.StartedAt.Before(recorded.StartedAt)):
		run, err := s.store.GetExecutionByID(recorded.ID)
		if err != nil {
			log.Printf("[ERROR] failed to get execution %d: %v", recorded.ID, err)
			http.Error(w, "Failed to load execution", http.StatusInternalServerError)
			return
		}
		out := s.newRunOutput(job, run)
		s.writeOutput(w, jobOutput{JobID: job.ID, Run: &out})
	case found != nil:
		live := s.newLiveOutput(job, *found)
		s.writeOutput(w, jobOutput{JobID: job.ID, Live: &live})
	default:
		w.Header().Del("HX-Replace-Url")
		data := jobOutput{JobID: job.ID, After: after}
		status := http.StatusOK
		if time.Since(time.UnixMicro(after)) > startTimeout {
			data.NoStart, status = true, 286 // htmx stops polling on this code
		}
		var buf bytes.Buffer
		if err := s.templates["partials"].ExecuteTemplate(&buf, "wait-body", data); err != nil {
			log.Printf("[ERROR] failed to render wait: %v", err)
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if _, err := buf.WriteTo(w); err != nil {
			log.Printf("[WARN] failed to write wait: %v", err)
		}
	}
}

// writeOutput replaces the job page's output pane with out and tells the page a run was selected
func (s *Server) writeOutput(w http.ResponseWriter, out jobOutput) {
	w.Header().Set("HX-Retarget", "#joutput")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("HX-Trigger-After-Swap", "run-selected")
	s.render(w, "partials", "job-output", out)
}

// newLiveOutput pairs a run in progress with its command and the output it has produced so far
func (s *Server) newLiveOutput(job persistence.JobInfo, run activeRun) liveOutputData {
	data := liveOutputData{liveRunView: run.view(), JobID: job.ID, Command: job.Command,
		CommandLabel: "Job command", Capture: run.Output != nil}
	if run.ExecutedCommand != "" {
		data.Command, data.CommandLabel = run.ExecutedCommand, "Executed"
	}
	if run.Output != nil {
		data.Lines = s.logLines(run.Output())
		if n := len(data.Lines); n > 0 {
			data.Lines[n-1].Cursor = true
		}
	}
	return data
}

// view makes the runs list entry of a run in progress
func (r activeRun) view() liveRunView {
	start := r.StartedAt.UnixMicro()
	return liveRunView{Start: start, Token: fmt.Sprintf("live:%d", start), StartedAt: r.StartedAt,
		ExecutedCommand: r.ExecutedCommand}
}

// newRunOutput pairs a run with the command it ran: the executed command of a manual run, the job command
// otherwise, since scheduled runs do not record their resolved command
func (s *Server) newRunOutput(job persistence.JobInfo, run persistence.ExecutionInfo) runOutputData {
	data := runOutputData{JobID: job.ID, Run: run, Command: job.Command, CommandLabel: "Job command",
		Lines: s.logLines(run.Output)}
	if run.ExecutedCommand != "" {
		data.Command, data.CommandLabel = run.ExecutedCommand, "Executed"
	}
	return data
}

// logLines splits output into lines, marking error lines; a trailing newline adds no empty line
func (s *Server) logLines(output string) []logLine {
	if output == "" {
		return nil
	}
	parts := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	lines := make([]logLine, 0, len(parts))
	for _, p := range parts {
		lines = append(lines, logLine{Text: p, Err: errLine.MatchString(p)})
	}
	return lines
}
