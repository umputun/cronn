// Package service provides top level scheduler. Combined all elements (cron, resumer and crontab updater) together
package service

import (
	"context"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"

	"github.com/umputun/cronn/crontab"
	"github.com/umputun/cronn/resumer"
)

// Scheduler is a top-level service wiring cron, resumer ans parser and provifing the main entry point (blocking) to start the process
type Scheduler struct {
	*cron.Cron
	Resumer        Resumer
	CrontabParser  CrontabParser
	UpdatesEnabled bool
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

type cronReq struct {
	spec, command string
}

// Do runs blocking scheduler
func (s *Scheduler) Do(ctx context.Context) {
	s.resumeInterrupted()

	if s.UpdatesEnabled {
		log.Printf("[CRON] updater activated for %s", s.CrontabParser.String())
		go s.reload(ctx) // start background updater
	}
	if err := s.loadFromFileParser(); err != nil {
		log.Printf("[WARN] can't load crontab file, %v", err)
		return
	}
	s.Start()
	<-ctx.Done()
	<-s.Stop().Done()
}

// schedule makes new cron job from cronReq and adds to cron
func (s *Scheduler) schedule(r cronReq) error {
	log.Printf("[CRON] new cron, command %q", r.command)
	sched, e := cron.ParseStandard(r.spec)
	if e != nil {
		return errors.Wrapf(e, "can't parse %s", r.spec)
	}

	id := s.Schedule(sched, cron.FuncJob(func() {
		cmd, err := NewDayTemplate(time.Now()).Parse(r.command)
		if err != nil {
			log.Printf("[CRON] failed to schedule %s, %v", r.command, err)
			return
		}

		rfile, rerr := s.Resumer.OnStart(cmd)
		s.execute(cmd)
		if rerr == nil {
			if e := s.Resumer.OnFinish(rfile); e != nil {
				log.Printf("[CRON] failed to finish resumer for %s, %s", rfile, e)
			}
		}
		log.Printf("[CRON] next: %s, %q", s.Entries()[0].Schedule.Next(time.Now()).Format(time.RFC3339), r.command)
	}))

	log.Printf("[CRON] first: %s, %q (%v)", sched.Next(time.Now()).Format(time.RFC3339), r.command, id)
	return nil
}

func (s *Scheduler) loadFromFileParser() error {
	for _, entry := range s.Entries() {
		s.Remove(entry.ID)
	}

	jss, err := s.CrontabParser.List()
	if err != nil {
		return errors.Wrapf(err, "failed to load file %s", s.CrontabParser.String())
	}

	for _, js := range jss {
		req := cronReq{spec: js.Spec, command: js.Command}
		if err = s.schedule(req); err != nil {
			return errors.Wrapf(err, "can't add %s, %s", js.Spec, js.Command)
		}
	}
	return nil
}

func (s *Scheduler) execute(command string) {
	log.Printf("[CRON] executing: %q", command)
	cmd := exec.Command("sh", "-c", command) // nolint gosec

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[CRON] failed %q, %v", command, err)
	} else {
		log.Printf("[CRON] completed %q", command)
	}
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
			log.Printf("[CRON] jobs update detected, total %d jobs scheduled", len(jobs))
			if err = s.loadFromFileParser(); err != nil {
				log.Printf("[CRON] failed to update jobs, %v", err)
			}
		}
	}
}

func (s *Scheduler) resumeInterrupted() {
	cmds := s.Resumer.List()
	if len(cmds) > 0 {
		log.Printf("[CRON] interrupted commands detected - %+v", cmds)
	}

	go func() {
		for _, cmd := range cmds {
			s.execute(cmd.Command)
			if err := s.Resumer.OnFinish(cmd.Fname); err != nil {
				log.Printf("[WARN] failed to finish resumer for %s, %s", cmd.Fname, err)
			}
		}
	}()
}
