// Package service provides top level scheduler. Combined all elements (cron, resumer and crontab updater) together
package service

import (
	"context"
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

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/resumer"
)

//go:generate moq -out mocks/resumer.go -pkg mocks -skip-ensure -fmt goimports . Resumer
//go:generate moq -out mocks/crontab.go -pkg mocks -skip-ensure -fmt goimports . CrontabParser
//go:generate moq -out mocks/cron.go -pkg mocks -skip-ensure -fmt goimports . Cron
//go:generate moq -out mocks/notifier.go -pkg mocks -skip-ensure -fmt goimports . Notifier
//go:generate moq -out mocks/dedupper.go -pkg mocks -skip-ensure -fmt goimports . Dedupper
//go:generate moq -out mocks/repeater.go -pkg mocks -skip-ensure -fmt goimports . Repeater
//go:generate moq -out mocks/schedule.go -pkg mocks -skip-ensure -fmt goimports . Schedule

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
	HostName          string
	MaxLogLines       int
	EnableLogPrefix   bool
	Repeater          Repeater
	Stdout            io.Writer
	NotifyTimeout     time.Duration
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

// Do runs blocking scheduler
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
	if err := s.loadFromFileParser(); err != nil {
		log.Printf("[WARN] can't load crontab file, %v", err)
		return
	}
	s.Start()
	<-ctx.Done()
	log.Print("[DEBUG] terminate")
	<-s.Stop().Done()
}

// schedule makes new cron job from crontab.JobSpec and adds to cron
func (s *Scheduler) schedule(r crontab.JobSpec) error {
	jobDesc := s.jobDescription(r)
	log.Printf("[INFO] new cron, %s", jobDesc)
	sched, e := cron.ParseStandard(r.Spec)
	if e != nil {
		return fmt.Errorf("can't parse %s: %w", r.Spec, e)
	}

	id := s.Schedule(sched, s.jobFunc(r, sched))
	log.Printf("[INFO] first: %s, %s (%v)", sched.Next(time.Now()).Format(time.RFC3339), jobDesc, id)
	return nil
}

func (s *Scheduler) jobFunc(r crontab.JobSpec, sched Schedule) cron.FuncJob {

	runJob := func(r crontab.JobSpec, rptr Repeater) error {
		cmd, err := NewDayTemplate(time.Now()).Parse(r.Command)
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

		err = s.executeCommand(cmd, s.Stdout, rptr)
		ctxTimeout, cancel := context.WithTimeout(context.Background(), s.NotifyTimeout)
		defer cancel()
		var errMsg string
		if err != nil {
			errMsg = err.Error()
		}
		if e := s.notify(ctxTimeout, r, errMsg); e != nil {
			return fmt.Errorf("failed to notify: %w", err)
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

	return func() {
		jobDesc := s.jobDescription(r)
		jobRepeater := s.getJobRepeater(r.Repeater)
		log.Printf("[INFO] executing: %s", jobDesc)
		if err := runJob(r, jobRepeater); err != nil {
			log.Printf("[WARN] job failed: %s, %v", jobDesc, err)
		} else {
			log.Printf("[INFO] completed %s", jobDesc)
		}
		log.Printf("[INFO] next: %s, %s", sched.Next(time.Now()).Format(time.RFC3339), jobDesc)
	}
}

func (s *Scheduler) executeCommand(command string, logWriter io.Writer, rptr Repeater) error {
	if s.Jitter > 0 {
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(int(s.Jitter.Milliseconds())))) //nolint jitter up to jitter duration
	}

	err := rptr.Do(context.Background(), func() error {
		cmd := exec.Command("sh", "-c", command) // nolint gosec
		serr := NewErrorWriter(s.MaxLogLines)
		logWithErr := io.MultiWriter(logWriter, serr)
		if s.EnableLogPrefix {
			prefixer := NewLogPrefixer(logWriter, command)
			logWithErr = io.MultiWriter(prefixer, serr)
		}
		cmd.Stdout = logWithErr
		cmd.Stderr = logWithErr
		if e := cmd.Run(); e != nil {
			serr.SerError(fmt.Errorf("failed to execute command %s: %w", command, e))
			return serr
		}
		return nil
	})

	return err
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
		return s.Notifier.Send(ctx, fmt.Sprintf("failed %q on %s", r.Command, s.HostName), msg)
	}

	if errMsg == "" && s.Notifier.IsOnCompletion() {
		msg, err := s.Notifier.MakeCompletionHTML(r.Spec, r.Command)
		if err != nil {
			return fmt.Errorf("can't make html email: %w", err)
		}
		return s.Notifier.Send(ctx, fmt.Sprintf("completed %q on %s", r.Command, s.HostName), msg)
	}

	return nil
}

func (s *Scheduler) loadFromFileParser() error {
	for _, entry := range s.Entries() {
		s.Remove(entry.ID)
	}

	jss, err := s.CrontabParser.List()
	if err != nil {
		return fmt.Errorf("failed to load file %s: %w", s.CrontabParser.String(), err)
	}

	for _, js := range jss {
		req := crontab.JobSpec{Spec: js.Spec, Command: js.Command, Name: js.Name, Repeater: js.Repeater}
		if err = s.schedule(req); err != nil {
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
			if err = s.loadFromFileParser(); err != nil {
				log.Printf("[WARN] failed to update jobs, %v", err)
			}
		}
	}
}

func (s *Scheduler) resumeInterrupted(concur int) {
	cmds := s.Resumer.List()
	if len(cmds) > 0 {
		log.Printf("[INFO] interrupted commands detected - %+v", cmds)
	}

	go func() {
		gr := syncs.NewSizedGroup(concur)
		for _, cmd := range cmds {
			cmd := cmd
			time.Sleep(time.Millisecond * 100) // keep some time between commands and prevent reordering if no concurrency
			gr.Go(func(ctx context.Context) {
				if err := s.executeCommand(cmd.Command, s.Stdout, s.Repeater); err != nil {
					r := crontab.JobSpec{Spec: "auto-resume", Command: cmd.Command}
					ctxTimeout, cancel := context.WithTimeout(ctx, s.NotifyTimeout)
					defer cancel()
					if e := s.notify(ctxTimeout, r, err.Error()); e != nil {
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

	backoff := s.extractGlobalBackoff()
	if backoff == nil {
		return s.Repeater
	}

	s.applyJobConfig(backoff, jobConfig)
	return repeater.New(backoff)
}

// extractGlobalBackoff attempts to extract backoff configuration from the global repeater
func (s *Scheduler) extractGlobalBackoff() *strategy.Backoff {
	globalStrategy, ok := s.Repeater.(*repeater.Repeater)
	if !ok {
		return nil
	}

	globalBackoff, ok := globalStrategy.Strategy.(*strategy.Backoff)
	if !ok {
		return nil
	}

	// create a copy of the global backoff settings
	return &strategy.Backoff{
		Repeats:  globalBackoff.Repeats,
		Duration: globalBackoff.Duration,
		Factor:   globalBackoff.Factor,
		Jitter:   globalBackoff.Jitter,
	}
}

// applyJobConfig applies job-specific configuration to the backoff strategy
func (s *Scheduler) applyJobConfig(backoff *strategy.Backoff, config *crontab.RepeaterConfig) {
	if config.Attempts != nil {
		backoff.Repeats = *config.Attempts
	}
	if config.Duration != nil {
		backoff.Duration = *config.Duration
	}
	if config.Factor != nil {
		backoff.Factor = *config.Factor
	}
	if config.Jitter != nil {
		backoff.Jitter = *config.Jitter
	}
}
