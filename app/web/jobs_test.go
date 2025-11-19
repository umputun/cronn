package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/cronn/app/crontab"
	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
	"github.com/umputun/cronn/app/web/persistence"
)

func TestServer_sortJobs(t *testing.T) {
	server := &Server{}

	// create test jobs with different attributes
	now := time.Now()
	jobs := []persistence.JobInfo{
		{ID: "1", Command: "cmd1", SortIndex: 2, LastRun: now.Add(-2 * time.Hour), NextRun: now.Add(2 * time.Hour)},
		{ID: "2", Command: "cmd2", SortIndex: 0, LastRun: now.Add(-1 * time.Hour), NextRun: now.Add(1 * time.Hour)},
		{ID: "3", Command: "cmd3", SortIndex: 1, LastRun: now.Add(-3 * time.Hour), NextRun: now.Add(30 * time.Minute)},
		{ID: "4", Command: "cmd4", SortIndex: 3, LastRun: time.Time{}, NextRun: time.Time{}}, // never run
	}

	t.Run("default sort", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeDefault)

		// should be sorted by SortIndex
		assert.Equal(t, "2", sorted[0].ID)
		assert.Equal(t, "3", sorted[1].ID)
		assert.Equal(t, "1", sorted[2].ID)
		assert.Equal(t, "4", sorted[3].ID)
	})

	t.Run("sort by last run", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeLastrun)

		// should be sorted by LastRun, most recent first
		assert.Equal(t, "2", sorted[0].ID, "First should be ID 2 (1 hour ago - most recent)")
		assert.Equal(t, "1", sorted[1].ID, "Second should be ID 1 (2 hours ago)")
		assert.Equal(t, "3", sorted[2].ID, "Third should be ID 3 (3 hours ago)")
		assert.Equal(t, "4", sorted[3].ID, "Fourth should be ID 4 (never run)")

		// verify actual time ordering
		assert.True(t, sorted[0].LastRun.After(sorted[1].LastRun), "First job should have later LastRun than second")
		assert.True(t, sorted[1].LastRun.After(sorted[2].LastRun), "Second job should have later LastRun than third")
		assert.True(t, sorted[3].LastRun.IsZero(), "Last job should have zero LastRun")
	})

	t.Run("sort by next run", func(t *testing.T) {
		sorted := make([]persistence.JobInfo, len(jobs))
		copy(sorted, jobs)
		server.sortJobs(sorted, enums.SortModeNextrun)

		// should be sorted by NextRun, soonest first
		assert.Equal(t, "3", sorted[0].ID) // 30 minutes
		assert.Equal(t, "2", sorted[1].ID) // 1 hour
		assert.Equal(t, "1", sorted[2].ID) // 2 hours
		assert.Equal(t, "4", sorted[3].ID) // never (zero time)
	})

	t.Run("stable sort with equal times", func(t *testing.T) {
		// create jobs with some having equal next run times
		sameTime := now.Add(1 * time.Hour)
		sameTime2 := now.Add(2 * time.Hour)
		jobsEqual := []persistence.JobInfo{
			{ID: "A", Command: "cmdA", SortIndex: 0, NextRun: sameTime, LastRun: sameTime2},
			{ID: "B", Command: "cmdB", SortIndex: 1, NextRun: sameTime, LastRun: sameTime2},
			{ID: "C", Command: "cmdC", SortIndex: 2, NextRun: sameTime, LastRun: sameTime2},
			{ID: "D", Command: "cmdD", SortIndex: 3, NextRun: now.Add(30 * time.Minute), LastRun: now},
			{ID: "E", Command: "cmdE", SortIndex: 4, NextRun: sameTime, LastRun: sameTime2},
		}

		// test next run sorting stability
		sortedNext := make([]persistence.JobInfo, len(jobsEqual))
		copy(sortedNext, jobsEqual)
		server.sortJobs(sortedNext, enums.SortModeNextrun)

		// d should be first (30 minutes), then A,B,C,E in original order (all 1 hour)
		assert.Equal(t, "D", sortedNext[0].ID, "D should be first (soonest)")
		assert.Equal(t, "A", sortedNext[1].ID, "A should maintain position relative to B,C,E")
		assert.Equal(t, "B", sortedNext[2].ID, "B should maintain position relative to C,E")
		assert.Equal(t, "C", sortedNext[3].ID, "C should maintain position relative to E")
		assert.Equal(t, "E", sortedNext[4].ID, "E should be last among equal times")

		// test last run sorting stability
		sortedLast := make([]persistence.JobInfo, len(jobsEqual))
		copy(sortedLast, jobsEqual)
		server.sortJobs(sortedLast, enums.SortModeLastrun)

		// a,B,C,E have same LastRun (2 hours from now), D is different (now)
		// since LastRun sorts most recent first, A,B,C,E should come first in original order
		assert.Equal(t, "A", sortedLast[0].ID, "A should maintain position relative to B,C,E")
		assert.Equal(t, "B", sortedLast[1].ID, "B should maintain position relative to C,E")
		assert.Equal(t, "C", sortedLast[2].ID, "C should maintain position relative to E")
		assert.Equal(t, "E", sortedLast[3].ID, "E should be last among equal times")
		assert.Equal(t, "D", sortedLast[4].ID, "D should be last (oldest)")
	})
}

func TestServer_OnJobStart(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	server.OnJobStart(request.OnJobStart{
		Command:         "echo test",
		ExecutedCommand: "echo test",
		Schedule:        "* * * * *",
		StartTime:       startTime,
	})

	// wait for event to be processed
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		_, exists := server.jobs[HashCommand("echo test")]
		return exists
	}, time.Second, 10*time.Millisecond)

	// verify job in memory (not in database since persistJobs runs periodically)
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "echo test", job.Command)
	assert.Equal(t, "* * * * *", job.Schedule)
	assert.True(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusRunning.String(), job.LastStatus.String())
	assert.Equal(t, startTime.Unix(), job.LastRun.Unix())
}

func TestServer_OnJobComplete(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	startTime := time.Now()
	endTime := startTime.Add(time.Second)

	// first start the job
	server.OnJobStart(request.OnJobStart{Command: "echo test", ExecutedCommand: "echo test", Schedule: "* * * * *", StartTime: startTime})
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastStatus == enums.JobStatusRunning
	}, time.Second, 10*time.Millisecond)

	// then complete it successfully
	server.OnJobComplete(request.OnJobComplete{Command: "echo test", ExecutedCommand: "echo test", Schedule: "* * * * *", StartTime: startTime, EndTime: endTime, ExitCode: 0, Output: "", Err: nil})
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastStatus == enums.JobStatusSuccess
	}, time.Second, 10*time.Millisecond)

	// verify job status was updated in memory
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.False(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)

	// test with error
	server.OnJobStart(request.OnJobStart{Command: "echo error", ExecutedCommand: "echo error", Schedule: "* * * * *", StartTime: startTime})
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo error")]
		return exists && job.LastStatus == enums.JobStatusRunning
	}, time.Second, 10*time.Millisecond)

	server.OnJobComplete(request.OnJobComplete{Command: "echo error", ExecutedCommand: "echo error", Schedule: "* * * * *", StartTime: startTime, EndTime: endTime, ExitCode: 1, Output: "", Err: fmt.Errorf("test error")})
	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo error")]
		return exists && job.LastStatus == enums.JobStatusFailed
	}, time.Second, 10*time.Millisecond)

	server.jobsMu.RLock()
	job2, exists2 := server.jobs[HashCommand("echo error")]
	server.jobsMu.RUnlock()

	assert.True(t, exists2)
	assert.False(t, job2.IsRunning)
	assert.Equal(t, enums.JobStatusFailed, job2.LastStatus)
}

func TestServer_OnJobComplete_OutputStorage(t *testing.T) {
	t.Run("stores output when logExecMaxLines > 0", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		cfg := Config{
			DBPath:          dbPath,
			UpdateInterval:  time.Minute,
			Version:         "test",
			JobsProvider:    createTestProvider(t, tmpDir),
			ExecMaxLogLines: 100,
			LogExecMaxHist:  50,
		}

		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go server.processEvents(ctx)

		startTime := time.Now()
		endTime := startTime.Add(time.Second)
		output := "line 1\nline 2\nline 3"

		server.OnJobStart(request.OnJobStart{
			Command:         "echo test",
			ExecutedCommand: "echo test",
			Schedule:        "* * * * *",
			StartTime:       startTime,
		})

		server.OnJobComplete(request.OnJobComplete{
			Command:         "echo test",
			ExecutedCommand: "echo test",
			Schedule:        "* * * * *",
			StartTime:       startTime,
			EndTime:         endTime,
			ExitCode:        0,
			Output:          output,
			Err:             nil,
		})

		// wait for event processing
		require.Eventually(t, func() bool {
			execs, errGet := server.store.GetExecutions(HashCommand("echo test"), 10)
			return errGet == nil && len(execs) == 1
		}, time.Second, 10*time.Millisecond)

		// verify output was stored
		executions, err := server.store.GetExecutions(HashCommand("echo test"), 10)
		require.NoError(t, err)
		require.Len(t, executions, 1)
		assert.Equal(t, output, executions[0].Output)
	})

	t.Run("skips output storage when logExecMaxLines == 0", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		cfg := Config{
			DBPath:          dbPath,
			UpdateInterval:  time.Minute,
			Version:         "test",
			JobsProvider:    createTestProvider(t, tmpDir),
			ExecMaxLogLines: 0, // disabled
			LogExecMaxHist:  50,
		}

		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go server.processEvents(ctx)

		startTime := time.Now()
		endTime := startTime.Add(time.Second)
		output := "this should not be stored"

		server.OnJobStart(request.OnJobStart{
			Command:         "echo test",
			ExecutedCommand: "echo test",
			Schedule:        "* * * * *",
			StartTime:       startTime,
		})

		server.OnJobComplete(request.OnJobComplete{
			Command:         "echo test",
			ExecutedCommand: "echo test",
			Schedule:        "* * * * *",
			StartTime:       startTime,
			EndTime:         endTime,
			ExitCode:        0,
			Output:          output,
			Err:             nil,
		})

		// wait for event processing
		require.Eventually(t, func() bool {
			execs, errGet := server.store.GetExecutions(HashCommand("echo test"), 10)
			return errGet == nil && len(execs) == 1
		}, time.Second, 10*time.Millisecond)

		// verify output was NOT stored
		executions, err := server.store.GetExecutions(HashCommand("echo test"), 10)
		require.NoError(t, err)
		require.Len(t, executions, 1)
		assert.Empty(t, executions[0].Output, "output should be empty when logExecMaxLines is 0")
	})

	t.Run("cleanup old executions when logExecMaxHist > 0", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		cfg := Config{
			DBPath:          dbPath,
			UpdateInterval:  time.Minute,
			Version:         "test",
			JobsProvider:    createTestProvider(t, tmpDir),
			ExecMaxLogLines: 100,
			LogExecMaxHist:  3, // keep only 3 executions
		}

		server, err := New(cfg)
		require.NoError(t, err)
		defer server.store.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go server.processEvents(ctx)

		// create 5 executions
		baseTime := time.Now()
		for i := 0; i < 5; i++ {
			startTime := baseTime.Add(-time.Duration(5-i) * time.Minute)
			endTime := startTime.Add(time.Second)

			server.OnJobStart(request.OnJobStart{
				Command:         "echo test",
				ExecutedCommand: "echo test",
				Schedule:        "* * * * *",
				StartTime:       startTime,
			})

			server.OnJobComplete(request.OnJobComplete{
				Command:         "echo test",
				ExecutedCommand: "echo test",
				Schedule:        "* * * * *",
				StartTime:       startTime,
				EndTime:         endTime,
				ExitCode:        0,
				Output:          fmt.Sprintf("output %d", i),
				Err:             nil,
			})
		}

		// wait for all events to be processed - check that newest execution exists and count is 3
		require.Eventually(t, func() bool {
			execs, errGet := server.store.GetExecutions(HashCommand("echo test"), 100)
			if errGet != nil || len(execs) != 3 {
				return false
			}
			// verify newest execution (output 4) is present
			return execs[0].Output == "output 4"
		}, 2*time.Second, 10*time.Millisecond)

		// verify only 3 most recent executions remain
		executions, err := server.store.GetExecutions(HashCommand("echo test"), 100)
		require.NoError(t, err)
		assert.Len(t, executions, 3, "should keep only 3 most recent executions")

		// verify they are the most recent ones (outputs 2, 3, 4)
		assert.Contains(t, executions[0].Output, "output 4")
		assert.Contains(t, executions[1].Output, "output 3")
		assert.Contains(t, executions[2].Output, "output 2")
	})
}

func TestServer_OnJobStartEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   createTestProvider(t, tmpDir),
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// start event processor
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.processEvents(ctx)

	// test with invalid schedule that will fail to parse
	startTime := time.Now()
	server.OnJobStart(request.OnJobStart{Command: "echo test", ExecutedCommand: "echo test", Schedule: "invalid schedule", StartTime: startTime})

	// wait a bit for processing
	time.Sleep(50 * time.Millisecond)

	// job should still be created but NextRun calculation might fail
	server.jobsMu.RLock()
	job, exists := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	assert.True(t, exists)
	assert.Equal(t, "echo test", job.Command)
	assert.Equal(t, "invalid schedule", job.Schedule)
	assert.True(t, job.IsRunning)
	assert.Equal(t, enums.JobStatusRunning, job.LastStatus)

	// test updating an existing job - schedule should NOT change from events
	server.OnJobStart(request.OnJobStart{Command: "echo test", ExecutedCommand: "echo test", Schedule: "* * * * *", StartTime: startTime.Add(time.Hour)})

	require.Eventually(t, func() bool {
		server.jobsMu.RLock()
		defer server.jobsMu.RUnlock()
		job, exists := server.jobs[HashCommand("echo test")]
		return exists && job.LastRun.Equal(startTime.Add(time.Hour))
	}, time.Second, 10*time.Millisecond)

	server.jobsMu.RLock()
	updatedJob := server.jobs[HashCommand("echo test")]
	server.jobsMu.RUnlock()

	// schedule should remain unchanged - job events don't update schedule
	assert.Equal(t, "invalid schedule", updatedJob.Schedule)
	assert.True(t, updatedJob.IsRunning)
}

func TestServer_syncWithCrontab(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	crontabFile := filepath.Join(tmpDir, "crontab")

	// create test crontab file
	crontabContent := `# test crontab
0 * * * * echo hourly
*/5 * * * * echo five-minutes
@daily echo daily`
	err := os.WriteFile(crontabFile, []byte(crontabContent), 0o600)
	require.NoError(t, err)

	// create crontab parser as jobs provider
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	// load jobs from crontab
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)

	// persist the jobs to database
	server.persistJobs()

	// verify jobs were synced by loading from store
	jobs, err := server.store.LoadJobs()
	require.NoError(t, err)
	assert.Len(t, jobs, 3)

	// create map for easier verification
	jobMap := make(map[string]string)
	for _, job := range jobs {
		jobMap[job.Command] = job.Schedule
	}

	assert.Equal(t, "@daily", jobMap["echo daily"])
	assert.Equal(t, "*/5 * * * *", jobMap["echo five-minutes"])
	assert.Equal(t, "0 * * * *", jobMap["echo hourly"])
}

func TestServer_loadJobsFromCrontab_NilProvider(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create server with some existing jobs
	crontabFile := filepath.Join(tmpDir, "crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte("0 * * * * echo initial"), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// load initial job
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)
	assert.Len(t, server.jobs, 1)

	// set provider to nil and attempt to load again
	server.jobsProvider = nil
	err = server.loadJobsFromCrontab()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot load jobs")

	// verify that jobs map remains unchanged after error
	assert.Len(t, server.jobs, 1)
	assert.Contains(t, server.jobs, HashCommand("echo initial"))
}

func TestServer_loadJobsFromCrontab_ProviderError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// first create a working crontab to load initial jobs
	crontabFile := filepath.Join(tmpDir, "crontab")
	require.NoError(t, os.WriteFile(crontabFile, []byte("0 * * * * echo existing"), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// load initial job successfully
	err = server.loadJobsFromCrontab()
	require.NoError(t, err)
	assert.Len(t, server.jobs, 1)
	initialJobID := HashCommand("echo existing")
	assert.Contains(t, server.jobs, initialJobID)

	// now remove the file to trigger error
	require.NoError(t, os.Remove(crontabFile))

	// test that loadJobsFromCrontab handles provider errors
	err = server.loadJobsFromCrontab()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load job specifications")

	// verify that jobs map remains unchanged after error
	assert.Len(t, server.jobs, 1)
	assert.Contains(t, server.jobs, initialJobID)

	// verify the existing job details remain intact
	job := server.jobs[initialJobID]
	assert.Equal(t, "echo existing", job.Command)
	assert.Equal(t, "0 * * * *", job.Schedule)
}

func TestServer_filterJobs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create crontab parser as jobs provider
	parser := crontab.New("test-crontab", 0, nil)

	server, err := New(Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		JobsProvider:   parser,
	})
	require.NoError(t, err)
	defer server.store.Close()

	// create test jobs
	jobs := []persistence.JobInfo{
		{ID: "1", Command: "cmd1", IsRunning: true, LastStatus: enums.JobStatusRunning},
		{ID: "2", Command: "cmd2", IsRunning: false, LastStatus: enums.JobStatusSuccess},
		{ID: "3", Command: "cmd3", IsRunning: false, LastStatus: enums.JobStatusFailed},
		{ID: "4", Command: "cmd4", IsRunning: false, LastStatus: enums.JobStatusSuccess},
		{ID: "5", Command: "cmd5", IsRunning: true, LastStatus: enums.JobStatusRunning},
	}

	t.Run("filter all returns everything", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeAll)
		assert.Len(t, filtered, 5)
	})

	t.Run("filter running", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeRunning)
		assert.Len(t, filtered, 2)
		for _, job := range filtered {
			assert.True(t, job.IsRunning)
		}
	})

	t.Run("filter success", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeSuccess)
		assert.Len(t, filtered, 2)
		for _, job := range filtered {
			assert.Equal(t, enums.JobStatusSuccess, job.LastStatus)
		}
	})

	t.Run("filter failed", func(t *testing.T) {
		filtered := server.filterJobs(jobs, enums.FilterModeFailed)
		assert.Len(t, filtered, 1)
		assert.Equal(t, enums.JobStatusFailed, filtered[0].LastStatus)
	})

	t.Run("empty input", func(t *testing.T) {
		filtered := server.filterJobs([]persistence.JobInfo{}, enums.FilterModeRunning)
		assert.Empty(t, filtered)
	})

	t.Run("nil input", func(t *testing.T) {
		filtered := server.filterJobs(nil, enums.FilterModeRunning)
		assert.Empty(t, filtered)
	})
}

func TestServer_sortJobs_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// create a dummy provider for testing
	crontabFile := filepath.Join(tmpDir, "dummy")
	require.NoError(t, os.WriteFile(crontabFile, []byte(""), 0o600))
	parser := crontab.New(crontabFile, 0, nil)

	cfg := Config{
		DBPath:         dbPath,
		UpdateInterval: time.Minute,
		Version:        "test",
		JobsProvider:   parser,
	}

	server, err := New(cfg)
	require.NoError(t, err)
	defer server.store.Close()

	now := time.Now()

	t.Run("sort by next run with zero times", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1", NextRun: time.Time{}},
			{ID: "2", Command: "cmd2", NextRun: now.Add(time.Hour)},
			{ID: "3", Command: "cmd3", NextRun: time.Time{}},
			{ID: "4", Command: "cmd4", NextRun: now.Add(-time.Hour)},
		}

		server.sortJobs(jobs, enums.SortModeNextrun)
		sorted := jobs

		// zero times should be sorted last
		assert.Equal(t, "4", sorted[0].ID) // past time first
		assert.Equal(t, "2", sorted[1].ID) // future time second
		assert.Equal(t, "1", sorted[2].ID) // zero times last
		assert.Equal(t, "3", sorted[3].ID) // zero times last
	})

	t.Run("sort by last run with zero times", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1", LastRun: now},
			{ID: "2", Command: "cmd2", LastRun: time.Time{}},
			{ID: "3", Command: "cmd3", LastRun: now.Add(-time.Hour)},
			{ID: "4", Command: "cmd4", LastRun: time.Time{}},
		}

		server.sortJobs(jobs, enums.SortModeLastrun)
		sorted := jobs

		// most recent first, zero times last
		assert.Equal(t, "1", sorted[0].ID) // most recent
		assert.Equal(t, "3", sorted[1].ID) // older
		assert.Equal(t, "2", sorted[2].ID) // zero time
		assert.Equal(t, "4", sorted[3].ID) // zero time
	})

	t.Run("empty job list", func(t *testing.T) {
		jobs := []persistence.JobInfo{}
		server.sortJobs(jobs, enums.SortModeDefault)
		assert.Empty(t, jobs)
	})

	t.Run("single job", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "cmd1"},
		}
		server.sortJobs(jobs, enums.SortModeDefault)
		assert.Len(t, jobs, 1)
		assert.Equal(t, "1", jobs[0].ID)
	})

	t.Run("sort by status with all same status", func(t *testing.T) {
		jobs := []persistence.JobInfo{
			{ID: "1", Command: "zzz", LastStatus: enums.JobStatusSuccess},
			{ID: "2", Command: "aaa", LastStatus: enums.JobStatusSuccess},
			{ID: "3", Command: "mmm", LastStatus: enums.JobStatusSuccess},
		}

		server.sortJobs(jobs, enums.SortModeDefault)
		sorted := jobs

		// when all statuses are same, should maintain original order
		assert.Equal(t, "1", sorted[0].ID)
		assert.Equal(t, "2", sorted[1].ID)
		assert.Equal(t, "3", sorted[2].ID)
	})
}
