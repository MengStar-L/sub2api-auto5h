package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const accountSelect = `SELECT
  a.id, a.connection_uuid, a.remote_id, a.remote_created_at, a.identity_hash, a.identity_generation,
  a.name, a.email, a.plan_type, a.platform, a.account_type, a.status, a.schedulable, a.parent_account_id,
  a.expires_at, a.auto_pause_on_expired, a.rate_limit_reset_at, a.temp_unschedulable_until,
  a.temp_unschedulable_reason, a.missing, a.eligible, a.eligibility_reason,
  a.five_reset_at, a.five_used_percent, a.seven_reset_at, a.seven_used_percent, a.quota_fetched_at,
  a.quota_state, a.next_action_at, a.runtime_state, a.last_error, a.last_seen_at,
  COALESCE(p.enabled, 0), COALESCE(p.enable_generation, 0), p.model_override,
  p.grace_override_seconds, p.max_retries_override, p.retry_base_override_seconds
FROM remote_accounts a LEFT JOIN account_policies p ON p.account_id = a.id`

type scanner interface {
	Scan(dest ...any) error
}

func scanAccount(row scanner) (Account, error) {
	var out Account
	var parent, fiveReset, sevenReset, fetched, nextAction sql.NullInt64
	var fiveUsed, sevenUsed sql.NullFloat64
	var expires, rateReset, tempUntil sql.NullString
	var model sql.NullString
	var grace, retries, retryBase sql.NullInt64
	var lastSeen int64
	err := row.Scan(
		&out.ID, &out.ConnectionUUID, &out.RemoteID, &out.RemoteCreatedAt, &out.IdentityHash, &out.IdentityGeneration,
		&out.Name, &out.Email, &out.PlanType, &out.Platform, &out.AccountType, &out.Status, &out.Schedulable, &parent,
		&expires, &out.AutoPauseOnExpired, &rateReset, &tempUntil, &out.TempUnschedulableReason,
		&out.Missing, &out.Eligible, &out.EligibilityReason, &fiveReset, &fiveUsed, &sevenReset, &sevenUsed,
		&fetched, &out.QuotaState, &nextAction, &out.RuntimeState, &out.LastError, &lastSeen,
		&out.Policy.Enabled, &out.Policy.EnableGeneration, &model, &grace, &retries, &retryBase,
	)
	if err != nil {
		return Account{}, err
	}
	out.ParentAccountID = int64Ptr(parent)
	out.FiveResetAt = int64Ptr(fiveReset)
	out.FiveUsedPercent = float64Ptr(fiveUsed)
	out.SevenResetAt = int64Ptr(sevenReset)
	out.SevenUsedPercent = float64Ptr(sevenUsed)
	out.QuotaFetchedAt = int64Ptr(fetched)
	out.NextActionAt = int64Ptr(nextAction)
	out.ExpiresAt = stringValue(expires)
	out.RateLimitResetAt = stringValue(rateReset)
	out.TempUnschedulableUntil = stringValue(tempUntil)
	out.Policy.ModelOverride = stringPtr(model)
	out.Policy.GraceOverrideSeconds = intPtr(grace)
	out.Policy.MaxRetriesOverride = intPtr(retries)
	out.Policy.RetryBaseOverrideSeconds = intPtr(retryBase)
	out.LastSeenAt = time.Unix(lastSeen, 0).UTC()
	return out, nil
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func intPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func float64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func stringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func (s *Store) ReplaceInventory(ctx context.Context, connectionUUID string, inputs []RemoteAccountInput) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET missing=1, next_action_at=NULL, runtime_state='missing', updated_at=? WHERE connection_uuid=?`, now, connectionUUID); err != nil {
		return err
	}
	for _, input := range inputs {
		var id, oldIdentity string
		var identityGeneration int
		err := tx.QueryRowContext(ctx, `SELECT id, identity_hash, identity_generation FROM remote_accounts WHERE connection_uuid=? AND remote_id=? AND remote_created_at=?`,
			connectionUUID, input.RemoteID, input.RemoteCreatedAt).Scan(&id, &oldIdentity, &identityGeneration)
		if errors.Is(err, sql.ErrNoRows) {
			id, err = randomID()
			identityGeneration = 1
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		identityChanged := oldIdentity != "" && input.IdentityHash != "" && oldIdentity != input.IdentityHash
		if identityChanged {
			identityGeneration++
			if _, err := tx.ExecContext(ctx, `UPDATE account_policies SET enabled=0, enable_generation=enable_generation+1, updated_at=? WHERE account_id=?`, now, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE cycles SET status='identity_replaced', lease_until=NULL, next_attempt_at=NULL, reason='remote OpenAI identity changed', updated_at=? WHERE account_id=? AND status IN ('waiting','retry_wait','dispatching')`, now, id); err != nil {
				return err
			}
		}
		identity := oldIdentity
		if input.IdentityHash != "" {
			identity = input.IdentityHash
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO remote_accounts (
        id, connection_uuid, remote_id, remote_created_at, identity_hash, identity_generation,
        name, email, plan_type, platform, account_type, status, schedulable, parent_account_id,
        expires_at, auto_pause_on_expired, rate_limit_reset_at, temp_unschedulable_until,
        temp_unschedulable_reason, missing, eligible, eligibility_reason, runtime_state, last_seen_at, created_at, updated_at
      ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?,''), ?, NULLIF(?,''), NULLIF(?,''), ?, 0, ?, ?, ?, ?, ?, ?)
      ON CONFLICT(connection_uuid, remote_id, remote_created_at) DO UPDATE SET
        identity_hash=excluded.identity_hash, identity_generation=excluded.identity_generation,
        name=excluded.name, email=excluded.email, plan_type=CASE WHEN excluded.plan_type='' THEN remote_accounts.plan_type ELSE excluded.plan_type END,
        platform=excluded.platform, account_type=excluded.account_type, status=excluded.status,
        schedulable=excluded.schedulable, parent_account_id=excluded.parent_account_id,
        expires_at=excluded.expires_at, auto_pause_on_expired=excluded.auto_pause_on_expired,
        rate_limit_reset_at=excluded.rate_limit_reset_at, temp_unschedulable_until=excluded.temp_unschedulable_until,
        temp_unschedulable_reason=excluded.temp_unschedulable_reason, missing=0, eligible=excluded.eligible,
        eligibility_reason=excluded.eligibility_reason,
        runtime_state=CASE WHEN ? THEN 'identity_changed' WHEN excluded.eligible=0 THEN 'paused' ELSE remote_accounts.runtime_state END,
        next_action_at=CASE WHEN ? OR excluded.eligible=0 THEN NULL ELSE remote_accounts.next_action_at END,
        last_seen_at=excluded.last_seen_at, updated_at=excluded.updated_at`,
			id, connectionUUID, input.RemoteID, input.RemoteCreatedAt, identity, identityGeneration,
			truncate(input.Name, 200), truncate(input.Email, 320), strings.ToLower(strings.TrimSpace(input.PlanType)), input.Platform,
			input.AccountType, input.Status, input.Schedulable, input.ParentAccountID, input.ExpiresAt,
			input.AutoPauseOnExpired, input.RateLimitResetAt, input.TempUnschedulableUntil, truncate(input.TempUnschedulableReason, 500),
			input.Eligible, truncate(input.EligibilityReason, 200), map[bool]string{true: "identity_changed", false: "disabled"}[identityChanged], now, now, now,
			identityChanged, identityChanged)
		if err != nil {
			return fmt.Errorf("upsert remote account %d: %w", input.RemoteID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO account_policies (account_id, enabled, enable_generation, updated_at) VALUES (?, 0, 0, ?)`, id, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, accountSelect+` ORDER BY a.missing ASC, a.name COLLATE NOCASE ASC, a.remote_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		account, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func (s *Store) GetAccount(ctx context.Context, id string) (Account, error) {
	account, err := scanAccount(s.db.QueryRowContext(ctx, accountSelect+` WHERE a.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return account, err
}

func (s *Store) SetPolicy(ctx context.Context, id string, policy Policy) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous bool
	var generation int
	if err := tx.QueryRowContext(ctx, `SELECT enabled, enable_generation FROM account_policies WHERE account_id=?`, id).Scan(&previous, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if policy.Enabled && !previous {
		generation++
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_policies SET enabled=?, enable_generation=?, model_override=?, grace_override_seconds=?, max_retries_override=?, retry_base_override_seconds=?, updated_at=? WHERE account_id=?`,
		policy.Enabled, generation, policy.ModelOverride, policy.GraceOverrideSeconds, policy.MaxRetriesOverride, policy.RetryBaseOverrideSeconds, now, id); err != nil {
		return err
	}
	state := "disabled"
	var next any
	if policy.Enabled {
		state = "pending_check"
		next = now
	}
	if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state=?, next_action_at=?, last_error='', updated_at=? WHERE id=?`, state, next, now, id); err != nil {
		return err
	}
	if !policy.Enabled {
		if _, err := tx.ExecContext(ctx, `UPDATE cycles SET status='disabled', lease_until=NULL, next_attempt_at=NULL, reason='automation disabled', updated_at=? WHERE account_id=? AND status IN ('waiting','retry_wait','dispatching')`, now, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ApplyQuota(ctx context.Context, id string, update QuotaUpdate) (bool, error) {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var previous string
	if err := tx.QueryRowContext(ctx, `SELECT identity_hash FROM remote_accounts WHERE id=?`, id).Scan(&previous); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	changed := previous != "" && update.IdentityHash != "" && previous != update.IdentityHash
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET identity_hash=?, identity_generation=identity_generation+1,
        five_reset_at=NULL, five_used_percent=NULL, seven_reset_at=NULL, seven_used_percent=NULL,
        quota_fetched_at=?, quota_state='identity_changed', next_action_at=NULL, runtime_state='identity_changed',
        last_error='remote OpenAI identity changed; re-enable automation', updated_at=? WHERE id=?`, update.IdentityHash, update.FetchedAt, now, id); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_policies SET enabled=0, enable_generation=enable_generation+1, updated_at=? WHERE account_id=?`, now, id); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cycles SET status='identity_replaced', lease_until=NULL, next_attempt_at=NULL, reason='remote OpenAI identity changed', updated_at=? WHERE account_id=? AND status IN ('waiting','retry_wait','dispatching')`, now, id); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	identity := previous
	if update.IdentityHash != "" {
		identity = update.IdentityHash
	}
	_, err = tx.ExecContext(ctx, `UPDATE remote_accounts SET identity_hash=?,
      plan_type=CASE WHEN ?='' THEN plan_type ELSE ? END,
      five_reset_at=?, five_used_percent=?, seven_reset_at=?, seven_used_percent=?, quota_fetched_at=?,
      quota_state=?, next_action_at=?, runtime_state=?, last_error=?, updated_at=? WHERE id=?`, identity,
		update.PlanType, strings.ToLower(strings.TrimSpace(update.PlanType)), update.FiveResetAt, update.FiveUsedPercent,
		update.SevenResetAt, update.SevenUsedPercent, update.FetchedAt, update.State, update.NextActionAt,
		update.RuntimeState, truncate(update.LastError, 500), now, id)
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func (s *Store) SetAccountRuntime(ctx context.Context, id, state, message string, nextAction *int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state=?, last_error=?, next_action_at=?, updated_at=? WHERE id=?`, state, truncate(message, 500), nextAction, time.Now().Unix(), id)
	return err
}

func (s *Store) SetEligibility(ctx context.Context, id string, eligible bool, reason, plan string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE remote_accounts SET eligible=?, eligibility_reason=?,
      plan_type=CASE WHEN ?='' THEN plan_type ELSE ? END, updated_at=? WHERE id=?`,
		eligible, truncate(reason, 200), plan, strings.ToLower(strings.TrimSpace(plan)), time.Now().Unix(), id)
	return err
}

func (s *Store) ListDueAccountIDs(ctx context.Context, now int64, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id FROM remote_accounts a JOIN account_policies p ON p.account_id=a.id
      WHERE p.enabled=1 AND a.missing=0 AND a.next_action_at IS NOT NULL AND a.next_action_at<=?
      ORDER BY a.next_action_at ASC LIMIT ?`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
