package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-pkgz/repeater"
	"github.com/go-pkgz/repeater/strategy"
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
		Stdout:         out,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()

	svc.Do(ctx)
	t.Log(out.String())
	assert.Contains(t, out.String(), "{echo 123} 123\n")
	notif.AssertExpectations(t)
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
	resmr := &mocks.Resumer{}
	scheduleMock := &scheduleMock{next: time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)}
	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, Stdout: wr, Resumer: resmr,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true), EnableLogPrefix: true}

	resmr.On("List").Return(nil).Once()
	resmr.On("OnStart", "echo 123").Return("resume.file", nil).Once()
	resmr.On("OnFinish", "resume.file").Return(nil).Once()

	svc.jobFunc(crontab.JobSpec{Spec: "@startup", Command: "echo 123"}, scheduleMock).Run()
	assert.Equal(t, "{echo 123} 123\n", wr.String())
}

func TestScheduler_jobFuncFailed(t *testing.T) {
	resmr := &mocks.Resumer{}
	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil)
	notif.On("IsOnError").Return(true)
	notif.On("MakeErrorHTML", "@startup", "no-such-thing", mock.MatchedBy(func(s string) bool {
		return strings.Contains(s, "not found")
	})).Return("email msg", nil)

	scheduleMock := &scheduleMock{next: time.Date(2020, 7, 21, 16, 30, 0, 0, time.UTC)}
	wr := bytes.NewBuffer(nil)
	svc := Scheduler{MaxLogLines: 10, Stdout: wr, Resumer: resmr, Notifier: notif,
		Repeater: repeater.New(&strategy.Once{}), DeDup: NewDeDup(true)}

	resmr.On("List").Return(nil).Once()
	resmr.On("OnStart", "no-such-thing").Return("resume.file", nil).Once()
	resmr.On("OnFinish", "resume.file").Return(nil).Once()

	svc.jobFunc(crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, scheduleMock).Run()
	assert.Contains(t, wr.String(), "not found")
	notif.AssertExpectations(t)
}

type scheduleMock struct {
	next time.Time
}

func (s *scheduleMock) Next(time.Time) time.Time {
	return s.next
}

func TestScheduler_notifyOnError(t *testing.T) {
	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil).Once()
	notif.On("IsOnError").Return(true)
	notif.On("MakeErrorHTML", "@startup", "no-such-thing", "message").Return("email msg", nil)

	svc := Scheduler{MaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(crontab.JobSpec{Spec: "@startup", Command: "no-such-thing"}, "message")
	require.NoError(t, err)
	notif.AssertExpectations(t)

}

func TestScheduler_notifyOnCompletion(t *testing.T) {
	notif := &mocks.Notifier{}
	notif.On("Send", mock.Anything, mock.Anything).Return(nil).Once()
	notif.On("IsOnCompletion").Return(true)
	notif.On("MakeCompletionHTML", "@startup", "ls -la").Return("email msg", nil)

	svc := Scheduler{MaxLogLines: 10, Notifier: notif, Repeater: repeater.New(&strategy.Once{})}
	err := svc.notify(crontab.JobSpec{Spec: "@startup", Command: "ls -la"}, "")
	require.NoError(t, err)
	notif.AssertExpectations(t)
}

func TestScheduler_DoWithReload(t *testing.T) {

	cr := &mocks.Cron{}
	resmr := &mocks.Resumer{}
	parser := &mocks.CrontabParser{}
	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resmr.On("List").Return(nil).Once()
	cr.On("Entries").Return([]cron.Entry{{}, {}, {}}).Times(2)
	cr.On("Remove", mock.Anything).Times(6)
	cr.On("Start").Once()
	cr.On("Stop").Return(ctx).Once()

	parser.On("String").Return("parser")
	parser.On("List").Return([]crontab.JobSpec{
		{Spec: "1 * * * *", Command: "test1"},
		{Spec: "2 * * * *", Command: "test2"},
	}, nil).Once()

	parser.On("List").Return([]crontab.JobSpec{
		{Spec: "11 * * * *", Command: "test1"},
	}, nil).Once()

	upCh := func() <-chan []crontab.JobSpec {
		ch := make(chan []crontab.JobSpec, 1)
		ch <- []crontab.JobSpec{{Command: "cmd", Spec: "@reboot"}}
		close(ch)
		return ch
	}()

	parser.On("Changes", mock.Anything).Return(upCh, nil).Once()

	cr.On("Schedule", mock.Anything, mock.Anything).Return(cron.EntryID(1)).Times(2)
	cr.On("Schedule", mock.Anything, mock.Anything).Return(cron.EntryID(2)).Once()

	svc.Do(ctx)

	cr.AssertExpectations(t)
	resmr.AssertExpectations(t)
	parser.AssertExpectations(t)
}

func TestScheduler_DoWithResume(t *testing.T) {
	cr := &mocks.Cron{}
	resmr := &mocks.Resumer{}
	parser := &mocks.CrontabParser{}
	svc := Scheduler{
		Cron:           cr,
		Resumer:        resmr,
		CrontabParser:  parser,
		UpdatesEnabled: false,
		Repeater:       repeater.New(&strategy.Once{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resmr.On("List").Return([]resumer.Cmd{{Command: "cmd1", Fname: "f1"}, {Command: "cmd2", Fname: "f2"}}).Once()
	cr.On("Entries").Return([]cron.Entry{}).Times(1)
	parser.On("List").Return([]crontab.JobSpec{}, nil).Once()

	cr.On("Start").Once()
	cr.On("Stop").Return(ctx).Once()

	resmr.On("OnFinish", "f1").Return(nil).Once()
	resmr.On("OnFinish", "f2").Return(nil).Once()

	svc.Do(ctx)

	cr.AssertExpectations(t)
	resmr.AssertExpectations(t)
	parser.AssertExpectations(t)
}
