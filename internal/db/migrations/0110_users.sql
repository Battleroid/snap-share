-- Users live in the core range so every package can reference them.
-- Sessions, identities, tokens and invites are added by the accounts package (02xx).

CREATE TABLE users (
  id                INTEGER PRIMARY KEY,
  username          TEXT NOT NULL COLLATE NOCASE UNIQUE,
  email             TEXT COLLATE NOCASE UNIQUE,
  email_verified_at INTEGER,
  password_hash     TEXT,                    -- NULL: SSO only
  role              TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  quota_bytes       INTEGER,                 -- NULL: site default
  max_upload_bytes  INTEGER,                 -- NULL: site default
  used_bytes        INTEGER NOT NULL DEFAULT 0 CHECK (used_bytes >= 0),
  reserved_bytes    INTEGER NOT NULL DEFAULT 0 CHECK (reserved_bytes >= 0),
  prefs             TEXT NOT NULL DEFAULT '{}', -- JSON: naming, default visibility/expiry, embed, strip_location
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,
  last_login_at     INTEGER
) STRICT;
