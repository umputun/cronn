package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service/request"
)

// stepsParams is one run of a job with steps
type stepsParams struct {
	job        crontab.JobSpec    // the job, for identity, schedule and notifications
	steps      []crontab.StepSpec // the job's steps from fromStep on, possibly edited for a manual run
	fromStep   int                // 1-based position of steps[0] in the job
	customTime *time.Time         // time templates resolve against, nil for now
	repeater   Repeater           // retries of each step
	manual     bool               // started from the web UI rather than by the schedule
}

// runSteps runs the steps one after another, stopping at the first that fails after its retries. Templates
// of all steps resolve against one time, so every step sees the same date even if the run crosses the EOD hour
func (s *Scheduler) runSteps(ctx context.Context, sr stepsParams) error {
	r := sr.job
	templateTime := time.Now()
	if sr.customTime != nil {
		templateTime = *sr.customTime
	}
	tmpl := NewDayTemplate(templateTime, AltTemplateFormat(s.AltTemplate), EndOfDay(s.EODHour))
	cmds := make([]string, len(sr.steps))
	for i, st := range sr.steps {
		cmd, err := tmpl.Parse(st.Command)
		if err != nil {
			return fmt.Errorf("can't parse step %d: %w", sr.fromStep+i, err)
		}
		cmds[i] = cmd
	}

	executed := strings.Join(cmds, " && ")
	dedupKey := executed + "#" + r.Spec
	if !s.DeDup.Add(dedupKey) {
		return fmt.Errorf("duplicated job %q ignored", dedupKey)
	}
	defer s.DeDup.Remove(dedupKey)

	run := newStepRun(sr.steps, cmds, sr.fromStep, s.ExecMaxLogLines)
	startTime := time.Now()
	executedCommand := ""
	if sr.manual {
		executedCommand = executed
	}
	if s.JobEventHandler != nil {
		s.JobEventHandler.OnJobStart(request.OnJobStart{Command: r.Command, ExecutedCommand: executedCommand,
			Schedule: r.Spec, StartTime: startTime, LiveSteps: run.snapshot})
	}

	s.jitter()
	notifyOutput, err := s.executeSteps(ctx, sr, run, cmds)

	if s.JobEventHandler != nil {
		s.JobEventHandler.OnJobComplete(request.OnJobComplete{Command: r.Command, ExecutedCommand: executedCommand,
			Schedule: r.Spec, StartTime: startTime, EndTime: time.Now(), ExitCode: exitCodeOf(err),
			Steps: run.snapshot(), Err: err})
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, s.NotifyTimeout)
	defer cancel()
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if notifyOutput != "" {
			errMsg += "\n\n" + notifyOutput
		}
	}
	if e := s.notify(ctxTimeout, r, errMsg); e != nil {
		return fmt.Errorf("failed to notify: %w", e)
	}
	return err
}

// executeSteps runs the resolved commands in order, each through its own retries. Before a step starts, the
// resumer registers the rest of the chain from that step, and the registration is removed once the step
// succeeds, so a restart resumes the chain from the step that failed or was interrupted.
// It returns the notification output of the step that failed
func (s *Scheduler) executeSteps(ctx context.Context, sr stepsParams, run *stepRun, cmds []string) (notifyOutput string, err error) {
	total := len(sr.job.Steps)
	for i, cmd := range cmds {
		index := run.states[i].Index
		rfile, rerr := s.Resumer.OnStart(resumeChain(cmds[i:]))
		if rerr != nil {
			run.finish(i, 0, rerr)
			run.skipFrom(i + 1)
			return "", fmt.Errorf("failed to initiate resumer for step %d/%d %q: %w", index, total, cmd, rerr)
		}

		run.start(i)
		counter := &countedRepeater{Repeater: sr.repeater}
		out, execErr := s.runCommand(ctx, cmd, s.Stdout, counter, run.capture(i))
		run.finish(i, counter.attempts, execErr)
		if execErr != nil {
			run.skipFrom(i + 1)
			return out, fmt.Errorf("step %d/%d %q failed: %w", index, total, cmd, execErr)
		}

		if ferr := s.Resumer.OnFinish(rfile); ferr != nil {
			log.Printf("[WARN] failed to finish resumer for %s: %v", rfile, ferr)
		}
	}
	return "", nil
}

// resumeChain joins commands into one shell command that runs them in order and stops at the first failure.
// Each runs in its own subshell, as a separate step would, so one step's cd or exports never reach the next
func resumeChain(cmds []string) string {
	if len(cmds) == 1 {
		return cmds[0]
	}
	parts := make([]string, len(cmds))
	for i, cmd := range cmds {
		parts[i] = "(" + cmd + ")"
	}
	return strings.Join(parts, " && ")
}

// countedRepeater counts the attempts of the wrapped repeater, reported per step
type countedRepeater struct {
	Repeater
	attempts int
}

// Do runs fun through the wrapped repeater, counting each call
func (c *countedRepeater) Do(ctx context.Context, fun func() error, errs ...error) error {
	return c.Repeater.Do(ctx, func() error { //nolint:wrapcheck // a transparent decorator returns the repeater's own error
		c.attempts++
		return fun()
	}, errs...)
}
