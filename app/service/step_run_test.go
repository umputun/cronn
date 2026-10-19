package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/web/enums"
)

func TestStepRun(t *testing.T) {
	steps := []crontab.StepSpec{{Name: "two", Command: "b {{.YYYY}}"}, {Command: "c"}, {Command: "d"}}

	t.Run("pending states numbered from the first step run", func(t *testing.T) {
		r := newStepRun(steps, []string{"b 2026", "c", "d"}, 2, 5)
		st := r.snapshot()
		require.Len(t, st, 3)
		assert.Equal(t, 2, st[0].Index)
		assert.Equal(t, 4, st[2].Index)
		assert.Equal(t, "two", st[0].Name)
		assert.Equal(t, "b 2026", st[0].Command)
		assert.Equal(t, []enums.StepStatus{enums.StepStatusPending, enums.StepStatusPending, enums.StepStatusPending},
			stepStatuses(st))
		require.NotNil(t, r.capture(0))
	})

	t.Run("start, finish and skip", func(t *testing.T) {
		r := newStepRun(steps, []string{"b", "c", "d"}, 1, 5)
		r.start(0)
		_, err := r.capture(0).Write([]byte("partial\n"))
		require.NoError(t, err)
		assert.Equal(t, "partial", r.snapshot()[0].Output, "a running step shows its output so far")

		r.finish(0, 2, nil)
		r.start(1)
		r.finish(1, 1, errors.New("broken"))
		r.skipFrom(2)

		st := r.snapshot()
		assert.Equal(t, []enums.StepStatus{enums.StepStatusSuccess, enums.StepStatusFailed, enums.StepStatusSkipped},
			stepStatuses(st))
		assert.Equal(t, 2, st[0].Attempts)
		assert.Equal(t, "partial", st[0].Output)
		assert.Equal(t, 1, st[1].ExitCode)
		assert.False(t, st[1].FinishedAt.IsZero())
	})

	t.Run("snapshot is a copy", func(t *testing.T) {
		r := newStepRun(steps, []string{"b", "c", "d"}, 1, 5)
		st := r.snapshot()
		st[0].Status = enums.StepStatusFailed
		assert.Equal(t, enums.StepStatusPending, r.snapshot()[0].Status)
	})

	t.Run("no captures when output is disabled", func(t *testing.T) {
		r := newStepRun(steps, []string{"b", "c", "d"}, 1, 0)
		assert.Nil(t, r.capture(0))
		r.start(0)
		r.finish(0, 1, nil)
		assert.Empty(t, r.snapshot()[0].Output)
	})
}
