package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service/mocks"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

// stepsScheduler makes a scheduler that records job events and resumer registrations
func stepsScheduler(t *testing.T) (*Scheduler, *mocks.JobEventHandlerMock, *mocks.ResumerMock) {
	t.Helper()
	handler := &mocks.JobEventHandlerMock{
		OnJobStartFunc:    func(request.OnJobStart) {},
		OnJobCompleteFunc: func(request.OnJobComplete) {},
	}
	var mu sync.Mutex
	files := 0
	resmr := &mocks.ResumerMock{
		OnStartFunc: func(string) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			files++
			return "resume-" + strconv.Itoa(files), nil
		},
		OnFinishFunc: func(string) error { return nil },
	}
	svc := &Scheduler{
		Stdout:          bytes.NewBuffer(nil),
		Resumer:         resmr,
		DeDup:           NewDeDup(true),
		JobEventHandler: handler,
		NotifyTimeout:   time.Second,
		ExecMaxLogLines: 10,
	}
	return svc, handler, resmr
}

func stepsJob(cmds ...string) crontab.JobSpec {
	steps := make([]crontab.StepSpec, len(cmds))
	for i, cmd := range cmds {
		steps[i] = crontab.StepSpec{Command: cmd}
	}
	return crontab.JobSpec{Spec: "* * * * *", Steps: steps, Command: crontab.JoinSteps(steps)}
}

func stepStatuses(states []request.StepState) []enums.StepStatus {
	res := make([]enums.StepStatus, len(states))
	for i, st := range states {
		res[i] = st.Status
	}
	return res
}

func TestScheduler_runSteps(t *testing.T) {
	once := repeater.New(&strategy.Once{})

	t.Run("all steps succeed in order", func(t *testing.T) {
		svc, handler, resmr := stepsScheduler(t)
		job := stepsJob("echo one", "echo two", "echo three")
		job.Steps[0].Name = "First"

		require.NoError(t, svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once}))

		require.Len(t, handler.OnJobStartCalls(), 1)
		start := handler.OnJobStartCalls()[0].Req
		assert.Equal(t, job.Command, start.Command)
		assert.Empty(t, start.ExecutedCommand, "a scheduled run records no executed command")
		require.NotNil(t, start.LiveSteps)
		assert.Nil(t, start.LiveOutput)

		require.Len(t, handler.OnJobCompleteCalls(), 1)
		done := handler.OnJobCompleteCalls()[0].Req
		require.NoError(t, done.Err)
		assert.Equal(t, 0, done.ExitCode)
		assert.Empty(t, done.Output)
		require.Len(t, done.Steps, 3)
		assert.Equal(t, []enums.StepStatus{enums.StepStatusSuccess, enums.StepStatusSuccess, enums.StepStatusSuccess},
			stepStatuses(done.Steps))
		for i, want := range []string{"one", "two", "three"} {
			assert.Equal(t, i+1, done.Steps[i].Index)
			assert.Equal(t, want, done.Steps[i].Output)
			assert.Equal(t, 1, done.Steps[i].Attempts)
			assert.False(t, done.Steps[i].StartedAt.IsZero())
			assert.False(t, done.Steps[i].FinishedAt.Before(done.Steps[i].StartedAt))
		}
		assert.Equal(t, "First", done.Steps[0].Name)
		assert.Equal(t, "echo two", done.Steps[1].Command)
		assert.True(t, done.Steps[1].StartedAt.After(done.Steps[0].StartedAt), "steps run one after another")

		require.Len(t, resmr.OnStartCalls(), 3)
		assert.Equal(t, "(echo one) && (echo two) && (echo three)", resmr.OnStartCalls()[0].Cmd)
		assert.Equal(t, "(echo two) && (echo three)", resmr.OnStartCalls()[1].Cmd)
		assert.Equal(t, "echo three", resmr.OnStartCalls()[2].Cmd)
		require.Len(t, resmr.OnFinishCalls(), 3)
		assert.Equal(t, "resume-1", resmr.OnFinishCalls()[0].Fname)
		assert.Equal(t, "resume-3", resmr.OnFinishCalls()[2].Fname)
	})

	t.Run("failed step stops the chain", func(t *testing.T) {
		svc, handler, resmr := stepsScheduler(t)
		notif := &mocks.NotifierMock{
			IsOnErrorFunc:     func() bool { return true },
			MakeErrorHTMLFunc: func(_, _, errorLog string) (string, error) { return errorLog, nil },
			SendFunc:          func(context.Context, string, string) error { return nil },
		}
		svc.Notifier = notif
		svc.NotifyMaxLogLines = 10
		job := stepsJob("echo one", "echo boom; exit 3", "echo three")

		err := svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `step 2/3 "echo boom; exit 3" failed`)

		done := handler.OnJobCompleteCalls()[0].Req
		require.Error(t, done.Err)
		assert.Equal(t, 3, done.ExitCode)
		assert.Equal(t, []enums.StepStatus{enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped},
			stepStatuses(done.Steps))
		assert.Equal(t, 3, done.Steps[1].ExitCode)
		assert.Equal(t, "boom", done.Steps[1].Output)
		assert.Empty(t, done.Steps[2].Output)
		assert.True(t, done.Steps[2].StartedAt.IsZero(), "a skipped step never starts")

		require.Len(t, notif.MakeErrorHTMLCalls(), 1)
		assert.Contains(t, notif.MakeErrorHTMLCalls()[0].ErrorLog, "step 2/3")
		assert.Contains(t, notif.MakeErrorHTMLCalls()[0].ErrorLog, "\n\nboom")

		require.Len(t, resmr.OnStartCalls(), 2)
		assert.Equal(t, "(echo boom; exit 3) && (echo three)", resmr.OnStartCalls()[1].Cmd)
		assert.Len(t, resmr.OnFinishCalls(), 1, "the failed step stays registered for resume")
	})

	t.Run("retries repeat only the failed step", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		dir := t.TempDir()
		logFile, marker := filepath.Join(dir, "log"), filepath.Join(dir, "marker")
		job := stepsJob("echo one >> "+logFile,
			"test -f "+marker+" || { touch "+marker+"; echo first try; exit 1; }; echo second try",
			"echo three >> "+logFile)

		rptr := repeater.New(&strategy.FixedDelay{Repeats: 3, Delay: time.Millisecond})
		require.NoError(t, svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: rptr}))

		data, err := os.ReadFile(logFile) //nolint:gosec // test file in temp dir
		require.NoError(t, err)
		assert.Equal(t, "one\nthree\n", string(data), "the steps around the retried one run once")
		done := handler.OnJobCompleteCalls()[0].Req
		assert.Equal(t, []int{1, 2, 1}, []int{done.Steps[0].Attempts, done.Steps[1].Attempts, done.Steps[2].Attempts})
		assert.Equal(t, "first try\nsecond try", done.Steps[1].Output)
	})

	t.Run("templates resolve against one time", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		job := stepsJob("echo {{.YYYYMMDD}}", "echo {{.YYYYMMDD}}")
		at := time.Date(2026, 10, 16, 3, 0, 0, 0, time.Local)

		require.NoError(t, svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, customTime: &at, repeater: once}))

		done := handler.OnJobCompleteCalls()[0].Req
		assert.Equal(t, "echo 20261016", done.Steps[0].Command)
		assert.Equal(t, "20261016", done.Steps[0].Output)
		assert.Equal(t, "20261016", done.Steps[1].Output)
	})

	t.Run("bad template fails before any step runs", func(t *testing.T) {
		svc, handler, resmr := stepsScheduler(t)
		job := stepsJob("echo ok", "echo {{.Nope")

		err := svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't parse step 2")
		assert.Empty(t, handler.OnJobStartCalls())
		assert.Empty(t, resmr.OnStartCalls())
	})

	t.Run("manual run from a later step with edited commands", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		job := stepsJob("echo one", "echo two", "echo three")
		steps := []crontab.StepSpec{{Command: "echo TWO"}, {Command: "echo three"}}

		require.NoError(t, svc.runSteps(context.Background(), stepsParams{job: job, steps: steps, fromStep: 2, repeater: once, manual: true}))

		start := handler.OnJobStartCalls()[0].Req
		assert.Equal(t, "echo TWO && echo three", start.ExecutedCommand)
		done := handler.OnJobCompleteCalls()[0].Req
		assert.Equal(t, job.Command, done.Command, "the run is recorded against the job")
		assert.Equal(t, "echo TWO && echo three", done.ExecutedCommand)
		require.Len(t, done.Steps, 2)
		assert.Equal(t, 2, done.Steps[0].Index)
		assert.Equal(t, "TWO", done.Steps[0].Output)
		assert.Equal(t, 3, done.Steps[1].Index)
	})

	t.Run("duplicate run is rejected", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		job := stepsJob("echo one", "echo two")
		require.True(t, svc.DeDup.Add("echo one && echo two#* * * * *"))

		err := svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicated job")
		assert.Empty(t, handler.OnJobStartCalls())
	})

	t.Run("resumer failure fails the step", func(t *testing.T) {
		svc, handler, resmr := stepsScheduler(t)
		resmr.OnStartFunc = func(cmd string) (string, error) {
			if cmd == "echo two" {
				return "", errors.New("disk full")
			}
			return "resume", nil
		}
		job := stepsJob("echo one", "echo two")

		err := svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to initiate resumer for step 2/2")
		done := handler.OnJobCompleteCalls()[0].Req
		assert.Equal(t, 1, done.ExitCode)
		assert.Equal(t, []enums.StepStatus{enums.StepStatusSuccess, enums.StepStatusFailed}, stepStatuses(done.Steps))
	})

	t.Run("live states while a step runs", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		fifo := filepath.Join(t.TempDir(), "gate")
		require.NoError(t, syscall.Mkfifo(fifo, 0o600))
		started := make(chan request.OnJobStart, 1)
		handler.OnJobStartFunc = func(req request.OnJobStart) { started <- req }
		job := stepsJob("echo one", "echo waiting; read line < "+fifo+"; echo $line", "echo three")

		done := make(chan error, 1)
		go func() {
			done <- svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once})
		}()

		live := (<-started).LiveSteps
		require.NotNil(t, live)
		require.Eventually(t, func() bool {
			st := live()
			return st[1].Status == enums.StepStatusRunning && st[1].Output == "waiting"
		}, 5*time.Second, 10*time.Millisecond)
		st := live()
		assert.Equal(t, []enums.StepStatus{enums.StepStatusSuccess, enums.StepStatusRunning, enums.StepStatusPending},
			stepStatuses(st))
		assert.Equal(t, "one", st[0].Output)

		require.NoError(t, os.WriteFile(fifo, []byte("released\n"), 0o600))
		require.NoError(t, <-done)
		assert.Equal(t, "waiting\nreleased", live()[1].Output)
		assert.Equal(t, enums.StepStatusSuccess, live()[2].Status)
	})

	t.Run("no step output when capture is disabled", func(t *testing.T) {
		svc, handler, _ := stepsScheduler(t)
		svc.ExecMaxLogLines = 0
		job := stepsJob("echo one", "echo two")

		require.NoError(t, svc.runSteps(context.Background(), stepsParams{job: job, steps: job.Steps, fromStep: 1, repeater: once}))
		done := handler.OnJobCompleteCalls()[0].Req
		assert.Empty(t, done.Steps[0].Output)
		assert.Equal(t, enums.StepStatusSuccess, done.Steps[1].Status)
	})
}

func TestResumeChain(t *testing.T) {
	assert.Equal(t, "echo a", resumeChain([]string{"echo a"}))
	assert.Equal(t, "(cd /tmp; ls) && (echo b || true)", resumeChain([]string{"cd /tmp; ls", "echo b || true"}))
}

func TestCountedRepeater(t *testing.T) {
	c := &countedRepeater{Repeater: repeater.New(&strategy.FixedDelay{Repeats: 3, Delay: time.Millisecond})}
	calls := 0
	err := c.Do(context.Background(), func() error {
		calls++
		if calls < 2 {
			return errors.New("not yet")
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, c.attempts)
}
