package admin

import (
	"encoding/json"

	"github.com/Battleroid/snap-share/internal/settings"
)

// RoundTrip renders d's default as the editor shows it and reads it back.
// It returns the kind and whether the value came back unchanged.
func RoundTrip(d settings.Definition) (kind string, ok bool, msg string) {
	s := describe(d)
	def := json.RawMessage(d.DefaultJSON())
	v, on := s.display(def)
	if s.Kind == kindBool && on {
		v = "on"
	}
	raw, msg := s.parse(v)
	return string(s.Kind), msg == "" && sameJSON(raw, def), msg
}
