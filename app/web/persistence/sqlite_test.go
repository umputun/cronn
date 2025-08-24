package persistence

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/web/enums"
)

func TestNewSQLiteStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	assert.NotNil(t, store)
	err = store.Close()
	require.NoError(t, err)
}

func TestSQLiteStore_Initialize(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// initialize should create tables
	err = store.Initialize()
	require.NoError(t, err)

	// verify tables exist
	var count int
	err = store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='executions'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// running initialize again should not fail
	err = store.Initialize()
	require.NoError(t, err)
}

func TestSQLiteStore_SaveAndLoadJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	err = store.Initialize()
	require.NoError(t, err)

	// create test jobs
	now := time.Now()
	jobs := []JobInfo{
		{
			ID:         "job1",
			Command:    "echo test1",
			Schedule:   "* * * * *",
			NextRun:    now.Add(time.Minute),
			LastRun:    now.Add(-time.Hour),
			LastStatus: enums.JobStatusSuccess,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
			SortIndex:  0,
		},
		{
			ID:         "job2",
			Command:    "echo test2",
			Schedule:   "@daily",
			NextRun:    now.Add(24 * time.Hour),
			LastRun:    time.Time{}, // zero time
			LastStatus: enums.JobStatusIdle,
			Enabled:    false,
			CreatedAt:  now,
			UpdatedAt:  now,
			SortIndex:  1,
		},
	}

	// save jobs
	err = store.SaveJobs(jobs)
	require.NoError(t, err)

	// load jobs back
	loadedJobs, err := store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, loadedJobs, 2)

	// create map for easier verification
	jobMap := make(map[string]JobInfo)
	for _, job := range loadedJobs {
		jobMap[job.ID] = job
	}

	// verify job1
	job1 := jobMap["job1"]
	assert.Equal(t, "echo test1", job1.Command)
	assert.Equal(t, "* * * * *", job1.Schedule)
	assert.Equal(t, enums.JobStatusSuccess, job1.LastStatus)
	assert.True(t, job1.Enabled)
	assert.WithinDuration(t, now.Add(time.Minute), job1.NextRun, time.Second)
	assert.WithinDuration(t, now.Add(-time.Hour), job1.LastRun, time.Second)

	// verify job2
	job2 := jobMap["job2"]
	assert.Equal(t, "echo test2", job2.Command)
	assert.Equal(t, "@daily", job2.Schedule)
	assert.Equal(t, enums.JobStatusIdle, job2.LastStatus)
	assert.False(t, job2.Enabled)
	assert.True(t, job2.LastRun.IsZero())
}

func TestSQLiteStore_RecordExecution(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	err = store.Initialize()
	require.NoError(t, err)

	// record an execution
	started := time.Now().Add(-5 * time.Second)
	finished := time.Now()
	err = store.RecordExecution("job1", started, finished, enums.JobStatusSuccess, 0)
	require.NoError(t, err)

	// verify execution was recorded
	var count int
	err = store.db.QueryRow("SELECT COUNT(*) FROM executions WHERE job_id = ?", "job1").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// verify execution details
	var jobID, status string
	var startedAt, finishedAt, exitCode int64
	err = store.db.QueryRow("SELECT job_id, started_at, finished_at, status, exit_code FROM executions WHERE job_id = ?", "job1").
		Scan(&jobID, &startedAt, &finishedAt, &status, &exitCode)
	require.NoError(t, err)
	assert.Equal(t, "job1", jobID)
	assert.Equal(t, started.Unix(), startedAt)
	assert.Equal(t, finished.Unix(), finishedAt)
	assert.Equal(t, enums.JobStatusSuccess.String(), status)
	assert.Equal(t, int64(0), exitCode)
}

func TestSQLiteStore_UpdateExistingJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	err = store.Initialize()
	require.NoError(t, err)

	// save initial job
	now := time.Now()
	jobs := []JobInfo{
		{
			ID:         "job1",
			Command:    "echo test",
			Schedule:   "* * * * *",
			LastStatus: enums.JobStatusIdle,
			Enabled:    true,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
	err = store.SaveJobs(jobs)
	require.NoError(t, err)

	// update the job
	jobs[0].LastStatus = enums.JobStatusSuccess
	jobs[0].LastRun = now
	jobs[0].UpdatedAt = now.Add(time.Minute)
	err = store.SaveJobs(jobs)
	require.NoError(t, err)

	// load and verify update
	loadedJobs, err := store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, loadedJobs, 1)
	assert.Equal(t, enums.JobStatusSuccess, loadedJobs[0].LastStatus)
	assert.WithinDuration(t, now, loadedJobs[0].LastRun, time.Second)
}

func TestSQLiteStore_EmptyDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	err = store.Initialize()
	require.NoError(t, err)

	// loading from empty database should not fail
	jobs, err := store.LoadJobs()
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

func TestSQLiteStore_WALMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// verify WAL mode is enabled
	var mode string
	err = store.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode)
}
