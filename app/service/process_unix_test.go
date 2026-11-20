//go:build !windows

package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
)

func TestScheduler_terminate(t *testing.T) {
	saved := stopGrace
	stopGrace = 300 * time.Millisecond
	t.Cleanup(func() { stopGrace = saved })
	spec := crontab.JobSpec{Spec: "* * * * *", Command: "group"}

	tbl := []struct {
		name    string
		command string
	}{
		{name: "descendant ignoring TERM while the shell waits", command: "(trap '' TERM; sleep 30) & echo $! > %s; wait"},
		{name: "descendant holding the output after the shell exited", command: "(trap '' TERM; sleep 30) & echo $! > %s"},
	}
	for _, tc := range tbl {
		t.Run(tc.name, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			started := make(chan request.OnJobStart, 1)
			svc := &Scheduler{Stdout: bytes.NewBuffer(nil), DeDup: NewDeDup(false), NotifyTimeout: time.Second,
				Resumer: &mocks.ResumerMock{OnStartFunc: func(string) (string, error) { return "", nil },
					OnFinishFunc: func(string) error { return nil }},
				JobEventHandler: &mocks.JobEventHandlerMock{OnJobStartFunc: func(req request.OnJobStart) { started <- req },
					OnJobCompleteFunc: func(request.OnJobComplete) {}}}
			done := make(chan error, 1)
			go func() {
				done <- svc.runJobWithCommand(context.Background(), spec, strings.ReplaceAll(tc.command, "%s", pidFile), nil,
					repeater.New(&strategy.Once{}), false)
			}()
			req := <-started

			var pid int
			require.Eventually(t, func() bool {
				data, err := os.ReadFile(pidFile) //nolint:gosec // test temp file
				if err != nil {
					return false
				}
				pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
				return err == nil
			}, 5*time.Second, 10*time.Millisecond)
			require.NoError(t, syscall.Kill(pid, 0), "descendant is running before the stop")

			stopAt := time.Now()
			require.True(t, svc.StopRun(spec.Command, req.StartTime.UnixMicro()))
			select {
			case err := <-done:
				require.ErrorIs(t, err, errStopped)
			case <-time.After(5 * time.Second):
				t.Fatal("stopped run did not finish")
			}
			assert.GreaterOrEqual(t, time.Since(stopAt), stopGrace, "the grace runs before SIGKILL")
			assert.Eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) },
				2*time.Second, 10*time.Millisecond, "descendant is gone")
		})
	}
}

func TestScheduler_runCommandOutsideGroup(t *testing.T) {
	saved := stopGrace
	stopGrace = 200 * time.Millisecond
	t.Cleanup(func() { stopGrace = saved })
	spec := crontab.JobSpec{Spec: "* * * * *", Command: "escaped"}

	t.Run("a stopped run ends while a process outside the group holds its output", func(t *testing.T) {
		pidFile := filepath.Join(t.TempDir(), "pid")
		started := make(chan request.OnJobStart, 1)
		svc := &Scheduler{Stdout: bytes.NewBuffer(nil), DeDup: NewDeDup(false), NotifyTimeout: time.Second,
			Resumer: &mocks.ResumerMock{OnStartFunc: func(string) (string, error) { return "", nil },
				OnFinishFunc: func(string) error { return nil }},
			JobEventHandler: &mocks.JobEventHandlerMock{OnJobStartFunc: func(req request.OnJobStart) { started <- req },
				OnJobCompleteFunc: func(request.OnJobComplete) {}}}
		command := "perl -e 'setpgrp(0,0); open(F, \">\", $ARGV[0]); print F $$; close F; sleep 10' " + pidFile + " &"
		done := make(chan error, 1)
		go func() {
			done <- svc.runJobWithCommand(context.Background(), spec, command, nil, repeater.New(&strategy.Once{}), false)
		}()
		req := <-started
		var pid int
		require.Eventually(t, func() bool {
			data, err := os.ReadFile(pidFile) //nolint:gosec // test temp file
			if err != nil {
				return false
			}
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			return err == nil
		}, 5*time.Second, 10*time.Millisecond)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

		require.True(t, svc.StopRun(spec.Command, req.StartTime.UnixMicro()))
		select {
		case err := <-done:
			require.ErrorIs(t, err, errStopped)
		case <-time.After(3 * time.Second):
			t.Fatal("stopped run waited for the escaped process")
		}
		assert.NoError(t, syscall.Kill(pid, 0), "a process outside the group gets no signal")
	})

	t.Run("an uncanceled run reads late output of a process outside the group", func(t *testing.T) {
		out := bytes.NewBuffer(nil)
		svc := &Scheduler{}
		command := "perl -e 'setpgrp(0,0); select(undef, undef, undef, 0.3); print \"late\\n\"' & echo early"
		_, err := svc.executeCommand(t.Context(), command, out, repeater.New(&strategy.Once{}), nil)
		require.NoError(t, err)
		assert.Equal(t, "early\nlate\n", out.String())
	})

	t.Run("an uncanceled run reads late output of a child in the group", func(t *testing.T) {
		out := bytes.NewBuffer(nil)
		svc := &Scheduler{}
		_, err := svc.executeCommand(t.Context(), "(sleep 0.3; echo late) & echo early", out, repeater.New(&strategy.Once{}), nil)
		require.NoError(t, err)
		assert.Equal(t, "early\nlate\n", out.String())
	})
}
