-- Items: files, pastes, short links and albums share one slug namespace.
-- Timestamps are INTEGER unix milliseconds (UTC).

CREATE TABLE items (
  id                  INTEGER PRIMARY KEY,
  owner_id            INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind                TEXT NOT NULL CHECK (kind IN ('file', 'paste', 'link', 'album')),
  slug                TEXT NOT NULL COLLATE NOCASE UNIQUE,
  title               TEXT NOT NULL DEFAULT '',
  filename            TEXT NOT NULL DEFAULT '',
  mime                TEXT NOT NULL DEFAULT '',
  size                INTEGER NOT NULL DEFAULT 0 CHECK (size >= 0),
  sha256              TEXT,
  width               INTEGER,
  height              INTEGER,
  duration_ms         INTEGER,
  blob_key            TEXT,
  thumb_key           TEXT,
  preview_key         TEXT,
  render_key          TEXT,
  target_url          TEXT,
  visibility          TEXT NOT NULL DEFAULT 'unlisted' CHECK (visibility IN ('unlisted', 'private')),
  password_hash       TEXT,
  expires_at          INTEGER,
  expiry_action       TEXT NOT NULL DEFAULT 'delete' CHECK (expiry_action IN ('delete', 'private')),
  views               INTEGER NOT NULL DEFAULT 0,
  downloads           INTEGER NOT NULL DEFAULT 0,
  first_opened_at     INTEGER,
  last_opened_at      INTEGER,
  opened_count        INTEGER NOT NULL DEFAULT 0,
  deletion_token_hash TEXT UNIQUE,         -- SHA-256 hex of the ShareX deletion token
  meta                TEXT NOT NULL DEFAULT '{}', -- JSON: paste language, render info
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
) STRICT;
CREATE INDEX items_owner_created ON items (owner_id, created_at DESC, id DESC);
CREATE INDEX items_expires ON items (expires_at) WHERE expires_at IS NOT NULL;

-- Search over title, filename, a paste excerpt and the link target. rowid is
-- items.id. Maintained from Go in the same transaction as the item (no triggers).
CREATE VIRTUAL TABLE items_fts USING fts5 (title, filename, body, target_url, tokenize = 'trigram');

-- Uploads in progress. Every quota reservation lives here: chunked browser
-- uploads (with a partial file in the blob store's temp dir) and streamed
-- multipart/PUT uploads, so the reconcile job can recompute reserved_bytes.
CREATE TABLE uploads (
  id            TEXT PRIMARY KEY,          -- random 128-bit hex
  owner_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind          TEXT NOT NULL CHECK (kind IN ('chunked', 'stream')),
  filename      TEXT NOT NULL DEFAULT '',
  declared_type TEXT NOT NULL DEFAULT '',
  size          INTEGER NOT NULL CHECK (size >= 0), -- declared size (chunked) or byte limit (stream)
  received      INTEGER NOT NULL DEFAULT 0,
  reserved      INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  temp_ref      TEXT,                      -- partial name in the blob store
  options       TEXT NOT NULL DEFAULT '{}', -- JSON: expiry, visibility, password hash, naming
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  expires_at    INTEGER NOT NULL
) STRICT;
CREATE INDEX uploads_owner ON uploads (owner_id);
CREATE INDEX uploads_expires ON uploads (expires_at);
