-- Core tables shared by every feature. Timestamps are INTEGER unix milliseconds (UTC).

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,           -- JSON
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE jobs (
  id           INTEGER PRIMARY KEY,
  kind         TEXT NOT NULL,
  payload      TEXT NOT NULL DEFAULT '{}',
  state        TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done', 'failed')),
  attempts     INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  run_after    INTEGER NOT NULL,
  locked_until INTEGER,
  last_error   TEXT,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
) STRICT;
CREATE INDEX jobs_ready ON jobs (state, run_after);

CREATE TABLE audit_log (
  id            INTEGER PRIMARY KEY,
  at            INTEGER NOT NULL,
  actor_user_id INTEGER,
  actor_key_id  INTEGER,
  ip            TEXT,
  action        TEXT NOT NULL,
  target        TEXT,
  details       TEXT NOT NULL DEFAULT '{}'
) STRICT;
CREATE INDEX audit_log_at ON audit_log (at);
