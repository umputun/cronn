package service

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestScheduler_JobDescription(t *testing.T) {
	s := &Scheduler{}

	tests := []struct {
		name     string
		jobSpec  crontab.JobSpec
		expected string
	}{
		{
			name:     "with name",
			jobSpec:  crontab.JobSpec{Command: "echo test", Name: "Test job"},
			expected: `"echo test" (Test job)`,
		},
		{
			name:     "without name",
			jobSpec:  crontab.JobSpec{Command: "ls -la"},
			expected: `"ls -la"`,
		},
		{
			name:     "empty name",
			jobSpec:  crontab.JobSpec{Command: "date", Name: ""},
			expected: `"date"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.jobDescription(tt.jobSpec)
			assert.Equal(t, tt.expected, result)
		})
	}
}

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
		StringFunc: func() string {
			return "test"
		},
		ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
			return nil, nil
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
	parser := crontab.New("testfiles/crontab", time.Minute, nil)
	res := resumer.New("/tmp", false)

	notif := &mocks.NotifierMock{
		SendFunc:           func(ctx context.Context, destination string, text string) error { return nil },
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
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{EnableLogPrefix: true}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("echo 123", wr, rep)
	require.NoError(t, err)
	assert.Equal(t, "{echo 123} 123\n", wr.String())

	svc = Scheduler{EnableLogPrefix: false}
	wr = bytes.NewBuffer(nil)
	err = svc.executeCommand("echo 123", wr, rep)
	require.NoError(t, err)
	assert.Equal(t, "123\n", wr.String())
}

func TestScheduler_executeFailedNotFound(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("no-such-command", wr, rep)
	require.Error(t, err)
	assert.Contains(t, wr.String(), "not found")
}

func TestScheduler_executeFailedExitCode(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{MaxLogLines: 10}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("testfiles/fail.sh", wr, rep)
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

func TestScheduler_jobFuncWithName(t *testing.T) {
	resmr := &mocks.ResumerMock{
		OnStartFunc: func(cmd string) (string, error) {
			assert.Equal(t, "echo test", cmd)
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

	// test with name field set
	svc.jobFunc(crontab.JobSpec{Spec: "@startup", Command: "echo test", Name: "Test job"}, scheduleMock).Run()
	assert.Equal(t, "{echo test} test\n", wr.String())

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
		SendFunc:           func(ctx context.Context, destination string, text string) error { return nil },
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
		SendFunc: func(ctx context.Context, destination string, text string) error {
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
	err := svc.notify(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, "message")
	require.NoError(t, err)

	assert.Equal(t, 1, len(notif.SendCalls()))
	assert.Equal(t, 1, len(notif.IsOnErrorCalls()))
	assert.Equal(t, 1, len(notif.MakeErrorHTMLCalls()))
}

func TestScheduler_notifyOnCompletion(t *testing.T) {

	notif := &mocks.NotifierMock{
		SendFunc: func(ctx context.Context, destination string, text string) error {
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
	err := svc.notify(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.NoError(t, err)

	assert.Equal(t, 1, len(notif.SendCalls()))
	assert.Equal(t, 1, len(notif.IsOnCompletionCalls()))
	assert.Equal(t, 1, len(notif.MakeCompletionHTMLCalls()))
}

func TestScheduler_notifyContextCancellation(t *testing.T) {
	notif := &mocks.NotifierMock{
		SendFunc: func(ctx context.Context, destination string, text string) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return nil
			}
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.notify(ctx, crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.EqualError(t, err, "context canceled")

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

	var prsListCalls int32
	parser := &mocks.CrontabParserMock{
		ListFunc: func() ([]crontab.JobSpec, error) {
			atomic.AddInt32(&prsListCalls, 1)
			if atomic.LoadInt32(&prsListCalls) == 1 {
				return []crontab.JobSpec{{Spec: "1 * * * *", Command: "test1"}, {Spec: "2 * * * *", Command: "test2"}}, nil
			}
			if atomic.LoadInt32(&prsListCalls) == 2 {
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
		ListFunc:    func() ([]crontab.JobSpec, error) { return []crontab.JobSpec{}, nil },
		StringFunc:  func() string { return "test" },
		ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) { return nil, nil },
	}

	svc := Scheduler{
		Cron:              cr,
		Resumer:           resmr,
		ResumeConcurrency: 1,
		CrontabParser:     parser,
		UpdatesEnabled:    false,
		Repeater:          repeater.New(&strategy.Once{}),
	}

	svc.Do(ctx)

	assert.Equal(t, 1, len(resmr.ListCalls()))
	assert.Equal(t, 2, len(resmr.OnFinishCalls()))
	assert.Equal(t, 1, len(cr.EntriesCalls()))
	assert.Equal(t, 1, len(cr.StartCalls()))
	assert.Equal(t, 1, len(cr.StopCalls()))
	assert.Equal(t, 1, len(parser.ListCalls()))
}

func TestScheduler_getJobRepeater(t *testing.T) {
	// create global repeater with specific settings
	globalRepeater := repeater.New(&strategy.Backoff{
		Repeats:  3,
		Duration: 1 * time.Second,
		Factor:   2.0,
		Jitter:   false,
	})

	svc := Scheduler{
		Repeater: globalRepeater,
	}

	t.Run("nil config returns global repeater", func(t *testing.T) {
		result := svc.getJobRepeater(nil)
		assert.Equal(t, globalRepeater, result)
	})

	t.Run("full override", func(t *testing.T) {
		attempts := 5
		duration := 2 * time.Second
		factor := 3.0
		jitter := true
		
		config := &crontab.RepeaterConfig{
			Attempts: &attempts,
			Duration: &duration,
			Factor:   &factor,
			Jitter:   &jitter,
		}
		
		result := svc.getJobRepeater(config)
		assert.NotEqual(t, globalRepeater, result)
		
		// verify the new repeater has the overridden settings
		resultRepeater, ok := result.(*repeater.Repeater)
		require.True(t, ok)
		resultBackoff, ok := resultRepeater.Strategy.(*strategy.Backoff)
		require.True(t, ok)
		
		assert.Equal(t, 5, resultBackoff.Repeats)
		assert.Equal(t, 2*time.Second, resultBackoff.Duration)
		assert.Equal(t, 3.0, resultBackoff.Factor)
		assert.Equal(t, true, resultBackoff.Jitter)
	})

	t.Run("partial override", func(t *testing.T) {
		attempts := 10
		
		config := &crontab.RepeaterConfig{
			Attempts: &attempts,
		}
		
		result := svc.getJobRepeater(config)
		assert.NotEqual(t, globalRepeater, result)
		
		// verify the new repeater has merged settings
		resultRepeater, ok := result.(*repeater.Repeater)
		require.True(t, ok)
		resultBackoff, ok := resultRepeater.Strategy.(*strategy.Backoff)
		require.True(t, ok)
		
		assert.Equal(t, 10, resultBackoff.Repeats) // overridden
		assert.Equal(t, 1*time.Second, resultBackoff.Duration) // from global
		assert.Equal(t, 2.0, resultBackoff.Factor) // from global
		assert.Equal(t, false, resultBackoff.Jitter) // from global
	})

	t.Run("only jitter override", func(t *testing.T) {
		jitter := true
		
		config := &crontab.RepeaterConfig{
			Jitter: &jitter,
		}
		
		result := svc.getJobRepeater(config)
		assert.NotEqual(t, globalRepeater, result)
		
		// verify the new repeater has merged settings
		resultRepeater, ok := result.(*repeater.Repeater)
		require.True(t, ok)
		resultBackoff, ok := resultRepeater.Strategy.(*strategy.Backoff)
		require.True(t, ok)
		
		assert.Equal(t, 3, resultBackoff.Repeats) // from global
		assert.Equal(t, 1*time.Second, resultBackoff.Duration) // from global
		assert.Equal(t, 2.0, resultBackoff.Factor) // from global
		assert.Equal(t, true, resultBackoff.Jitter) // overridden
	})
}
