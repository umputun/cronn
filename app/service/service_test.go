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
	"github.com/umputun/cronn/app/resumer"
	"github.com/umputun/cronn/app/service/mocks"
)

func TestScheduler_Do(t *testing.T) {

	cr := &mocks.Cron{}
	resmr := &mocks.Resumer{}
	parser := &mocks.CrontabParser{}
	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resmr.On("List").Return(nil).Once()
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
	resmr.AssertExpectations(t)
	parser.AssertExpectations(t)
}

func TestScheduler_DoIntegration(t *testing.T) {
	t.Skip()
	out := bytes.NewBuffer(nil)
	cr := cron.New()
	parser := crontab.New("testfiles/crontab", time.Minute)
	res := resumer.New("/tmp", false)

	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil)
	notif.On("IsOnError").Return(true)
	notif.On("IsOnCompletion").Return(false)
	svc := Scheduler{
		Cron:           cr,
		Resumer:        res,
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
	err := svc.executeCommand("echo 123", wr)
	require.NoError(t, err)
	assert.Equal(t, "123\n", string(wr.Bytes()))
}

func TestScheduler_executeFailedNotFound(t *testing.T) {
	svc := Scheduler{}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("no-such-command", wr)
	require.Error(t, err)
	assert.Equal(t, "sh: no-such-command: command not found\n", string(wr.Bytes()))
}

func TestScheduler_executeFailedExitCode(t *testing.T) {
	svc := Scheduler{MaxLogLines: 10}
	wr := bytes.NewBuffer(nil)
	err := svc.executeCommand("testfiles/fail.sh", wr)
	require.Error(t, err)
	assert.Contains(t, string(wr.Bytes()), "TestScheduler_executeFailed")
	t.Log(err)
	assert.Equal(t, 10+3, len(strings.Split(err.Error(), "\n")))
	assert.Equal(t, "TestScheduler_executeFailed 14", strings.Split(err.Error(), "\n")[12])
}

func TestScheduler_jobFunc(t *testing.T) {
	resmr := &mocks.Resumer{}
	scheduleMock := &scheduleMock{next: time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)}
	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, stdout: wr, Resumer: resmr}

	resmr.On("List").Return(nil).Once()
	resmr.On("OnStart", "echo 123").Return("resume.file", nil).Once()
	resmr.On("OnFinish", "resume.file").Return(nil).Once()

	svc.jobFunc(cronReq{spec: "@startup", command: "echo 123"}, scheduleMock).Run()
	assert.Equal(t, "123\n", wr.String())
}

func TestScheduler_jobFuncFailed(t *testing.T) {
	resmr := &mocks.Resumer{}
	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil)
	notif.On("IsOnError").Return(true)
	scheduleMock := &scheduleMock{next: time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)}
	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, stdout: wr, Resumer: resmr, Notifier: notif}

	resmr.On("List").Return(nil).Once()
	resmr.On("OnStart", "no-such-thing").Return("resume.file", nil).Once()
	resmr.On("OnFinish", "resume.file").Return(nil).Once()

	svc.jobFunc(cronReq{spec: "@startup", command: "no-such-thing"}, scheduleMock).Run()
	assert.Equal(t, "sh: no-such-thing: command not found\n", wr.String())
	notif.AssertExpectations(t)
}

type scheduleMock struct {
	next time.Time
}

func (s *scheduleMock) Next(time.Time) time.Time {
	return s.next
}

func TestScheduler_notify(t *testing.T) {
	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil).Once()
	notif.On("IsOnError").Return(true)
	svc := Scheduler{MaxLogLines: 10, Notifier: notif}
	err := svc.notify(cronReq{spec: "@startup", command: "no-such-thing"}, "message")
	require.NoError(t, err)
	notif.AssertExpectations(t)

}
