# cronn - cli cron replacement and scheduler container

<div align="center">
  <img class="logo" src="https://raw.githubusercontent.com/umputun/cronn/master/site/docs/logo.png" width="355px" height="142px" alt="Cronn | cli cron replacement and scheduler container"/>
</div>


[![Build Status](https://github.com/umputun/cronn/workflows/build/badge.svg)](https://github.com/umputun/cronn/actions) [![Coverage Status](https://coveralls.io/repos/github/umputun/cronn/badge.svg?branch=master)](https://coveralls.io/github/umputun/cronn?branch=master)

Cronn is a crontab jobs scheduler with some nice extras. It allows to run commands on specified time intervals and can be used directly as well as from a container.

## Use cases

- Run any job with easy to use cli scheduler
- Schedule tasks in containers (`cronn` can be used as CMD or ENTRYPOINT)
- Use `umputun/cronn` as a base image

In addition `cronn` provides:

- Both single-job scheduler and more traditional crontab file with multiple jobs
- Runs as an ordinary process or the entry point of a container
- Supports wide range of date templates 
- Optional email notification on failed or/and passed jobs
- Optional jitter adding a random delay prior to execution of a job
- Automatic resume (restart) of jobs in cronn service or container failed unexpectedly
- Reload crontab file on changes
- Optional repeater for failed jobs
- Optional de-duplication preventing the same jobs to run in parallel
- Rotated logs

## Basic usage
 
- `cronn -c "30 23 * * 1-5 command arg1 arg2 ..."` - runs `command` with `arg1` and `arg2` every day at 23:30
- `cronn -c "@every 5s ls -la"` - runs `ls -la` every 5 seconds
- `cronn -f crontab` - runs jobs from the `crontab` file

Scheduling can be defined as:

- standard 5-parts crontab syntax `minute, hour, day-of-month, month, day-of-week`
- @syntax (descriptors), like `@every 5m`, `@midnight`, `@daily`, `@yearly`, `@annually`, `@monthly`, `@weekly` and `@hourly`.

Cronn also understands various day-realted templates evaluated at the time of job's execution:

- `{{.YYYYMMDD}}` - current day in local TZ
- `{{.YYYY}}` - current year
- `{{.YYYYMM}}` - year and month
- `{{.YY}}` - current year (short form)
- `{{.MM}}` - current month
- `{{.DD}}` - current day
- `{{.ISODATE}` - day-time formatted as `2006-01-02T00:00:00.000Z` (RFC3339)
- `{{.UNIX}}` - unix timestamp (in seconds)
- `{{.UNIXMSEC}}` - unix timestamp (in milliseconds)


Templates can be passed in command line or crontab file and will be evaluated and replaced at the moment 
cronn executes the command. For example `cronn "0 0 * * 1-5" echo {{.YYYYMMDD}}` will print the current date every 
weekday on midnight.
 
## Optional modes

- Logging mode
- Debug mode: produces more debug info
- Auto-resume mode: executes terminated task(s) on startup.
- Auto-update mode: checks for changes in crontab file (`-f` mode only) and reloads updated jobs.

### Auto-Resume

- auto-resume mode is disabled by default. To enable it, add `--resume=<directory>` to the command line or set `$CRONN_RESUME` environment variable.
- each task creates a flag-file named as `<ts>-<seq>.cronn` in `$CRONN_RESUME` directory and removes this flag on completion.
- flag file's content is the command line for the running job.
- at the start time `cronn` will discover all flag files and will execute them sequentially in case if multiple flag files discovered. 
Note: it won't block usual, scheduled tasks and it is possible to have initial (auto-resume) task running in parallel with regular tasks.
- old resume files (>=24h) ignored 
- usually it is not necessary to map `$CRONN_RESUME` location to host's FS as we don't want it to survive container recreation. 
However, it will survive container's restart.
- by default resume jobs executed sequentially, but can be configured to run in parallel with `--resume-concur=2..N` option.

### Repeater

- Optional repeater retries failed job multiple times. It uses backoff strategy with an exponential interval. 
- Duration interval goes in steps with `last * math.Pow(factor, attempt)` increments. 
- Optional jitter randomizes intervals a little bit. Factor = 1 effectively makes this strategy fixed with `duration` delay.

### Deduplication

Optional de-duplication prevents the same jobs to run in parallel. Jobs are identified by their command line and scheduling. 
By default this option is off. To enable it, add `--dedup` to the command line or set `$CRONN_DEDUP` environment variable.

### Notifications

Optional notifications are sent to the specified email address on job failure or completion. User can define custom templates for notifications messages with `--notify.complere-template`  and `--notify.err-template` options.  The following templates are available:

- `{{.Command}}` - the command with arguments
- `{{.Spec}}` - the crontab specification
- `{{.Host}}` - the hostname of the machine where the job is running
- `{{.TS}}` - the time-stamp of event
- `{{.Error}}` - the error message if any

### Logging

- In order to allow logging, add `--log.enabled` to the command line or set `$CRONN_LOG_ENABLED` environment variable.
- By default logs send to stdout and errors to stderr.
- To specify log file, add `--log.filename=<path>` to the command line or set `$CRONN_LOG_FILE` environment variable.
- Files are rotated every according to `--log.max-size=<size>`, `--log.max-files=<files>` and `--log.max-age=<days>` options.
- The maximum number of rotated backup files set with `--log.max-backups=<files>`.
- To turn compression on, add `--log.enabled-compress` to the command line or set `$CRONN_LOG_ENABLED_COMPRESS` environment variable.

## Things to know

1. CTRL-C won't kill `cronn` process right away but will wait till job(s) completion
2. each job runs as `sh -c cmd args...` to allow use of env and all other shell-related goodies

## All application Options

```
  -f, --file=                     crontab file (default: crontab) [$CRONN_FILE]
  -c, --command=                  crontab single command [$CRONN_COMMAND]
  -r, --resume=                   auto-resume location [$CRONN_RESUME]
      --resume-concur=            auto-resume concurrency level (default: 1) [$CRONN_RESUME_CONCUR]
  -u, --update                    auto-update mode [$CRONN_UPDATE]
  -j, --jitter                    enable jitter [$CRONN_JITTER]
      --jitter-duration=          jitter duration (default: 10s) [$CRONN_JITTER_DURATION]
      --dedup                     prevent duplicated jobs [$CRONN_DEDUP]

repeater:
      --repeater.attempts=        how many time repeat failed job (default: 1) [$CRONN_REPEATER_ATTEMPTS]
      --repeater.duration=        initial duration (default: 1s) [$CRONN_REPEATER_DURATION]
      --repeater.factor=          backoff factor (default: 3) [$CRONN_REPEATER_FACTOR]
      --repeater.jitter           jitter [$CRONN_REPEATER_JITTER]

notify:
      --notify.enabled-error      enable email notifications on errors [$CRONN_NOTIFY_ENABLED_ERROR]
      --notify.enabled-complete   enable completion notifications [$CRONN_NOTIFY_ENABLED_COMPLETE]
      --notify.err-template=      error template file [$CRONN_NOTIFY_ERR_TEMPLATE]
      --notify.complete-template= completion template file [$CRONN_NOTIFY_COMPLET_TEMPLATE]
      --notify.smtp-host=         SMTP host [$CRONN_NOTIFY_SMTP_HOST]
      --notify.smtp-port=         SMTP port [$CRONN_NOTIFY_SMTP_PORT]
      --notify.smtp-username=     SMTP user name [$CRONN_NOTIFY_SMTP_USERNAME]
      --notify.smtp-password=     SMTP password [$CRONN_NOTIFY_SMTP_PASSWORD]
      --notify.smtp-tls           enable SMTP TLS [$CRONN_NOTIFY_SMTP_TLS]
      --notify.smtp-timeout=      SMTP TCP connection timeout (default: 10s) [$CRONN_NOTIFY_SMTP_TIMEOUT]
      --notify.from=              SMTP from email [$CRONN_NOTIFY_FROM]
      --notify.to=                SMTP to email(s) [$CRONN_NOTIFY_TO]
      --notify.max-log=           max number of log lines name (default: 100) [$CRONN_NOTIFY_MAX_LOG]
      --notify.host=              host name running cronn [$CRONN_NOTIFY_HOSTNAME]

log:
      --log.enabled               enable logging [$CRONN_LOG_ENABLED]
      --log.debug                 debug mode [$CRONN_LOG_DEBUG]
      --log.prefix                enable log prefix with current command [$CRONN_LOG_PREFIX]
      --log.filename=             file to write logs to. Log to stdout if not specified [$CRONN_LOG_FILENAME]
      --log.max-size=             maximum size in megabytes of the log file before it gets rotated (default: 100) [$CRONN_LOG_MAX_SIZE]
      --log.max-age=              maximum number of days to retain old log files (default: 0) [$CRONN_LOG_MAX_AGE]
      --log.max-backups=          maximum number of old log files to retain (default: 7) [$CRONN_LOG_MAX_BACKUPS]
      --log.enabled-compress      determines if the rotated log files should be compressed using gzip [$CRONN_LOG_ENABLED_COMPRESS]

Help Options:
  -h, --help                      Show this help message

```

## Credits

- [robfig/cron](https://github.com/robfig/cron) for parsing and scheduling.
- [pkg/errors](https://github.com/pkg/errors) for errors wrapping and reporting.
- [jessevdk/go-flags](https://github.com/jessevdk/go-flags) for cli parameters parser.
- [lumberjack](https://gopkg.in/natefinch/lumberjack.v2) for rotated logs
