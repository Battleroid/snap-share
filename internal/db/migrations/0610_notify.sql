-- Open notifications (internal/notify). See docs/notifications.md.
--
-- Everything is off by default: a user without a notify_prefs row gets no
-- messages, and a new row has both channels off and default_event 'none'.
-- Times are INTEGER unix milliseconds UTC.

-- Per-user channels and the default for item opens.
--   ntfy_token_ct: the ntfy access token, AES-256-GCM sealed with a key
--                  derived from SNAP_SECRET_KEY (nonce || ciphertext, the
--                  user id as associated data); NULL when no token is set.
CREATE TABLE notify_prefs (
  user_id         INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  email_enabled   INTEGER NOT NULL DEFAULT 0 CHECK (email_enabled IN (0, 1)),
  ntfy_enabled    INTEGER NOT NULL DEFAULT 0 CHECK (ntfy_enabled IN (0, 1)),
  ntfy_server_url TEXT    NOT NULL DEFAULT 'https://ntfy.sh',
  ntfy_topic      TEXT    NOT NULL DEFAULT '',
  ntfy_token_ct   BLOB,
  default_event   TEXT    NOT NULL DEFAULT 'none' CHECK (default_event IN ('none', 'first', 'every')),
  updated_at      INTEGER NOT NULL
) STRICT;

-- Per-item overrides of default_event. user_id is the owner when the row
-- was written; a row whose owner no longer matches is ignored and swept.
-- Secrets keep their own secrets.notify flag as their per-secret override,
-- so only 'item' rows are written today.
CREATE TABLE notify_overrides (
  subject_type TEXT    NOT NULL CHECK (subject_type IN ('item', 'secret')),
  subject_id   TEXT    NOT NULL,
  user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  event        TEXT    NOT NULL CHECK (event IN ('none', 'first', 'every')),
  updated_at   INTEGER NOT NULL,
  PRIMARY KEY (subject_type, subject_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX notify_overrides_user ON notify_overrides (user_id);

-- Coalescing for "every open": after a message about a subject, no other
-- message about it goes out before `until`; opens in between are counted
-- in `pending` and summarised in one message when the window ends.
CREATE TABLE notify_windows (
  subject_type TEXT    NOT NULL,
  subject_id   TEXT    NOT NULL,
  until        INTEGER NOT NULL,
  pending      INTEGER NOT NULL DEFAULT 0,
  pending_from INTEGER,
  PRIMARY KEY (subject_type, subject_id)
) STRICT, WITHOUT ROWID;

-- Last delivery result per user and channel, shown on the settings page.
-- last_error never holds a token, topic or URL.
CREATE TABLE notify_status (
  user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  channel       TEXT    NOT NULL CHECK (channel IN ('email', 'ntfy')),
  last_ok_at    INTEGER,
  last_error    TEXT    NOT NULL DEFAULT '',
  last_error_at INTEGER,
  PRIMARY KEY (user_id, channel)
) STRICT, WITHOUT ROWID;
