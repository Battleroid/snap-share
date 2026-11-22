package keys

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/web"
)

// KeyJSON is a key in API responses. It never carries the secret.
type KeyJSON struct {
	ID           int64        `json:"id"`
	Name         string       `json:"name"`
	PublicID     string       `json:"public_id"`
	Prefix       string       `json:"prefix"`
	Scopes       []auth.Scope `json:"scopes"`
	AllowedCIDRs []string     `json:"allowed_cidrs"`
	Constraints  Constraints  `json:"constraints"`
	Status       string       `json:"status"`
	ExpiresAt    *time.Time   `json:"expires_at"`
	LastUsedAt   *time.Time   `json:"last_used_at"`
	LastUsedIP   string       `json:"last_used_ip,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	RevokedAt    *time.Time   `json:"revoked_at"`
}

func (s *Service) keyJSON(k *Key) KeyJSON {
	nets := make([]string, len(k.CIDRs))
	for i, p := range k.CIDRs {
		nets[i] = p.String()
	}
	scopes := k.Scopes
	if scopes == nil {
		scopes = []auth.Scope{}
	}
	return KeyJSON{
		ID: k.ID, Name: k.Name, PublicID: k.PublicID, Prefix: k.Prefix(), Scopes: scopes, AllowedCIDRs: nets,
		Constraints: k.Constraints, Status: k.Status(s.now()), ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
		LastUsedIP: k.LastUsedIP, CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
	}
}

// apiList handles GET /api/v1/keys.
func (s *Service) apiList(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	list, err := s.store.List(r.Context(), p.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := make([]KeyJSON, 0, len(list))
	for _, k := range list {
		out = append(out, s.keyJSON(k))
	}
	web.JSON(w, http.StatusOK, map[string]any{"keys": out})
}

// apiCreate handles POST /api/v1/keys. The token is in the response only.
func (s *Service) apiCreate(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	var in Input
	if err := web.DecodeJSON(w, r, 16<<10, &in); err != nil {
		var ae *web.APIError
		if errors.As(err, &ae) {
			s.d.Errors.Error(w, r, ae.Status, ae.Msg)
			return
		}
		s.serverError(w, r, err)
		return
	}
	nk, ferrs := Validate(in, p.Role, s.now())
	if ferrs != nil {
		web.JSON(w, http.StatusBadRequest, map[string]any{"error": ferrs.Error(), "fields": ferrs})
		return
	}
	token, k, err := s.store.Create(r.Context(), p.UserID, nk, accounts.Actor(r))
	if errors.Is(err, ErrTooManyKeys) {
		s.d.Errors.Error(w, r, http.StatusConflict, tooManyMsg())
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	web.JSON(w, http.StatusCreated, map[string]any{"key": s.keyJSON(k), "token": token})
}

func tooManyMsg() string {
	return fmt.Sprintf("You have %d active keys. Revoke one first.", MaxActiveKeys)
}

// apiRevoke handles DELETE /api/v1/keys/{id}.
func (s *Service) apiRevoke(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	switch err := s.store.Revoke(r.Context(), p.UserID, id, accounts.Actor(r)); {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		s.d.Errors.NotFound(w, r)
	default:
		s.serverError(w, r, err)
	}
}

// MeJSON is the response of GET /api/v1/me.
type MeJSON struct {
	ID            int64     `json:"id"`
	Username      string    `json:"username"`
	Role          auth.Role `json:"role"`
	EmailVerified bool      `json:"email_verified"`
	Quota         QuotaJSON `json:"quota"`
	Key           *MeKey    `json:"key,omitempty"`
}

// QuotaJSON is the caller's storage use and limits; 0 quota means unlimited.
type QuotaJSON struct {
	QuotaBytes     int64 `json:"quota_bytes"`
	UsedBytes      int64 `json:"used_bytes"`
	ReservedBytes  int64 `json:"reserved_bytes"`
	MaxUploadBytes int64 `json:"max_upload_bytes"`
}

// MeKey describes the key a request was made with.
type MeKey struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Prefix      string       `json:"prefix"`
	Scopes      []auth.Scope `json:"scopes"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	Constraints Constraints  `json:"constraints"`
}

// me handles GET /api/v1/me (scope account:read).
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := auth.FromContext(ctx)
	out := MeJSON{ID: p.UserID, Username: p.Username, Role: p.Role, EmailVerified: p.EmailVerified}
	var quota, maxUp sql.NullInt64
	err := s.d.DB.R.QueryRowContext(ctx, `SELECT quota_bytes, max_upload_bytes, used_bytes, reserved_bytes FROM users WHERE id = ?`, p.UserID).
		Scan(&quota, &maxUp, &out.Quota.UsedBytes, &out.Quota.ReservedBytes)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// The same rules as items.userLimits: the user's values override the site's,
	// and the key's cap lowers the file limit.
	out.Quota.QuotaBytes = items.DefaultQuotaBytes.Get(s.d.Settings)
	if quota.Valid {
		out.Quota.QuotaBytes = quota.Int64
	}
	out.Quota.MaxUploadBytes = items.MaxUploadBytes.Get(s.d.Settings)
	if maxUp.Valid && maxUp.Int64 > 0 && maxUp.Int64 < out.Quota.MaxUploadBytes {
		out.Quota.MaxUploadBytes = maxUp.Int64
	}
	out.Quota.MaxUploadBytes = p.Constraints().UploadLimit(out.Quota.MaxUploadBytes)
	if p.ViaKey() {
		k, err := s.store.Get(ctx, p.UserID, p.KeyID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		out.Key = &MeKey{ID: k.ID, Name: k.Name, Prefix: k.Prefix(), Scopes: p.Scopes, ExpiresAt: k.ExpiresAt, Constraints: k.Constraints}
	}
	web.JSON(w, http.StatusOK, out)
}
