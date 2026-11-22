-- One-time secrets, after the security review (internal/secrets).
--
-- has_passphrase: whether the secret was created with a passphrase. The
-- passphrase verifier (salt, kdf, h_auth, commit_tag) is cleared when a
-- secret leaves 'ready', so the tombstone can't be used to test passphrases
-- offline; this flag keeps what the owner's page shows.
--
-- quota_reserved, quota_used: the ciphertext bytes this secret currently
-- holds against its owner's quota (users.reserved_bytes while uploading,
-- users.used_bytes once ready). Both drop to 0 when the content is deleted.
-- For anonymous secrets they track the live anonymous total
-- (secrets.anonymous_max_total_bytes).
ALTER TABLE secrets ADD COLUMN has_passphrase INTEGER NOT NULL DEFAULT 0 CHECK (has_passphrase IN (0, 1));
ALTER TABLE secrets ADD COLUMN quota_reserved INTEGER NOT NULL DEFAULT 0 CHECK (quota_reserved >= 0);
ALTER TABLE secrets ADD COLUMN quota_used INTEGER NOT NULL DEFAULT 0 CHECK (quota_used >= 0);
UPDATE secrets SET has_passphrase = 1 WHERE kdf IS NOT NULL;

CREATE INDEX secrets_anon_live ON secrets (created_at)
  WHERE owner_id IS NULL AND (quota_reserved > 0 OR quota_used > 0);

-- Per-ticket download budget (GET /api/v1/secrets/{id}/blob).
ALTER TABLE secret_tickets ADD COLUMN requests INTEGER NOT NULL DEFAULT 0 CHECK (requests >= 0);
ALTER TABLE secret_tickets ADD COLUMN bytes_served INTEGER NOT NULL DEFAULT 0 CHECK (bytes_served >= 0);
