package persistence

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // sqlite driver

	"github.com/umputun/cronn/app/web/enums"
)

// JobInfo represents a cron job with its execution state
type JobInfo struct {
	ID         string          `db:"id"`
	Command    string          `db:"command"`
	Schedule   string          `db:"schedule"`
	NextRun    time.Time       `db:"next_run"`
	LastRun    time.Time       `db:"last_run"`
	LastStatus enums.JobStatus `db:"last_status"`
	IsRunning  bool            `db:"-"` // not stored in DB
	Enabled    bool            `db:"enabled"`
	CreatedAt  time.Time       `db:"created_at"`
	UpdatedAt  time.Time       `db:"updated_at"`
	SortIndex  int             `db:"sort_index"`
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

	// helper to execute pragma with proper error handling
	execPragma := func(pragma, errMsgPrefix string) error {
		if _, err := db.Exec(pragma); err != nil {
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
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return store, nil
}

// initialize creates the database schema
func (s *SQLiteStore) initialize() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			command TEXT NOT NULL,
			schedule TEXT NOT NULL,
			next_run DATETIME,
			last_run DATETIME,
			last_status TEXT,
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
			FOREIGN KEY (job_id) REFERENCES jobs(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_job_id ON executions(job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_executions_started_at ON executions(started_at)`,
	}

	for _, query := range queries {
		if _, err := s.db.Exec(query); err != nil {
			return fmt.Errorf("failed to execute query: %w", err)
		}
	}

	// run schema migrations
	if err := s.migrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}

// migrate performs schema migrations for existing databases
func (s *SQLiteStore) migrate() error {
	// check if executed_command column exists
	var columnExists bool
	err := s.db.QueryRow(`
		SELECT COUNT(*) > 0
		FROM pragma_table_info('executions')
		WHERE name = 'executed_command'
	`).Scan(&columnExists)
	if err != nil {
		return fmt.Errorf("failed to check for executed_command column: %w", err)
	}

	// add executed_command column if it doesn't exist
	if !columnExists {
		if _, err := s.db.Exec("ALTER TABLE executions ADD COLUMN executed_command TEXT DEFAULT ''"); err != nil {
			return fmt.Errorf("failed to add executed_command column: %w", err)
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
		SELECT id, command, schedule, next_run, last_run, last_status, enabled, 
		       created_at, updated_at, sort_index
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
			(id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at, sort_index)
			VALUES (:id, :command, :schedule, :next_run, :last_run, :last_status, :enabled, :created_at, :updated_at, :sort_index)`,
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
func (s *SQLiteStore) RecordExecution(jobID string, started, finished time.Time, status enums.JobStatus, exitCode int, executedCommand string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (job_id, started_at, finished_at, status, exit_code, executed_command)
		VALUES (?, ?, ?, ?, ?, ?)`,
		jobID, started, finished, status.String(), exitCode, executedCommand)

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
		SELECT id, job_id, started_at, finished_at, status, exit_code, executed_command
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

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.db.Close()
}
