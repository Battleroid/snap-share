-- An album's own display settings. view is the layout visitors get by
-- default: 'grid' (tiles) or 'list' (rows). No row is the grid.

CREATE TABLE album_prefs (
  album_id INTEGER PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
  view     TEXT NOT NULL CHECK (view IN ('grid', 'list'))
) STRICT;
