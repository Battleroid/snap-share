-- snap: foreign_keys=off
-- Gists: one item with one link holding several text files.
--
-- items is rebuilt to allow kind 'gist' (SQLite's 12-step procedure, as in
-- 0320; the runner turns foreign keys off around this transaction and runs
-- foreign_key_check before it commits). Every column, index and id is kept.
-- The AUTOINCREMENT high-water mark is carried over to the new table, so ids
-- of items deleted before the rebuild are still never reused. album_items,
-- item_tags and gist_files reference items (id); after the RENAME they point
-- at the new table. items_fts is keyed by rowid = items.id, which is kept.
--
-- A gist's items row has no blob_key. Its size is the sum of its files'
-- sizes and its render_size the sum of their cached renders, so quota
-- accounting needs no special case.

CREATE TABLE items_new (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id            INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind                TEXT NOT NULL CHECK (kind IN ('file', 'paste', 'link', 'album', 'gist')),
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
  updated_at          INTEGER NOT NULL,
  previewed_at        INTEGER,
  nonce               TEXT NOT NULL DEFAULT (lower(hex(randomblob(16)))),
  created_by_key      INTEGER,
  render_size         INTEGER NOT NULL DEFAULT 0 CHECK (render_size >= 0)
) STRICT;

INSERT INTO items_new (id, owner_id, kind, slug, title, filename, mime, size, sha256, width, height, duration_ms,
  blob_key, thumb_key, preview_key, render_key, target_url, visibility, password_hash, expires_at, expiry_action,
  views, downloads, first_opened_at, last_opened_at, opened_count, deletion_token_hash, meta, created_at, updated_at,
  previewed_at, nonce, created_by_key, render_size)
SELECT id, owner_id, kind, slug, title, filename, mime, size, sha256, width, height, duration_ms,
  blob_key, thumb_key, preview_key, render_key, target_url, visibility, password_hash, expires_at, expiry_action,
  views, downloads, first_opened_at, last_opened_at, opened_count, deletion_token_hash, meta, created_at, updated_at,
  previewed_at, nonce, created_by_key, render_size
FROM items;

-- Keep the sequence: the INSERT only raised items_new's to the largest live
-- id, and dropping items removes its row.
INSERT INTO sqlite_sequence (name, seq)
  SELECT 'items_new', 0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'items_new');
UPDATE sqlite_sequence SET seq = max(seq, coalesce((SELECT seq FROM sqlite_sequence WHERE name = 'items'), 0))
  WHERE name = 'items_new';

DROP TABLE items;
ALTER TABLE items_new RENAME TO items;

CREATE INDEX items_owner_created ON items (owner_id, created_at DESC, id DESC);
CREATE INDEX items_expires ON items (expires_at) WHERE expires_at IS NOT NULL;

-- A gist's files in order (position 0 first). Each file is a blob of UTF-8
-- text with an optional cached render, both in the files store. Rows go with
-- their item; the item's delete removes the blobs.
CREATE TABLE gist_files (
  item_id     INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  position    INTEGER NOT NULL CHECK (position >= 0),
  filename    TEXT NOT NULL,
  lang        TEXT NOT NULL DEFAULT '',
  mime        TEXT NOT NULL,
  size        INTEGER NOT NULL CHECK (size >= 0),
  sha256      TEXT NOT NULL,
  blob_key    TEXT NOT NULL,
  render_key  TEXT,
  render_size INTEGER NOT NULL DEFAULT 0 CHECK (render_size >= 0),
  PRIMARY KEY (item_id, position),
  UNIQUE (item_id, filename COLLATE NOCASE)
) STRICT, WITHOUT ROWID;
