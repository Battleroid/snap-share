-- Accounts: SSO identities, browser sessions, emailed tokens and invites.
-- Secrets (session ids, tokens) are stored only as SHA-256 hashes.

CREATE TABLE user_identities (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  provider       TEXT NOT NULL,              -- OIDC issuer URL
  subject        TEXT NOT NULL,              -- the provider's stable user id (sub)
  username       TEXT,                       -- last preferred_username
  email          TEXT,                       -- last email
  email_verified INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0, 1)),
  groups         TEXT NOT NULL DEFAULT '[]', -- last groups claim, JSON array
  created_at     INTEGER NOT NULL,
  last_login_at  INTEGER,
  UNIQUE (provider, subject),
  UNIQUE (user_id, provider)
) STRICT;

CREATE TABLE sessions (
  id           BLOB PRIMARY KEY,             -- SHA-256 of the 256-bit cookie value
  user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,             -- throttled; idle expiry counts from here
  expires_at   INTEGER NOT NULL,             -- absolute expiry
  ip           TEXT,
  user_agent   TEXT
) STRICT;
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE tokens (
  hash       BLOB PRIMARY KEY,               -- SHA-256 of the emailed token
  kind       TEXT NOT NULL CHECK (kind IN ('verify', 'reset', 'email_change')),
  user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  payload    TEXT NOT NULL DEFAULT '{}',     -- JSON, e.g. {"email": "..."} for email_change
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER
) STRICT;
CREATE INDEX tokens_user ON tokens (user_id, kind);
CREATE INDEX tokens_expires ON tokens (expires_at);

CREATE TABLE invites (
  id         INTEGER PRIMARY KEY,
  hash       BLOB NOT NULL UNIQUE,           -- SHA-256 of the invite token
  created_by INTEGER REFERENCES users (id) ON DELETE CASCADE, -- NULL: created with the admin CLI
  email      TEXT,                           -- optional; prefilled and trusted when the invitee keeps it
  role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  max_uses   INTEGER NOT NULL DEFAULT 1 CHECK (max_uses >= 1),
  uses       INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  revoked_at INTEGER
) STRICT;
CREATE INDEX invites_created ON invites (created_at);
