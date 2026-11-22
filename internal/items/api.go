package items

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/web"
)

// ownItem loads {id} and checks the principal owns it (404 otherwise).
func (s *Service) ownItem(r *http.Request) (*auth.Principal, *Item, error) {
	p := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || p == nil {
		return nil, nil, ErrNotFound
	}
	it, err := s.Get(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if it.OwnerID != p.UserID {
		return nil, nil, ErrNotFound
	}
	return p, it, nil
}

func intParam(r *http.Request, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

// apiList handles GET /api/v1/items?limit=&offset= and the list filters
// (filter.go): own items, newest first by default.
func (s *Service) apiList(w http.ResponseWriter, r *http.Request) error {
	p := auth.FromContext(r.Context())
	limit := intParam(r, "limit", 50, 1, 200)
	offset := intParam(r, "offset", 0, 0, 1<<30)
	f, err := ParseFilter(r.URL.Query())
	if err != nil {
		return err
	}
	list, total, err := s.Find(r.Context(), p.UserID, f, limit, offset)
	if err != nil {
		return err
	}
	prefs, _ := LoadPrefs(r.Context(), s.d.DB, p.UserID)
	out := make([]ItemJSON, 0, len(list))
	for _, it := range list {
		out = append(out, s.JSON(it, prefs.DirectLink))
	}
	web.JSON(w, http.StatusOK, map[string]any{"items": out, "total": total, "limit": limit, "offset": offset})
	return nil
}

func (s *Service) apiGet(w http.ResponseWriter, r *http.Request) error {
	p, it, err := s.ownItem(r)
	if err != nil {
		return err
	}
	prefs, _ := LoadPrefs(r.Context(), s.d.DB, p.UserID)
	web.JSON(w, http.StatusOK, s.JSON(it, prefs.DirectLink))
	return nil
}

// patchReq fields are optional; a JSON null password clears it.
type patchReq struct {
	Title        *string         `json:"title"`
	Expires      *string         `json:"expires"`
	ExpiryAction *string         `json:"expiry_action"`
	Visibility   *string         `json:"visibility"`
	Password     json.RawMessage `json:"password"`
	Slug         *string         `json:"slug"`
}

// apiPatch handles PATCH /api/v1/items/{id}: title, expiry, visibility,
// password set/clear and slug rename.
func (s *Service) apiPatch(w http.ResponseWriter, r *http.Request) error {
	p, it, err := s.ownItem(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	var req patchReq
	if err := web.DecodeJSON(w, r, 16<<10, &req); err != nil {
		return err
	}
	type set struct {
		col string
		val any
	}
	var sets []set
	if req.Title != nil {
		t := cleanText(*req.Title, 200)
		it.Title = t
		sets = append(sets, set{"title", t})
	}
	if req.Expires != nil {
		if !validExpirySpec(*req.Expires) {
			return errExpiry
		}
		exp, err := parseExpiry(*req.Expires, s.now(), MaxExpiryDays.Get(s.d.Settings))
		if err != nil {
			return err
		}
		sets = append(sets, set{"expires_at", nullMillis(exp)})
	}
	if req.ExpiryAction != nil {
		if *req.ExpiryAction != ExpireDelete && *req.ExpiryAction != ExpirePrivate {
			return web.Errorf(http.StatusBadRequest, "Expiry action must be delete or private.")
		}
		sets = append(sets, set{"expiry_action", *req.ExpiryAction})
	}
	if req.Visibility != nil {
		if *req.Visibility != string(Unlisted) && *req.Visibility != string(Private) {
			return web.Errorf(http.StatusBadRequest, "Visibility must be unlisted or private.")
		}
		sets = append(sets, set{"visibility", *req.Visibility})
	}
	if len(req.Password) > 0 {
		var pw *string
		if err := json.Unmarshal(req.Password, &pw); err != nil {
			return web.Errorf(http.StatusBadRequest, "Password must be a string or null.")
		}
		if pw == nil || *pw == "" {
			sets = append(sets, set{"password_hash", nil})
		} else {
			if len(*pw) > 256 {
				return web.Errorf(http.StatusBadRequest, "The password is too long.")
			}
			h, err := s.passwords.Hash(*pw)
			if err != nil {
				return err
			}
			sets = append(sets, set{"password_hash", h})
		}
	}
	rename := ""
	if req.Slug != nil && *req.Slug != it.Slug {
		rename = *req.Slug
		if !ValidSlug(rename) {
			return errSlugInvalid
		}
		if IsReserved(rename, s.d.ExtraReserved...) {
			return errSlugReserved
		}
	}
	err = s.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		if rename != "" {
			var other int64
			err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE slug = ?`, rename).Scan(&other)
			if err == nil && other != it.ID {
				return errSlugTaken
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			sets = append(sets, set{"slug", rename})
		}
		if len(sets) == 0 {
			return nil
		}
		q := "UPDATE items SET "
		args := make([]any, 0, len(sets)+2)
		for _, st := range sets {
			q += st.col + " = ?, " // #nosec G202 -- column names are constants above; values are bound
			args = append(args, st.val)
		}
		q += "updated_at = ? WHERE id = ? AND owner_id = ?"
		args = append(args, db.Millis(s.now()), it.ID, p.UserID)
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
		return reindexTx(ctx, tx, it)
	})
	if err != nil {
		return err
	}
	fresh, err := s.Get(ctx, it.ID)
	if err != nil {
		return err
	}
	prefs, _ := LoadPrefs(ctx, s.d.DB, p.UserID)
	web.JSON(w, http.StatusOK, s.JSON(fresh, prefs.DirectLink))
	return nil
}

func (s *Service) apiDelete(w http.ResponseWriter, r *http.Request) error {
	_, it, err := s.ownItem(r)
	if err != nil {
		return err
	}
	if err := s.Delete(r.Context(), it.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Service) dashboardDelete(w http.ResponseWriter, r *http.Request) error {
	_, it, err := s.ownItem(r)
	if err != nil {
		return err
	}
	page := 1
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if r.ParseForm() == nil {
		if n, err := strconv.Atoi(r.PostForm.Get("page")); err == nil && n > 1 {
			page = n
		}
	}
	if err := s.Delete(r.Context(), it.ID); err != nil {
		return err
	}
	http.Redirect(w, r, "/dashboard?page="+strconv.Itoa(page), http.StatusSeeOther)
	return nil
}

// Deletion by token (the ShareX DeletionURL /x#<token>).

type tokenReq struct {
	Token string `json:"token"`
}

func (s *Service) tokenItem(w http.ResponseWriter, r *http.Request) (*Item, error) {
	ip := clientip.From(r.Context())
	if !clientip.In(ip, s.d.Cfg.RateLimitExempt) {
		if ok, _ := s.deleteLimit.Allow("delete:" + ip.String()); !ok {
			return nil, web.Errorf(http.StatusTooManyRequests, "Too many requests. Try again in a minute.")
		}
	}
	var req tokenReq
	if err := web.DecodeJSON(w, r, 4<<10, &req); err != nil {
		return nil, err
	}
	if len(req.Token) < 32 || len(req.Token) > 128 {
		return nil, web.Errorf(http.StatusNotFound, "This deletion link is not valid.")
	}
	it, err := getItem(r.Context(), s.d.DB.R, `deletion_token_hash = ?`, tokenHash(req.Token))
	if errors.Is(err, ErrNotFound) || (err == nil && it.Expired(s.now())) {
		return nil, web.Errorf(http.StatusNotFound, "This deletion link is not valid.")
	}
	return it, err
}

func (s *Service) tokenLookup(w http.ResponseWriter, r *http.Request) error {
	it, err := s.tokenItem(w, r)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{
		"name": it.Name(), "kind": it.Kind, "type": typeLabel(it), "size": it.Size, "size_text": web.HumanBytes(it.Size),
		"created_at": it.CreatedAt, "url": s.PublicURL(it.Slug),
	})
	return nil
}

func (s *Service) tokenDelete(w http.ResponseWriter, r *http.Request) error {
	it, err := s.tokenItem(w, r)
	if err != nil {
		return err
	}
	if err := s.Delete(r.Context(), it.ID); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true})
	return nil
}

// Sweep applies expiry actions to items whose expiry has passed: delete
// removes the item and its blobs, private hides it and clears the expiry.
// Access checks use expires_at on every request; this only cleans up.
func (s *Service) Sweep(ctx context.Context) error {
	for {
		now := db.Millis(s.now())
		type due struct {
			id     int64
			action string
		}
		list, err := queryAll(ctx, s.d.DB.R, `SELECT id, expiry_action FROM items WHERE expires_at IS NOT NULL AND expires_at <= ? LIMIT 200`,
			func(rows *sql.Rows) (due, error) {
				var d due
				err := rows.Scan(&d.id, &d.action)
				return d, err
			}, now)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return nil
		}
		for _, d := range list {
			if d.action == ExpirePrivate {
				if _, err := s.d.DB.W.ExecContext(ctx, `UPDATE items SET visibility = 'private', expires_at = NULL, updated_at = ?
					WHERE id = ? AND expires_at <= ?`, now, d.id, now); err != nil {
					return err
				}
				continue
			}
			if err := s.Delete(ctx, d.id); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		if len(list) < 200 || ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(10 * time.Millisecond) // let other writers in between batches
	}
}
