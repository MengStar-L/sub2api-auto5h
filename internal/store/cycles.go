package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const cycleSelect = `SELECT id, account_id, identity_generation, cycle_key, kind, source_reset_at, due_at,
  status, attempt_count, lease_until, accepted_at, next_attempt_at, reason, created_at, updated_at FROM cycles`

func scanCycle(row scanner) (Cycle, error) {
	var out Cycle
	var source, lease, accepted, next sql.NullInt64
	err := row.Scan(&out.ID, &out.AccountID, &out.IdentityGeneration, &out.CycleKey, &out.Kind, &source,
		&out.DueAt, &out.Status, &out.AttemptCount, &lease, &accepted, &next, &out.Reason, &out.CreatedAt, &out.UpdatedAt)
	out.SourceResetAt = int64Ptr(source)
	out.LeaseUntil = int64Ptr(lease)
	out.AcceptedAt = int64Ptr(accepted)
	out.NextAttemptAt = int64Ptr(next)
	return out, err
}

func (s *Store) EnsureCycle(ctx context.Context, accountID string, identityGeneration int, key, kind string, sourceReset *int64, due int64) (Cycle, error) {
	id, err := randomID()
	if err != nil {
		return Cycle{}, err
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cycle{}, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO cycles (id, account_id, identity_generation, cycle_key, kind, source_reset_at, due_at, status, created_at, updated_at)
      VALUES (?, ?, ?, ?, ?, ?, ?, 'waiting', ?, ?)`, id, accountID, identityGeneration, key, kind, sourceReset, due, now, now)
	if err != nil {
		return Cycle{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET next_action_at=?, runtime_state='waiting', updated_at=? WHERE id=?`, due, now, accountID); err != nil {
		return Cycle{}, err
	}
	cycle, err := scanCycle(tx.QueryRowContext(ctx, cycleSelect+` WHERE account_id=? AND identity_generation=? AND cycle_key=?`, accountID, identityGeneration, key))
	if err != nil {
		return Cycle{}, err
	}
	return cycle, tx.Commit()
}

func (s *Store) DueCycle(ctx context.Context, accountID string, now int64) (Cycle, error) {
	cycle, err := scanCycle(s.db.QueryRowContext(ctx, cycleSelect+` WHERE account_id=? AND (
      (status='waiting' AND due_at<=?) OR
      (status='retry_wait' AND next_attempt_at<=?) OR
      (status='dispatching' AND lease_until<=?)
    ) ORDER BY created_at ASC LIMIT 1`, accountID, now, now, now))
	if errors.Is(err, sql.ErrNoRows) {
		return Cycle{}, ErrNotFound
	}
	return cycle, err
}

func (s *Store) StartAttempt(ctx context.Context, cycleID string, now, leaseUntil int64) (Cycle, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Cycle{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE cycles SET status='dispatching', attempt_count=attempt_count+1,
      lease_until=?, dispatch_started_at=?, next_attempt_at=NULL, updated_at=? WHERE id=? AND (
        status IN ('waiting','retry_wait') OR (status='dispatching' AND lease_until<=?)
      )`, leaseUntil, now, now, cycleID, now)
	if err != nil {
		return Cycle{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Cycle{}, false, nil
	}
	cycle, err := scanCycle(tx.QueryRowContext(ctx, cycleSelect+` WHERE id=?`, cycleID))
	if err != nil {
		return Cycle{}, false, err
	}
	return cycle, true, tx.Commit()
}

type AttemptResult struct {
	Outcome    string
	HTTPStatus *int
	ErrorCode  string
	Message    string
	Status     string
	NextAt     *int64
	AcceptedAt *int64
	Reason     string
}

func (s *Store) FinishAttempt(ctx context.Context, cycle Cycle, startedAt, endedAt int64, result AttemptResult) error {
	id, err := randomID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts (id, cycle_id, attempt_number, started_at, ended_at, outcome, http_status, error_code, message)
      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, cycle.ID, cycle.AttemptCount, startedAt, endedAt,
		result.Outcome, result.HTTPStatus, truncate(result.ErrorCode, 80), truncate(result.Message, 500)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cycles SET status=?, lease_until=NULL, accepted_at=COALESCE(?, accepted_at),
      next_attempt_at=?, reason=?, updated_at=? WHERE id=?`, result.Status, result.AcceptedAt, result.NextAt,
		truncate(result.Reason, 500), endedAt, cycle.ID); err != nil {
		return err
	}
	accountState := result.Status
	if result.Status == "success" {
		accountState = "success_unverified"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state=?, next_action_at=?, last_error=?, updated_at=? WHERE id=?`,
		accountState, result.NextAt, truncate(result.Message, 500), endedAt, cycle.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkCycle(ctx context.Context, cycleID, status, reason string, nextAction *int64) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM cycles WHERE id=?`, cycleID).Scan(&accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cycles SET status=?, lease_until=NULL, next_attempt_at=NULL, reason=?, updated_at=? WHERE id=?`, status, truncate(reason, 500), now, cycleID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state=?, next_action_at=?, last_error=?, updated_at=? WHERE id=?`, status, nextAction, truncate(reason, 500), now, accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListCycles(ctx context.Context, accountID string, limit int) ([]Cycle, error) {
	rows, err := s.db.QueryContext(ctx, cycleSelect+` WHERE account_id=? ORDER BY created_at DESC LIMIT ?`, accountID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Cycle, 0)
	for rows.Next() {
		cycle, err := scanCycle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cycle)
	}
	return out, rows.Err()
}

func (s *Store) ListAttempts(ctx context.Context, cycleID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, cycle_id, attempt_number, started_at, ended_at, outcome, http_status, error_code, message FROM attempts WHERE cycle_id=? ORDER BY attempt_number`, cycleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Attempt, 0)
	for rows.Next() {
		var item Attempt
		var ended, status sql.NullInt64
		if err := rows.Scan(&item.ID, &item.CycleID, &item.AttemptNumber, &item.StartedAt, &ended, &item.Outcome, &status, &item.ErrorCode, &item.Message); err != nil {
			return nil, err
		}
		item.EndedAt = int64Ptr(ended)
		if status.Valid {
			value := int(status.Int64)
			item.HTTPStatus = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
