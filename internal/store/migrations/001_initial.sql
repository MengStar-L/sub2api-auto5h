CREATE TABLE IF NOT EXISTS app_meta (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    setup_complete INTEGER NOT NULL DEFAULT 0,
    setup_token_hash TEXT,
    setup_token_expires_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
INSERT OR IGNORE INTO app_meta (id, created_at, updated_at) VALUES (1, unixepoch(), unixepoch());

CREATE TABLE IF NOT EXISTS admin_users (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    connection_uuid TEXT NOT NULL,
    base_url TEXT NOT NULL,
    api_key_cipher TEXT NOT NULL,
    global_model TEXT NOT NULL,
    sync_interval_seconds INTEGER NOT NULL DEFAULT 300,
    reset_grace_seconds INTEGER NOT NULL DEFAULT 30,
    max_retries INTEGER NOT NULL DEFAULT 3,
    retry_base_seconds INTEGER NOT NULL DEFAULT 30,
    request_timeout_seconds INTEGER NOT NULL DEFAULT 90,
    max_concurrency INTEGER NOT NULL DEFAULT 4,
    allow_private_http INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    csrf_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    idle_expires_at INTEGER NOT NULL,
    absolute_expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (absolute_expires_at);

CREATE TABLE IF NOT EXISTS remote_accounts (
    id TEXT PRIMARY KEY,
    connection_uuid TEXT NOT NULL,
    remote_id INTEGER NOT NULL,
    remote_created_at TEXT NOT NULL,
    identity_hash TEXT NOT NULL DEFAULT '',
    identity_generation INTEGER NOT NULL DEFAULT 1,
    name TEXT NOT NULL DEFAULT '',
    email TEXT NOT NULL DEFAULT '',
    plan_type TEXT NOT NULL DEFAULT '',
    platform TEXT NOT NULL,
    account_type TEXT NOT NULL,
    status TEXT NOT NULL,
    schedulable INTEGER NOT NULL,
    parent_account_id INTEGER,
    expires_at TEXT,
    auto_pause_on_expired INTEGER NOT NULL DEFAULT 0,
    rate_limit_reset_at TEXT,
    temp_unschedulable_until TEXT,
    temp_unschedulable_reason TEXT NOT NULL DEFAULT '',
    missing INTEGER NOT NULL DEFAULT 0,
    eligible INTEGER NOT NULL DEFAULT 0,
    eligibility_reason TEXT NOT NULL DEFAULT '',
    five_reset_at INTEGER,
    five_used_percent REAL,
    seven_reset_at INTEGER,
    seven_used_percent REAL,
    quota_fetched_at INTEGER,
    quota_state TEXT NOT NULL DEFAULT 'unknown',
    next_action_at INTEGER,
    runtime_state TEXT NOT NULL DEFAULT 'disabled',
    last_error TEXT NOT NULL DEFAULT '',
    last_seen_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (connection_uuid, remote_id, remote_created_at)
);
CREATE INDEX IF NOT EXISTS remote_accounts_due_idx ON remote_accounts (next_action_at);
CREATE INDEX IF NOT EXISTS remote_accounts_remote_idx ON remote_accounts (connection_uuid, remote_id);

CREATE TABLE IF NOT EXISTS account_policies (
    account_id TEXT PRIMARY KEY REFERENCES remote_accounts(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0,
    enable_generation INTEGER NOT NULL DEFAULT 0,
    model_override TEXT,
    grace_override_seconds INTEGER,
    max_retries_override INTEGER,
    retry_base_override_seconds INTEGER,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS cycles (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES remote_accounts(id) ON DELETE CASCADE,
    identity_generation INTEGER NOT NULL,
    cycle_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    source_reset_at INTEGER,
    due_at INTEGER NOT NULL,
    status TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    lease_until INTEGER,
    dispatch_started_at INTEGER,
    accepted_at INTEGER,
    next_attempt_at INTEGER,
    reason TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (account_id, identity_generation, cycle_key)
);
CREATE INDEX IF NOT EXISTS cycles_due_idx ON cycles (status, due_at, next_attempt_at);

CREATE TABLE IF NOT EXISTS attempts (
    id TEXT PRIMARY KEY,
    cycle_id TEXT NOT NULL REFERENCES cycles(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    started_at INTEGER NOT NULL,
    ended_at INTEGER,
    outcome TEXT NOT NULL,
    http_status INTEGER,
    error_code TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    UNIQUE (cycle_id, attempt_number)
);

CREATE TABLE IF NOT EXISTS audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    level TEXT NOT NULL,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    account_id TEXT REFERENCES remote_accounts(id) ON DELETE SET NULL,
    message TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events (created_at DESC);
