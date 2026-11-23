package items

import (
	"context"
	"net/http"

	"github.com/Battleroid/snap-share/internal/auth"
)

// MediaState says where an item's preview (the thumbnail or video poster)
// stands. It is derived, never stored: a thumbnail on the item means ready,
// otherwise the item's media job decides (SetMediaJobs). Only the owner's
// pages show it.
type MediaState string

// Media states.
const (
	// MediaNone: no preview, and none is being made (not an image or video,
	// no media job, or a finished job that made nothing).
	MediaNone MediaState = "none"
	// MediaPending: the item's media job is queued or running.
	MediaPending MediaState = "pending"
	// MediaFailed: the media job failed for good and left no preview.
	MediaFailed MediaState = "failed"
	// MediaReady: the item has a thumbnail or poster.
	MediaReady MediaState = "ready"
)

// MediaJobStates looks up the media jobs of the items with ids and returns
// the pending and failed ones. An id with no entry has no active or failed
// job. The mediajobs package provides it.
type MediaJobStates func(ctx context.Context, ids []int64) (map[int64]MediaState, error)

// SetMediaJobs installs the media job lookup. Call it before serving; without
// one every item without a thumbnail is MediaNone.
func (s *Service) SetMediaJobs(f MediaJobStates) { s.hooks.mediaJobs = f }

// previewable reports whether it can get a thumbnail or poster at all, so
// the job lookup is skipped for pastes, links and audio.
func previewable(it *Item) bool {
	if it.Kind != KindFile || it.BlobKey == "" {
		return false
	}
	t := TypeOf(it)
	return t == "image" || t == "video"
}

// MediaStates derives the media state of each item of list in one lookup,
// keyed by item id. A lookup error is logged and leaves those items at
// MediaNone, so a page still renders.
func (s *Service) MediaStates(ctx context.Context, list []*Item) map[int64]MediaState {
	out := make(map[int64]MediaState, len(list))
	var ask []int64
	for _, it := range list {
		switch {
		case it.ThumbKey != "":
			out[it.ID] = MediaReady
		case previewable(it) && s.hooks.mediaJobs != nil:
			ask = append(ask, it.ID)
		default:
			out[it.ID] = MediaNone
		}
	}
	if len(ask) == 0 {
		return out
	}
	active, err := s.hooks.mediaJobs(ctx, ask)
	if err != nil {
		s.log.Warn("media state", "err", err)
	}
	for _, id := range ask {
		if st, ok := active[id]; ok {
			out[id] = st
		} else {
			out[id] = MediaNone
		}
	}
	return out
}

// MediaState is MediaStates for one item.
func (s *Service) MediaState(ctx context.Context, it *Item) MediaState {
	return s.MediaStates(ctx, []*Item{it})[it.ID]
}

// ShowsMediaState reports whether p gets the owner's processing marker on
// it: the owner's browser session. The marker polls a session-only route, so
// a key (even the owner's) is treated like a visitor.
func ShowsMediaState(p *auth.Principal, it *Item) bool {
	return p != nil && !p.ViaKey() && p.UserID == it.OwnerID
}

// OwnerSession reports whether r is the owner's browser session for it.
func OwnerSession(r *http.Request, it *Item) bool {
	return ShowsMediaState(auth.FromContext(r.Context()), it)
}

// Marker is the state to draw as a processing marker, or "" for the normal
// thumbnail or icon: pending and failed only.
func Marker(st MediaState) string {
	if st == MediaPending || st == MediaFailed {
		return string(st)
	}
	return ""
}
