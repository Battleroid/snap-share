-- snap: foreign_keys=off
-- Rebuild items (SQLite's 12-step procedure; the runner turns foreign keys
-- off around this transaction and runs foreign_key_check before it commits):
--
--   id             AUTOINCREMENT, so a deleted item's id is never given to a
--                  new item (stats rows, open notifications, signed URLs and
--                  unlock cookies are keyed by id).
--   nonce          a per-item random value bound into signed raw URLs.
--   created_by_key the API key that created the item (NULL: a browser
--                  session); a key without items:read may edit only these.
--   render_size    bytes of the cached render (render_key), charged to the
--                  owner's quota with size. 0 with a render_key means not yet
--                  measured (renders made before this migration); the hourly
--                  reconcile fills it in.
--
-- album_items and item_tags reference items (id); with foreign keys off the
-- DROP does not cascade, and after the RENAME their references point at the
-- new table. items_fts is keyed by rowid = items.id, which is kept.

CREATE TABLE items_new (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
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
  updated_at          INTEGER NOT NULL,
  previewed_at        INTEGER,
  nonce               TEXT NOT NULL DEFAULT (lower(hex(randomblob(16)))),
  created_by_key      INTEGER,
  render_size         INTEGER NOT NULL DEFAULT 0 CHECK (render_size >= 0)
) STRICT;

INSERT INTO items_new (id, owner_id, kind, slug, title, filename, mime, size, sha256, width, height, duration_ms,
  blob_key, thumb_key, preview_key, render_key, target_url, visibility, password_hash, expires_at, expiry_action,
  views, downloads, first_opened_at, last_opened_at, opened_count, deletion_token_hash, meta, created_at, updated_at,
  previewed_at)
SELECT id, owner_id, kind, slug, title, filename, mime, size, sha256, width, height, duration_ms,
  blob_key, thumb_key, preview_key, render_key, target_url, visibility, password_hash, expires_at, expiry_action,
  views, downloads, first_opened_at, last_opened_at, opened_count, deletion_token_hash, meta, created_at, updated_at,
  previewed_at
FROM items;

DROP TABLE items;
ALTER TABLE items_new RENAME TO items;

CREATE INDEX items_owner_created ON items (owner_id, created_at DESC, id DESC);
CREATE INDEX items_expires ON items (expires_at) WHERE expires_at IS NOT NULL;

-- Slugs freed by a delete, an expiry or a rename. For the cooldown
-- (items.SlugCooldown) only previous_owner may take the slug again, so
-- links already shared do not lead to someone else's content. Rows past the
-- cooldown are swept. previous_owner has no foreign key: a deleted
-- account's slugs stay held for the cooldown too.
CREATE TABLE released_slugs (
  slug           TEXT NOT NULL COLLATE NOCASE PRIMARY KEY,
  previous_owner INTEGER NOT NULL,
  released_at    INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX released_slugs_at ON released_slugs (released_at);
