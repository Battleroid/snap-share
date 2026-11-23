-- Second factors and passkeys (internal/accounts, docs/accounts.md).
-- Times are INTEGER unix milliseconds UTC.
--
-- A user "has 2FA" while they have a WebAuthn credential or a TOTP secret;
-- it is worked out from these tables on every request, never stored as a
-- flag that could drift.

-- The WebAuthn user handle: 32 random bytes, made the first time a passkey
-- is registered. Never the integer id. ALTER TABLE can't add UNIQUE, hence
-- the index.
ALTER TABLE users ADD COLUMN webauthn_id BLOB CHECK (webauthn_id IS NULL OR length(webauthn_id) = 32);
CREATE UNIQUE INDEX users_webauthn_id ON users (webauthn_id) WHERE webauthn_id IS NOT NULL;

-- Passkeys and security keys. The backup flags are kept as the authenticator
-- reported them: the library refuses an assertion whose flags contradict
-- the stored ones.
CREATE TABLE webauthn_credentials (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  credential_id   BLOB    NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 16 AND 1023),
  public_key      BLOB    NOT NULL,         -- COSE key
  sign_count      INTEGER NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
  transports      TEXT    NOT NULL DEFAULT '[]', -- JSON array
  backup_eligible INTEGER NOT NULL DEFAULT 0 CHECK (backup_eligible IN (0, 1)),
  backup_state    INTEGER NOT NULL DEFAULT 0 CHECK (backup_state IN (0, 1)),
  name            TEXT    NOT NULL DEFAULT '' CHECK (length(name) <= 64),
  created_at      INTEGER NOT NULL,
  last_used_at    INTEGER
) STRICT;
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

-- A confirmed authenticator-app secret (RFC 6238), sealed with a key derived
-- from SNAP_SECRET_KEY and bound to the user id. last_step is the newest
-- time step accepted, so a code can't be used twice. A setup in progress
-- lives in its auth_challenges row, so it never replaces a working secret.
CREATE TABLE user_totp (
  user_id    INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  secret     BLOB    NOT NULL,
  last_step  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
) STRICT;

-- Single-use recovery codes: SHA-256 of a label, the user id and the
-- normalized 80-bit code. Only the hash is kept.
CREATE TABLE recovery_codes (
  user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  code_hash  BLOB    NOT NULL CHECK (length(code_hash) = 32),
  created_at INTEGER NOT NULL,
  used_at    INTEGER,
  PRIMARY KEY (user_id, code_hash)
) STRICT, WITHOUT ROWID;

-- Short-lived state between the steps of a sign-in or a WebAuthn ceremony.
-- hash is SHA-256 of a random token kept in the __Host-snap_auth cookie.
-- Rows are taken with DELETE ... RETURNING (single use, also on failure);
-- the sweeper removes expired ones.
CREATE TABLE auth_challenges (
  hash       BLOB    PRIMARY KEY CHECK (length(hash) = 32),
  kind       TEXT    NOT NULL CHECK (kind IN ('passkey_login', 'mfa_login', 'register', 'reauth')),
  user_id    INTEGER REFERENCES users (id) ON DELETE CASCADE, -- NULL: passkey sign-in
  data       TEXT    NOT NULL DEFAULT '{}', -- JSON
  next       TEXT    NOT NULL DEFAULT '',
  attempts   INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX auth_challenges_expires ON auth_challenges (expires_at);
