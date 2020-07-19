package service

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/umputun/cronn/crontab"
	"github.com/umputun/cronn/day"
	"github.com/umputun/cronn/resumer"
)

type Scheduler struct {
	*cron.Cron
	resumer        Resumer
	updatesEnabled bool
}

// Resumer defines interface for resumer.Resumer providing auto-restart for failed jobs
type Resumer interface {
	OnStart(cmd string) (string, error)
	OnFinish(fname string) error
	List() (res []resumer.Cmd)
	String() string
}

type cronReq struct {
	rmr           Resumer
	spec, command string
}

func New(resumer Resumer, updates bool, args []string) (*Scheduler, error){
		res := Scheduler{Cron: cron.New(), resumer: resumer, updatesEnabled: updates}

		if args[1] == "-f" {
			ctab := crontab.New(os.Args[2], 10*time.Second)
			err = loadFromFileParser(c, ctab, rmr)
			if updates {
				// start background updater
				log.Printf("[CRON] updater activated for %s", ctab.String())
				go reload(context.TODO(), c, ctab, rmr)
			}
			return c, err
		}

		spec := os.Args[1]
		command := strings.Join(os.Args[2:], " ")
		req := cronReq{rmr: rmr, spec: spec, command: command}
		if e := addCron(c, req); e != nil {
			return nil, e
		}

		return &res, err
}

func (s *Scheduler) Do(ctx context.Context)  error){

}

// addCron makes new cron job from cronReq and adds to cron
func (s *Scheduler) addCron(r cronReq) error {
	log.Printf("[CRON] new cron, command %q, resumer %q", r.command, r.rmr)
	sched, e := cron.ParseStandard(r.spec)
	if e != nil {
		return errors.Wrapf(e, "can't parse %s", r.spec)
	}

	id := s.Schedule(sched, cron.FuncJob(func() {
		cmd, err := day.NewTemplate(time.Now()).Parse(r.command)
		if err != nil {
			log.Printf("[CRON] failed to schedule %s, %v", r.command, err)
			return
		}

		rfile, rerr := r.rmr.OnStart(cmd)
		s.execute(cmd)
		if rerr == nil {
			if e := r.rmr.OnFinish(rfile); e != nil {
				log.Printf("[CRON] failed to finish resumer for %s, %s", rfile, e)
			}
		}
		log.Printf("[CRON] next: %s, %q", s.Entries()[0].Schedule.Next(time.Now()).Format(time.RFC3339), r.command)
	}))

	log.Printf("[CRON] first: %s, %q (%v)", sched.Next(time.Now()).Format(time.RFC3339), r.command, id)
	return nil
}

func  (s *Scheduler) execute(command string) {
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