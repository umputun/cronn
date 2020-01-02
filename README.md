# cronn - cli cron replacement and scheduler container

[![Build Status](https://github.com/umputun/cronn/workflows/build/badge.svg)](https://github.com/umputun/cronn/actions)


## Use cases

- Run any job with an easy to use cli scheduler
- Schedule tasks in containers (`cronn` can be used as CMD or ENTRYPOINT)
- Use `umputun/cronn` as a base image

The main reason to use this thing is super-paranoid nature of regular cron(d).
This replacement `cronn` supposed to run under regular user and won't loose env and won't jail job.
 Another good reason - support of day-templates and especially "last business day EOD" - `{{.YYYYMMDDEOD}}`


## Usage
 
- `cronn "30 23 * * 1-5" command arg1 arg2 ...`
- `cronn "@every 5s" "ls -la"`
- `cronn -f crontab`

Scheduling can be defined as:

- standard crontab syntax `minute, hour, day-of-month, month, day-of-week`
- @ syntax, like `@every 5m`, `@midnight`  and so on.

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

 
## Optional modes:

- Debug mode. Produces more debug info. Off by default. Controlled by env `DEBUG` (`yes` or `true` to turn it on)
- Auto-resume mode. Executes terminated task(s) on startup, controlled by env `CRONN_RESUME`
- Auto-update mode. Checks for changes in crontab file (`-f` mode only) and reloads updated jobs. Controlled by env `CRONN_UPDATE`

### Auto-Resume details

- each task creates a flag file named as `<ts>-<seq>.cronn` in `./resume` and removes this flag on completion.
- flag file's content is the command line for running task.
- usually it is not necessary to map `./resume` to host's FS as we don't want it to survive container recreation. However, tt will survive container's restart.
- at the start time `cronn` will discover all flag files and will execute them sequentially in case if multiple flag files discovered. Note: it won't block usual, scheduled tasks and it is possible to have initial (auto-resume) task running in parallel with regular tasks.


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
