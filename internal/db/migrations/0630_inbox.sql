-- In-app notifications (internal/notify): the bell and /notifications.
-- See docs/notifications.md.
--
-- One row per event for the owner: an item's first open, a secret's reveal,
-- a secret burned after wrong passphrases. title and body are rendered when
-- the event happens; body holds the visitor's browser summary and IP, so
-- rows are deleted after stats.retention_days, beyond the newest 200 per
-- user, and when their item or secret is gone. subject_id is the item id
-- (as text) or the secret id. Times are INTEGER unix milliseconds UTC.

CREATE TABLE inbox (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind         TEXT    NOT NULL,
  status       TEXT    NOT NULL CHECK (status IN ('info', 'ok', 'warn', 'down')),
  title        TEXT    NOT NULL,
  body         TEXT    NOT NULL DEFAULT '',
  link         TEXT    NOT NULL DEFAULT '',
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('item', 'secret')),
  subject_id   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  read_at      INTEGER
) STRICT;

-- Listing newest first, and the per-user cap.
CREATE INDEX inbox_user_created ON inbox (user_id, created_at DESC, id DESC);
-- The bell's unread count.
CREATE INDEX inbox_unread ON inbox (user_id) WHERE read_at IS NULL;
-- Retention.
CREATE INDEX inbox_created ON inbox (created_at);
-- Rows about one item or secret (deleted with a purged secret, and swept).
CREATE INDEX inbox_subject ON inbox (subject_type, subject_id);
