package main

import (
	"context"
	"fmt"
	"io"
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
	"gopkg.in/natefinch/lumberjack.v2"

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
	DeDup        bool   `long:"dedup" env:"CRONN_DEDUP" description:"prevent duplicated jobs"`

	Repeater struct {
		Attempts int           `long:"attempts" env:"ATTEMPTS" default:"1" description:"how many time repeat failed job"`
		Duration time.Duration `long:"duration" env:"DURATION" default:"1s" description:"initial duration"`
		Factor   float64       `long:"factor" env:"FACTOR" default:"3" description:"backoff factor"`
		Jitter   bool          `long:"jitter" env:"JITTER" description:"jitter"`
	} `group:"repeater" namespace:"repeater" env-namespace:"CRONN_REPEATER"`

	Notify struct {
		EnabledError       bool          `long:"enabled-error" env:"ENABLED_ERROR" description:"enable email notifications on errors"`
		EnabledCompletion  bool          `long:"enabled-complete" env:"ENABLED_COMPLETE" description:"enable completion notifications"`
		ErrorTemplate      string        `long:"err-template" env:"ERR_TEMPLATE" description:"error template file"`
		CompletionTemplate string        `long:"complete-template" env:"COMPLET_TEMPLATE" description:"completion template file"`
		SMTPHost           string        `long:"smtp-host" env:"SMTP_HOST" description:"SMTP host"`
		SMTPPort           int           `long:"smtp-port" env:"SMTP_PORT" description:"SMTP port"`
		SMTPUsername       string        `long:"smtp-username" env:"SMTP_USERNAME" description:"SMTP user name"`
		SMTPPassword       string        `long:"smtp-password" env:"SMTP_PASSWORD" description:"SMTP password"`
		SMTPTLS            bool          `long:"smtp-tls" env:"SMTP_TLS" description:"enable SMTP TLS"`
		SMTPTimeOut        time.Duration `long:"smtp-timeout" env:"SMTP_TIMEOUT" default:"10s" description:"SMTP TCP connection timeout"`
		From               string        `long:"from" env:"FROM" description:"SMTP from email"`
		To                 []string      `long:"to" env:"TO" description:"SMTP to email(s)" env-delim:","`
		MaxLogLines        int           `long:"max-log" env:"MAX_LOG" default:"100" description:"max number of log lines name"`
		HostName           string        `long:"host" env:"HOSTNAME" description:"host name running cronn"`
	} `group:"notify" namespace:"notify" env-namespace:"CRONN_NOTIFY"`

	Log struct {
		Enabled         bool   `long:"enabled" env:"ENABLED" description:"enable logging"`
		Debug           bool   `long:"debug" env:"DEBUG" description:"debug mode"`
		Filename        string `long:"filename" env:"FILENAME" description:"file to write logs to. Log to stdout if not specified"`
		MaxSize         int    `long:"max-size" env:"MAX_SIZE" default:"100" description:"maximum size in megabytes of the log file before it gets rotated"`
		MaxAge          int    `long:"max-age" env:"MAX_AGE" default:"0" description:"maximum number of days to retain old log files"`
		MaxBackups      int    `long:"max-backups" env:"MAX_BACKUPS" default:"7" description:"maximum number of old log files to retain"`
		EnabledCompress bool   `long:"enabled-compress" env:"ENABLED_COMPRESS" description:"determines if the rotated log files should be compressed using gzip"`
	} `group:"log" namespace:"log" env-namespace:"CRONN_LOG"`
}

var revision = "unknown"

func main() {
	fmt.Printf("cronn %s\n", revision)

	if _, err := flags.Parse(&opts); err != nil {
		os.Exit(2)
	}
	stdout := setupLogs()

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
		Stdout:         stdout,
		DeDup:          service.NewDeDup(opts.DeDup),
	}
	signals(cancel) // handle SIGQUIT and SIGTERM
	cronService.Do(ctx)
}

func makeNotifier() *notify.Service {

	if !opts.Notify.EnabledError && !opts.Notify.EnabledCompletion {
		return nil
	}

	from := opts.Notify.From
	if from == "" {
		from = "cronn@" + makeHostName()
	}

	email := notify.NewEmailClient(notify.EmailParams{
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
	return notify.NewService(email, opts.Notify.ErrorTemplate, opts.Notify.CompletionTemplate)
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

func setupLogs() io.Writer {
	if !opts.Log.Enabled {
		log.Setup(log.Out(ioutil.Discard), log.Err(ioutil.Discard))
		return os.Stdout
	}

	log.Setup(log.Msec)

	if opts.Log.Debug {
		log.Setup(log.Debug, log.CallerFunc, log.CallerPkg, log.CallerFile)
	}

	if opts.Log.Filename != "" {
		fileLogger := &lumberjack.Logger{
			Filename:   opts.Log.Filename,
			MaxSize:    opts.Log.MaxSize,
			MaxBackups: opts.Log.MaxBackups,
			MaxAge:     opts.Log.MaxAge,
			Compress:   opts.Log.EnabledCompress,
			LocalTime:  true, // as log files have content in local time format
		}

		log.Setup(
			log.Out(fileLogger),
			log.Err(fileLogger),
		)

		return fileLogger
	}

	return os.Stdout
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
