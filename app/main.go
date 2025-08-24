package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	log "github.com/go-pkgz/lgr"
	ntf "github.com/go-pkgz/notify"
	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
	"github.com/robfig/cron/v3"
	"github.com/umputun/go-flags"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/umputun/cronn/app/conditions"
	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/notify"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service"
	"github.com/umputun/cronn/app/web"
)

var opts struct {
	CrontabFile         string        `short:"f" long:"file" env:"CRONN_FILE" default:"crontab" description:"crontab file"`
	Command             string        `short:"c" long:"command" env:"CRONN_COMMAND" description:"crontab single command"`
	Resume              string        `short:"r" long:"resume" env:"CRONN_RESUME" description:"auto-resume location"`
	ResumeConcur        int           `long:"resume-concur" env:"CRONN_RESUME_CONCUR" default:"1" description:"auto-resume concurrency level"`
	UpdateEnable        bool          `short:"u" long:"update" env:"CRONN_UPDATE" description:"auto-update mode"`
	UpdateInterval      time.Duration `long:"update-interval" env:"CRONN_UPDATE_INTERVAL" default:"10s" description:"auto-update interval"`
	JitterEnable        bool          `short:"j" long:"jitter" env:"CRONN_JITTER" description:"enable jitter"`
	JitterDuration      time.Duration `long:"jitter-duration" env:"CRONN_JITTER_DURATION" default:"10s" description:"jitter duration"`
	DeDup               bool          `long:"dedup" env:"CRONN_DEDUP" description:"prevent duplicated jobs"`
	MaxConcurrentChecks int           `long:"max-concurrent-checks" env:"CRONN_MAX_CONCURRENT_CHECKS" default:"10" description:"max concurrent condition checks"`

	Repeater struct {
		Attempts int           `long:"attempts" env:"ATTEMPTS" default:"1" description:"how many time repeat failed job"`
		Duration time.Duration `long:"duration" env:"DURATION" default:"1s" description:"initial duration"`
		Factor   float64       `long:"factor" env:"FACTOR" default:"3" description:"backoff factor"`
		Jitter   bool          `long:"jitter" env:"JITTER" description:"jitter"`
	} `group:"repeater" namespace:"repeater" env-namespace:"CRONN_REPEATER"`

	Notify struct {
		EnabledError         bool          `long:"enabled-error" env:"ENABLED_ERROR" description:"enable email notifications on errors"`
		EnabledCompletion    bool          `long:"enabled-complete" env:"ENABLED_COMPLETE" description:"enable completion notifications"`
		ErrorTemplate        string        `long:"err-template" env:"ERR_TEMPLATE" description:"error template file"`
		CompletionTemplate   string        `long:"complete-template" env:"COMPLET_TEMPLATE" description:"completion template file"`
		SMTPHost             string        `long:"smtp-host" env:"SMTP_HOST" description:"SMTP host"`
		SMTPPort             int           `long:"smtp-port" env:"SMTP_PORT" description:"SMTP port"`
		SMTPUsername         string        `long:"smtp-username" env:"SMTP_USERNAME" description:"SMTP user name"`
		SMTPPassword         string        `long:"smtp-password" env:"SMTP_PASSWORD" description:"SMTP password"`
		SMTPTLS              bool          `long:"smtp-tls" env:"SMTP_TLS" description:"enable SMTP TLS"`
		SMTPStartTLS         bool          `long:"smtp-starttls" env:"SMTP_STARTTLS" description:"enable SMTP StartTLS"`
		SMTPTimeOut          time.Duration `long:"smtp-timeout" env:"SMTP_TIMEOUT" default:"10s" description:"SMTP TCP connection timeout"`
		FromEmail            string        `long:"from" env:"FROM" description:"SMTP from email"`
		ToEmails             []string      `long:"to" env:"TO" description:"SMTP to email(s)" env-delim:","`
		MaxLogLines          int           `long:"max-log" env:"MAX_LOG" default:"100" description:"max number of log lines name"`
		HostName             string        `long:"host" env:"HOSTNAME" description:"host name running cronn"`
		SlackToken           string        `long:"slack-token" env:"SLACK_TOKEN" description:"API token for the Slack bot"`
		SlackChannels        []string      `long:"slack-channels" env:"SLACK_CHANNELS" description:"List of Slack channels the bot will post messages to" env-delim:","`
		TelegramToken        string        `long:"telegram-token" env:"TELEGRAM_TOKEN" description:"API token for the Telegram bot"`
		TelegramDestinations []string      `long:"telegram-destinations" env:"TELEGRAM_DESTINATIONS" description:"List of Telegram chat IDs the bot will post messages to" env-delim:","`
		WebhookURLs          []string      `long:"webhook-urls" env:"WEBHOOK_URLS" description:"List of webhook URLs the bot will post messages to" env-delim:","`
		TimeOut              time.Duration `long:"timeout" env:"TIMEOUT" default:"10s" description:"timeout for notification"`
	} `group:"notify" namespace:"notify" env-namespace:"CRONN_NOTIFY"`

	Log struct {
		Enabled         bool   `long:"enabled" env:"ENABLED" description:"enable logging"`
		Debug           bool   `long:"debug" env:"DEBUG" description:"debug mode"`
		EnablePrefix    bool   `long:"prefix" env:"PREFIX" description:"enable log prefix with current command"`
		Filename        string `long:"filename" env:"FILENAME" description:"file to write logs to. Log to stdout if not specified"`
		MaxSize         int    `long:"max-size" env:"MAX_SIZE" default:"100" description:"maximum size in megabytes of the log file before it gets rotated"`
		MaxAge          int    `long:"max-age" env:"MAX_AGE" default:"0" description:"maximum number of days to retain old log files"`
		MaxBackups      int    `long:"max-backups" env:"MAX_BACKUPS" default:"7" description:"maximum number of old log files to retain"`
		EnabledCompress bool   `long:"enabled-compress" env:"ENABLED_COMPRESS" description:"determines if the rotated log files should be compressed using gzip"`
	} `group:"log" namespace:"log" env-namespace:"CRONN_LOG"`

	Web struct {
		Enabled        bool          `long:"enabled" env:"ENABLED" description:"enable web UI"`
		Address        string        `long:"address" env:"ADDRESS" default:":8080" description:"web UI address"`
		DBPath         string        `long:"db-path" env:"DB_PATH" default:"cronn.db" description:"path to SQLite database"`
		UpdateInterval time.Duration `long:"update-interval" env:"UPDATE_INTERVAL" default:"30s" description:"interval to sync crontab file"`
	} `group:"web" namespace:"web" env-namespace:"CRONN_WEB"`
}

var revision = "unknown"

func main() {
	fmt.Printf("cronn %s\n", revision)

	p := flags.NewParser(&opts, flags.PassDoubleDash|flags.HelpFlag)
	if _, err := p.Parse(); err != nil {
		if err.(*flags.Error).Type != flags.ErrHelp {
			fmt.Printf("%v\n", err)
			os.Exit(1)
		}
		p.WriteHelp(os.Stderr)
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
	hupCh := signals(cancel) // handle SIGQUIT and SIGTERM

	updateInterval := time.Duration(math.MaxInt64) // very long interval, effectively disabling automatic refresh
	if opts.UpdateEnable && opts.UpdateInterval > 0 {
		updateInterval = opts.UpdateInterval
	} else {
		log.Printf("[INFO] auto-update disabled")
	}

	var crontabParser service.CrontabParser
	if opts.Command != "" {
		crontabParser = crontab.NewSingle(opts.Command)
	} else {
		crontabParser = crontab.New(opts.CrontabFile, updateInterval, hupCh)
	}

	rptr := repeater.New(&strategy.Backoff{Repeats: opts.Repeater.Attempts, Duration: opts.Repeater.Duration,
		Factor: opts.Repeater.Factor, Jitter: opts.Repeater.Jitter})

	var jitterDuration time.Duration
	if opts.JitterEnable {
		jitterDuration = opts.JitterDuration
	}

	// initialize web server if enabled
	var eventHandler service.JobEventHandler
	if opts.Web.Enabled {
		cfg := web.Config{
			CrontabFile:    opts.CrontabFile,
			DBPath:         opts.Web.DBPath,
			UpdateInterval: opts.Web.UpdateInterval,
			Version:        revision,
		}
		webServer, err := web.New(cfg)
		if err != nil {
			log.Printf("[ERROR] failed to create web server: %v", err)
			os.Exit(1)
		}
		eventHandler = webServer // web.Server implements JobEventHandler

		// start web server in background
		go func() {
			if err := webServer.Run(ctx, opts.Web.Address); err != nil {
				log.Printf("[ERROR] web server failed: %v", err)
			}
		}()
		log.Printf("[INFO] web UI enabled on %s", opts.Web.Address)
	}

	cronService := service.Scheduler{
		Cron:              cron.New(),
		Resumer:           resumer.New(opts.Resume, opts.Resume != ""),
		ResumeConcurrency: opts.ResumeConcur,
		CrontabParser:     crontabParser,
		UpdatesEnabled:    opts.UpdateEnable,
		Jitter:            jitterDuration,
		Repeater:          rptr,
		Notifier:          makeNotifier(),
		ConditionChecker:  conditions.NewChecker(opts.MaxConcurrentChecks),
		HostName:          makeHostName(),
		MaxLogLines:       opts.Notify.MaxLogLines,
		EnableLogPrefix:   opts.Log.EnablePrefix,
		Stdout:            stdout,
		DeDup:             service.NewDeDup(opts.DeDup),
		NotifyTimeout:     opts.Notify.TimeOut,
		JobEventHandler:   eventHandler,
	}

	cronService.RepeaterDefaults.Attempts = opts.Repeater.Attempts
	cronService.RepeaterDefaults.Duration = opts.Repeater.Duration
	cronService.RepeaterDefaults.Factor = opts.Repeater.Factor
	cronService.RepeaterDefaults.Jitter = opts.Repeater.Jitter

	cronService.Do(ctx)
}

func makeNotifier() *notify.Service {
	if !opts.Notify.EnabledError && !opts.Notify.EnabledCompletion {
		return nil
	}
	if opts.Notify.FromEmail == "" {
		opts.Notify.FromEmail = "cronn@" + makeHostName()
	}
	return notify.NewService(
		notify.Params{
			EnabledError:       opts.Notify.EnabledError,
			EnabledCompletion:  opts.Notify.EnabledCompletion,
			ErrorTemplate:      opts.Notify.ErrorTemplate,
			CompletionTemplate: opts.Notify.CompletionTemplate,
		},
		notify.SendersParams{
			SMTPParams: ntf.SMTPParams{
				Host:        opts.Notify.SMTPHost,
				Port:        opts.Notify.SMTPPort,
				TLS:         opts.Notify.SMTPTLS,
				StartTLS:    opts.Notify.SMTPStartTLS,
				Username:    opts.Notify.SMTPUsername,
				Password:    opts.Notify.SMTPPassword,
				TimeOut:     opts.Notify.SMTPTimeOut,
				ContentType: "text/html",
				Charset:     "utf-8",
			},
			FromEmail:            opts.Notify.FromEmail,
			ToEmails:             opts.Notify.ToEmails,
			SlackToken:           opts.Notify.SlackToken,
			SlackChannels:        opts.Notify.SlackChannels,
			TelegramToken:        opts.Notify.TelegramToken,
			TelegramDestinations: opts.Notify.TelegramDestinations,
			WebhookURLs:          opts.Notify.WebhookURLs,
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

func setupLogs() io.Writer {
	if !opts.Log.Enabled {
		log.Setup(log.Out(io.Discard), log.Err(io.Discard))
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

func signals(cancel context.CancelFunc) (hupCh chan struct{}) {
	sigChan := make(chan os.Signal, 1)
	hupCh = make(chan struct{})
	go func() {
		stacktrace := make([]byte, 8192)
		for sig := range sigChan {
			switch sig {
			case syscall.SIGQUIT: // catch SIGQUIT and print stack traces
				length := runtime.Stack(stacktrace, true)
				fmt.Println(string(stacktrace[:length]))
			case syscall.SIGHUP: // reload crontab file on SIGHUP
				hupCh <- struct{}{}
			case syscall.SIGTERM: // terminate on SIGTERM
				cancel()
			}
		}
	}()
	signal.Notify(sigChan, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP)
	return hupCh
}
