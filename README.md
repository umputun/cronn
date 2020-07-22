# cronn - cli cron replacement and scheduler container

[![Build Status](https://github.com/umputun/cronn/workflows/build/badge.svg)](https://github.com/umputun/cronn/actions)


## Use cases

- Run any job with easy to use cli scheduler
- Schedule tasks in containers (`cronn` can be used as CMD or ENTRYPOINT)
- Use `umputun/cronn` as a base image

In addition `cronn` provides:

- Runs as an ordinary process or the entry point of container
- Supports wide range of date templates 
- Optional email notification on failed or/and passed jobs
- Optional jitter adding a random delay prior to execution of the job
- Automatic restart of jobs in cronn (or container) failed unexpectedly
- Reload crontab file on changes
- Optional repeater for failed jobs

## Basic usage
 
- `cronn -c "30 23 * * 1-5 command arg1 arg2 ..."`
- `cronn -c "@every 5s ls -la"`
- `cronn -f crontab`

Scheduling can be defined as:

- standard 5-parts crontab syntax `minute, hour, day-of-month, month, day-of-week`
- @ syntax, like `@every 5m`, `@midnight` and so on.

If `-f` defined it gets **standard crontab** formatted file only. 

Cronn also understands day templates:

- `{{.YYYYMMDD}}` - current day in local TZ
- `{{.YYYY}}` - current year
- `{{.YYYYMM}}` - year and month
- `{{.ISODATE}` - day-time (local TZ) formatted as `2006-01-02T00:00:00.000Z`
- `{{.YYYYMMDDEOD}}` - current EOD day in local TZ. If hour < 17:00 (default) will use prev. day

Templates can be passed in command line or crontab file and will be evaluated and replaced at the moment 
cronn executes a command. For example `cronn "0 0 * * 1-5" echo {{.YYYYMMDD}}` will print the current date every 
weekday on midnight. 

## Application Options

```
     -f, --config=                  crontab file (default: crontab) [$CRONN_FILE]
     -r, --resume=                  auto-resume location [$CRONN_RESUME]
     -u, --update                   auto-update mode [$CRONN_UPDATE]
     -j, --jitter                   up to 10s jitter [$CRONN_JITTER]
         --log                      enable logging [$CRONN_LOG]
         --host=                    host name [$CRONN_HOST]
         --dbg                      debug mode [$CRONN_DEBUG]
   
   notify:
         --notify.enabled-error     enable email notifications on errors [$CRONN_NOTIFY_ENABLED_ERROR]
         --notify.enabled-complete  enable completion notifications [$CRONN_NOTIFY_ENABLED_COMPLETE]
         --notify.host=             SMTP host [$CRONN_NOTIFY_HOST]
         --notify.port=             SMTP port [$CRONN_NOTIFY_PORT]
         --notify.username=         SMTP user name [$CRONN_NOTIFY_USERNAME]
         --notify.password=         SMTP password [$CRONN_NOTIFY_PASSWORD]
         --notify.tls               enable TLS [$CRONN_NOTIFY_TLS]
         --notify.timeout=          SMTP TCP connection timeout (default: 10s) [$CRONN_NOTIFY_TIMEOUT]
         --notify.from=             SMTP from email [$CRONN_NOTIFY_FROM]
         --notify.to=               SMTP to email(s) [$CRONN_NOTIFY_TO]
         --notify.max-log=          max number of log lines name (default: 100) [$CRONN_NOTIFY_MAX_LOG]
```
 
## Optional modes:

- Debug mode. Produces more debug info. Off by default. Controlled by env `DEBUG` (`yes` or `true` to turn it on)
- Auto-resume mode. Executes terminated task(s) on startup, controlled by env `CRONN_RESUME`
- Auto-update mode. Checks for changes in crontab file (`-f` mode only) and reloads updated jobs. Controlled by env `CRONN_UPDATE`

### Auto-Resume details

- each task creates a flag file named as `<ts>-<seq>.cronn` in `$CRONN_RESUME` and removes this flag on completion.
- flag file's content is the command line for running task.
- usually it is not necessary to map `$CRONN_RESUME` to host's FS as we don't want it to survive container recreation. 
However, it will survive container's restart.
- at the start time `cronn` will discover all flag files and will execute them sequentially in case if multiple flag files discovered. Note: it won't block usual, scheduled tasks and it is possible to have initial (auto-resume) task running in parallel with regular tasks.
- old resume files (>=24h) ignored 


## Examples

### command line usage

    ```
    > cronn "@every 5s" "ls -la"

    2016/12/11 18:29:07 [CRON]  new cron: @every 5s
    2016/12/11 18:29:07 [CRON]  next: 2016-12-11T18:29:12-06:00
    2016/12/11 18:29:07 [CRON]  start cron service
    2016/12/11 18:29:12 [CRON]  executing: ls -la
    total 8
    drwxr-xr-x   3 umputun  staff   102 Dec 11 17:16 .
    drwxr-xr-x  10 umputun  staff   340 Dec 11 18:10 ..
    -rw-r--r--   1 umputun  staff  2067 Dec 11 18:24 main.go
    2016/12/11 18:29:12 [CRON]  next: 2016-12-11T18:29:17-06:00
    ```

## Things to know

1. @syntax won't work in `-f` mode, but regular (full, 5-parts) "30 23 * * 1-5" only
2. CTRL-C won't kill `cronn` process right away but will wait till job(s) completion
3. each job runs as `sh -p cmd args` to allow use of env and all other shell-related goodies
