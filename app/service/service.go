// Package service provides top level scheduler. Combined all elements (cron, resumer and crontab updater) together
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"reflect"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
	"github.com/go-pkgz/syncs"
	"github.com/robfig/cron/v3"

	"github.com/umputun/cronn/app/conditions"
	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service/request"
)

//go:generate moq -out mocks/resumer.go -pkg mocks -skip-ensure -fmt goimports . Resumer
//go:generate moq -out mocks/crontab.go -pkg mocks -skip-ensure -fmt goimports . CrontabParser
//go:generate moq -out mocks/cron.go -pkg mocks -skip-ensure -fmt goimports . Cron
//go:generate moq -out mocks/notifier.go -pkg mocks -skip-ensure -fmt goimports . Notifier
//go:generate moq -out mocks/dedupper.go -pkg mocks -skip-ensure -fmt goimports . Dedupper
//go:generate moq -out mocks/repeater.go -pkg mocks -skip-ensure -fmt goimports . Repeater
//go:generate moq -out mocks/schedule.go -pkg mocks -skip-ensure -fmt goimports . Schedule
//go:generate moq -out mocks/condition_checker.go -pkg mocks -skip-ensure -fmt goimports . ConditionChecker
//go:generate moq -out mocks/job_event_handler.go -pkg mocks -skip-ensure -fmt goimports . JobEventHandler

// Scheduler is a top-level service wiring cron, resumer ans parser and provifing the main entry point (blocking)
// to start the process
type Scheduler struct {
	Cron
	Resumer           Resumer
	ResumeConcurrency int
	CrontabParser     CrontabParser
	UpdatesEnabled    bool
	Jitter            time.Duration
	Notifier          Notifier
	DeDup             Dedupper
	ConditionChecker  ConditionChecker
	HostName          string
	NotifyMaxLogLines int // controls notification output capture buffer size
	ExecMaxLogLines   int // controls web log output capture buffer size (0 = disabled)
	EnableLogPrefix   bool
	Repeater          Repeater
	RepeaterDefaults  struct {
		Attempts int
		Duration time.Duration
		Factor   float64
		Jitter   bool
	}
	Stdout          io.Writer
	NotifyTimeout   time.Duration
	JobEventHandler JobEventHandler       // handler for job execution events
	ManualTrigger   chan ManualJobRequest // channel for manual job triggers
	AltTemplate     bool                  // use alternative template format [[.YYYYMMDD]]
}

// ManualJobRequest represents a request to manually trigger a job
type ManualJobRequest struct {
	JobID      string // original job ID to record execution against
	Command    string // command to execute (may be edited)
	Schedule   string
	CustomDate *time.Time
}

// Resumer defines interface for resumer.Resumer providing auto-restart for failed jobs
type Resumer interface {
	OnStart(cmd string) (string, error)
	OnFinish(fname string) error
	List() (res []resumer.Cmd)
	String() string
}

// CrontabParser interface loads the list of jobs and provides updates channel if the corresponding crontab file updated
type CrontabParser interface {
	String() string
	List() (result []crontab.JobSpec, err error)
	Changes(ctx context.Context) (<-chan []crontab.JobSpec, error)
}

// Cron interface defines basic robfig/cron methods used by service
type Cron interface {
	Start()
	Stop() context.Context
	Entries() []cron.Entry
	Schedule(schedule cron.Schedule, cmd cron.Job) cron.EntryID
	Remove(id cron.EntryID)
}

// Notifier interface defines notification delivery on failed executions
type Notifier interface {
	Send(ctx context.Context, subj, text string) error
	IsOnError() bool
	IsOnCompletion() bool
	MakeErrorHTML(spec, command, errorLog string) (string, error)
	MakeCompletionHTML(spec, command string) (string, error)
}

// Dedupper defines a locking primitive to register/unregister command in order to prevent dbl registration
type Dedupper interface {
	Add(key string) bool
	Remove(key string)
}

// Repeater repeats failed function
type Repeater interface {
	Do(ctx context.Context, fun func() error, errors ...error) (err error)
}

// Schedule describes a job's duty cycle.
type Schedule interface {
	Next(time.Time) time.Time
}

// ConditionChecker defines interface for checking job execution conditions
type ConditionChecker interface {
	Check(conditions conditions.Config) (bool, string)
}

// JobEventHandler defines interface for handling job execution events
type JobEventHandler interface {
	OnJobStart(req request.OnJobStart)
	OnJobComplete(req request.OnJobComplete)
}

// Do runs blocking scheduler. If UpdatesEnabled is true and crontab file fails to load,
// the scheduler starts with zero jobs and waits for crontab updates.
func (s *Scheduler) Do(ctx context.Context) {
	if s.ResumeConcurrency <= 0 {
		s.ResumeConcurrency = 1
	}
	if s.Stdout == nil {
		s.Stdout = os.Stdout
	}
	s.resumeInterrupted(s.ResumeConcurrency)

	if s.UpdatesEnabled {
		log.Printf("[INFO] updater activated for %s", s.CrontabParser.String())
		go s.reload(ctx) // start background updater
	}

	// start manual trigger listener if channel is provided
	if s.ManualTrigger != nil {
		go s.listenForManualTriggers(ctx)
	}

	if err := s.loadFromFileParser(ctx); err != nil {
		// only tolerate missing file errors when updates are enabled
		// other errors (permission, parse, validation) should still abort
		if !s.UpdatesEnabled || !errors.Is(err, os.ErrNotExist) {
			log.Printf("[WARN] can't load crontab file, %v", err)
			return
		}
		log.Printf("[INFO] crontab file doesn't exist yet, running with zero jobs, waiting for updates")
	}
	s.Start()
	<-ctx.Done()
	log.Print("[DEBUG] terminate")
	<-s.Stop().Done()
}

// schedule makes new cron job from crontab.JobSpec and adds to cron
func (s *Scheduler) schedule(ctx context.Context, r crontab.JobSpec) error {
	jobDesc := s.jobDescription(r)
	log.Printf("[INFO] new cron, %s", jobDesc)
	sched, e := cron.ParseStandard(r.Spec)
	if e != nil {
		return fmt.Errorf("can't parse %s: %w", r.Spec, e)
	}

	id := s.Schedule(sched, s.jobFunc(ctx, r, sched))
	log.Printf("[INFO] first: %s, %s (%v)", sched.Next(time.Now()).Format(time.RFC3339), jobDesc, id)
	return nil
}

func (s *Scheduler) jobFunc(ctx context.Context, r crontab.JobSpec, sched Schedule) cron.FuncJob {
	return s.jobFuncWithTime(ctx, r, sched, nil)
}

// runJobWithCommand executes a job with the specified command string after parsing through template
// r.Command is used for job identity, commandToParse is what actually gets parsed and executed
// isManual indicates whether this is a manual execution (from "Run Now" button)
func (s *Scheduler) runJobWithCommand(ctx context.Context, r crontab.JobSpec, commandToParse string, customTime *time.Time, rptr Repeater, isManual bool) error {
	// use custom time if provided, otherwise use current time
	templateTime := time.Now()
	if customTime != nil {
		templateTime = *customTime
	}

	// parse the command through template
	cmd, err := NewDayTemplate(templateTime, AltTemplateFormat(s.AltTemplate)).Parse(commandToParse)
	if err != nil {
		return err
	}

	dedupKey := cmd + "#" + r.Spec // dedup by command and spec
	if !s.DeDup.Add(dedupKey) {
		// already running
		return fmt.Errorf("duplicated job %q ignored", dedupKey)
	}
	defer s.DeDup.Remove(dedupKey)

	rfile, rerr := s.Resumer.OnStart(cmd) // register job in resumer prior to execution
	if rerr != nil {
		return fmt.Errorf("failed to initiate resumer for %+v: %w", cmd, rerr)
	}

	// notify job start after resumer registration
	// for manual runs: set executedCommand to the command that will be executed (after template parsing)
	// for scheduled runs: set executedCommand to empty string
	startTime := time.Now()
	executedCommand := ""
	if isManual {
		executedCommand = cmd
	}
	if s.JobEventHandler != nil {
		s.JobEventHandler.OnJobStart(request.OnJobStart{
			Command:         r.Command,
			ExecutedCommand: executedCommand,
			Schedule:        r.Spec,
			StartTime:       startTime,
		})
	}

	notifyOutput, webOutput, err := s.executeCommand(ctx, cmd, s.Stdout, rptr)

	// notify job complete
	endTime := time.Now()
	if s.JobEventHandler != nil {
		exitCode := 0
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				exitCode = exitError.ExitCode()
			} else {
				exitCode = 1 // generic error for non-exec errors
			}
		}
		s.JobEventHandler.OnJobComplete(request.OnJobComplete{
			Command:         r.Command,
			ExecutedCommand: executedCommand,
			Schedule:        r.Spec,
			StartTime:       startTime,
			EndTime:         endTime,
			ExitCode:        exitCode,
			Output:          webOutput, // use web-specific output
			Err:             err,
		})
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, s.NotifyTimeout)
	defer cancel()
	var errMsg string
	if err != nil {
		// combine error with notify output for notification emails
		if notifyOutput != "" {
			errMsg = err.Error() + "\n\n" + notifyOutput
		} else {
			errMsg = err.Error()
		}
	}
	if e := s.notify(ctxTimeout, r, errMsg); e != nil {
		return fmt.Errorf("failed to notify: %w", e)
	}
	if err != nil {
		return err
	}

	// if no error, finish (unregister) resumer
	if err := s.Resumer.OnFinish(rfile); err != nil {
		return fmt.Errorf("failed to finish resumer for %s: %w", rfile, err)
	}

	return nil
}

// jobFuncWithEditedCommand creates job function with edited command for manual execution
// r.Command is used for job identity, editedCmd is what actually gets executed
func (s *Scheduler) jobFuncWithEditedCommand(ctx context.Context, r crontab.JobSpec, editedCmd string, customTime *time.Time) cron.FuncJob {
	return func() {
		jobDesc := s.jobDescription(r)
		jobRepeater := s.getJobRepeater(r.Repeater)

		// check conditions if configured
		if r.Conditions != nil {
			shouldExecute := s.waitForConditions(ctx, *r.Conditions, jobDesc)
			if !shouldExecute {
				return
			}
		}

		if err := s.runJobWithCommand(ctx, r, editedCmd, customTime, jobRepeater, true); err != nil {
			log.Printf("[WARN] %v, %s", err, jobDesc)
		} else {
			log.Printf("[INFO] completed %s", jobDesc)
		}
	}
}

func (s *Scheduler) jobFuncWithTime(ctx context.Context, r crontab.JobSpec, sched Schedule, customTime *time.Time) cron.FuncJob {
	return func() {
		jobDesc := s.jobDescription(r)
		jobRepeater := s.getJobRepeater(r.Repeater)

		// check conditions if configured
		if r.Conditions != nil {
			shouldExecute := s.waitForConditions(ctx, *r.Conditions, jobDesc)
			if !shouldExecute {
				return
			}
		}

		log.Printf("[INFO] executing: %s", jobDesc)
		if err := s.runJobWithCommand(ctx, r, r.Command, customTime, jobRepeater, false); err != nil {
			log.Printf("[WARN] job failed: %s, %v", jobDesc, err)
		} else {
			log.Printf("[INFO] completed %s", jobDesc)
		}
		log.Printf("[INFO] next: %s, %s", sched.Next(time.Now()).Format(time.RFC3339), jobDesc)
	}
}

func (s *Scheduler) executeCommand(ctx context.Context, command string, logWriter io.Writer, rptr Repeater) (notifyOutput, webOutput string, err error) {
	if s.Jitter > 0 {
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(int(s.Jitter.Milliseconds())))) //nolint jitter up to jitter duration
	}

	// create output capture for notifications
	notifyCapture := NewOutputCapture(s.NotifyMaxLogLines)

	// create output capture for web logs if enabled (0 = disabled)
	var webCapture *OutputCapture
	if s.ExecMaxLogLines > 0 {
		webCapture = NewOutputCapture(s.ExecMaxLogLines)
	}

	execErr := rptr.Do(ctx, func() error {
		cmd := exec.Command("sh", "-c", command) // nolint gosec

		// build multi-writer with both captures
		writers := []io.Writer{notifyCapture}
		if webCapture != nil {
			writers = append(writers, webCapture)
		}

		if s.EnableLogPrefix {
			writers = append(writers, NewLogPrefixer(logWriter, command))
		} else {
			writers = append(writers, logWriter)
		}
		logWithErr := io.MultiWriter(writers...)

		cmd.Stdout = logWithErr
		cmd.Stderr = logWithErr
		if e := cmd.Run(); e != nil {
			return fmt.Errorf("failed to execute command %s: %w", command, e)
		}
		return nil
	})

	notifyOutput = notifyCapture.GetOutput()
	if webCapture != nil {
		webOutput = webCapture.GetOutput()
	}

	if execErr != nil {
		return notifyOutput, webOutput, fmt.Errorf("command execution failed: %w", execErr)
	}
	return notifyOutput, webOutput, nil
}

func (s *Scheduler) notify(ctx context.Context, r crontab.JobSpec, errMsg string) error {
	if s.Notifier == nil || reflect.ValueOf(s.Notifier).IsNil() {
		return nil
	}

	if errMsg != "" && s.Notifier.IsOnError() {
		msg, err := s.Notifier.MakeErrorHTML(r.Spec, r.Command, errMsg)
		if err != nil {
			return fmt.Errorf("can't make html email: %w", err)
		}
		if err := s.Notifier.Send(ctx, fmt.Sprintf("failed %q on %s", r.Command, s.HostName), msg); err != nil {
			return fmt.Errorf("failed to send error notification: %w", err)
		}
		return nil
	}

	if errMsg == "" && s.Notifier.IsOnCompletion() {
		msg, err := s.Notifier.MakeCompletionHTML(r.Spec, r.Command)
		if err != nil {
			return fmt.Errorf("can't make html email: %w", err)
		}
		if err := s.Notifier.Send(ctx, fmt.Sprintf("completed %q on %s", r.Command, s.HostName), msg); err != nil {
			return fmt.Errorf("failed to send completion notification: %w", err)
		}
		return nil
	}

	return nil
}

func (s *Scheduler) loadFromFileParser(ctx context.Context) error {
	for _, entry := range s.Entries() {
		s.Remove(entry.ID)
	}

	jss, err := s.CrontabParser.List()
	if err != nil {
		return fmt.Errorf("failed to load file %s: %w", s.CrontabParser.String(), err)
	}

	for _, js := range jss {
		req := crontab.JobSpec{Spec: js.Spec, Command: js.Command, Name: js.Name, Repeater: js.Repeater, Conditions: js.Conditions}
		if err = s.schedule(ctx, req); err != nil {
			return fmt.Errorf("can't add %s, %s: %w", js.Spec, js.Command, err)
		}
	}
	return nil
}

// reload runs blocking loop reacting on updates in crontab file and reloading jobs
func (s *Scheduler) reload(ctx context.Context) {
	ch, err := s.CrontabParser.Changes(ctx)
	if err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case jobs, ok := <-ch:
			if !ok {
				return
			}
			log.Printf("[DEBUG] jobs update detected, total %d jobs scheduled", len(jobs))
			if err = s.loadFromFileParser(ctx); err != nil {
				log.Printf("[WARN] failed to update jobs, %v", err)
			}
		}
	}
}

// listenForManualTriggers listens for manual job trigger requests
func (s *Scheduler) listenForManualTriggers(ctx context.Context) {
	log.Printf("[INFO] manual trigger listener started")
	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] manual trigger listener stopped: %v", ctx.Err())
			return
		case req, ok := <-s.ManualTrigger:
			if !ok {
				log.Printf("[INFO] manual trigger channel closed")
				return
			}

			log.Printf("[INFO] manual trigger requested for job ID: %s", req.JobID)

			// get all jobs from parser
			allJobs, err := s.CrontabParser.List()
			if err != nil {
				log.Printf("[WARN] manual trigger failed: cannot list jobs: %v", err)
				continue
			}

			// find original job by ID (ID is hash of command)
			var originalJob *crontab.JobSpec
			for i := range allJobs {
				if s.jobIDFromCommand(allJobs[i].Command) == req.JobID {
					originalJob = &allJobs[i]
					break
				}
			}

			if originalJob == nil {
				log.Printf("[WARN] manual trigger failed: job not found for ID %s", req.JobID)
				continue
			}

			// create a JobSpec with original job properties for identity
			// but we'll execute the edited command
			// note: we copy Repeater (retry logic) but not Conditions (manual = run now, bypass guards)
			jobSpec := crontab.JobSpec{
				Spec:     originalJob.Spec,    // use original schedule
				Command:  originalJob.Command, // use original command for identity
				Name:     originalJob.Name,
				Repeater: originalJob.Repeater, // preserve retry logic
			}

			// validate the schedule can be parsed
			if _, err := cron.ParseStandard(jobSpec.Spec); err != nil {
				log.Printf("[WARN] manual trigger failed: invalid schedule %s: %v", jobSpec.Spec, err)
				continue
			}

			// execute job in a new goroutine with context check
			go func(js crontab.JobSpec, editedCmd string, customTime *time.Time) {
				// check if context is still valid before executing
				select {
				case <-ctx.Done():
					log.Printf("[WARN] skipping manual execution of %s, context canceled", js.Command)
					return
				default:
					log.Printf("[INFO] manually executing job: %s (edited: %s)", js.Command, editedCmd)
					// create and execute the job function with edited command
					jobFunc := s.jobFuncWithEditedCommand(ctx, js, editedCmd, customTime)
					jobFunc.Run()
				}
			}(jobSpec, req.Command, req.CustomDate)
		}
	}
}

// jobIDFromCommand generates a SHA256 hash of a command for use as a unique ID
func (s *Scheduler) jobIDFromCommand(cmd string) string {
	h := sha256.Sum256([]byte(cmd))
	return hex.EncodeToString(h[:])
}

func (s *Scheduler) resumeInterrupted(concur int) {
	cmds := s.Resumer.List()
	if len(cmds) > 0 {
		log.Printf("[INFO] interrupted commands detected - %+v", cmds)
	}

	go func() {
		gr := syncs.NewSizedGroup(concur)
		for _, cmd := range cmds {
			time.Sleep(time.Millisecond * 100) // keep some time between commands and prevent reordering if no concurrency
			gr.Go(func(ctx context.Context) {
				notifyOutput, _, err := s.executeCommand(ctx, cmd.Command, s.Stdout, s.Repeater)
				if err != nil {
					r := crontab.JobSpec{Spec: "auto-resume", Command: cmd.Command}
					ctxTimeout, cancel := context.WithTimeout(ctx, s.NotifyTimeout)
					defer cancel()
					// combine error with notify output for notification emails
					var errMsg string
					if notifyOutput != "" {
						errMsg = err.Error() + "\n\n" + notifyOutput
					} else {
						errMsg = err.Error()
					}
					if e := s.notify(ctxTimeout, r, errMsg); e != nil {
						log.Printf("[WARN] failed to notify, %v", e)
						return
					}
				}
				if err := s.Resumer.OnFinish(cmd.Fname); err != nil {
					log.Printf("[WARN] failed to finish resumer for %s, %s", cmd.Fname, err)
				}
			})
		}
	}()
}

// jobDescription returns a formatted job description with name if available
func (s *Scheduler) jobDescription(r crontab.JobSpec) string {
	if r.Name != "" {
		return fmt.Sprintf("%q (%s)", r.Command, r.Name)
	}
	return fmt.Sprintf("%q", r.Command)
}

// getJobRepeater returns a repeater for the job, merging job-specific settings with global defaults
func (s *Scheduler) getJobRepeater(jobConfig *crontab.RepeaterConfig) Repeater {
	if jobConfig == nil {
		return s.Repeater
	}

	// start with defaults from CLI
	backoff := &strategy.Backoff{
		Repeats:  s.RepeaterDefaults.Attempts,
		Duration: s.RepeaterDefaults.Duration,
		Factor:   s.RepeaterDefaults.Factor,
		Jitter:   s.RepeaterDefaults.Jitter,
	}

	// apply job-specific overrides
	if jobConfig.Attempts != nil {
		backoff.Repeats = *jobConfig.Attempts
	}
	if jobConfig.Duration != nil {
		backoff.Duration = *jobConfig.Duration
	}
	if jobConfig.Factor != nil {
		backoff.Factor = *jobConfig.Factor
	}
	if jobConfig.Jitter != nil {
		backoff.Jitter = *jobConfig.Jitter
	}

	return repeater.New(backoff)
}

// waitForConditions checks if conditions are met and optionally waits for them
// Returns true if the job should execute, false if it should be skipped
func (s *Scheduler) waitForConditions(ctx context.Context, cond conditions.Config, jobDesc string) bool {
	// if no condition checker configured, always execute
	if s.ConditionChecker == nil {
		return true
	}

	met, reason := s.ConditionChecker.Check(cond)
	if met {
		return true
	}

	// no postpone configured - skip job
	if cond.MaxPostpone == nil {
		log.Printf("[INFO] job skipped: %s, reason: %s", jobDesc, reason)
		return false
	}

	// set up postponement
	deadline := time.Now().Add(*cond.MaxPostpone)
	log.Printf("[INFO] job postponed: %s, reason: %s, deadline: %s",
		jobDesc, reason, deadline.Format(time.RFC3339))

	checkInterval := 30 * time.Second
	if cond.CheckInterval != nil {
		checkInterval = *cond.CheckInterval
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	deadlineTimer := time.NewTimer(*cond.MaxPostpone)
	defer deadlineTimer.Stop()

	for {
		select {
		case <-ticker.C:
			met, reason = s.ConditionChecker.Check(cond)
			if met {
				log.Printf("[INFO] conditions met, executing postponed job: %s", jobDesc)
				return true
			}
			log.Printf("[DEBUG] conditions not met yet: %s, reason: %s", jobDesc, reason)

		case <-deadlineTimer.C:
			log.Printf("[WARN] max postpone reached, executing anyway: %s", jobDesc)
			return true

		case <-ctx.Done():
			log.Printf("[INFO] postponed job canceled: %s", jobDesc)
			return false
		}
	}
}
