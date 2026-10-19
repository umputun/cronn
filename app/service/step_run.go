package service

import (
	"sync"
	"time"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

// stepRun tracks the states of one run's steps; the web UI reads them while the run is in progress
type stepRun struct {
	mu       sync.Mutex
	states   []request.StepState
	captures []*OutputCapture // nil entries when web output capture is disabled
}

// newStepRun makes the pending states of steps with their resolved commands, the first of them being
// step fromStep (1-based) of the job. maxLines limits each step's captured output, 0 disables it
func newStepRun(steps []crontab.StepSpec, cmds []string, fromStep, maxLines int) *stepRun {
	r := &stepRun{states: make([]request.StepState, len(steps)), captures: make([]*OutputCapture, len(steps))}
	for i, st := range steps {
		r.states[i] = request.StepState{Index: fromStep + i, Name: st.Name, Command: cmds[i],
			Status: enums.StepStatusPending}
		if maxLines > 0 {
			r.captures[i] = NewOutputCapture(maxLines)
		}
	}
	return r
}

// capture returns the web output capture of step i, nil when capture is disabled
func (r *stepRun) capture(i int) *OutputCapture {
	return r.captures[i]
}

// start marks step i as running
func (r *stepRun) start(i int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[i].Status = enums.StepStatusRunning
	r.states[i].StartedAt = time.Now()
}

// finish records the result of step i after its attempts
func (r *stepRun) finish(i, attempts int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := &r.states[i]
	st.Status = enums.StepStatusSuccess
	if err != nil {
		st.Status = enums.StepStatusFailed
	}
	st.ExitCode = exitCodeOf(err)
	st.Attempts = attempts
	st.FinishedAt = time.Now()
	if r.captures[i] != nil {
		st.Output = r.captures[i].GetOutput()
	}
}

// skipFrom marks step i and the steps after it as skipped
func (r *stepRun) skipFrom(i int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for j := i; j < len(r.states); j++ {
		r.states[j].Status = enums.StepStatusSkipped
	}
}

// snapshot returns a copy of the step states, with the output a running step has produced so far
func (r *stepRun) snapshot() []request.StepState {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]request.StepState, len(r.states))
	copy(res, r.states)
	for i := range res {
		if res[i].Status == enums.StepStatusRunning && r.captures[i] != nil {
			res[i].Output = r.captures[i].GetOutput()
		}
	}
	return res
}
