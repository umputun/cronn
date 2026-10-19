package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/umputun/cronn/app/service/request"
	"github.com/umputun/cronn/app/web/enums"
)

// StepInfo is the recorded state of one step of an execution of a job with steps
type StepInfo struct {
	ExecutionID int              `db:"execution_id"`
	Index       int              `db:"idx"` // 1-based position of the step in the job
	Name        string           `db:"name"`
	Command     string           `db:"command"` // command as executed, templates resolved
	Status      enums.StepStatus `db:"status"`
	ExitCode    int              `db:"exit_code"`
	Attempts    int              `db:"attempts"`
	StartedAt   time.Time        `db:"started_at"`
	FinishedAt  time.Time        `db:"finished_at"`
	Output      string           `db:"output"`
}

// GetExecutionSteps retrieves the steps of an execution in order, empty for an execution of a job without steps
func (s *SQLiteStore) GetExecutionSteps(execID int) ([]StepInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	steps := []StepInfo{}
	err := s.db.Select(&steps, `
		SELECT execution_id, idx, name, command, status, exit_code, attempts, started_at, finished_at, output
		FROM execution_steps
		WHERE execution_id = ?
		ORDER BY idx`,
		execID)
	if err != nil {
		return nil, fmt.Errorf("failed to query steps of execution %d: %w", execID, err)
	}
	return steps, nil
}

// insertSteps records the step states of an execution within its transaction
func insertSteps(ctx context.Context, tx *sqlx.Tx, execID int64, steps []request.StepState) error {
	for _, st := range steps {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO execution_steps
			(execution_id, idx, name, command, status, exit_code, attempts, started_at, finished_at, output)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			execID, st.Index, st.Name, st.Command, st.Status.String(), st.ExitCode, st.Attempts, st.StartedAt,
			st.FinishedAt, st.Output)
		if err != nil {
			return fmt.Errorf("failed to record step %d of execution %d: %w", st.Index, execID, err)
		}
	}
	return nil
}
