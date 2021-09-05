package service

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service/mocks"
)

func TestScheduler_Do(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	scCalls := 0
	cr := &mocks.CronMock{
		EntriesFunc: func() []cron.Entry { return []cron.Entry{{}, {}, {}} },
		RemoveFunc:  func(id cron.EntryID) {},
		StartFunc:   func() {},
		StopFunc:    func() context.Context { return ctx },
		ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
			scCalls++
			return cron.EntryID(scCalls)
		},
	}

	resmr := &mocks.ResumerMock{ListFunc: func() []resumer.Cmd { return nil }}

	parser := &mocks.CrontabParserMock{
		ListFunc: func() ([]crontab.JobSpec, error) {
			return []crontab.JobSpec{{Spec: "1 * * * *", Command: "test1"}, {Spec: "2 * * * *", Command: "test2"}}, nil
		},
	}

	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: false,
	}

	svc.Do(ctx)

	assert.Equal(t, 1, len(resmr.ListCalls()))

	assert.Equal(t, 1, len(cr.EntriesCalls()))
	assert.Equal(t, 3, len(cr.RemoveCalls()))
	assert.Equal(t, 1, len(cr.StartCalls()))
	assert.Equal(t, 1, len(cr.StopCalls()))

	assert.Equal(t, 1, len(parser.ListCalls()))
}

func TestScheduler_DoIntegration(t *testing.T) {
	t.Skip()
	out := bytes.NewBuffer(nil)
	cr := cron.New()
	parser := crontab.New("testfiles/crontab", time.Minute)
	res := resumer.New("/tmp", false)

	notif := &mocks.NotifierMock{
		SendFunc:           func(subj string, text string) error { return nil },
		IsOnErrorFunc:      func() bool { return true },
		IsOnCompletionFunc: func() bool { return false },
		MakeErrorHTMLFunc: func(spec string, command string, errorLog string) (string, error) {
			return "blah error", nil
		},
	}
	rep := &mocks.RepeaterMock{DoFunc: func(ctx context.Context, fun func() error, errors ...error) error {
		return fun()
	}}

	svc := Scheduler{
		Cron:           cr,
		Resumer:        res,
		CrontabParser:  parser,
		UpdatesEnabled: false,
		Notifier:       notif,
		DeDup:          NewDeDup(true),
		Repeater:       rep,
		Stdout:         out,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	svc.Do(ctx)
	t.Log(out.String())
	assert.Contains(t, out.String(), "something: command not found")
}

func TestScheduler_execute(t *testing.T) {
	svc := Scheduler{Repeater: repeater.New(&strategy.Once{}), EnableLogPrefix: true}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("echo 123", wr)
	require.NoError(t, err)
	assert.Equal(t, "{echo 123} 123\n", wr.String())

	svc = Scheduler{Repeater: repeater.New(&strategy.Once{}), EnableLogPrefix: false}
	wr = bytes.NewBuffer(nil)
	err = svc.executeCommand("echo 123", wr)
	require.NoError(t, err)
	assert.Equal(t, "123\n", wr.String())
}

func TestScheduler_executeFailedNotFound(t *testing.T) {
	svc := Scheduler{Repeater: repeater.New(&strategy.Once{})}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("no-such-command", wr)
	require.Error(t, err)
	assert.Contains(t, wr.String(), "not found")
}

func TestScheduler_executeFailedExitCode(t *testing.T) {
	svc := Scheduler{MaxLogLines: 10, Repeater: repeater.New(&strategy.Once{})}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("testfiles/fail.sh", wr)
	require.Error(t, err)
	assert.Contains(t, wr.String(), "TestScheduler_executeFailed")
	t.Log(err)
	assert.Equal(t, 10+3, len(strings.Split(err.Error(), "\n")))
	assert.Equal(t, "TestScheduler_executeFailed 14", strings.Split(err.Error(), "\n")[12])
}

func TestScheduler_jobFunc(t *testing.T) {
	resmr := &mocks.ResumerMock{
		OnStartFunc: func(cmd string) (string, error) {
			assert.Equal(t, "echo 123", cmd)
			return "resume.file", nil
		},
		OnFinishFunc: func(fname string) error {
			assert.Equal(t, "resume.file", fname)
			return nil
		},
	}
	scheduleMock := &mocks.ScheduleMock{
		NextFunc: func(timeMoqParam time.Time) time.Time {
			return time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)
		},
	}
	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, Stdout: wr, Resumer: resmr,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true), EnableLogPrefix: true}

	svc.jobFunc(crontab.JobSpec{Spec: "@startup", Command: "echo 123"}, scheduleMock).Run()
	assert.Equal(t, "{echo 123} 123\n", wr.String())

	assert.Equal(t, 1, len(resmr.OnFinishCalls()))
	assert.Equal(t, 1, len(resmr.OnFinishCalls()))
}

func TestScheduler_jobFuncFailed(t *testing.T) {

	resmr := &mocks.ResumerMock{
		OnStartFunc: func(cmd string) (string, error) {
			assert.Equal(t, "no-such-thing", cmd)
			return "resume.file", nil
		},
	}
	scheduleMock := &mocks.ScheduleMock{
		NextFunc: func(timeMoqParam time.Time) time.Time {
			return time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)
		},
	}

	notif := &mocks.NotifierMock{
		SendFunc:           func(subj string, text string) error { return nil },
		IsOnErrorFunc:      func() bool { return true },
		IsOnCompletionFunc: func() bool { return false },
		MakeErrorHTMLFunc: func(spec string, command string, errorLog string) (string, error) {
			assert.Equal(t, "@startup", spec)
			assert.Equal(t, "no-such-thing", command)
			return "email msg", nil
		},
	}

	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, Stdout: wr, Resumer: resmr, Notifier: notif,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true)}

	svc.jobFunc(crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, scheduleMock).Run()
	assert.Contains(t, wr.String(), "not found")

	assert.Equal(t, 1, len(resmr.OnStartCalls()))
	assert.Equal(t, 1, len(notif.SendCalls()))
}

func TestScheduler_notifyOnError(t *testing.T) {
	notif := &mocks.NotifierMock{
		SendFunc: func(subj string, text string) error {
			return nil
		},
		IsOnErrorFunc: func() bool {
			return true
		},
		MakeErrorHTMLFunc: func(spec string, command string, errorLog string) (string, error) {
			assert.Equal(t, spec, "@startup")
			assert.Equal(t, command, "no-such-thing")
			assert.Equal(t, errorLog, "message")
			return "email msg", nil
		},
	}

	svc := Scheduler{MaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, "message")
	require.NoError(t, err)

	assert.Equal(t, 1, len(notif.SendCalls()))
	assert.Equal(t, 1, len(notif.IsOnErrorCalls()))
	assert.Equal(t, 1, len(notif.MakeErrorHTMLCalls()))
}

func TestScheduler_notifyOnCompletion(t *testing.T) {

	notif := &mocks.NotifierMock{
		SendFunc: func(subj string, text string) error {
			return nil
		},
		IsOnCompletionFunc: func() bool {
			return true
		},
		MakeCompletionHTMLFunc: func(spec string, command string) (string, error) {
			assert.Equal(t, spec, "@startup")
			assert.Equal(t, command, "ls -la")
			return "email msg", nil
		},
	}
	svc := Scheduler{MaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.NoError(t, err)

	assert.Equal(t, 1, len(notif.SendCalls()))
	assert.Equal(t, 1, len(notif.IsOnCompletionCalls()))
	assert.Equal(t, 1, len(notif.MakeCompletionHTMLCalls()))
}

func TestScheduler_DoWithReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	scCalls := 0
	cr := &mocks.CronMock{
		EntriesFunc: func() []cron.Entry { return []cron.Entry{{}, {}, {}} },
		RemoveFunc:  func(id cron.EntryID) {},
		StartFunc:   func() {},
		StopFunc:    func() context.Context { return ctx },
		ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
			scCalls++
			switch scCalls {
			case 1, 2:
				return cron.EntryID(1)
			case 3:
				return cron.EntryID(1)
			default:
				t.Fatal("unexpected")
			}
			return cron.EntryID(0)
		},
	}

	resmr := &mocks.ResumerMock{ListFunc: func() []resumer.Cmd { return nil }}

	prsListCalls := 0
	parser := &mocks.CrontabParserMock{
		ListFunc: func() ([]crontab.JobSpec, error) {
			prsListCalls++
			if prsListCalls == 1 {
				return []crontab.JobSpec{{Spec: "1 * * * *", Command: "test1"}, {Spec: "2 * * * *", Command: "test2"}}, nil
			}
			if prsListCalls == 2 {
				return []crontab.JobSpec{{Spec: "11 * * * *", Command: "test1"}}, nil
			}
			return nil, errors.New("error")
		},
		StringFunc: func() string {
			return "parser"
		},
		ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
			ch := make(chan []crontab.JobSpec, 1)
			ch <- []crontab.JobSpec{{Command: "cmd", Spec: "@reboot"}}
			close(ch)
			return ch, nil
		},
	}

	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: true,
	}

	svc.Do(ctx)

	assert.Equal(t, 1, len(resmr.ListCalls()))
	assert.Equal(t, 2, len(cr.EntriesCalls()))
	assert.Equal(t, 6, len(cr.RemoveCalls()))
	assert.Equal(t, 1, len(cr.StartCalls()))
	assert.Equal(t, 1, len(cr.StopCalls()))
	assert.Equal(t, 2, len(parser.ListCalls()))
}

func TestScheduler_DoWithResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	cr := &mocks.CronMock{
		EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
		StartFunc:   func() {},
		StopFunc:    func() context.Context { return ctx },
	}

	rsmFinCalls := 0
	resmr := &mocks.ResumerMock{
		ListFunc: func() []resumer.Cmd {
			return []resumer.Cmd{{Command: "cmd1", Fname: "f1"}, {Command: "cmd2", Fname: "f2"}}
		},
		OnFinishFunc: func(fname string) error {
			rsmFinCalls++
			assert.Equal(t, "f"+strconv.Itoa(rsmFinCalls), fname)
			return nil
		},
	}
	parser := &mocks.CrontabParserMock{
		ListFunc: func() ([]crontab.JobSpec, error) { return []crontab.JobSpec{}, nil },
	}

	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: false,
		Repeater:       repeater.New(&strategy.Once{}),
	}

	svc.Do(ctx)

	assert.Equal(t, 1, len(resmr.ListCalls()))
	assert.Equal(t, 2, len(resmr.OnFinishCalls()))
	assert.Equal(t, 1, len(cr.EntriesCalls()))
	assert.Equal(t, 1, len(cr.StartCalls()))
	assert.Equal(t, 1, len(cr.StopCalls()))
	assert.Equal(t, 1, len(parser.ListCalls()))
}
