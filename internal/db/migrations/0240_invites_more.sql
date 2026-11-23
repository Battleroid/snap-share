-- Invites: a label, the link kept for copying again, and the email result
-- (internal/accounts/invites.go, docs/admin.md).
--
-- token_ct is the raw token sealed under SNAP_SECRET_KEY (auth.Sealer, the
-- invite id bound in), so an admin can copy the link again while the invite
-- is active. It is set to NULL once the invite is used up, revoked or
-- expired. Invites made before this migration have none.
ALTER TABLE invites ADD COLUMN label TEXT CHECK (label IS NULL OR length(label) BETWEEN 1 AND 80);
ALTER TABLE invites ADD COLUMN token_ct BLOB;
ALTER TABLE invites ADD COLUMN mail_sent_at INTEGER; -- the invite email went out
ALTER TABLE invites ADD COLUMN mail_error TEXT;      -- or why it did not
