-- Visitor events and daily rollups (internal/stats). See docs/stats.md.
--
-- Times are INTEGER unix milliseconds UTC; `day` is the unix ms of 00:00
-- UTC. Item ids are INTEGER, secret ids TEXT. There are no foreign keys:
-- items and secrets live in other migrations, and stats.DeleteItemStats /
-- stats.DeleteSecretEvents remove a subject's rows when it is deleted.
-- Text columns use '' for "none" rather than NULL.

-- Raw item events. Holds IPs and user agents, so rows are deleted after
-- the retention period (stats.Purge); the rollups below stay.
CREATE TABLE IF NOT EXISTS item_events (
  id            INTEGER PRIMARY KEY,
  item_id       INTEGER NOT NULL,
  at            INTEGER NOT NULL,
  kind          TEXT    NOT NULL CHECK (kind IN ('preview', 'view', 'embed', 'download', 'click', 'play')),
  ip            TEXT    NOT NULL DEFAULT '',
  user_agent    TEXT    NOT NULL DEFAULT '',
  ua_summary    TEXT    NOT NULL DEFAULT '',
  bot_name      TEXT    NOT NULL DEFAULT '',
  referrer_host TEXT    NOT NULL DEFAULT '',
  visitor_key   TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX IF NOT EXISTS item_events_item_at ON item_events (item_id, at);
CREATE INDEX IF NOT EXISTS item_events_at ON item_events (at);

-- Per-item, per-day counts, kept for the item's lifetime.
--   uniques:      distinct visitors that day (view, download, click, play)
--   new_visitors: visitors seen for the first time that day; the sum over
--                 all days is the item's lifetime unique visitors
CREATE TABLE IF NOT EXISTS item_stats_daily (
  item_id      INTEGER NOT NULL,
  day          INTEGER NOT NULL,
  views        INTEGER NOT NULL DEFAULT 0,
  uniques      INTEGER NOT NULL DEFAULT 0,
  new_visitors INTEGER NOT NULL DEFAULT 0,
  previews     INTEGER NOT NULL DEFAULT 0,
  embeds       INTEGER NOT NULL DEFAULT 0,
  downloads    INTEGER NOT NULL DEFAULT 0,
  clicks       INTEGER NOT NULL DEFAULT 0,
  plays        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, day)
) STRICT, WITHOUT ROWID;

-- The last day each visitor key was counted for an item; this is what
-- keeps uniques and new_visitors correct across flushes. Rows are purged
-- with the raw events once last_day is past retention; a visitor who
-- returns after that counts as new again.
CREATE TABLE IF NOT EXISTS item_visitors (
  item_id     INTEGER NOT NULL,
  visitor_key TEXT    NOT NULL,
  last_day    INTEGER NOT NULL,
  PRIMARY KEY (item_id, visitor_key)
) STRICT, WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS item_visitors_last_day ON item_visitors (last_day);

-- Raw secret events (owner's timeline). Same retention as item_events.
CREATE TABLE IF NOT EXISTS secret_events (
  id            INTEGER PRIMARY KEY,
  secret_id     TEXT    NOT NULL,
  at            INTEGER NOT NULL,
  kind          TEXT    NOT NULL CHECK (kind IN ('link_opened', 'revealed', 'failed', 'burned')),
  ip            TEXT    NOT NULL DEFAULT '',
  user_agent    TEXT    NOT NULL DEFAULT '',
  ua_summary    TEXT    NOT NULL DEFAULT '',
  bot_name      TEXT    NOT NULL DEFAULT '',
  referrer_host TEXT    NOT NULL DEFAULT '',
  visitor_key   TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX IF NOT EXISTS secret_events_secret_at ON secret_events (secret_id, at);
CREATE INDEX IF NOT EXISTS secret_events_at ON secret_events (at);
