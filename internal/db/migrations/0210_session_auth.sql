-- When the person behind a session last proved who they are. auth_at is the
-- sign-in (every sign-in starts a new session); sensitive account changes
-- without the current password need it to be recent. sso_at is the last
-- sign-in with SSO; with an SSO role mapping, admin rights on the session
-- last a day from it. Unix milliseconds UTC; 0 / NULL for sessions from
-- before this migration.
ALTER TABLE sessions ADD COLUMN auth_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN sso_at INTEGER;
