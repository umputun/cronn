package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
	"github.com/robfig/cron/v3"
	"github.com/umputun/go-flags"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/notify"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service"
)

var opts struct {
	CrontabFile  string `short:"f" long:"file" env:"CRONN_FILE" default:"crontab" description:"crontab file"`
	Command      string `short:"c" long:"command" env:"CRONN_COMMAND" description:"crontab single command"`
	Resume       string `short:"r" long:"resume" env:"CRONN_RESUME" description:"auto-resume location"`
	UpdateEnable bool   `short:"u" long:"update" env:"CRONN_UPDATE" description:"auto-update mode"`
	JitterEnable bool   `short:"j" long:"jitter" env:"CRONN_JITTER" description:"up to 10s jitter"`
	LogEnabled   bool   `long:"log" env:"CRONN_LOG" description:"enable logging"`
	Dbg          bool   `long:"dbg" env:"CRONN_DEBUG" description:"debug mode"`

	Repeater struct {
		Attempts int           `long:"attempts" env:"ATTEMPTS" default:"1" description:"how many time repeat failed job"`
		Duration time.Duration `long:"duration" env:"DURATION" default:"1s" description:"initial duration"`
		Factor   float64       `long:"factor" env:"FACTOR" default:"3" description:"backoff factor"`
		Jitter   bool          `long:"jitter" env:"JITTER" description:"jitter"`
	} `group:"repeater" namespace:"repeater" env-namespace:"CRONN_REPEATER"`

	Notify struct {
		EnabledError      bool          `long:"enabled-error" env:"ENABLED_ERROR" description:"enable email notifications on errors"`
		EnabledCompletion bool          `long:"enabled-complete" env:"ENABLED_COMPLETE" description:"enable completion notifications"`
		SMTPHost          string        `long:"smtp-host" env:"SMTP_HOST" description:"SMTP host"`
		SMTPPort          int           `long:"smtp-port" env:"SMTP_PORT" description:"SMTP port"`
		SMTPUsername      string        `long:"smtp-username" env:"SMTP_USERNAME" description:"SMTP user name"`
		SMTPPassword      string        `long:"smtp-password" env:"SMTP_PASSWORD" description:"SMTP password"`
		SMTPTLS           bool          `long:"smtp-tls" env:"SMTP_TLS" description:"enable SMTP TLS"`
		SMTPTimeOut       time.Duration `long:"smtp-timeout" env:"SMTP_TIMEOUT" default:"10s" description:"SMTP TCP connection timeout"`
		From              string        `long:"from" env:"FROM" description:"SMTP from email"`
		To                []string      `long:"to" env:"TO" description:"SMTP to email(s)" env-delim:","`
		MaxLogLines       int           `long:"max-log" env:"MAX_LOG" default:"100" description:"max number of log lines name"`
		HostName          string        `long:"host" env:"HOSTNAME" description:"host name running cronn"`
	} `group:"notify" namespace:"notify" env-namespace:"CRONN_NOTIFY"`
}

var revision = "unknown"

func main() {
	fmt.Printf("cronn %s\n", revision)

	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(2)
	}
	setupLogs(opts.LogEnabled, opts.Dbg)

	defer func() {
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())

	var crontabParser service.CrontabParser
	if opts.Command != "" {
		crontabParser = crontab.Single{Line: opts.Command}
	} else {
		crontabParser = crontab.New(opts.CrontabFile, 10*time.Second)
	}

	rptr := repeater.New(&strategy.Backoff{Repeats: opts.Repeater.Attempts, Duration: opts.Repeater.Duration,
		Factor: opts.Repeater.Factor, Jitter: opts.Repeater.Jitter})

	cronService := service.Scheduler{
		Cron:           cron.New(),
		Resumer:        resumer.New(opts.Resume, opts.Resume != ""),
		CrontabParser:  crontabParser,
		UpdatesEnabled: opts.UpdateEnable,
		JitterEnabled:  opts.JitterEnable,
		Repeater:       rptr,
		Notifier:       makeNotifier(),
		HostName:       makeHostName(),
		MaxLogLines:    opts.Notify.MaxLogLines,
	}
	signals(cancel) // handle SIGQUIT and SIGTERM
	cronService.Do(ctx)
}

func makeNotifier() *notify.Email {

	if !opts.Notify.EnabledError && !opts.Notify.EnabledCompletion {
		return nil
	}

	from := opts.Notify.From
	if from == "" {
		from = "cronn@" + makeHostName()
	}

	return notify.NewEmailClient(notify.EmailParams{
		Host:         opts.Notify.SMTPHost,
		Port:         opts.Notify.SMTPPort,
		From:         from,
		To:           opts.Notify.To,
		TLS:          opts.Notify.SMTPTLS,
		SMTPUserName: opts.Notify.SMTPUsername,
		SMTPPassword: opts.Notify.SMTPPassword,
		TimeOut:      opts.Notify.SMTPTimeOut,
		ContentType:  "text/html",
		OnError:      opts.Notify.EnabledError,
		OnCompletion: opts.Notify.EnabledCompletion,
	})
}

func makeHostName() string {
	if opts.Notify.HostName != "" {
		return opts.Notify.HostName
	}
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
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

func signals(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal)
	go func() {
		stacktrace := make([]byte, 8192)
		for sig := range sigChan {
			if sig == syscall.SIGQUIT { // catch SIGQUIT and print stack traces
				length := runtime.Stack(stacktrace, true)
				fmt.Println(string(stacktrace[:length]))
				continue
			}
			cancel() // terminate on SIGTERM
		}
	}()
	signal.Notify(sigChan, syscall.SIGQUIT, syscall.SIGTERM)
}
