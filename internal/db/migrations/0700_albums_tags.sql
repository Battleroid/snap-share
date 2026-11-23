-- Albums and tags (dashboard).
-- An album is an item of kind 'album'; album_items lists its members in order.
-- Removing either the album or a member removes the row; the member item itself
-- is never changed by being in an album.

CREATE TABLE album_items (
  album_id INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  item_id  INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  added_at INTEGER NOT NULL,
  PRIMARY KEY (album_id, item_id)
) STRICT;
CREATE INDEX album_items_order ON album_items (album_id, position);
CREATE INDEX album_items_item ON album_items (item_id);

-- Tags are per owner; names are unique per owner, ignoring case.
CREATE TABLE tags (
  id         INTEGER PRIMARY KEY,
  owner_id   INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name       TEXT NOT NULL COLLATE NOCASE,
  created_at INTEGER NOT NULL,
  UNIQUE (owner_id, name)
) STRICT;

CREATE TABLE item_tags (
  item_id INTEGER NOT NULL REFERENCES items (id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
  PRIMARY KEY (item_id, tag_id)
) STRICT;
CREATE INDEX item_tags_tag ON item_tags (tag_id);

-- Search covers tags too: items_fts gains a tags column (space-separated
-- names), kept current from Go in the transaction that changes an item's tags.
-- FTS5 tables cannot add columns, so the index is copied into a new table.
CREATE VIRTUAL TABLE items_fts_v2 USING fts5 (title, filename, body, target_url, tags, tokenize = 'trigram');
INSERT INTO items_fts_v2 (rowid, title, filename, body, target_url, tags)
  SELECT rowid, title, filename, body, target_url, '' FROM items_fts;
DROP TABLE items_fts;
ALTER TABLE items_fts_v2 RENAME TO items_fts;
