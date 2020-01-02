package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/umputun/cronn/day"

	"github.com/umputun/cronn/crontab"
	"github.com/umputun/cronn/resumer"
)

var revision = "unknown"

// Resumer defines interface for resumer.Resumer providing auto-restart for failed jobs
type Resumer interface {
	OnStart(cmd string) (string, error)
	OnFinish(fname string) error
	List() (res []resumer.Cmd)
	String() string
}

// accepts "* * * * *" cmd args...
// or -f crontab
// ENV: CRONN_RESUME, CRONN_UPDATE, CRONN_LOG, DEBUG
func main() {
	fmt.Printf("cronn %s\n", revision)

	logEnabled := strings.EqualFold(os.Getenv("CRONN_LOG"), "true") || strings.EqualFold(os.Getenv("CRONN_LOG"), "yes")
	logDbg := strings.EqualFold(os.Getenv("DEBUG"), "true") || strings.EqualFold(os.Getenv("DEBUG"), "yes")
	setupLogs(logEnabled, logDbg)

	if len(os.Args) < 3 {
		log.Fatalf("[CRON] expects at least 2 arguments, got %d (%q)", len(os.Args), strings.Join(os.Args[1:], ","))
	}

	defer func() {
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}
	}()

	croner, err := create()
	if err != nil {
		log.Fatalf("[ERROR] failed to create cron, %v", err) // nolint
	}
	signals() // handle SIGQUIT

	go start(croner)

	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh, syscall.SIGINT, syscall.SIGTERM)
	<-termCh
	stop(croner)
}

func create() (cr *cron.Cron, err error) {
	resumeEnabled := strings.EqualFold(os.Getenv("CRONN_RESUME"), "true") || strings.EqualFold(os.Getenv("CRONN_RESUME"), "yes")
	log.Printf("[CRON] auto-resume = %v", resumeEnabled)

	c := cron.New()
	rmr := resumer.New("/srv/var", resumeEnabled)
	resumeInterrupted(rmr)

	if os.Args[1] == "-f" {
		ctab := crontab.New(os.Args[2], 10*time.Second)
		err = loadFromFileParser(c, ctab, rmr)
		updEnabled := strings.EqualFold(os.Getenv("CRONN_UPDATE"), "true") || strings.EqualFold(os.Getenv("CRONN_UPDATE"), "yes")
		if updEnabled {
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

	return c, err
}

// reload runs blocking loop reacting on updates in crontab file and reloading jobs
func reload(ctx context.Context, c *cron.Cron, p *crontab.Parser, rmr Resumer) {
	ch, err := p.Changes(ctx)
	if err != nil {
		return
	}

	for jobs := range ch {
		log.Printf("[CRON] jobs update detected, total %d jobs scheduled", len(jobs))
		if err = loadFromFileParser(c, p, rmr); err != nil {
			log.Printf("[CRON] failed to update jobs, %v", err)
		}
	}
}

type cronReq struct {
	rmr           Resumer
	spec, command string
}

// addCron makes new cron job from cronReq and adds to cron
func addCron(c *cron.Cron, r cronReq) error {
	log.Printf("[CRON] new cron, command %q, resumer %q", r.command, r.rmr)
	sched, e := cron.ParseStandard(r.spec)
	if e != nil {
		return e
	}

	c.Schedule(sched, cron.FuncJob(func() {

		cmd, err := day.NewTemplate(time.Now()).Parse(r.command)
		if err != nil {
			log.Printf("[CRON] failed to schedule %s, %v", r.command, err)
			return
		}

		rfile, rerr := r.rmr.OnStart(cmd)
		execute(cmd)
		if rerr == nil {
			if e := r.rmr.OnFinish(rfile); e != nil {
				log.Printf("[CRON] failed to finish resumer for %s, %s", rfile, e)
			}
		}
		log.Printf("[CRON] next: %s, %q", c.Entries()[0].Schedule.Next(time.Now()).Format(time.RFC3339), r.command)
	}))

	log.Printf("[CRON] first: %s, %q", sched.Next(time.Now()).Format(time.RFC3339), r.command)
	return nil
}

func loadFromFileParser(cr *cron.Cron, ctab *crontab.Parser, rmr Resumer) error {
	for _, entry := range cr.Entries() {
		cr.Remove(entry.ID)
	}

	jss, err := ctab.List()
	if err != nil {
		return errors.Wrapf(err, "failed to load file %s", ctab.String())
	}

	for _, js := range jss {
		req := cronReq{rmr: rmr, spec: js.Spec, command: js.Command}
		if err = addCron(cr, req); err != nil {
			return errors.Wrapf(err, "can't add %s, %s", js.Spec, js.Command)
		}
	}
	return nil
}

func resumeInterrupted(rmr Resumer) {
	cmds := rmr.List()
	if len(cmds) > 0 {
		log.Printf("[CRON] interrupted commands detected - %+v", cmds)
	}

	go func() {
		for _, cmd := range cmds {
			execute(cmd.Command)
			if err := rmr.OnFinish(cmd.Fname); err != nil {
				log.Printf("[WARN] failed to finish resumer for %s, %s", cmd.Fname, err)
			}
		}
	}()
}

func start(c *cron.Cron) {
	log.Print("[CRON] start cron service")
	c.Start()
}

func stop(c *cron.Cron) {
	log.Print("[CRON] stop cron service")
	<-c.Stop().Done()
	log.Print("[CRON] stop completed")
	os.Exit(0)
}

func execute(command string) {
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

func setupLogs(enabled, dbg bool) {
	if !enabled {
		log.Setup(log.Out(ioutil.Discard), log.Err(ioutil.Discard))
		return
	}

	if dbg {
		log.Setup(log.Debug, log.Msec, log.CallerFunc, log.CallerPkg, log.CallerFile)
		return
	}
	log.Setup(log.Msec)
}

func signals() {
	// catch SIGQUIT and print stack traces
	sigChan := make(chan os.Signal)
	go func() {
		stacktrace := make([]byte, 8192)
		for range sigChan {
			length := runtime.Stack(stacktrace, true)
			fmt.Println(string(stacktrace[:length]))
		}
	}()
	signal.Notify(sigChan, syscall.SIGQUIT)
}
