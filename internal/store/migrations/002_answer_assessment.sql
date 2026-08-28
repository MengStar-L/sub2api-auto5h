ALTER TABLE attempts ADD COLUMN answer_status TEXT NOT NULL DEFAULT '';
ALTER TABLE attempts ADD COLUMN answer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_status TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE remote_accounts ADD COLUMN last_answer_at INTEGER;
