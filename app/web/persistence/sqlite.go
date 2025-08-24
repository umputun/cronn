package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	log "github.com/go-pkgz/lgr"
	_ "modernc.org/sqlite" // sqlite driver

	"github.com/umputun/cronn/app/web/enums"
)

// JobInfo represents a cron job with its execution state
type JobInfo struct {
	ID         string // SHA256 of command
	Command    string
	Schedule   string
	NextRun    time.Time
	LastRun    time.Time
	LastStatus enums.JobStatus
	IsRunning  bool
	Enabled    bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	SortIndex  int // original order in crontab
}

// SQLiteStore implements persistence using SQLite
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLite store
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
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
			next_run INTEGER,
			last_run INTEGER,
			last_status TEXT,
			enabled BOOLEAN DEFAULT 1,
			created_at INTEGER,
			updated_at INTEGER,
			sort_index INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS executions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id TEXT,
			started_at INTEGER,
			finished_at INTEGER,
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
	rows, err := s.db.Query(`
		SELECT id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at 
		FROM jobs`)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer rows.Close()

	jobs := []JobInfo{}
	for rows.Next() {
		var job JobInfo
		var nextRun, lastRun, createdAt, updatedAt sql.NullInt64
		var lastStatus sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.Command,
			&job.Schedule,
			&nextRun,
			&lastRun,
			&lastStatus,
			&job.Enabled,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			log.Printf("[WARN] failed to scan job row: %v", err)
			continue
		}

		// convert timestamps
		if nextRun.Valid && nextRun.Int64 > 0 {
			job.NextRun = time.Unix(nextRun.Int64, 0)
		}
		if lastRun.Valid && lastRun.Int64 > 0 {
			job.LastRun = time.Unix(lastRun.Int64, 0)
		}
		if createdAt.Valid {
			job.CreatedAt = time.Unix(createdAt.Int64, 0)
		}
		if updatedAt.Valid {
			job.UpdatedAt = time.Unix(updatedAt.Int64, 0)
		}

		// parse status
		if lastStatus.Valid && lastStatus.String != "" {
			if status, err := enums.ParseJobStatus(lastStatus.String); err == nil {
				job.LastStatus = status
			} else {
				log.Printf("[WARN] invalid job status %q for job %s: %v", lastStatus.String, job.ID, err)
				job.LastStatus = enums.JobStatusIdle
			}
		} else {
			job.LastStatus = enums.JobStatusIdle
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return jobs, nil
}

// SaveJobs persists multiple jobs in a transaction
func (s *SQLiteStore) SaveJobs(jobs []JobInfo) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	for idx, job := range jobs {
		var nextRun, lastRun int64
		if !job.NextRun.IsZero() {
			nextRun = job.NextRun.Unix()
		}
		if !job.LastRun.IsZero() {
			lastRun = job.LastRun.Unix()
		}

		_, err := tx.Exec(`
			INSERT OR REPLACE INTO jobs 
			(id, command, schedule, next_run, last_run, last_status, enabled, created_at, updated_at, sort_index)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			job.ID,
			job.Command,
			job.Schedule,
			nextRun,
			lastRun,
			job.LastStatus.String(),
			job.Enabled,
			job.CreatedAt.Unix(),
			job.UpdatedAt.Unix(),
			idx,
		)
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
		jobID, started.Unix(), finished.Unix(), status.String(), exitCode)

	if err != nil {
		return fmt.Errorf("failed to record execution: %w", err)
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
