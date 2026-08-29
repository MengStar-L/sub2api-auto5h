ALTER TABLE settings ADD COLUMN direct_wakeup_enabled INTEGER NOT NULL DEFAULT 0;

ALTER TABLE attempts ADD COLUMN request_model TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN transport_path TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN answer_source TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN quota_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN terminal_summary TEXT NOT NULL DEFAULT '';

ALTER TABLE remote_accounts ADD COLUMN last_request_model TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_transport_path TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_source TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_quota_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_terminal_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN verification_deadline_at INTEGER;

UPDATE attempts
SET answer_status = 'legacy_invalid',
    answer_source = 'sub2api_test_v0.1.5',
    terminal_summary = 'v0.1.5 did not forward the configured puzzle'
WHERE answer_status <> '';

UPDATE remote_accounts
SET last_answer_status = 'legacy_invalid',
    last_answer_source = 'sub2api_test_v0.1.5',
    last_terminal_summary = 'v0.1.5 did not forward the configured puzzle'
WHERE last_answer_status <> '';

UPDATE cycles
SET status = 'accepted_unverified',
    lease_until = NULL,
    next_attempt_at = NULL,
    reason = '旧版请求成功，额度未确认',
    updated_at = unixepoch()
WHERE status = 'success_unverified'
  AND COALESCE(accepted_at, updated_at) <= unixepoch() - 60;

UPDATE cycles
SET status = 'verifying',
    lease_until = NULL,
    next_attempt_at = NULL,
    reason = '旧版请求成功，等待额度核验恢复',
    updated_at = unixepoch()
WHERE status = 'success_unverified';

UPDATE remote_accounts
SET runtime_state = 'direct_disabled',
    next_action_at = NULL,
    verification_deadline_at = NULL,
    last_error = '',
    updated_at = unixepoch()
WHERE id IN (SELECT account_id FROM account_policies WHERE enabled = 1)
  AND runtime_state <> 'dispatching';
