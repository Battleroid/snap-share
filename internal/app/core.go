package app

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/web"
)

func registerCore(a *App) {
	r := a.Router
	r.Handle("GET /static/", a.Assets.Handler(), web.Public())
	r.HandleFunc("GET /healthz", a.healthz, web.Public())
	r.HandleFunc("GET /robots.txt", robots, web.Public())
	r.HandleFunc("GET /favicon.ico", a.favicon, web.Public())
	// The composer's Secret tab derives keys with Argon2id (WebAssembly).
	r.HandleFunc("GET /{$}", a.home, web.Public(), web.WithCSP(web.CSPWasm))
	r.HandleFunc("GET /login", a.loginPage, web.Public())
	r.HandleFunc("GET /_kit", a.kit, web.Admin())
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	if err := a.DB.R.PingContext(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

// Share links are unlisted; nothing here should be indexed.
func robots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "User-agent: *\nDisallow: /\n")
}

func (a *App) favicon(w http.ResponseWriter, r *http.Request) {
	u, err := a.Assets.URL("img/favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, u, http.StatusMovedPermanently) // #nosec G710 -- fixed asset path
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	// The home page is for browsers: an API key gets the signed-out page,
	// never the composer's list of recent items.
	if p := auth.FromContext(r.Context()); p == nil || p.ViaKey() {
		a.Render.Page(w, r, http.StatusOK, "auth/login", web.Page{Title: "Sign in"})
		return
	}
	if a.Home != nil { // items track: the composer
		a.Home(w, r)
		return
	}
	a.Render.Page(w, r, http.StatusOK, "core/home", web.Page{Title: "New", Nav: "home"})
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	next := web.SafeNext(r.URL.Query().Get("next"))
	if auth.FromContext(r.Context()) != nil {
		http.Redirect(w, r, next, http.StatusSeeOther) // #nosec G710 -- next is web.SafeNext (relative path only)
		return
	}
	a.Render.Page(w, r, http.StatusOK, "auth/login", web.Page{Title: "Sign in", Data: map[string]any{"Next": next}})
}

func (a *App) kit(w http.ResponseWriter, r *http.Request) {
	a.Render.Page(w, r, http.StatusOK, "core/kit", web.Page{Title: "Components", Nav: "admin"})
}
