package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

func newStepsStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	return store
}

func TestSQLiteStore_ExecutionSteps(t *testing.T) {
	store := newStepsStore(t)
	start := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	steps := []request.StepState{
		{Index: 2, Name: "Transform", Command: "transform.sh 20261016", Status: enums.StepStatusSuccess, Attempts: 2,
			StartedAt: start, FinishedAt: start.Add(48 * time.Second), Output: "rows: 10"},
		{Index: 3, Command: "load.sh", Status: enums.StepStatusFailed, ExitCode: 3, Attempts: 1,
			StartedAt: start.Add(48 * time.Second), FinishedAt: start.Add(time.Minute), Output: "boom"},
		{Index: 4, Command: "notify.sh", Status: enums.StepStatusSkipped},
	}
	require.NoError(t, store.RecordExecution(request.RecordExecution{JobID: "job1", StartedAt: start,
		FinishedAt: start.Add(time.Minute), Status: enums.JobStatusFailed, ExitCode: 3, Steps: steps}))
	require.NoError(t, store.RecordExecution(request.RecordExecution{JobID: "job1", StartedAt: start.Add(time.Hour),
		FinishedAt: start.Add(time.Hour), Status: enums.JobStatusSuccess, Output: "plain"}))

	execs, err := store.GetExecutions("job1", 10)
	require.NoError(t, err)
	require.Len(t, execs, 2)
	assert.Zero(t, execs[0].StepFirst, "a run without steps has no step range")
	assert.Zero(t, execs[0].StepLast)
	assert.Equal(t, 2, execs[1].StepFirst)
	assert.Equal(t, 3, execs[1].StepLast, "the range ends at the failed step, skipped steps excluded")

	byID, err := store.GetExecutionByID(execs[1].ID)
	require.NoError(t, err)
	assert.Equal(t, 2, byID.StepFirst)
	assert.Equal(t, 3, byID.StepLast)
	byStart, err := store.GetExecutionByStart("job1", start.UnixMicro())
	require.NoError(t, err)
	assert.Equal(t, 3, byStart.StepLast)

	loaded, err := store.GetExecutionSteps(execs[1].ID)
	require.NoError(t, err)
	require.Len(t, loaded, 3)
	assert.Equal(t, StepInfo{ExecutionID: execs[1].ID, Index: 2, Name: "Transform", Command: "transform.sh 20261016",
		Status: enums.StepStatusSuccess, Attempts: 2, StartedAt: start, FinishedAt: start.Add(48 * time.Second),
		Output: "rows: 10"}, normalizeStepTimes(loaded[0]))
	assert.Equal(t, enums.StepStatusFailed, loaded[1].Status)
	assert.Equal(t, 3, loaded[1].ExitCode)
	assert.Equal(t, enums.StepStatusSkipped, loaded[2].Status)
	assert.True(t, loaded[2].StartedAt.IsZero(), "a skipped step keeps a zero start")

	plain, err := store.GetExecutionSteps(execs[0].ID)
	require.NoError(t, err)
	assert.Empty(t, plain)
	assert.NotNil(t, plain)
}

// normalizeStepTimes converts the step's times to UTC, as stored times come back in the local zone
func normalizeStepTimes(st StepInfo) StepInfo {
	st.StartedAt, st.FinishedAt = st.StartedAt.UTC(), st.FinishedAt.UTC()
	return st
}

func TestSQLiteStore_CleanupOldExecutionsRemovesSteps(t *testing.T) {
	store := newStepsStore(t)
	base := time.Now()
	for i := range 4 {
		started := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, store.RecordExecution(request.RecordExecution{JobID: "job1", StartedAt: started,
			FinishedAt: started, Status: enums.JobStatusSuccess,
			Steps: []request.StepState{{Index: 1, Command: "a", Status: enums.StepStatusSuccess},
				{Index: 2, Command: "b", Status: enums.StepStatusSuccess}}}))
	}
	require.NoError(t, store.RecordExecution(request.RecordExecution{JobID: "job2", StartedAt: base, FinishedAt: base,
		Status: enums.JobStatusSuccess, Steps: []request.StepState{{Index: 1, Command: "x", Status: enums.StepStatusSuccess}}}))

	require.NoError(t, store.CleanupOldExecutions("job1", 2))

	execs, err := store.GetExecutions("job1", 10)
	require.NoError(t, err)
	require.Len(t, execs, 2)
	assert.Equal(t, 5, countSteps(t, store.db), "two kept runs of job1 with two steps each, and job2's step")
	for _, e := range execs {
		steps, err := store.GetExecutionSteps(e.ID)
		require.NoError(t, err)
		assert.Len(t, steps, 2)
	}
}

func countSteps(t *testing.T, db *sqlx.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.Get(&n, "SELECT COUNT(*) FROM execution_steps"))
	return n
}

func TestSQLiteStore_SaveAndLoadJobs_LastStep(t *testing.T) {
	store := newStepsStore(t)
	require.NoError(t, store.SaveJobs([]JobInfo{
		{ID: "steps", Command: "a && b", Schedule: "@daily", LastStep: 2, RunningStep: 1,
			Steps: []JobStep{{Command: "a"}, {Command: "b"}}, LastStatus: enums.JobStatusFailed, Enabled: true},
		{ID: "plain", Command: "c", Schedule: "@daily", LastStatus: enums.JobStatusIdle, Enabled: true},
	}))

	loaded, err := store.LoadJobs()
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	assert.Equal(t, 2, loaded[0].LastStep)
	assert.Zero(t, loaded[0].RunningStep, "the running step is not stored")
	assert.Nil(t, loaded[0].Steps, "steps come from the crontab, not the database")
	assert.Zero(t, loaded[1].LastStep)
}

func TestSQLiteStore_Migration_LastStepColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	db, err := sqlx.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, command TEXT NOT NULL, schedule TEXT NOT NULL,
		next_run DATETIME, last_run DATETIME, last_status TEXT, enabled BOOLEAN DEFAULT 1, created_at DATETIME,
		updated_at DATETIME, sort_index INTEGER DEFAULT 0)`)
	require.NoError(t, err)
	now := time.Now()
	_, err = db.Exec(`INSERT INTO jobs (id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at)
		VALUES ('j1', 'echo', '@daily', ?, ?, 'idle', 1, ?, ?)`, now, now, now, now)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	jobs, err := store.LoadJobs()
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Zero(t, jobs[0].LastStep)
	steps, err := store.GetExecutionSteps(1)
	require.NoError(t, err, "the steps table exists on a migrated database")
	assert.Empty(t, steps)
}
