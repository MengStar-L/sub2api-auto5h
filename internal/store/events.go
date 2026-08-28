package store

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) AddEvent(ctx context.Context, level, actor, action, accountID, message, metadata string) error {
	var account any
	if accountID != "" {
		account = accountID
	}
	if metadata == "" {
		metadata = "{}"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events (level, actor, action, account_id, message, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		level, actor, action, account, truncate(message, 500), truncate(metadata, 2000), time.Now().Unix())
	return err
}

func (s *Store) ListEvents(ctx context.Context, before int64, limit int) ([]Event, error) {
	if before == 0 {
		before = time.Now().Add(time.Second).Unix()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, level, actor, action, account_id, message, metadata_json, created_at FROM audit_events WHERE created_at<? ORDER BY created_at DESC, id DESC LIMIT ?`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0)
	for rows.Next() {
		var item Event
		var account sql.NullString
		if err := rows.Scan(&item.ID, &item.Level, &item.Actor, &item.Action, &account, &item.Message, &item.MetadataJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.AccountID = stringValue(account)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) Prune(ctx context.Context, before int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE created_at<?`, before); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM attempts WHERE ended_at IS NOT NULL AND ended_at<?`, before); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE absolute_expires_at<?`, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
