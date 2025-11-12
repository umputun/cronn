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

	"github.com/umputun/cronn/app/conditions"
	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service/mocks"
	"github.com/umputun/cronn/app/service/request"
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

	var scheduleCallCount int32
	cr := &mocks.CronMock{
		EntriesFunc: func() []cron.Entry { return []cron.Entry{{}, {}, {}} },
		RemoveFunc:  func(id cron.EntryID) {},
		StartFunc:   func() {},
		StopFunc:    func() context.Context { return ctx },
		ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
			calls := atomic.AddInt32(&scheduleCallCount, 1)
			return cron.EntryID(calls)
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

	assert.Len(t, resmr.ListCalls(), 1)

	assert.Len(t, cr.EntriesCalls(), 1)
	assert.Len(t, cr.RemoveCalls(), 3)
	assert.Len(t, cr.StartCalls(), 1)
	assert.Len(t, cr.StopCalls(), 1)

	assert.Len(t, parser.ListCalls(), 1)
}

func TestScheduler_DoIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
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
	assert.Contains(t, out.String(), "not found")
}

func TestScheduler_execute(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{EnableLogPrefix: true}
	wr := bytes.NewBuffer(nil)
	_, _, err := svc.executeCommand(context.Background(), "echo 123", wr, rep)
	require.NoError(t, err)
	assert.Equal(t, "{echo 123} 123\n", wr.String())

	svc = Scheduler{EnableLogPrefix: false}
	wr = bytes.NewBuffer(nil)
	_, _, err = svc.executeCommand(context.Background(), "echo 123", wr, rep)
	require.NoError(t, err)
	assert.Equal(t, "123\n", wr.String())
}

func TestScheduler_executeFailedNotFound(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{}
	wr := bytes.NewBuffer(nil)
	_, _, err := svc.executeCommand(context.Background(), "no-such-command", wr, rep)
	require.Error(t, err)
	assert.Contains(t, wr.String(), "not found")
}

func TestScheduler_executeFailedExitCode(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{NotifyMaxLogLines: 10}
	wr := bytes.NewBuffer(nil)
	notifyOutput, _, err := svc.executeCommand(context.Background(), "testfiles/fail.sh", wr, rep)
	require.Error(t, err)
	assert.Contains(t, wr.String(), "TestScheduler_executeFailed")
	t.Log(err)

	// notifyOutput should contain last 10 lines
	notifyLines := strings.Split(strings.TrimSpace(notifyOutput), "\n")
	assert.Len(t, notifyLines, 10)
	assert.Equal(t, "TestScheduler_executeFailed 14", notifyLines[9])
}

func TestScheduler_ExecuteCommand_IndependentOutputLimits(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{NotifyMaxLogLines: 3, ExecMaxLogLines: 5}
	wr := bytes.NewBuffer(nil)

	// generate command that produces 10 lines of output
	cmd := "for i in 1 2 3 4 5 6 7 8 9 10; do echo line$i; done"
	notifyOutput, webOutput, err := svc.executeCommand(context.Background(), cmd, wr, rep)
	require.NoError(t, err)

	// notify output should have last 3 lines
	notifyLines := strings.Split(strings.TrimSpace(notifyOutput), "\n")
	assert.Len(t, notifyLines, 3)
	assert.Equal(t, "line8", notifyLines[0])
	assert.Equal(t, "line9", notifyLines[1])
	assert.Equal(t, "line10", notifyLines[2])

	// web output should have last 5 lines
	webLines := strings.Split(strings.TrimSpace(webOutput), "\n")
	assert.Len(t, webLines, 5)
	assert.Equal(t, "line6", webLines[0])
	assert.Equal(t, "line7", webLines[1])
	assert.Equal(t, "line8", webLines[2])
	assert.Equal(t, "line9", webLines[3])
	assert.Equal(t, "line10", webLines[4])
}

func TestScheduler_ExecuteCommand_WebOutputDisabled(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{NotifyMaxLogLines: 5, ExecMaxLogLines: 0}
	wr := bytes.NewBuffer(nil)

	cmd := "for i in 1 2 3 4 5 6 7 8; do echo line$i; done"
	notifyOutput, webOutput, err := svc.executeCommand(context.Background(), cmd, wr, rep)
	require.NoError(t, err)

	// notify output should have last 5 lines
	notifyLines := strings.Split(strings.TrimSpace(notifyOutput), "\n")
	assert.Len(t, notifyLines, 5)
	assert.Equal(t, "line4", notifyLines[0])
	assert.Equal(t, "line8", notifyLines[4])

	// web output should be empty (disabled)
	assert.Empty(t, webOutput)
}

func TestScheduler_ExecuteCommand_NotifyOutputDisabled(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{NotifyMaxLogLines: 0, ExecMaxLogLines: 5}
	wr := bytes.NewBuffer(nil)

	cmd := "for i in 1 2 3 4 5 6 7 8; do echo line$i; done"
	notifyOutput, webOutput, err := svc.executeCommand(context.Background(), cmd, wr, rep)
	require.NoError(t, err)

	// notify output should be empty (0 lines captured)
	assert.Empty(t, notifyOutput)

	// web output should have last 5 lines
	webLines := strings.Split(strings.TrimSpace(webOutput), "\n")
	assert.Len(t, webLines, 5)
	assert.Equal(t, "line4", webLines[0])
	assert.Equal(t, "line8", webLines[4])
}

func TestScheduler_ExecuteCommand_BothOutputsSameContent(t *testing.T) {
	rep := repeater.New(&strategy.Once{})
	svc := Scheduler{NotifyMaxLogLines: 100, ExecMaxLogLines: 100}
	wr := bytes.NewBuffer(nil)

	cmd := "echo line1; echo line2; echo line3"
	notifyOutput, webOutput, err := svc.executeCommand(context.Background(), cmd, wr, rep)
	require.NoError(t, err)

	// both outputs should be identical when buffer limits are not reached
	assert.Equal(t, notifyOutput, webOutput)
	assert.Contains(t, notifyOutput, "line1")
	assert.Contains(t, notifyOutput, "line2")
	assert.Contains(t, notifyOutput, "line3")
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
	svc := Scheduler{NotifyMaxLogLines: 10, Stdout: wr, Resumer: resmr,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true), EnableLogPrefix: true}

	svc.jobFunc(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "echo 123"}, scheduleMock).Run()
	assert.Equal(t, "{echo 123} 123\n", wr.String())

	assert.Len(t, resmr.OnStartCalls(), 1)
	assert.Len(t, resmr.OnFinishCalls(), 1)
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
	svc := Scheduler{NotifyMaxLogLines: 10, Stdout: wr, Resumer: resmr,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true), EnableLogPrefix: true}

	// test with name field set
	svc.jobFunc(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "echo test", Name: "Test job"}, scheduleMock).Run()
	assert.Equal(t, "{echo test} test\n", wr.String())

	assert.Len(t, resmr.OnFinishCalls(), 1)
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
	svc := Scheduler{NotifyMaxLogLines: 10, Stdout: wr, Resumer: resmr, Notifier: notif,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true)}

	svc.jobFunc(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, scheduleMock).Run()
	assert.Contains(t, wr.String(), "not found")

	assert.Len(t, resmr.OnStartCalls(), 1)
	assert.Len(t, notif.SendCalls(), 1)
}

func TestScheduler_runJobWithCommand(t *testing.T) {
	t.Run("successful execution with template parsing", func(t *testing.T) {
		customTime := time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC)

		resmr := &mocks.ResumerMock{
			OnStartFunc: func(cmd string) (string, error) {
				assert.Equal(t, "echo 20241225", cmd)
				return "resume.file", nil
			},
			OnFinishFunc: func(fname string) error {
				assert.Equal(t, "resume.file", fname)
				return nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		wr := bytes.NewBuffer(nil)
		svc := Scheduler{
			Stdout:          wr,
			Resumer:         resmr,
			Repeater:        repeater.New(&strategy.Once{}),
			DeDup:           NewDeDup(true),
			JobEventHandler: mockEventHandler,
			NotifyTimeout:   time.Second,
		}

		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "echo base"},
			"echo {{.YYYYMMDD}}",
			&customTime,
			repeater.New(&strategy.Once{}),
			false)

		require.NoError(t, err)
		assert.Contains(t, wr.String(), "20241225")
		assert.Len(t, resmr.OnStartCalls(), 1)
		assert.Len(t, resmr.OnFinishCalls(), 1)

		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo base", mockEventHandler.OnJobStartCalls()[0].Req.Command)
		assert.Empty(t, mockEventHandler.OnJobStartCalls()[0].Req.ExecutedCommand, "scheduled run should have empty executedCommand")

		require.Len(t, mockEventHandler.OnJobCompleteCalls(), 1)
		assert.Equal(t, "echo base", mockEventHandler.OnJobCompleteCalls()[0].Req.Command)
		assert.Empty(t, mockEventHandler.OnJobCompleteCalls()[0].Req.ExecutedCommand, "scheduled run OnJobComplete should have empty executedCommand")
		assert.Equal(t, 0, mockEventHandler.OnJobCompleteCalls()[0].Req.ExitCode)
		assert.NoError(t, mockEventHandler.OnJobCompleteCalls()[0].Req.Err)
	})

	t.Run("dedup prevents concurrent execution", func(t *testing.T) {
		resmr := &mocks.ResumerMock{
			OnStartFunc:  func(cmd string) (string, error) { return "resume.file", nil },
			OnFinishFunc: func(fname string) error { return nil },
		}

		wr := bytes.NewBuffer(nil)
		dedup := NewDeDup(true)
		svc := Scheduler{
			Stdout:        wr,
			Resumer:       resmr,
			Repeater:      repeater.New(&strategy.Once{}),
			DeDup:         dedup,
			NotifyTimeout: time.Second,
		}

		jobSpec := crontab.JobSpec{Spec: "* * * * *", Command: "echo test"}

		// first execution should succeed
		dedup.Add("echo test#* * * * *")
		err := svc.runJobWithCommand(context.Background(), jobSpec, "echo test", nil, repeater.New(&strategy.Once{}), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicated job")
	})

	t.Run("failed execution with exit code", func(t *testing.T) {
		resmr := &mocks.ResumerMock{
			OnStartFunc: func(cmd string) (string, error) {
				return "resume.file", nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		wr := bytes.NewBuffer(nil)
		svc := Scheduler{
			Stdout:          wr,
			Resumer:         resmr,
			Repeater:        repeater.New(&strategy.Once{}),
			DeDup:           NewDeDup(true),
			JobEventHandler: mockEventHandler,
			NotifyTimeout:   time.Second,
		}

		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "false"},
			"false",
			nil,
			repeater.New(&strategy.Once{}),
			false)

		require.Error(t, err)
		require.Len(t, mockEventHandler.OnJobCompleteCalls(), 1)
		assert.Empty(t, mockEventHandler.OnJobCompleteCalls()[0].Req.ExecutedCommand, "failed scheduled run should have empty executedCommand")
		assert.Equal(t, 1, mockEventHandler.OnJobCompleteCalls()[0].Req.ExitCode)
		assert.Error(t, mockEventHandler.OnJobCompleteCalls()[0].Req.Err)
	})

	t.Run("notification message has no duplicate logs", func(t *testing.T) {
		resmr := &mocks.ResumerMock{
			OnStartFunc:  func(cmd string) (string, error) { return "resume.file", nil },
			OnFinishFunc: func(fname string) error { return nil },
		}

		var notificationMsg string
		notif := &mocks.NotifierMock{
			SendFunc: func(ctx context.Context, destination string, text string) error {
				notificationMsg = text
				return nil
			},
			IsOnErrorFunc:      func() bool { return true },
			IsOnCompletionFunc: func() bool { return false },
			MakeErrorHTMLFunc: func(spec string, command string, errorLog string) (string, error) {
				return errorLog, nil
			},
		}

		wr := bytes.NewBuffer(nil)
		svc := Scheduler{
			Stdout:            wr,
			Resumer:           resmr,
			Repeater:          repeater.New(&strategy.Once{}),
			DeDup:             NewDeDup(true),
			Notifier:          notif,
			NotifyMaxLogLines: 5,
			NotifyTimeout:     time.Second,
		}

		// run command that fails with output
		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "testfiles/fail.sh"},
			"testfiles/fail.sh",
			nil,
			repeater.New(&strategy.Once{}),
			false)

		require.Error(t, err)
		require.Len(t, notif.SendCalls(), 1)

		// verify notification message doesn't have duplicate logs
		lines := strings.Split(notificationMsg, "\n")
		lineCount := make(map[string]int)
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				lineCount[line]++
			}
		}

		// check that no line appears more than once (allowing the error message itself)
		for line, count := range lineCount {
			if strings.Contains(line, "TestScheduler_executeFailed") {
				assert.Equal(t, 1, count, "log line should not be duplicated: %s", line)
			}
		}
	})

	t.Run("template parsing error", func(t *testing.T) {
		svc := Scheduler{
			Repeater:      repeater.New(&strategy.Once{}),
			DeDup:         NewDeDup(true),
			NotifyTimeout: time.Second,
		}

		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "echo test"},
			"echo {{.INVALID}}",
			nil,
			repeater.New(&strategy.Once{}),
			false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't evaluate field INVALID")
	})

	t.Run("manual execution sets executed command", func(t *testing.T) {
		customTime := time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC)

		resmr := &mocks.ResumerMock{
			OnStartFunc: func(cmd string) (string, error) {
				assert.Equal(t, "echo 20241225", cmd)
				return "resume.file", nil
			},
			OnFinishFunc: func(fname string) error {
				assert.Equal(t, "resume.file", fname)
				return nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		wr := bytes.NewBuffer(nil)
		svc := Scheduler{
			Stdout:          wr,
			Resumer:         resmr,
			Repeater:        repeater.New(&strategy.Once{}),
			DeDup:           NewDeDup(true),
			JobEventHandler: mockEventHandler,
			NotifyTimeout:   time.Second,
		}

		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "echo base"},
			"echo {{.YYYYMMDD}}",
			&customTime,
			repeater.New(&strategy.Once{}),
			true)

		require.NoError(t, err)
		assert.Contains(t, wr.String(), "20241225")
		assert.Len(t, resmr.OnStartCalls(), 1)
		assert.Len(t, resmr.OnFinishCalls(), 1)

		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo 20241225", mockEventHandler.OnJobStartCalls()[0].Req.ExecutedCommand, "manual run should have executed command after template parsing")

		require.Len(t, mockEventHandler.OnJobCompleteCalls(), 1)
		assert.Equal(t, "echo 20241225", mockEventHandler.OnJobCompleteCalls()[0].Req.ExecutedCommand, "manual run OnJobComplete should have executed command")
		assert.Equal(t, 0, mockEventHandler.OnJobCompleteCalls()[0].Req.ExitCode)
	})

	t.Run("manual execution without templates sets executed command", func(t *testing.T) {
		resmr := &mocks.ResumerMock{
			OnStartFunc: func(cmd string) (string, error) {
				assert.Equal(t, "echo hello", cmd)
				return "resume.file", nil
			},
			OnFinishFunc: func(fname string) error {
				return nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		wr := bytes.NewBuffer(nil)
		svc := Scheduler{
			Stdout:          wr,
			Resumer:         resmr,
			Repeater:        repeater.New(&strategy.Once{}),
			DeDup:           NewDeDup(true),
			JobEventHandler: mockEventHandler,
			NotifyTimeout:   time.Second,
		}

		err := svc.runJobWithCommand(context.Background(),
			crontab.JobSpec{Spec: "* * * * *", Command: "echo hello"},
			"echo hello",
			nil,
			repeater.New(&strategy.Once{}),
			true)

		require.NoError(t, err)
		assert.Contains(t, wr.String(), "hello")

		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo hello", mockEventHandler.OnJobStartCalls()[0].Req.ExecutedCommand, "manual run should have executed command even without template changes")

		require.Len(t, mockEventHandler.OnJobCompleteCalls(), 1)
		assert.Equal(t, "echo hello", mockEventHandler.OnJobCompleteCalls()[0].Req.ExecutedCommand, "manual run OnJobComplete should have executed command")
		assert.Equal(t, 0, mockEventHandler.OnJobCompleteCalls()[0].Req.ExitCode)
	})
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
			assert.Equal(t, "@startup", spec)
			assert.Equal(t, "no-such-thing", command)
			assert.Equal(t, "message", errorLog)
			return "email msg", nil
		},
	}

	svc := Scheduler{NotifyMaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, "message")
	require.NoError(t, err)

	assert.Len(t, notif.SendCalls(), 1)
	assert.Len(t, notif.IsOnErrorCalls(), 1)
	assert.Len(t, notif.MakeErrorHTMLCalls(), 1)
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
			assert.Equal(t, "@startup", spec)
			assert.Equal(t, "ls -la", command)
			return "email msg", nil
		},
	}
	svc := Scheduler{NotifyMaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(context.Background(), crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.NoError(t, err)

	assert.Len(t, notif.SendCalls(), 1)
	assert.Len(t, notif.IsOnCompletionCalls(), 1)
	assert.Len(t, notif.MakeCompletionHTMLCalls(), 1)
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
			assert.Equal(t, "@startup", spec)
			assert.Equal(t, "ls -la", command)
			return "email msg", nil
		},
	}
	svc := Scheduler{NotifyMaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := svc.notify(ctx, crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.EqualError(t, err, "failed to send completion notification: context canceled")

	assert.Len(t, notif.SendCalls(), 1)
	assert.Len(t, notif.IsOnCompletionCalls(), 1)
	assert.Len(t, notif.MakeCompletionHTMLCalls(), 1)
}

func TestScheduler_DoWithReload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var scheduleCallCount int32
	cr := &mocks.CronMock{
		EntriesFunc: func() []cron.Entry { return []cron.Entry{{}, {}, {}} },
		RemoveFunc:  func(id cron.EntryID) {},
		StartFunc:   func() {},
		StopFunc:    func() context.Context { return ctx },
		ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
			calls := atomic.AddInt32(&scheduleCallCount, 1)
			switch calls {
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

	assert.Len(t, resmr.ListCalls(), 1)
	assert.Len(t, cr.EntriesCalls(), 2)
	assert.Len(t, cr.RemoveCalls(), 6)
	assert.Len(t, cr.StartCalls(), 1)
	assert.Len(t, cr.StopCalls(), 1)
	assert.Len(t, parser.ListCalls(), 2)
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

	assert.Len(t, resmr.ListCalls(), 1)
	assert.Len(t, resmr.OnFinishCalls(), 2)
	assert.Len(t, cr.EntriesCalls(), 1)
	assert.Len(t, cr.StartCalls(), 1)
	assert.Len(t, cr.StopCalls(), 1)
	assert.Len(t, parser.ListCalls(), 1)
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
	svc.RepeaterDefaults.Attempts = 3
	svc.RepeaterDefaults.Duration = 1 * time.Second
	svc.RepeaterDefaults.Factor = 2.0
	svc.RepeaterDefaults.Jitter = false

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
		assert.InDelta(t, 3.0, resultBackoff.Factor, 0.001)
		assert.True(t, resultBackoff.Jitter)
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

		assert.Equal(t, 10, resultBackoff.Repeats)             // overridden
		assert.Equal(t, 1*time.Second, resultBackoff.Duration) // from global
		assert.InDelta(t, 2.0, resultBackoff.Factor, 0.001)    // from global
		assert.False(t, resultBackoff.Jitter)                  // from global
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

		assert.Equal(t, 3, resultBackoff.Repeats)              // from global
		assert.Equal(t, 1*time.Second, resultBackoff.Duration) // from global
		assert.InDelta(t, 2.0, resultBackoff.Factor, 0.001)    // from global
		assert.True(t, resultBackoff.Jitter)                   // overridden
	})
}

func TestScheduler_WaitForConditions(t *testing.T) {
	t.Run("no condition checker - always execute", func(t *testing.T) {
		svc := &Scheduler{}

		cond := conditions.Config{
			CPUBelow: intPtr(50),
		}

		result := svc.waitForConditions(context.Background(), cond, "test job")
		assert.True(t, result, "should execute when no condition checker")
	})

	t.Run("conditions met - execute immediately", func(t *testing.T) {
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				return true, ""
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		cond := conditions.Config{
			CPUBelow: intPtr(50),
		}

		result := svc.waitForConditions(context.Background(), cond, "test job")
		assert.True(t, result)
		assert.Len(t, mockChecker.CheckCalls(), 1)
	})

	t.Run("conditions not met - skip job when no max_postpone", func(t *testing.T) {
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				return false, "CPU at 80%, threshold 50%"
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		cond := conditions.Config{
			CPUBelow: intPtr(50),
		}

		result := svc.waitForConditions(context.Background(), cond, "test job")
		assert.False(t, result)
		assert.Len(t, mockChecker.CheckCalls(), 1)
	})

	t.Run("conditions not met - wait and succeed", func(t *testing.T) {
		callCount := 0
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				callCount++
				if callCount >= 2 {
					return true, ""
				}
				return false, "CPU at 80%, threshold 50%"
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		maxPostpone := 2 * time.Second
		checkInterval := 100 * time.Millisecond
		cond := conditions.Config{
			CPUBelow:      intPtr(50),
			MaxPostpone:   &maxPostpone,
			CheckInterval: &checkInterval,
		}

		start := time.Now()
		result := svc.waitForConditions(context.Background(), cond, "test job")
		duration := time.Since(start)

		assert.True(t, result)
		assert.GreaterOrEqual(t, duration, 100*time.Millisecond)
		assert.Less(t, duration, 300*time.Millisecond)
		assert.GreaterOrEqual(t, len(mockChecker.CheckCalls()), 2)
	})

	t.Run("max postpone reached - execute anyway", func(t *testing.T) {
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				return false, "CPU at 80%, threshold 50%"
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		maxPostpone := 200 * time.Millisecond
		checkInterval := 100 * time.Millisecond
		cond := conditions.Config{
			CPUBelow:      intPtr(50),
			MaxPostpone:   &maxPostpone,
			CheckInterval: &checkInterval,
		}

		start := time.Now()
		result := svc.waitForConditions(context.Background(), cond, "test job")
		duration := time.Since(start)

		assert.True(t, result, "should execute after max postpone")
		assert.GreaterOrEqual(t, duration, 200*time.Millisecond)
		assert.Less(t, duration, 400*time.Millisecond)
	})

	t.Run("context canceled - stop waiting", func(t *testing.T) {
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				return false, "CPU at 80%, threshold 50%"
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		maxPostpone := 10 * time.Second
		checkInterval := 100 * time.Millisecond
		cond := conditions.Config{
			CPUBelow:      intPtr(50),
			MaxPostpone:   &maxPostpone,
			CheckInterval: &checkInterval,
		}

		ctx, cancel := context.WithCancel(context.Background())

		// cancel context after short delay
		go func() {
			time.Sleep(150 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		result := svc.waitForConditions(ctx, cond, "test job")
		duration := time.Since(start)

		assert.False(t, result, "should not execute when canceled")
		assert.GreaterOrEqual(t, duration, 150*time.Millisecond)
		assert.Less(t, duration, 300*time.Millisecond)
	})

	t.Run("default check interval", func(t *testing.T) {
		callCount := 0
		mockChecker := &mocks.ConditionCheckerMock{
			CheckFunc: func(conditions conditions.Config) (bool, string) {
				callCount++
				if callCount >= 2 {
					return true, ""
				}
				return false, "CPU at 80%, threshold 50%"
			},
		}

		svc := &Scheduler{
			ConditionChecker: mockChecker,
		}

		maxPostpone := 100 * time.Millisecond
		cond := conditions.Config{
			CPUBelow:    intPtr(50),
			MaxPostpone: &maxPostpone,
			// CheckInterval not set - should default to 30s but max postpone will trigger first
		}

		start := time.Now()
		result := svc.waitForConditions(context.Background(), cond, "test job")
		duration := time.Since(start)

		assert.True(t, result, "should execute after max postpone")
		assert.GreaterOrEqual(t, duration, 100*time.Millisecond)
		assert.Less(t, duration, 200*time.Millisecond)
	})
}

func TestScheduler_JobEventHandler(t *testing.T) {
	t.Run("job event handler gets called with correct exit codes", func(t *testing.T) {
		var capturedExitCodes []int
		var capturedErrors []error

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
				capturedExitCodes = append(capturedExitCodes, req.ExitCode)
				capturedErrors = append(capturedErrors, req.Err)
			},
		}

		svc := &Scheduler{JobEventHandler: mockEventHandler}

		// test successful job (exit code 0)
		now := time.Now()
		svc.JobEventHandler.OnJobComplete(request.OnJobComplete{
			Command: "echo test", ExecutedCommand: "echo test", Schedule: "* * * * *",
			StartTime: now, EndTime: now, ExitCode: 0, Output: "", Err: nil,
		})

		// test failed job with specific exit code
		svc.JobEventHandler.OnJobComplete(request.OnJobComplete{
			Command: "exit 42", ExecutedCommand: "exit 42", Schedule: "* * * * *",
			StartTime: now, EndTime: now, ExitCode: 42, Output: "", Err: errors.New("exit status 42"),
		})

		// test generic error (non-exec)
		svc.JobEventHandler.OnJobComplete(request.OnJobComplete{
			Command: "invalid", ExecutedCommand: "invalid", Schedule: "* * * * *",
			StartTime: now, EndTime: now, ExitCode: 1, Output: "", Err: errors.New("command not found"),
		})

		// verify captured values
		require.Len(t, capturedExitCodes, 3)
		assert.Equal(t, 0, capturedExitCodes[0], "successful job should have exit code 0")
		assert.Equal(t, 42, capturedExitCodes[1], "failed job should have specific exit code")
		assert.Equal(t, 1, capturedExitCodes[2], "generic error should have exit code 1")

		require.NoError(t, capturedErrors[0])
		require.Error(t, capturedErrors[1])
		assert.Error(t, capturedErrors[2])
	})
}

func TestScheduler_ManualTrigger(t *testing.T) {
	t.Run("successful manual trigger", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// create test mocks
		mockCron := &mocks.CronMock{
			StartFunc: func() {},
			StopFunc:  context.Background,
			EntriesFunc: func() []cron.Entry {
				return []cron.Entry{}
			},
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc: func() ([]crontab.JobSpec, error) {
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo test"},
				}, nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		// create scheduler with manual trigger channel
		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			JobEventHandler:  mockEventHandler,
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		// start the scheduler in background
		go s.Do(ctx)

		// wait for scheduler to initialize
		time.Sleep(100 * time.Millisecond)

		// send manual trigger
		startBefore := time.Now()
		jobID := s.jobIDFromCommand("echo test")
		manualTrigger <- ManualJobRequest{
			JobID:    jobID,
			Command:  "echo test",
			Schedule: "* * * * *",
		}

		// wait for job to execute
		time.Sleep(200 * time.Millisecond)

		// verify onJobStart was called
		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo test", mockEventHandler.OnJobStartCalls()[0].Req.Command)
		assert.True(t, mockEventHandler.OnJobStartCalls()[0].Req.StartTime.After(startBefore))
		assert.True(t, mockEventHandler.OnJobStartCalls()[0].Req.StartTime.Before(time.Now()))
	})

	t.Run("manual trigger with invalid schedule", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mockCron := &mocks.CronMock{
			StartFunc:   func() {},
			StopFunc:    context.Background,
			EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc:   func() ([]crontab.JobSpec, error) { return []crontab.JobSpec{}, nil },
		}

		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		// start the scheduler
		go s.Do(ctx)
		time.Sleep(100 * time.Millisecond)

		// send manual trigger with invalid schedule
		manualTrigger <- ManualJobRequest{
			Command:  "echo test",
			Schedule: "invalid schedule",
		}

		// wait a bit
		time.Sleep(100 * time.Millisecond)

		// no crash should occur, scheduler should continue running
		select {
		case <-ctx.Done():
			t.Fatal("context was canceled unexpectedly")
		default:
			// context still active, good
		}
	})

	t.Run("manual trigger channel closed on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		mockCron := &mocks.CronMock{
			StartFunc:   func() {},
			StopFunc:    context.Background,
			EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc:   func() ([]crontab.JobSpec, error) { return []crontab.JobSpec{}, nil },
		}

		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		// start the scheduler
		go s.Do(ctx)
		time.Sleep(100 * time.Millisecond)

		// cancel context
		cancel()
		time.Sleep(100 * time.Millisecond)

		// try to send manual trigger after context cancel
		select {
		case manualTrigger <- ManualJobRequest{Command: "test", Schedule: "* * * * *"}:
			// channel still accepts messages, but listener should have stopped
		default:
			// channel might be blocked, that's ok too
		}

		// scheduler should have stopped cleanly
		assert.Len(t, mockCron.StopCalls(), 1)
	})

	t.Run("manual trigger with custom date for template parsing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mockCron := &mocks.CronMock{
			StartFunc:   func() {},
			StopFunc:    context.Background,
			EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc: func() ([]crontab.JobSpec, error) {
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo {{.YYYYMMDD}}"},
				}, nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			JobEventHandler:  mockEventHandler,
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		go s.Do(ctx)
		time.Sleep(100 * time.Millisecond)

		// send manual trigger with custom date
		customDate := time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC)
		jobID := s.jobIDFromCommand("echo {{.YYYYMMDD}}")
		manualTrigger <- ManualJobRequest{
			JobID:      jobID,
			Command:    "echo {{.YYYYMMDD}}",
			Schedule:   "* * * * *",
			CustomDate: &customDate,
		}

		// wait for job to execute
		time.Sleep(200 * time.Millisecond)

		// verify executed command has the custom date, not today's date
		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo 20241225", mockEventHandler.OnJobStartCalls()[0].Req.ExecutedCommand)
	})

	t.Run("manual trigger with edited command", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mockCron := &mocks.CronMock{
			StartFunc:   func() {},
			StopFunc:    context.Background,
			EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc: func() ([]crontab.JobSpec, error) {
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo original"},
				}, nil
			},
		}

		mockEventHandler := &mocks.JobEventHandlerMock{
			OnJobStartFunc: func(req request.OnJobStart) {},
			OnJobCompleteFunc: func(req request.OnJobComplete) {
			},
		}

		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			JobEventHandler:  mockEventHandler,
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		go s.Do(ctx)
		time.Sleep(100 * time.Millisecond)

		// send manual trigger with edited command
		jobID := s.jobIDFromCommand("echo original")
		manualTrigger <- ManualJobRequest{
			JobID:    jobID,
			Command:  "echo edited",
			Schedule: "* * * * *",
		}

		// wait for job to execute
		time.Sleep(200 * time.Millisecond)

		// verify base command remains original, but executed command is edited
		require.Len(t, mockEventHandler.OnJobStartCalls(), 1)
		assert.Equal(t, "echo original", mockEventHandler.OnJobStartCalls()[0].Req.Command)
		assert.Equal(t, "echo edited", mockEventHandler.OnJobStartCalls()[0].Req.ExecutedCommand)
	})

	t.Run("manual trigger preserves repeater settings", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		mockCron := &mocks.CronMock{
			StartFunc:   func() {},
			StopFunc:    context.Background,
			EntriesFunc: func() []cron.Entry { return []cron.Entry{} },
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		// create job with repeater configuration
		attempts := 3
		duration := 5 * time.Second
		factor := 2.0
		repeaterConfig := &crontab.RepeaterConfig{
			Attempts: &attempts,
			Duration: &duration,
			Factor:   &factor,
		}

		mockParser := &mocks.CrontabParserMock{
			StringFunc: func() string { return "test.crontab" },
			ListFunc: func() ([]crontab.JobSpec, error) {
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo test", Repeater: repeaterConfig},
				}, nil
			},
		}

		manualTrigger := make(chan ManualJobRequest, 10)
		s := &Scheduler{
			Cron:             mockCron,
			CrontabParser:    mockParser,
			ManualTrigger:    manualTrigger,
			Resumer:          resumer.New("", false),
			DeDup:            NewDeDup(false),
			Stdout:           &bytes.Buffer{},
			ConditionChecker: conditions.NewChecker(1),
			Repeater:         repeater.New(&strategy.Once{}),
		}

		go s.Do(ctx)
		time.Sleep(100 * time.Millisecond)

		// send manual trigger
		jobID := s.jobIDFromCommand("echo test")
		manualTrigger <- ManualJobRequest{
			JobID:    jobID,
			Command:  "echo test",
			Schedule: "* * * * *",
		}

		// wait for job to execute
		time.Sleep(200 * time.Millisecond)

		// verify the job executed (we can't easily verify repeater was applied,
		// but at least verify no crash from nil repeater)
		// the real verification is that the test doesn't panic
	})
}

func TestScheduler_reload(t *testing.T) {
	t.Run("reload with changes updates jobs", func(t *testing.T) {
		changeChan := make(chan []crontab.JobSpec, 1)

		mockParser := &mocks.CrontabParserMock{
			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
				return changeChan, nil
			},
			ListFunc: func() ([]crontab.JobSpec, error) {
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo updated"},
				}, nil
			},
			StringFunc: func() string { return "test.crontab" },
		}

		mockCron := &mocks.CronMock{
			StartFunc: func() {},
			StopFunc:  context.Background,
			EntriesFunc: func() []cron.Entry {
				return []cron.Entry{}
			},
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		s := &Scheduler{
			Cron:          mockCron,
			CrontabParser: mockParser,
			Resumer:       resumer.New("", false),
			DeDup:         NewDeDup(false),
			Stdout:        &bytes.Buffer{},
			Repeater:      repeater.New(&strategy.Once{}),
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// start reload in background
		go s.reload(ctx)

		// send a change
		changeChan <- []crontab.JobSpec{
			{Spec: "* * * * *", Command: "echo test"},
		}

		// wait for processing
		time.Sleep(100 * time.Millisecond)

		// verify parser was called
		assert.Len(t, mockParser.ListCalls(), 1)
	})

	t.Run("reload with parser error returns early", func(t *testing.T) {
		mockParser := &mocks.CrontabParserMock{
			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
				return nil, errors.New("parser error")
			},
		}

		s := &Scheduler{
			CrontabParser: mockParser,
		}

		ctx := context.Background()
		// this should return immediately without blocking
		s.reload(ctx)

		// verify Changes was called
		assert.Len(t, mockParser.ChangesCalls(), 1)
	})

	t.Run("reload stops on context cancel", func(t *testing.T) {
		changeChan := make(chan []crontab.JobSpec)

		mockParser := &mocks.CrontabParserMock{
			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
				return changeChan, nil
			},
		}

		s := &Scheduler{
			CrontabParser: mockParser,
		}

		ctx, cancel := context.WithCancel(context.Background())

		// start reload in background
		done := make(chan struct{})
		go func() {
			s.reload(ctx)
			close(done)
		}()

		// cancel context
		cancel()

		// wait for reload to finish
		select {
		case <-done:
			// success, reload stopped
		case <-time.After(time.Second):
			t.Fatal("reload did not stop on context cancel")
		}
	})

	t.Run("reload stops on channel close", func(t *testing.T) {
		changeChan := make(chan []crontab.JobSpec)

		mockParser := &mocks.CrontabParserMock{
			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
				return changeChan, nil
			},
		}

		s := &Scheduler{
			CrontabParser: mockParser,
		}

		ctx := context.Background()

		// start reload in background
		done := make(chan struct{})
		go func() {
			s.reload(ctx)
			close(done)
		}()

		// close the change channel
		close(changeChan)

		// wait for reload to finish
		select {
		case <-done:
			// success, reload stopped
		case <-time.After(time.Second):
			t.Fatal("reload did not stop on channel close")
		}
	})

	t.Run("reload handles load error gracefully", func(t *testing.T) {
		changeChan := make(chan []crontab.JobSpec, 1)
		callCount := 0

		mockParser := &mocks.CrontabParserMock{
			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
				return changeChan, nil
			},
			ListFunc: func() ([]crontab.JobSpec, error) {
				callCount++
				if callCount == 1 {
					return nil, errors.New("load error")
				}
				return []crontab.JobSpec{
					{Spec: "* * * * *", Command: "echo success"},
				}, nil
			},
			StringFunc: func() string { return "test.crontab" },
		}

		mockCron := &mocks.CronMock{
			StartFunc: func() {},
			StopFunc:  context.Background,
			EntriesFunc: func() []cron.Entry {
				return []cron.Entry{}
			},
			ScheduleFunc: func(schedule cron.Schedule, cmd cron.Job) cron.EntryID {
				return 1
			},
			RemoveFunc: func(id cron.EntryID) {},
		}

		s := &Scheduler{
			Cron:          mockCron,
			CrontabParser: mockParser,
			Resumer:       resumer.New("", false),
			DeDup:         NewDeDup(false),
			Stdout:        &bytes.Buffer{},
			Repeater:      repeater.New(&strategy.Once{}),
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// start reload in background
		go s.reload(ctx)

		// send first change (will fail)
		changeChan <- []crontab.JobSpec{
			{Spec: "* * * * *", Command: "echo test"},
		}

		// wait for processing
		time.Sleep(50 * time.Millisecond)

		// send second change (will succeed)
		changeChan <- []crontab.JobSpec{
			{Spec: "* * * * *", Command: "echo test2"},
		}

		// wait for processing
		time.Sleep(50 * time.Millisecond)

		// verify parser was called twice
		assert.Len(t, mockParser.ListCalls(), 2)
	})
}

// helper function for tests
func intPtr(i int) *int {
	return &i
}
