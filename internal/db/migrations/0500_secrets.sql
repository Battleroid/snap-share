-- One-time secrets (internal/secrets). See docs/secrets.md and the
-- "E2E envelope v1" and "One-time secrets" sections of docs/security.md.
--
-- No ciphertext is stored here: the sealed metadata and the content chunks
-- are files in the secrets blob store (SNAP_SECRETS_DIR), listed in
-- secret_blobs. Times are INTEGER unix milliseconds UTC.

CREATE TABLE secrets (
  id                TEXT    PRIMARY KEY CHECK (length(id) = 22), -- base64url of 16 random bytes
  owner_id          INTEGER REFERENCES users (id) ON DELETE CASCADE, -- NULL: anonymous
  kind              TEXT    NOT NULL CHECK (kind IN ('text', 'file')),
  mode              TEXT    NOT NULL CHECK (mode IN ('e2e', 'sealed')),
  state             TEXT    NOT NULL DEFAULT 'uploading'
                    CHECK (state IN ('uploading', 'ready', 'burned', 'expired', 'revoked')),

  -- Envelope values, set by finalize. kdf is JSON {"m","t","p"}; NULL means
  -- no passphrase. h_probe and h_auth are SHA-256 of K_probe and K_auth.
  salt              BLOB    CHECK (salt IS NULL OR length(salt) = 16),
  kdf               TEXT,
  h_probe           BLOB    CHECK (h_probe IS NULL OR length(h_probe) = 32),
  h_auth            BLOB    CHECK (h_auth IS NULL OR length(h_auth) = 32),
  commit_tag        BLOB    CHECK (commit_tag IS NULL OR length(commit_tag) = 32),

  -- Content ciphertext: ct_size bytes in chunk_count STREAM chunks.
  ct_size           INTEGER NOT NULL CHECK (ct_size >= 16),
  chunk_count       INTEGER NOT NULL CHECK (chunk_count >= 1),
  chunks_received   INTEGER NOT NULL DEFAULT 0 CHECK (chunks_received >= 0),

  views_left        INTEGER NOT NULL CHECK (views_left >= 0),
  max_views         INTEGER NOT NULL CHECK (max_views >= 1),
  failures          INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0),
  max_failures      INTEGER NOT NULL CHECK (max_failures >= 1),
  expires_at        INTEGER NOT NULL,

  note              TEXT    NOT NULL DEFAULT '' CHECK (length(note) <= 200), -- owner only; plaintext
  notify            INTEGER NOT NULL DEFAULT 0 CHECK (notify IN (0, 1)),

  -- Tombstone. burn_reason: 'viewed', 'failed attempts', 'revoked', 'expired'.
  opened_at         INTEGER,
  burned_at         INTEGER,
  burn_reason       TEXT,

  -- Only while uploading: SHA-256 of the upload token and the creator's IP
  -- (for the per-IP cap on open uploads). Both are cleared by finalize.
  upload_token_hash BLOB    CHECK (upload_token_hash IS NULL OR length(upload_token_hash) = 32),
  upload_ip         TEXT,

  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
) STRICT;

CREATE INDEX secrets_owner_created ON secrets (owner_id, created_at);
CREATE INDEX secrets_state_expires ON secrets (state, expires_at);
CREATE INDEX secrets_state_created ON secrets (state, created_at);
CREATE INDEX secrets_upload_ip ON secrets (upload_ip) WHERE upload_ip IS NOT NULL;

-- Download tickets for file secrets: SHA-256 of a 256-bit token, bound to
-- one secret. A ticket keeps the content blob alive until it expires.
CREATE TABLE secret_tickets (
  hash       BLOB    PRIMARY KEY CHECK (length(hash) = 32),
  secret_id  TEXT    NOT NULL REFERENCES secrets (id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX secret_tickets_secret ON secret_tickets (secret_id, expires_at);
CREATE INDEX secret_tickets_expires ON secret_tickets (expires_at);

-- Ledger of blob-store files per secret: part -1 is the sealed metadata,
-- parts 0..chunk_count-1 are the content chunks. A row exists exactly while
-- its file may exist. There is deliberately no foreign key: when a secret
-- row disappears (user deleted, tombstone purged) its ledger rows stay until
-- the sweeper has deleted the files.
CREATE TABLE secret_blobs (
  secret_id TEXT    NOT NULL,
  part      INTEGER NOT NULL CHECK (part >= -1),
  blob_key  TEXT    NOT NULL,
  size      INTEGER NOT NULL CHECK (size >= 0),
  sha256    BLOB    NOT NULL CHECK (length(sha256) = 32),
  PRIMARY KEY (secret_id, part)
) STRICT, WITHOUT ROWID;
