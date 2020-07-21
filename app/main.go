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
	"github.com/robfig/cron/v3"
	"github.com/umputun/go-flags"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/notify"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service"
)

var opts struct {
	CrontabFile  string `short:"f" long:"config" env:"CRONN_FILE" default:"crontab" description:"crontab file"`
	Resume       string `short:"r" long:"resume" env:"CRONN_RESUME" description:"auto-resume location"`
	UpdateEnable bool   `short:"u" long:"update" env:"CRONN_UPDATE" description:"auto-update mode"`
	JitterEnable bool   `short:"j" long:"jitter" env:"CRONN_JITTER" description:"up to 10s jitter"`
	LogEnabled   bool   `long:"log" env:"CRONN_LOG" description:"enable logging"`
	HostName     string `long:"host" env:"CRONN_HOST" description:"host name"`
	Dbg          bool   `long:"dbg" env:"CRONN_DEBUG" description:"debug mode"`

	Notify struct {
		Enabled     bool          `long:"enabled" env:"ENABLED" description:"enable email notifications"`
		Host        string        `long:"host" env:"HOST" description:"SMTP host"`
		Port        int           `long:"port" env:"PORT" description:"SMTP port"`
		Username    string        `long:"username" env:"USERNAME" description:"SMTP user name"`
		Password    string        `long:"password" env:"PASSWORD" description:"SMTP password"`
		TLS         bool          `long:"tls" env:"TLS" description:"enable TLS"`
		TimeOut     time.Duration `long:"timeout" env:"TIMEOUT" default:"10s" description:"SMTP TCP connection timeout"`
		From        string        `long:"from" env:"FROM" description:"SMTP from email"`
		To          []string      `long:"to" env:"TO" description:"SMTP to email(s)" env-delim:","`
		MaxLogLines int           `long:"max-log" env:"MAX_LOG" default:"100" description:"max number of log lines name"`
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
	cronService := service.Scheduler{
		Cron:           cron.New(),
		Resumer:        resumer.New(opts.Resume, opts.Resume != ""),
		CrontabParser:  crontab.New(opts.CrontabFile, 10*time.Second),
		UpdatesEnabled: opts.UpdateEnable,
		JitterEnabled:  opts.JitterEnable,
		Notifier:       makeNotifier(),
		HostName:       makeHostName(),
		MaxLogLines:    opts.Notify.MaxLogLines,
	}
	signals(cancel) // handle SIGQUIT and SIGTERM
	cronService.Do(ctx)
}

func makeNotifier() *notify.Email {
	if !opts.Notify.Enabled {
		return nil
	}
	from := opts.Notify.From
	if from == "" {
		from = "cronn@" + makeHostName()
	}
	return notify.NewEmailClient(notify.EmailParams{
		Host:         opts.Notify.Host,
		Port:         opts.Notify.Port,
		From:         from,
		To:           opts.Notify.To,
		TLS:          opts.Notify.TLS,
		SMTPUserName: opts.Notify.Username,
		SMTPPassword: opts.Notify.Password,
		TimeOut:      opts.Notify.TimeOut,
		ContentType:  "text/html",
	})
}

func makeHostName() string {
	if opts.HostName != "" {
		return opts.HostName
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
