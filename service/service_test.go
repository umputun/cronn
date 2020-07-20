package service

import (
	"context"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/mock"
	"github.com/umputun/cronn/crontab"
	"github.com/umputun/cronn/service/mocks"
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
