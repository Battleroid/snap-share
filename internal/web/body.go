package web

import (
	"mime"
	"net/http"
	"time"
)

// Request bodies. Unless a route declares LargeBody, its request body is
// limited to MaxFormBody bytes that must arrive within BodyReadTimeout, and
// multipart/form-data is refused. Without this, any handler that calls
// r.FormValue would parse an anonymous multipart upload of any size and
// spool its file parts to os.TempDir, before the handler (and its rate
// limits) ran. Routes that take uploads declare LargeBody and set their own
// limits; they stream to the blob store instead of parsing forms.

// Default body limits.
const (
	MaxFormBody     = 1 << 20          // bytes
	BodyReadTimeout = 30 * time.Second // to read the whole body, from the start of the handler
)

// LargeBody exempts a route from the default body limits: uploads, chunks
// and pastes, which read large bodies and limit them themselves.
func LargeBody() RouteOpt { return func(r *Route) { r.LargeBody = true } }

// limitBody applies the default body rules to r. It returns false when it
// has answered the request.
func (rt *Router) limitBody(route Route, w http.ResponseWriter, r *http.Request) bool {
	if route.LargeBody || r.Body == nil || r.Body == http.NoBody {
		return true
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt == "multipart/form-data" {
		rt.errors.Error(w, r, http.StatusUnsupportedMediaType, "This doesn't take file uploads. Send a form (application/x-www-form-urlencoded) or JSON.")
		return false
	}
	if r.ContentLength > MaxFormBody {
		rt.errors.Error(w, r, http.StatusRequestEntityTooLarge, "Request body is too large.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxFormBody)
	// A deadline for reading the body. The server lifts it once the body has
	// been read (it then watches the connection for a client that went away),
	// so it never cuts a long response short. Errors mean the connection
	// can't take deadlines (tests); the size limit still applies.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(rt.bodyTimeout))
	return true
}
