package persistence

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

func TestNewSQLiteStore(t *testing.T) {
	t.Run("successful creation", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		assert.NotNil(t, store)
		err = store.Close()
		require.NoError(t, err)
	})

	t.Run("invalid path", func(t *testing.T) {
		// try to create database in non-existent directory
		store, err := NewSQLiteStore("/invalid/path/that/does/not/exist/test.db")
		require.Error(t, err)
		assert.Nil(t, store)
	})
}

func TestSQLiteStore_TablesCreated(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// verify tables were created during initialization
	var count int
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='executions'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestSQLiteStore_SaveAndLoadJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

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

	// record an execution
	started := time.Now().Add(-5 * time.Second)
	finished := time.Now()
	err = store.RecordExecution(request.RecordExecution{
		JobID:           "job1",
		StartedAt:       started,
		FinishedAt:      finished,
		Status:          enums.JobStatusSuccess,
		ExitCode:        0,
		ExecutedCommand: "echo test",
		Output:          "",
	})
	require.NoError(t, err)

	ctx := context.Background()

	// verify execution was recorded
	var count int
	err = store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM executions WHERE job_id = ?", "job1").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// verify execution details
	var jobID, status string
	var startedAt, finishedAt time.Time
	var exitCode int
	err = store.db.QueryRowContext(ctx, "SELECT job_id, started_at, finished_at, status, exit_code FROM executions WHERE job_id = ?", "job1").
		Scan(&jobID, &startedAt, &finishedAt, &status, &exitCode)
	require.NoError(t, err)
	assert.Equal(t, "job1", jobID)
	assert.WithinDuration(t, started, startedAt, time.Second)
	assert.WithinDuration(t, finished, finishedAt, time.Second)
	assert.Equal(t, enums.JobStatusSuccess.String(), status)
	assert.Equal(t, 0, exitCode)
}

func TestSQLiteStore_RecordExecutionWithEditedCommand(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// record execution with different executed command
	started := time.Now().Add(-5 * time.Second)
	finished := time.Now()
	err = store.RecordExecution(request.RecordExecution{
		JobID:           "job1",
		StartedAt:       started,
		FinishedAt:      finished,
		Status:          enums.JobStatusSuccess,
		ExitCode:        0,
		ExecutedCommand: "echo edited",
		Output:          "",
	})
	require.NoError(t, err)

	// verify executed command is stored
	executions, err := store.GetExecutions("job1", 50)
	require.NoError(t, err)
	require.Len(t, executions, 1)
	assert.Equal(t, "echo edited", executions[0].ExecutedCommand)
	assert.Equal(t, enums.JobStatusSuccess, executions[0].Status)
}

func TestSQLiteStore_Migration_ExecutedCommandColumn(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	ctx := context.Background()

	// create database with old schema (without executed_command column)
	db, err := sqlx.Open("sqlite", dbPath)
	require.NoError(t, err)

	// create old schema without executed_command column
	_, err = db.ExecContext(ctx, `CREATE TABLE executions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id TEXT,
		started_at DATETIME,
		finished_at DATETIME,
		status TEXT,
		exit_code INTEGER,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	)`)
	require.NoError(t, err)

	// insert a record with old schema
	_, err = db.ExecContext(ctx, `INSERT INTO executions (job_id, started_at, finished_at, status, exit_code)
		VALUES (?, ?, ?, ?, ?)`, "job1", time.Now(), time.Now(), "success", 0)
	require.NoError(t, err)

	// close old connection
	require.NoError(t, db.Close())

	// open with new store (should run migration)
	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// verify executed_command column was added
	var columnExists bool
	err = store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) > 0
		FROM pragma_table_info('executions')
		WHERE name = 'executed_command'
	`).Scan(&columnExists)
	require.NoError(t, err)
	assert.True(t, columnExists, "executed_command column should exist after migration")

	// verify we can insert with executed_command
	now := time.Now()
	err = store.RecordExecution(request.RecordExecution{
		JobID:           "job2",
		StartedAt:       now,
		FinishedAt:      now,
		Status:          enums.JobStatusSuccess,
		ExitCode:        0,
		ExecutedCommand: "echo migrated",
		Output:          "",
	})
	require.NoError(t, err)

	// verify we can read executions (old one should have empty executed_command)
	executions, err := store.GetExecutions("job1", 50)
	require.NoError(t, err)
	require.Len(t, executions, 1)
	assert.Empty(t, executions[0].ExecutedCommand, "old execution should have empty executed_command")
}

func TestSQLiteStore_UpdateExistingJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

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

	ctx := context.Background()

	// verify WAL mode is enabled
	var mode string
	err = store.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode)
	require.NoError(t, err)
	assert.Equal(t, "wal", mode)
}

func TestSQLiteStore_LoadJobs_Error(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)

	ctx := context.Background()

	// corrupt the database by dropping the jobs table
	_, err = store.db.ExecContext(ctx, "DROP TABLE jobs")
	require.NoError(t, err)

	// now LoadJobs should fail
	jobs, err := store.LoadJobs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query jobs")
	assert.Nil(t, jobs)

	err = store.Close()
	require.NoError(t, err)
}

func TestSQLiteStore_SaveJobs_Error(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// corrupt the database by dropping the jobs table
	_, err = store.db.ExecContext(ctx, "DROP TABLE jobs")
	require.NoError(t, err)

	jobs := []JobInfo{
		{
			ID:       "test",
			Command:  "echo test",
			Schedule: "* * * * *",
		},
	}

	err = store.SaveJobs(jobs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to save job")
}

func TestSQLiteStore_GetExecutions(t *testing.T) {
	t.Run("retrieves executions ordered by started_at desc", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		baseTime := time.Now()
		executions := []struct {
			started  time.Time
			finished time.Time
			status   enums.JobStatus
			exitCode int
		}{
			{baseTime.Add(-5 * time.Minute), baseTime.Add(-4 * time.Minute), enums.JobStatusSuccess, 0},
			{baseTime.Add(-4 * time.Minute), baseTime.Add(-3 * time.Minute), enums.JobStatusFailed, 1},
			{baseTime.Add(-3 * time.Minute), baseTime.Add(-2 * time.Minute), enums.JobStatusSuccess, 0},
			{baseTime.Add(-2 * time.Minute), baseTime.Add(-1 * time.Minute), enums.JobStatusSuccess, 0},
			{baseTime.Add(-1 * time.Minute), baseTime, enums.JobStatusFailed, 2},
		}

		for _, exec := range executions {
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job1",
				StartedAt:       exec.started,
				FinishedAt:      exec.finished,
				Status:          exec.status,
				ExitCode:        exec.exitCode,
				ExecutedCommand: "echo test",
				Output:          "",
			})
			require.NoError(t, err)
		}

		results, err := store.GetExecutions("job1", 10)
		require.NoError(t, err)
		assert.Len(t, results, 5)

		// verify ordering - most recent first
		for i := 0; i < len(results)-1; i++ {
			assert.True(t, results[i].StartedAt.After(results[i+1].StartedAt) || results[i].StartedAt.Equal(results[i+1].StartedAt))
		}

		// verify first execution is the most recent
		assert.WithinDuration(t, baseTime.Add(-1*time.Minute), results[0].StartedAt, time.Second)
		assert.Equal(t, enums.JobStatusFailed, results[0].Status)
		assert.Equal(t, 2, results[0].ExitCode)

		// verify last execution is the oldest
		assert.WithinDuration(t, baseTime.Add(-5*time.Minute), results[4].StartedAt, time.Second)
		assert.Equal(t, enums.JobStatusSuccess, results[4].Status)
		assert.Equal(t, 0, results[4].ExitCode)
	})

	t.Run("respects limit parameter", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		baseTime := time.Now()
		for i := range 10 {
			started := baseTime.Add(-time.Duration(10-i) * time.Minute)
			finished := started.Add(30 * time.Second)
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job1",
				StartedAt:       started,
				FinishedAt:      finished,
				Status:          enums.JobStatusSuccess,
				ExitCode:        0,
				ExecutedCommand: "echo test",
				Output:          "",
			})
			require.NoError(t, err)
		}

		results, err := store.GetExecutions("job1", 3)
		require.NoError(t, err)
		assert.Len(t, results, 3)
	})

	t.Run("returns empty slice for job with no executions", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		results, err := store.GetExecutions("nonexistent", 50)
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("error when table dropped", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		ctx := context.Background()

		_, err = store.db.ExecContext(ctx, "DROP TABLE executions")
		require.NoError(t, err)

		results, err := store.GetExecutions("job1", 50)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query executions")
		assert.Nil(t, results)
	})
}

func TestSQLiteStore_Migration_OutputColumn(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	ctx := context.Background()

	// create database with old schema (without output column)
	db, err := sqlx.Open("sqlite", dbPath)
	require.NoError(t, err)

	// create old schema without output column
	_, err = db.ExecContext(ctx, `CREATE TABLE executions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		job_id TEXT,
		started_at DATETIME,
		finished_at DATETIME,
		status TEXT,
		exit_code INTEGER,
		executed_command TEXT,
		FOREIGN KEY (job_id) REFERENCES jobs(id)
	)`)
	require.NoError(t, err)

	// insert a record with old schema
	_, err = db.ExecContext(ctx, `INSERT INTO executions (job_id, started_at, finished_at, status, exit_code, executed_command)
		VALUES (?, ?, ?, ?, ?, ?)`, "job1", time.Now(), time.Now(), "success", 0, "echo test")
	require.NoError(t, err)

	// close old connection
	require.NoError(t, db.Close())

	// open with new store (should run migration)
	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// verify output column was added
	var columnExists bool
	err = store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) > 0
		FROM pragma_table_info('executions')
		WHERE name = 'output'
	`).Scan(&columnExists)
	require.NoError(t, err)
	assert.True(t, columnExists, "output column should exist after migration")

	// verify we can insert with output
	now := time.Now()
	err = store.RecordExecution(request.RecordExecution{
		JobID:           "job2",
		StartedAt:       now,
		FinishedAt:      now,
		Status:          enums.JobStatusSuccess,
		ExitCode:        0,
		ExecutedCommand: "echo migrated",
		Output:          "test output",
	})
	require.NoError(t, err)

	// verify we can read executions (old one should have empty output)
	executions, err := store.GetExecutions("job1", 50)
	require.NoError(t, err)
	require.Len(t, executions, 1)
	assert.Empty(t, executions[0].Output, "old execution should have empty output")
}

func TestSQLiteStore_RecordExecutionWithOutput(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	// record execution with output
	started := time.Now().Add(-5 * time.Second)
	finished := time.Now()
	output := "line 1\nline 2\nline 3"
	err = store.RecordExecution(request.RecordExecution{
		JobID:           "job1",
		StartedAt:       started,
		FinishedAt:      finished,
		Status:          enums.JobStatusSuccess,
		ExitCode:        0,
		ExecutedCommand: "echo test",
		Output:          output,
	})
	require.NoError(t, err)

	// verify output is stored
	executions, err := store.GetExecutions("job1", 50)
	require.NoError(t, err)
	require.Len(t, executions, 1)
	assert.Equal(t, output, executions[0].Output)
	assert.Equal(t, "echo test", executions[0].ExecutedCommand)
	assert.Equal(t, enums.JobStatusSuccess, executions[0].Status)
}

func TestSQLiteStore_CleanupOldExecutions(t *testing.T) {
	t.Run("removes old executions beyond limit", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		// create 10 executions
		baseTime := time.Now()
		for i := range 10 {
			started := baseTime.Add(-time.Duration(10-i) * time.Minute)
			finished := started.Add(30 * time.Second)
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job1",
				StartedAt:       started,
				FinishedAt:      finished,
				Status:          enums.JobStatusSuccess,
				ExitCode:        0,
				ExecutedCommand: "echo test",
				Output:          "output",
			})
			require.NoError(t, err)
		}

		// verify 10 executions exist
		executions, err := store.GetExecutions("job1", 100)
		require.NoError(t, err)
		assert.Len(t, executions, 10)

		// cleanup keeping only last 5
		err = store.CleanupOldExecutions("job1", 5)
		require.NoError(t, err)

		// verify only 5 remain
		executions, err = store.GetExecutions("job1", 100)
		require.NoError(t, err)
		assert.Len(t, executions, 5)

		// verify the 5 most recent executions are kept
		for i := 0; i < len(executions)-1; i++ {
			assert.True(t, executions[i].StartedAt.After(executions[i+1].StartedAt) || executions[i].StartedAt.Equal(executions[i+1].StartedAt))
		}
	})

	t.Run("does nothing when executions below limit", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		// create 3 executions
		baseTime := time.Now()
		for i := range 3 {
			started := baseTime.Add(-time.Duration(3-i) * time.Minute)
			finished := started.Add(30 * time.Second)
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job1",
				StartedAt:       started,
				FinishedAt:      finished,
				Status:          enums.JobStatusSuccess,
				ExitCode:        0,
				ExecutedCommand: "echo test",
				Output:          "output",
			})
			require.NoError(t, err)
		}

		// cleanup with higher limit
		err = store.CleanupOldExecutions("job1", 10)
		require.NoError(t, err)

		// verify all 3 still exist
		executions, err := store.GetExecutions("job1", 100)
		require.NoError(t, err)
		assert.Len(t, executions, 3)
	})

	t.Run("handles cleanup for specific job only", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		// create executions for two different jobs
		baseTime := time.Now()
		for i := range 10 {
			started := baseTime.Add(-time.Duration(10-i) * time.Minute)
			finished := started.Add(30 * time.Second)
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job1",
				StartedAt:       started,
				FinishedAt:      finished,
				Status:          enums.JobStatusSuccess,
				ExitCode:        0,
				ExecutedCommand: "echo test1",
				Output:          "output1",
			})
			require.NoError(t, err)
			err = store.RecordExecution(request.RecordExecution{
				JobID:           "job2",
				StartedAt:       started,
				FinishedAt:      finished,
				Status:          enums.JobStatusSuccess,
				ExitCode:        0,
				ExecutedCommand: "echo test2",
				Output:          "output2",
			})
			require.NoError(t, err)
		}

		// cleanup only job1
		err = store.CleanupOldExecutions("job1", 5)
		require.NoError(t, err)

		// verify job1 has 5 executions
		executions, err := store.GetExecutions("job1", 100)
		require.NoError(t, err)
		assert.Len(t, executions, 5)

		// verify job2 still has 10 executions
		executions, err = store.GetExecutions("job2", 100)
		require.NoError(t, err)
		assert.Len(t, executions, 10)
	})
}

func TestSQLiteStore_GetExecutionByID(t *testing.T) {
	t.Run("retrieves execution by ID", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		// create an execution
		started := time.Now().Add(-5 * time.Second)
		finished := time.Now()
		output := "test output"

		err = store.RecordExecution(request.RecordExecution{
			JobID:           "job1",
			StartedAt:       started,
			FinishedAt:      finished,
			Status:          enums.JobStatusSuccess,
			ExitCode:        0,
			ExecutedCommand: "echo test",
			Output:          output,
		})
		require.NoError(t, err)

		// get executions to find the ID
		executions, err := store.GetExecutions("job1", 10)
		require.NoError(t, err)
		require.Len(t, executions, 1)
		execID := executions[0].ID

		// retrieve by ID
		execution, err := store.GetExecutionByID(execID)
		require.NoError(t, err)
		assert.Equal(t, execID, execution.ID)
		assert.Equal(t, "job1", execution.JobID)
		assert.Equal(t, enums.JobStatusSuccess, execution.Status)
		assert.Equal(t, 0, execution.ExitCode)
		assert.Equal(t, "echo test", execution.ExecutedCommand)
		assert.Equal(t, output, execution.Output)
	})

	t.Run("returns ErrNotFound for non-existent execution", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		store, err := NewSQLiteStore(dbPath)
		require.NoError(t, err)
		defer store.Close()

		_, err = store.GetExecutionByID(99999)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}
