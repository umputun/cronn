package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // sqlite driver

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

// ErrNotFound is returned when a requested resource does not exist
var ErrNotFound = errors.New("not found")

// JobInfo represents a cron job with its execution state
type JobInfo struct {
	ID           string          `db:"id"`
	Name         string          `db:"name"`
	Command      string          `db:"command"`
	Schedule     string          `db:"schedule"`
	NextRun      time.Time       `db:"next_run"`
	LastRun      time.Time       `db:"last_run"`
	LastStatus   enums.JobStatus `db:"last_status"`
	LastExitCode *int            `db:"last_exit_code"` // nil when no finished run is known; -1 is a real signal exit
	LastDuration time.Duration   `db:"last_duration"`
	IsRunning    bool            `db:"-"` // not stored in DB
	Enabled      bool            `db:"enabled"`
	CreatedAt    time.Time       `db:"created_at"`
	UpdatedAt    time.Time       `db:"updated_at"`
	SortIndex    int             `db:"sort_index"`
}

// ExecutionInfo represents a single job execution record
type ExecutionInfo struct {
	ID              int             `db:"id"`
	JobID           string          `db:"job_id"`
	StartedAt       time.Time       `db:"started_at"`
	FinishedAt      time.Time       `db:"finished_at"`
	Status          enums.JobStatus `db:"status"`
	ExitCode        int             `db:"exit_code"`
	ExecutedCommand string          `db:"executed_command"`
	Output          string          `db:"output"`
}

// SQLiteStore implements persistence using SQLite
type SQLiteStore struct {
	db *sqlx.DB
	mu sync.RWMutex // protects concurrent database access
}

// NewSQLiteStore creates a new SQLite store and initializes the database
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	ctx := context.Background()

	// helper to execute pragma with proper error handling
	execPragma := func(pragma, errMsgPrefix string) error {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			if closeErr := db.Close(); closeErr != nil {
				return fmt.Errorf("%s: %w (also failed to close db: %v)", errMsgPrefix, err, closeErr)
			}
			return fmt.Errorf("%s: %w", errMsgPrefix, err)
		}
		return nil
	}

	// enable WAL mode for better concurrency
	if err := execPragma("PRAGMA journal_mode=WAL", "failed to set WAL mode"); err != nil {
		return nil, err
	}

	// set busy timeout to wait when database is locked
	if err := execPragma("PRAGMA busy_timeout=5000", "failed to set busy timeout"); err != nil {
		return nil, err
	}

	store := &SQLiteStore{db: db}

	// initialize database tables
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return store, nil
}

// initialize creates the database schema
func (s *SQLiteStore) initialize(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			name TEXT DEFAULT '',
			command TEXT NOT NULL,
			schedule TEXT NOT NULL,
			next_run DATETIME,
			last_run DATETIME,
			last_status TEXT,
			last_exit_code INTEGER,
			last_duration INTEGER DEFAULT 0,
			enabled BOOLEAN DEFAULT 1,
			created_at DATETIME,
			updated_at DATETIME,
			sort_index INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			status TEXT,
			exit_code INTEGER,
			executed_command TEXT,
			output TEXT,
			FOREIGN KEY (job_id) REFERENCES jobs(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_job_started ON executions(job_id, started_at DESC)`,
	}

	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	// run schema migrations
	if err := s.migrate(ctx); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// migrate performs schema migrations for existing databases
func (s *SQLiteStore) migrate(ctx context.Context) error {
	columns := []struct{ table, column, ddl string }{
		{"executions", "executed_command", "executed_command TEXT DEFAULT ''"},
		{"executions", "output", "output TEXT DEFAULT ''"},
		{"jobs", "name", "name TEXT DEFAULT ''"},
		{"jobs", "last_exit_code", "last_exit_code INTEGER"},
		{"jobs", "last_duration", "last_duration INTEGER DEFAULT 0"},
	}

	backfill := false
	for _, c := range columns {
		var exists bool
		err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) > 0 FROM pragma_table_info(?) WHERE name = ?", c.table, c.column).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check for %s.%s column: %w", c.table, c.column, err)
		}
		if exists {
			continue
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", c.table, c.ddl)); err != nil {
			return fmt.Errorf("failed to add %s.%s column: %w", c.table, c.column, err)
		}
		if c.column == "last_exit_code" {
			backfill = true
		}
	}

	if backfill {
		return s.backfillLastResult(ctx)
	}
	return nil
}

// backfillLastResult fills last_exit_code and last_duration of existing jobs from their latest retained
// execution, so an upgraded database keeps showing the result it already recorded
func (s *SQLiteStore) backfillLastResult(ctx context.Context) error {
	type lastRun struct {
		JobID      string       `db:"job_id"`
		ExitCode   int          `db:"exit_code"`
		StartedAt  sql.NullTime `db:"started_at"`
		FinishedAt sql.NullTime `db:"finished_at"`
	}
	var runs []lastRun
	err := s.db.SelectContext(ctx, &runs, `
		SELECT e.job_id, e.exit_code, e.started_at, e.finished_at
		FROM executions e
		WHERE e.id = (SELECT id FROM executions WHERE job_id = e.job_id ORDER BY started_at DESC, id DESC LIMIT 1)`)
	if err != nil {
		return fmt.Errorf("failed to read latest executions: %w", err)
	}

	for _, r := range runs {
		var duration time.Duration
		if r.StartedAt.Valid && r.FinishedAt.Valid {
			duration = r.FinishedAt.Time.Sub(r.StartedAt.Time)
		}
		if _, err := s.db.ExecContext(ctx, "UPDATE jobs SET last_exit_code = ?, last_duration = ? WHERE id = ?",
			r.ExitCode, int64(duration), r.JobID); err != nil {
			return fmt.Errorf("failed to backfill last result of job %s: %w", r.JobID, err)
		}
	}
	return nil
}

// LoadJobs retrieves all jobs from the database
func (s *SQLiteStore) LoadJobs() ([]JobInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var jobs []JobInfo
	err := s.db.Select(&jobs, `
		SELECT id, name, command, schedule, next_run, last_run, last_status, last_exit_code, last_duration,
		       enabled, created_at, updated_at, sort_index
		FROM jobs
		ORDER BY sort_index`)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}

	// ensure we return empty slice, not nil
	if jobs == nil {
		jobs = []JobInfo{}
	}

	return jobs, nil
}

// SaveJobs persists multiple jobs in a transaction
func (s *SQLiteStore) SaveJobs(jobs []JobInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for idx, job := range jobs {
		// set sort_index based on position
		job.SortIndex = idx

		_, err := tx.NamedExec(`
			INSERT OR REPLACE INTO jobs 
			(id, name, command, schedule, next_run, last_run, last_status, last_exit_code, last_duration,
			 enabled, created_at, updated_at, sort_index)
			VALUES (:id, :name, :command, :schedule, :next_run, :last_run, :last_status, :last_exit_code, :last_duration,
			 :enabled, :created_at, :updated_at, :sort_index)`,
			job)
		if err != nil {
			return fmt.Errorf("failed to save job %s: %w", job.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// RecordExecution logs a job execution event
func (s *SQLiteStore) RecordExecution(req request.RecordExecution) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (job_id, started_at, finished_at, status, exit_code, executed_command, output)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.JobID, req.StartedAt, req.FinishedAt, req.Status.String(), req.ExitCode, req.ExecutedCommand, req.Output)

	if err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

// GetExecutions retrieves execution history for a job, limited to the most recent executions
func (s *SQLiteStore) GetExecutions(jobID string, limit int) ([]ExecutionInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var executions []ExecutionInfo
	err := s.db.Select(&executions, `
		SELECT id, job_id, started_at, finished_at, status, exit_code, executed_command, output
		FROM executions
		WHERE job_id = ?
		ORDER BY started_at DESC
		LIMIT ?`,
		jobID, limit)

	if err != nil {
		return nil, fmt.Errorf("failed to query executions: %w", err)
	}

	// ensure we return empty slice, not nil
	if executions == nil {
		executions = []ExecutionInfo{}
	}

	return executions, nil
}

// GetExecutionByID retrieves a specific execution by its ID.
// Returns ErrNotFound if the execution does not exist.
func (s *SQLiteStore) GetExecutionByID(execID int) (ExecutionInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var execution ExecutionInfo
	err := s.db.Get(&execution, `
		SELECT id, job_id, started_at, finished_at, status, exit_code, executed_command, output
		FROM executions
		WHERE id = ?`,
		execID)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ExecutionInfo{}, ErrNotFound
		}
		return ExecutionInfo{}, fmt.Errorf("failed to query execution: %w", err)
	}

	return execution, nil
}

// CleanupOldExecutions removes old executions beyond the limit for a job
func (s *SQLiteStore) CleanupOldExecutions(jobID string, limit int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// delete executions beyond the limit, keeping the most recent ones
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM executions
		WHERE job_id = ?
		AND id NOT IN (
			SELECT id FROM executions
			WHERE job_id = ?
			ORDER BY started_at DESC
			LIMIT ?
		)`,
		jobID, jobID, limit)

	if err != nil {
		return fmt.Errorf("failed to cleanup old executions: %w", err)
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.db.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}
	return nil
}
