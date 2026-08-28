package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MengStar-L/sub2api-auto5h/internal/secure"
	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound      = errors.New("record not found")
	ErrSetupComplete = errors.New("setup is already complete")
	ErrInvalidToken  = errors.New("setup token is invalid or expired")
	ErrSecretsLocked = errors.New("encrypted settings cannot be opened with the configured master key")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const settingsAAD = "settings:api-key:v1"

type Store struct {
	db       *sql.DB
	box      *secure.Box
	lockFile *os.File
	close    sync.Once
}

func Open(ctx context.Context, path string, box *secure.Box) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("another sub2api-auto5h process owns %s: %w", path, err)
	}

	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29&_pragma=journal_mode%28WAL%29"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	store := &Store{db: db, box: box, lockFile: lockFile}
	if err := store.migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = store.Close()
		return nil, fmt.Errorf("restrict database permissions: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	var result error
	s.close.Do(func() {
		if s.db != nil {
			result = s.db.Close()
		}
		if s.lockFile != nil {
			_ = unix.Flock(int(s.lockFile.Fd()), unix.LOCK_UN)
			if err := s.lockFile.Close(); result == nil {
				result = err
			}
		}
	})
	return result
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, entry.Name()).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if exists != 0 {
			continue
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, entry.Name(), time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) IsSetupComplete(ctx context.Context) (bool, error) {
	var complete bool
	if err := s.db.QueryRowContext(ctx, `SELECT setup_complete FROM app_meta WHERE id = 1`).Scan(&complete); err != nil {
		return false, fmt.Errorf("read setup state: %w", err)
	}
	return complete, nil
}

func (s *Store) SetSetupToken(ctx context.Context, hash string, expiresAt int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE app_meta SET setup_token_hash = ?, setup_token_expires_at = ?, updated_at = ? WHERE id = 1 AND setup_complete = 0`, hash, expiresAt, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("save setup token: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrSetupComplete
	}
	return nil
}

func (s *Store) CompleteSetup(ctx context.Context, tokenHash, username, passwordHash string, settings Settings) error {
	if s.box == nil {
		return ErrSecretsLocked
	}
	ciphertext, err := s.box.Seal([]byte(settings.APIKey), settingsAAD)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var complete bool
	var expected sql.NullString
	var expires sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT setup_complete, setup_token_hash, setup_token_expires_at FROM app_meta WHERE id = 1`).Scan(&complete, &expected, &expires); err != nil {
		return err
	}
	if complete {
		return ErrSetupComplete
	}
	if !expected.Valid || expected.String != tokenHash || !expires.Valid || expires.Int64 < now {
		return ErrInvalidToken
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_users (id, username, password_hash, created_at, updated_at) VALUES (1, ?, ?, ?, ?)`, username, passwordHash, now, now); err != nil {
		return fmt.Errorf("create administrator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (
        id, connection_uuid, base_url, api_key_cipher, global_model, sync_interval_seconds,
        reset_grace_seconds, max_retries, retry_base_seconds, request_timeout_seconds,
        max_concurrency, allow_private_http, direct_wakeup_enabled, updated_at
      ) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		settings.ConnectionUUID, settings.BaseURL, ciphertext, settings.GlobalModel, settings.SyncIntervalSeconds,
		settings.ResetGraceSeconds, settings.MaxRetries, settings.RetryBaseSeconds, settings.RequestTimeoutSeconds,
		settings.MaxConcurrency, settings.AllowPrivateHTTP, settings.DirectWakeupEnabled, now); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_meta SET setup_complete = 1, setup_token_hash = NULL, setup_token_expires_at = NULL, updated_at = ? WHERE id = 1`, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminPassword(ctx context.Context, username string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM admin_users WHERE username = ?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (s *Store) UpdateAdminPassword(ctx context.Context, passwordHash, keepSessionHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE admin_users SET password_hash=?, updated_at=? WHERE id=1`, passwordHash, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash<>?`, keepSessionHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetSettings(ctx context.Context) (Settings, error) {
	var out Settings
	var encrypted string
	var allowPrivate, directWakeup bool
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT connection_uuid, base_url, api_key_cipher, global_model,
      sync_interval_seconds, reset_grace_seconds, max_retries, retry_base_seconds,
      request_timeout_seconds, max_concurrency, allow_private_http, direct_wakeup_enabled, updated_at FROM settings WHERE id = 1`).Scan(
		&out.ConnectionUUID, &out.BaseURL, &encrypted, &out.GlobalModel, &out.SyncIntervalSeconds,
		&out.ResetGraceSeconds, &out.MaxRetries, &out.RetryBaseSeconds, &out.RequestTimeoutSeconds,
		&out.MaxConcurrency, &allowPrivate, &directWakeup, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, ErrNotFound
	}
	if err != nil {
		return Settings{}, err
	}
	out.AllowPrivateHTTP = allowPrivate
	out.DirectWakeupEnabled = directWakeup
	out.UpdatedAt = time.Unix(updated, 0).UTC()
	if s.box == nil {
		return Settings{}, ErrSecretsLocked
	}
	apiKey, err := s.box.Open(encrypted, settingsAAD)
	if err != nil {
		return Settings{}, ErrSecretsLocked
	}
	out.APIKey = string(apiKey)
	return out, nil
}

func (s *Store) UpdateSettings(ctx context.Context, next Settings, replaceAPIKey bool, rotateConnection bool) error {
	current, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	if !replaceAPIKey {
		next.APIKey = current.APIKey
	}
	if !rotateConnection {
		next.ConnectionUUID = current.ConnectionUUID
	}
	ciphertext, err := s.box.Seal([]byte(next.APIKey), settingsAAD)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET connection_uuid=?, base_url=?, api_key_cipher=?, global_model=?,
      sync_interval_seconds=?, reset_grace_seconds=?, max_retries=?, retry_base_seconds=?, request_timeout_seconds=?,
	      max_concurrency=?, allow_private_http=?, direct_wakeup_enabled=?, updated_at=? WHERE id=1`, next.ConnectionUUID, next.BaseURL, ciphertext,
		next.GlobalModel, next.SyncIntervalSeconds, next.ResetGraceSeconds, next.MaxRetries, next.RetryBaseSeconds,
		next.RequestTimeoutSeconds, next.MaxConcurrency, next.AllowPrivateHTTP, next.DirectWakeupEnabled, now); err != nil {
		return err
	}
	if rotateConnection {
		if _, err := tx.ExecContext(ctx, `UPDATE account_policies SET enabled=0, enable_generation=enable_generation+1, updated_at=?`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET next_action_at=NULL, runtime_state='connection_changed', updated_at=?`, now); err != nil {
			return err
		}
	}
	if current.DirectWakeupEnabled != next.DirectWakeupEnabled {
		if next.DirectWakeupEnabled {
			if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state='pending_check', next_action_at=?, last_error='', updated_at=?
          WHERE missing=0 AND eligible=1 AND runtime_state<>'dispatching' AND id IN (
            SELECT account_id FROM account_policies WHERE enabled=1
          )`, now, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE remote_accounts SET runtime_state='direct_disabled', next_action_at=NULL, last_error='', verification_deadline_at=NULL, updated_at=?
          WHERE runtime_state<>'dispatching' AND id IN (
            SELECT account_id FROM account_policies WHERE enabled=1
          )`, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	complete, err := s.IsSetupComplete(ctx)
	if err != nil || !complete {
		return err
	}
	_, err = s.GetSettings(ctx)
	return err
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, csrf_hash, created_at, last_seen_at, idle_expires_at, absolute_expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		session.TokenHash, session.CSRFHash, session.CreatedAt, session.LastSeenAt, session.IdleExpiresAt, session.AbsoluteExpiresAt)
	return err
}

func (s *Store) GetSession(ctx context.Context, tokenHash string, now int64) (Session, error) {
	var out Session
	err := s.db.QueryRowContext(ctx, `SELECT token_hash, csrf_hash, created_at, last_seen_at, idle_expires_at, absolute_expires_at FROM sessions
      WHERE token_hash = ? AND idle_expires_at > ? AND absolute_expires_at > ?`, tokenHash, now, now).Scan(
		&out.TokenHash, &out.CSRFHash, &out.CreatedAt, &out.LastSeenAt, &out.IdleExpiresAt, &out.AbsoluteExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return out, err
}

func (s *Store) TouchSession(ctx context.Context, tokenHash string, now, idleExpires int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=?, idle_expires_at=MIN(absolute_expires_at, ?) WHERE token_hash=?`, now, idleExpires, tokenHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

func randomID() (string, error) {
	plain, _, err := secure.GenerateToken(16)
	return plain, err
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
