package web

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

// stepView is one step of a run as the inspector shows it
type stepView struct {
	Index    int
	Name     string
	Command  string // command as executed
	Status   string // pending, running, success, failed or skipped
	ExitCode int
	Attempts int
	Duration time.Duration // time the step took, or has taken so far while it runs
	Output   string
	Open     bool // a failed or running step starts expanded
}

// formStep is one editable step of the run dialog
type formStep struct {
	Index int
	Name  string
	Value string
}

// formStepOption is one choice of the run dialog's start step
type formStepOption struct {
	Index    int
	Label    string
	Selected bool
}

// toJobSteps converts the crontab steps of a job to the steps the dashboard shows
func toJobSteps(specs []crontab.StepSpec) []persistence.JobStep {
	if len(specs) == 0 {
		return nil
	}
	res := make([]persistence.JobStep, len(specs))
	for i, st := range specs {
		res[i] = persistence.JobStep{Name: st.Name, Command: st.Command}
	}
	return res
}

// lastStep returns the last step a run reached, the failed one if it failed; 0 for a run without steps
func lastStep(states []request.StepState) int {
	last := 0
	for _, st := range states {
		if st.Status != enums.StepStatusSkipped && st.Status != enums.StepStatusPending {
			last = st.Index
		}
	}
	return last
}

// currentStep returns the step a run in progress is at: the running one, or the last one reached between steps
func currentStep(states []request.StepState) int {
	for _, st := range states {
		if st.Status == enums.StepStatusRunning {
			return st.Index
		}
	}
	return lastStep(states)
}

// stateViews makes the inspector entries of the steps of a run in progress or just finished
func stateViews(states []request.StepState, now time.Time) []stepView {
	res := make([]stepView, len(states))
	for i, st := range states {
		res[i] = newStepView(stepView{Index: st.Index, Name: st.Name, Command: st.Command, Status: st.Status.String(),
			ExitCode: st.ExitCode, Attempts: st.Attempts, Output: st.Output}, st.StartedAt, st.FinishedAt, now)
	}
	return res
}

// infoViews makes the inspector entries of the recorded steps of a run
func infoViews(steps []persistence.StepInfo) []stepView {
	res := make([]stepView, len(steps))
	for i, st := range steps {
		res[i] = newStepView(stepView{Index: st.Index, Name: st.Name, Command: st.Command, Status: st.Status.String(),
			ExitCode: st.ExitCode, Attempts: st.Attempts, Output: st.Output}, st.StartedAt, st.FinishedAt, time.Time{})
	}
	return res
}

// newStepView completes a step entry with its duration and whether it starts expanded
func newStepView(v stepView, started, finished, now time.Time) stepView {
	switch {
	case started.IsZero():
	case v.Status == enums.StepStatusRunning.String():
		v.Duration = now.Sub(started)
	case !finished.IsZero():
		v.Duration = finished.Sub(started)
	}
	v.Open = v.Status == enums.StepStatusFailed.String() || v.Status == enums.StepStatusRunning.String()
	return v
}

// stepLabel is how a step is named in the dashboard: its name, or its command when it has none
func stepLabel(st persistence.JobStep) string {
	if st.Name != "" {
		return st.Name
	}
	return st.Command
}

// runStepsView is how far a recorded run of a job with steps got, as the inspector's runs list shows it
type runStepsView struct {
	First  int // first step the run ran, 0 for a job without steps
	Last   int // last step the run reached
	Total  int // steps of the job
	Failed bool
}

// runStepsOf makes the runs list entry of how far a run of a job with total steps got
func runStepsOf(run persistence.ExecutionInfo, total int) runStepsView {
	return runStepsView{First: run.StepFirst, Last: run.StepLast, Total: total, Failed: run.Status == enums.JobStatusFailed}
}

// stepAt returns the label of step i (1-based) of steps, empty when there is no such step
func stepAt(steps []persistence.JobStep, i int) string {
	if i < 1 || i > len(steps) {
		return ""
	}
	return stepLabel(steps[i-1])
}

// newStepsRunForm builds the run dialog of a job with steps, starting at step from with the given commands of
// the steps from it on; a step without a value shows its own command
func (s *Server) newStepsRunForm(job persistence.JobInfo, from int, values map[int]string, date string) runForm {
	form := runForm{JobID: job.ID, Name: job.Name, Command: job.Command, Date: date, CommandEditable: !s.disableCommandEdit,
		FromStep: from}
	for i, st := range job.Steps {
		index := i + 1
		form.StepOptions = append(form.StepOptions, formStepOption{Index: index,
			Label: fmt.Sprintf("%d · %s", index, stepLabel(st)), Selected: index == from})
		if index < from {
			continue
		}
		value, ok := values[index]
		if !ok {
			value = st.Command
		}
		form.Steps = append(form.Steps, formStep{Index: index, Name: st.Name, Value: value})
		form.HasTemplates = form.HasTemplates || strings.Contains(value, "{{") || strings.Contains(value, "[[")
	}
	form.HasTemplates = form.HasTemplates || date != ""
	return form
}

// lenientFromStep reads the start step of a run dialog request, falling back to the first for a missing or
// out of range value, so redrawing the dialog never fails
func lenientFromStep(r *http.Request, job persistence.JobInfo) int {
	from, err := strconv.Atoi(r.FormValue("from"))
	if err != nil || from < 1 || from > len(job.Steps) {
		return 1
	}
	return from
}

// submittedSteps returns the step commands a run dialog request carries, by step
func submittedSteps(r *http.Request, job persistence.JobInfo) map[int]string {
	values := map[int]string{}
	for i := range job.Steps {
		key := fmt.Sprintf("step-%d", i+1)
		if r.Form.Has(key) {
			values[i+1] = r.FormValue(key)
		}
	}
	return values
}

// rerunSteps returns the commands the given run of the job executed, by step, to prefill a re-run from a
// failed step with the dates the run resolved. It returns nil when the run is not the job's
func (s *Server) rerunSteps(job persistence.JobInfo, execID int) (map[int]string, error) {
	run, err := s.store.GetExecutionByID(execID)
	if errors.Is(err, persistence.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get execution %d: %w", execID, err)
	}
	if run.JobID != job.ID {
		return nil, nil
	}
	steps, err := s.store.GetExecutionSteps(execID)
	if err != nil {
		return nil, fmt.Errorf("failed to get steps of execution %d: %w", execID, err)
	}
	values := map[int]string{}
	for _, st := range steps {
		if st.Status != enums.StepStatusSkipped && st.Status != enums.StepStatusPending {
			values[st.Index] = st.Command
		}
	}
	return values, nil
}

// stepsRunForm builds the run dialog of a job with steps from a request: the start step, the commands of a
// recorded run to re-run (only when commands can be edited), and the values the dialog already holds
func (s *Server) stepsRunForm(r *http.Request, job persistence.JobInfo) (runForm, error) {
	if err := r.ParseForm(); err != nil {
		return runForm{}, fmt.Errorf("can't parse run form request: %w", err)
	}
	values := map[int]string{}
	if execID, err := strconv.Atoi(r.FormValue("run")); err == nil && !s.disableCommandEdit {
		rerun, err := s.rerunSteps(job, execID)
		if err != nil {
			return runForm{}, err
		}
		maps.Copy(values, rerun)
	}
	maps.Copy(values, submittedSteps(r, job))
	return s.newStepsRunForm(job, lenientFromStep(r, job), values, r.FormValue("date")), nil
}

// checkSteps validates the start step and step commands of a manual run of a job with steps
func (s *Server) checkSteps(r *http.Request, job persistence.JobInfo) (from int, cmds []string, rerr *runError) {
	from = 1
	if v := r.FormValue("from"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > len(job.Steps) {
			return 0, nil, &runError{status: http.StatusBadRequest, msg: "Invalid start step"}
		}
		from = n
	}
	values := submittedSteps(r, job)
	for i := from; i <= len(job.Steps); i++ {
		own := job.Steps[i-1].Command
		cmd, ok := values[i]
		if !ok {
			cmd = own
		}
		if strings.TrimSpace(cmd) == "" {
			return 0, nil, &runError{status: http.StatusBadRequest, msg: fmt.Sprintf("Step %d has no command", i)}
		}
		if s.disableCommandEdit && cmd != own {
			return 0, nil, &runError{status: http.StatusForbidden, msg: "Command editing is disabled"}
		}
		cmds = append(cmds, cmd)
	}
	return from, cmds, nil
}

// runSteps loads the steps of a recorded run of a job with steps, nil for a run without steps
func (s *Server) runSteps(run persistence.ExecutionInfo) []stepView {
	if run.StepFirst == 0 {
		return nil
	}
	steps, err := s.store.GetExecutionSteps(run.ID)
	if err != nil {
		log.Printf("[WARN] failed to get steps of execution %d: %v", run.ID, err)
		return nil
	}
	return infoViews(steps)
}

// runningStepLocked returns the step the newest run in progress of a job with steps is at, 0 when none runs.
// The caller holds jobsMu
func (s *Server) runningStepLocked(jobID string) int {
	var newest activeRun
	for _, run := range s.active[jobID] {
		if run.Steps != nil && run.StartedAt.After(newest.StartedAt) {
			newest = run
		}
	}
	if newest.Steps == nil {
		return 0
	}
	return currentStep(newest.Steps())
}
