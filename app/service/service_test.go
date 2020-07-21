package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/umputun/cronn/app/crontab"
	resumer2 "github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service/mocks"
)

func TestScheduler_Do(t *testing.T) {

	cr := &mocks.Cron{}
	resumer := &mocks.Resumer{}
	parser := &mocks.CrontabParser{}
	svc := Scheduler{
		Cron:           cr,
		Resumer:        resumer,
		CrontabParser:  parser,
		UpdatesEnabled: false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resumer.On("List").Return(nil).Once()
	cr.On("Entries").Return([]cron.Entry{{}, {}, {}}).Once()
	cr.On("Remove", mock.Anything).Times(3)
	cr.On("Start").Once()
	cr.On("Stop").Return(ctx).Once()
	parser.On("List").Return([]crontab.JobSpec{
		{Spec: "1 * * * *", Command: "test1"},
		{Spec: "2 * * * *", Command: "test2"},
	}, nil).Once()

	cr.On("Schedule", mock.Anything, mock.Anything).Return(cron.EntryID(1)).Once()
	cr.On("Schedule", mock.Anything, mock.Anything).Return(cron.EntryID(2)).Once()

	svc.Do(ctx)

	cr.AssertExpectations(t)
	resumer.AssertExpectations(t)
	parser.AssertExpectations(t)
}

func TestScheduler_DoIntegration(t *testing.T) {

	out := bytes.NewBuffer(nil)
	cr := cron.New()
	parser := crontab.New("testfiles/crontab", time.Minute)
	resumer := resumer2.New("/tmp", false)

	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil)
	svc := Scheduler{
		Cron:           cr,
		Resumer:        resumer,
		CrontabParser:  parser,
		UpdatesEnabled: false,
		Notifier:       notif,
		stdout:         out,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()

	svc.Do(ctx)
	t.Log(out.String())
	assert.Contains(t, out.String(), "123\n")
	notif.AssertExpectations(t)
}

func TestScheduler_execute(t *testing.T) {
	svc := Scheduler{}
	wr := bytes.NewBuffer(nil)
	err := svc.execute("echo 123", wr)
	require.NoError(t, err)
	assert.Equal(t, "123\n", string(wr.Bytes()))
}

func TestScheduler_executeFailedNotFound(t *testing.T) {
	svc := Scheduler{}
	wr := bytes.NewBuffer(nil)
	err := svc.execute("no-such-command", wr)
	require.Error(t, err)
	assert.Equal(t, "sh: no-such-command: command not found\n", string(wr.Bytes()))
}

func TestScheduler_executeFailedExitCode(t *testing.T) {
	svc := Scheduler{MaxLogLines: 10}
	wr := bytes.NewBuffer(nil)
	err := svc.execute("testfiles/fail.sh", wr)
	require.Error(t, err)
	assert.Contains(t, string(wr.Bytes()), "TestScheduler_executeFailed")
	t.Log(err)
	assert.Equal(t, 10+3, len(strings.Split(err.Error(), "\n")))
	assert.Equal(t, "TestScheduler_executeFailed 14", strings.Split(err.Error(), "\n")[12])
}
