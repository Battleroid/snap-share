-- API keys. A key is snap_<public_id>_<secret>_<checksum>; only the SHA-256 of
-- the secret part is stored, and the full key is shown once at creation.

CREATE TABLE api_keys (
  id            INTEGER PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  public_id     TEXT NOT NULL UNIQUE,         -- 12 base62 characters, shown in the UI
  secret_hash   BLOB NOT NULL CHECK (length(secret_hash) = 32), -- SHA-256 of the secret part
  scopes        TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(scopes)),        -- JSON array of scope names
  allowed_cidrs TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(allowed_cidrs)), -- JSON array; empty = any address
  constraints   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(constraints)),   -- JSON {visibility?, max_expiry_seconds?, max_upload_bytes?}
  expires_at    INTEGER,                      -- NULL: never
  last_used_at  INTEGER,                      -- written in batches, not per request
  last_used_ip  TEXT,
  created_at    INTEGER NOT NULL,
  revoked_at    INTEGER
) STRICT;
CREATE INDEX api_keys_user ON api_keys (user_id, created_at);
