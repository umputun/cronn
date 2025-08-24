package persistence

import (
	"context"
	"fmt"
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

// SQLiteStore implements persistence using SQLite
type SQLiteStore struct {
	db *sqlx.DB
}

// NewSQLiteStore creates a new SQLite store
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// enable WAL mode for better concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to set WAL mode: %w (also failed to close db: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("failed to set WAL mode: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Initialize creates the database schema
func (s *SQLiteStore) Initialize() error {
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

	return nil
}

// LoadJobs retrieves all jobs from the database
func (s *SQLiteStore) LoadJobs() ([]JobInfo, error) {
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
func (s *SQLiteStore) RecordExecution(jobID string, started, finished time.Time, status enums.JobStatus, exitCode int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO executions (job_id, started_at, finished_at, status, exit_code)
		VALUES (?, ?, ?, ?, ?)`,
		jobID, started, finished, status.String(), exitCode)

	if err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
