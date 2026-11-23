-- Notification events × channels (internal/notify). See docs/notifications.md.
--
-- notify_event_prefs holds the choices a user has saved: one row per event
-- and channel. An event and channel without a row take the default in
-- internal/notify (events.go). channel is 'inbox' (the bell), 'email' or
-- 'ntfy'; event names are checked by the code, so new events need no
-- migration.
CREATE TABLE notify_event_prefs (
  user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  event   TEXT    NOT NULL,
  channel TEXT    NOT NULL CHECK (channel IN ('inbox', 'email', 'ntfy')),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  PRIMARY KEY (user_id, event, channel)
) STRICT, WITHOUT ROWID;

-- The old item-open setting becomes the item-open rows for email and ntfy:
-- 'first' turns on first opens, 'every' first and every open, 'none'
-- neither. Reveals and burns keep their defaults, which are today's.
INSERT INTO notify_event_prefs (user_id, event, channel, enabled)
SELECT p.user_id, e.event, c.channel,
       CASE e.event WHEN 'item.first_open' THEN p.default_event IN ('first', 'every') ELSE p.default_event = 'every' END
FROM notify_prefs p
CROSS JOIN (SELECT 'item.first_open' AS event UNION ALL SELECT 'item.open') e
CROSS JOIN (SELECT 'email' AS channel UNION ALL SELECT 'ntfy') c;

-- Events that fire once per subject remember it here:
--   'item.expiring' per item: value is the expires_at it was sent for (a new
--                   expiry sends again);
--   'quota' per user (subject_id ''): value is the level last seen, 0, 80
--                   or 95, so each crossing fires once.
CREATE TABLE notify_marks (
  user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  mark       TEXT    NOT NULL,
  subject_id TEXT    NOT NULL,
  value      INTEGER NOT NULL,
  PRIMARY KEY (user_id, mark, subject_id)
) STRICT, WITHOUT ROWID;

-- The inbox gets account rows (storage quota, an expired item that was
-- deleted): subject_type 'account' with the user id as subject_id. SQLite
-- can't change a CHECK in place, so the table is rebuilt; ids and the
-- AUTOINCREMENT counter carry over, so no id is reused.
CREATE TABLE inbox_new (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind         TEXT    NOT NULL,
  status       TEXT    NOT NULL CHECK (status IN ('info', 'ok', 'warn', 'down')),
  title        TEXT    NOT NULL,
  body         TEXT    NOT NULL DEFAULT '',
  link         TEXT    NOT NULL DEFAULT '',
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('item', 'secret', 'account')),
  subject_id   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  read_at      INTEGER
) STRICT;

INSERT INTO inbox_new (id, user_id, kind, status, title, body, link, subject_type, subject_id, created_at, read_at)
SELECT id, user_id, kind, status, title, body, link, subject_type, subject_id, created_at, read_at FROM inbox;

UPDATE sqlite_sequence SET seq = max(seq, coalesce((SELECT seq FROM sqlite_sequence WHERE name = 'inbox'), 0))
WHERE name = 'inbox_new';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'inbox_new', seq FROM sqlite_sequence WHERE name = 'inbox'
  AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'inbox_new');

DROP TABLE inbox;
ALTER TABLE inbox_new RENAME TO inbox;

CREATE INDEX inbox_user_created ON inbox (user_id, created_at DESC, id DESC);
CREATE INDEX inbox_unread ON inbox (user_id) WHERE read_at IS NULL;
CREATE INDEX inbox_created ON inbox (created_at);
CREATE INDEX inbox_subject ON inbox (subject_type, subject_id);
