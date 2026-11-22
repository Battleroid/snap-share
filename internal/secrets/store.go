package secrets

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Battleroid/snap-share/internal/blob"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/e2e"
	"github.com/Battleroid/snap-share/internal/stats"
)

// States.
const (
	stateUploading = "uploading"
	stateReady     = "ready"
	stateBurned    = "burned"
	stateExpired   = "expired"
	stateRevoked   = "revoked"
)

// Burn reasons, shown to link holders on the tombstone.
const (
	reasonViewed   = "viewed"
	reasonFailures = "failed attempts"
	reasonRevoked  = "revoked"
	reasonRemoved  = "removed by an admin" // Revoke with ownerID 0 (moderation)
	reasonExpired  = "expired"
)

// metaPart is the ledger part number of the sealed metadata file.
const metaPart = -1

// row is one secrets row.
type row struct {
	ID             string
	OwnerID        sql.NullInt64
	Kind, Mode     string
	State          string
	Salt           []byte
	KDF            sql.NullString
	HProbe, HAuth  []byte
	Commit         []byte
	CTSize         int64
	ChunkCount     int64
	ChunksReceived int64
	ViewsLeft      int
	MaxViews       int
	Failures       int
	MaxFailures    int
	ExpiresAt      int64
	Note           string
	Notify         bool
	OpenedAt       sql.NullInt64
	BurnedAt       sql.NullInt64
	BurnReason     sql.NullString
	UploadToken    []byte
	CreatedAt      int64
	UpdatedAt      int64
	HasPassphrase  bool
}

const rowCols = `id, owner_id, kind, mode, state, salt, kdf, h_probe, h_auth, commit_tag,
  ct_size, chunk_count, chunks_received, views_left, max_views, failures, max_failures,
  expires_at, note, notify, opened_at, burned_at, burn_reason, upload_token_hash, created_at, updated_at, has_passphrase`

type scanner interface{ Scan(dest ...any) error }

func scanRow(sc scanner) (*row, error) {
	var r row
	var notify, hasPass int64
	err := sc.Scan(&r.ID, &r.OwnerID, &r.Kind, &r.Mode, &r.State, &r.Salt, &r.KDF, &r.HProbe, &r.HAuth, &r.Commit,
		&r.CTSize, &r.ChunkCount, &r.ChunksReceived, &r.ViewsLeft, &r.MaxViews, &r.Failures, &r.MaxFailures,
		&r.ExpiresAt, &r.Note, &notify, &r.OpenedAt, &r.BurnedAt, &r.BurnReason, &r.UploadToken, &r.CreatedAt, &r.UpdatedAt, &hasPass)
	if err != nil {
		return nil, err
	}
	r.Notify = notify != 0
	r.HasPassphrase = hasPass != 0
	return &r, nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// load returns the row for id, or errNotFound.
func load(ctx context.Context, q querier, id string) (*row, error) {
	r, err := scanRow(q.QueryRowContext(ctx, `SELECT `+rowCols+` FROM secrets WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: load: %w", err)
	}
	return r, nil
}

// effectiveState is the state as of now: a ready secret past its expiry is
// expired even before the sweeper has noticed.
func (r *row) effectiveState(now time.Time) string {
	if r.State == stateReady && r.ExpiresAt <= db.Millis(now) {
		return stateExpired
	}
	return r.State
}

// plainSize is the size of the plaintext stream (padded, for text).
func (r *row) plainSize() int64 { return r.CTSize - r.ChunkCount*e2e.TagSize }

func (r *row) kdf() (*e2e.KDFParams, error) {
	if !r.KDF.Valid {
		return nil, nil
	}
	var p e2e.KDFParams
	if err := json.Unmarshal([]byte(r.KDF.String), &p); err != nil {
		return nil, fmt.Errorf("secrets: kdf column: %w", err)
	}
	return &p, nil
}

// probeOK compares SHA-256(probe) with the stored hash in constant time. A
// row without a hash (not finalized) never matches.
func (r *row) probeOK(probe [32]byte) bool { return hashEqual(r.HProbe, probe[:]) }

func (r *row) authOK(k [32]byte) bool { return hashEqual(r.HAuth, k[:]) }

func (r *row) tokenOK(token [32]byte) bool { return hashEqual(r.UploadToken, token[:]) }

// hashEqual reports whether stored == SHA-256(secret), in constant time.
func hashEqual(stored, secret []byte) bool {
	h := sha256.Sum256(secret)
	return len(stored) == len(h) && subtle.ConstantTimeCompare(stored, h[:]) == 1
}

// expectedChunkSize is the ciphertext size of chunk n.
func (r *row) expectedChunkSize(n int64) int64 {
	if n < r.ChunkCount-1 {
		return fullChunk
	}
	return r.CTSize - (r.ChunkCount-1)*fullChunk
}

// ---- blob ledger ----

type part struct {
	Part int64
	Key  string
	Size int64
	Sum  []byte
}

func (s *Service) parts(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, id string, from int64) ([]part, error) {
	rows, err := q.QueryContext(ctx, `SELECT part, blob_key, size, sha256 FROM secret_blobs
WHERE secret_id = ? AND part >= ? ORDER BY part`, id, from)
	if err != nil {
		return nil, fmt.Errorf("secrets: parts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []part
	for rows.Next() {
		var p part
		if err := rows.Scan(&p.Part, &p.Key, &p.Size, &p.Sum); err != nil {
			return nil, fmt.Errorf("secrets: parts: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secrets: parts: %w", err)
	}
	return out, nil
}

func insertPart(ctx context.Context, tx *sql.Tx, id string, p part) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO secret_blobs (secret_id, part, blob_key, size, sha256) VALUES (?, ?, ?, ?, ?)`,
		id, p.Part, p.Key, p.Size, p.Sum)
	if err != nil {
		return fmt.Errorf("secrets: insert part: %w", err)
	}
	return nil
}

// writeBlob stores b as a new blob and returns its ledger entry (Part unset).
func (s *Service) writeBlob(ctx context.Context, b []byte) (part, error) {
	w, err := s.d.Blobs.Create(ctx)
	if err != nil {
		return part{}, err
	}
	if _, err := w.Write(b); err != nil {
		_ = w.Abort()
		return part{}, err
	}
	key, err := w.Commit()
	if err != nil {
		return part{}, err
	}
	sum := sha256.Sum256(b)
	return part{Key: key, Size: int64(len(b)), Sum: sum[:]}, nil
}

// readPart reads a whole ledger entry, checking its size and hash.
func (s *Service) readPart(ctx context.Context, p part) ([]byte, error) {
	f, err := s.d.Blobs.Open(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if f.Size() != p.Size {
		return nil, fmt.Errorf("secrets: blob size %d, ledger says %d", f.Size(), p.Size)
	}
	b := make([]byte, p.Size)
	if _, err := io.ReadFull(f, b); err != nil {
		return nil, fmt.Errorf("secrets: read blob: %w", err)
	}
	sum := sha256.Sum256(b)
	if subtle.ConstantTimeCompare(sum[:], p.Sum) != 1 {
		return nil, errors.New("secrets: blob does not match its ledger hash")
	}
	return b, nil
}

// Which files purgeBlobs removes.
const (
	purgeAll  = iota // metadata and content
	purgeMeta        // metadata only (content kept for a live ticket)
)

// purgeBlobs deletes a secret's files from the blob store and then their
// ledger rows. Rows whose file could not be deleted stay for the sweeper to
// retry. Once no content file is left, the secret's quota charge is
// released. Callers must have made sure no live ticket needs the content
// (except revoke, which kills tickets first).
func (s *Service) purgeBlobs(ctx context.Context, id string, which int) error {
	ctx = context.WithoutCancel(ctx)
	ps, err := s.parts(ctx, s.d.DB.R, id, metaPart)
	if err != nil {
		return err
	}
	var gone []int64
	var errs []error
	for _, p := range ps {
		if which == purgeMeta && p.Part != metaPart {
			continue
		}
		if err := s.d.Blobs.Delete(ctx, p.Key); err != nil && !errors.Is(err, blob.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		gone = append(gone, p.Part)
	}
	if len(gone) > 0 || which == purgeAll {
		err := s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
			for _, p := range gone {
				if _, err := tx.ExecContext(ctx, `DELETE FROM secret_blobs WHERE secret_id = ? AND part = ?`, id, p); err != nil {
					return err
				}
			}
			if which != purgeAll {
				return nil
			}
			var left int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM secret_blobs WHERE secret_id = ? AND part >= 0`, id).Scan(&left); err != nil {
				return err
			}
			if left > 0 {
				return nil
			}
			return releaseTx(ctx, tx, id)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("secrets: purge ledger: %w", err))
		}
	}
	if len(errs) > 0 {
		err := errors.Join(errs...)
		s.d.Log.Error("secrets: purge failed; the sweeper will retry", "err", err)
		return err
	}
	return nil
}

// liveTickets counts unexpired tickets for a secret.
func liveTickets(ctx context.Context, q querier, id string, now time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM secret_tickets WHERE secret_id = ? AND expires_at > ?`,
		id, db.Millis(now)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("secrets: tickets: %w", err)
	}
	return n, nil
}

// deleteRow removes a secret row and its events in one transaction, giving
// back any quota it still holds. Ledger rows (no foreign key) are left for
// purgeBlobs or the sweeper.
func deleteRow(ctx context.Context, tx *sql.Tx, id string) error {
	if err := releaseTx(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM secrets WHERE id = ?`, id); err != nil {
		return fmt.Errorf("secrets: delete: %w", err)
	}
	return stats.DeleteSecretEvents(ctx, tx, id)
}

// Revoke burns a secret now: its state becomes revoked, live tickets stop
// working and the files are deleted. ownerID 0 burns any secret (admin);
// otherwise only the owner's. It reports false when there is no such secret
// (or it is not the owner's). Revoking a tombstone kills its tickets and
// files and keeps its state.
func (s *Service) Revoke(ctx context.Context, id string, ownerID int64) (state string, ok bool, err error) {
	now := db.Millis(s.now())
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		r, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if ownerID != 0 && (!r.OwnerID.Valid || r.OwnerID.Int64 != ownerID) {
			return errNotFound
		}
		state = r.State
		if r.State == stateUploading || r.State == stateReady {
			state = stateRevoked
			reason := reasonRevoked
			if ownerID == 0 {
				reason = reasonRemoved
			}
			if _, err := tx.ExecContext(ctx, `UPDATE secrets SET state = 'revoked', burned_at = ?, burn_reason = ?,
  upload_token_hash = NULL, upload_ip = NULL, `+clearVerifier+`, updated_at = ? WHERE id = ?`, now, reason, now, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM secret_tickets WHERE secret_id = ?`, id)
		return err
	})
	if errors.Is(err, errNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("secrets: revoke: %w", err)
	}
	_ = s.purgeBlobs(ctx, id, purgeAll)
	s.truncateWAL(ctx)
	return state, true, nil
}

// clearVerifier is the SET clause that drops the passphrase verifier (salt,
// KDF parameters, SHA-256(K_auth), K_commit) when a secret leaves ready.
// With the link, those would let anyone holding a copy of the database
// test passphrases offline long after the content is gone. h_probe stays,
// so link holders still see the tombstone; has_passphrase keeps what the
// owner's page shows.
const clearVerifier = `salt = NULL, kdf = NULL, h_auth = NULL, commit_tag = NULL`

// truncateWAL checkpoints and truncates the WAL after rows or verifiers were
// deleted, so their old frames don't stay in snap.db-wal. Best effort: a
// busy database only delays it to the next burn or sweep.
func (s *Service) truncateWAL(ctx context.Context) {
	if err := s.d.DB.TruncateWAL(context.WithoutCancel(ctx)); err != nil {
		s.d.Log.Debug("secrets: wal truncate", "err", err)
	}
}
