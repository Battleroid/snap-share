// Package settings stores admin-editable site settings in the database.
//
// Each feature package defines its keys next to the code that uses them:
//
//	var SignupMode = settings.Define("auth.signup_mode", "invite", settings.OneOf("closed", "invite", "open"))
//
// Values are JSON in the settings table and cached in memory.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// Key is a typed setting with a default.
type Key[T any] struct {
	name     string
	def      T
	validate func(T) error
}

// Name returns the setting's storage key.
func (k *Key[T]) Name() string { return k.name }

// Default returns the default value.
func (k *Key[T]) Default() T { return k.def }

// Get returns the current value, or the default when unset or unreadable.
func (k *Key[T]) Get(s *Store) T {
	raw, ok := s.raw(k.name)
	if !ok {
		return k.def
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return k.def
	}
	if k.validate != nil && k.validate(v) != nil {
		return k.def
	}
	return v
}

// Set validates and stores v.
func (k *Key[T]) Set(ctx context.Context, s *Store, v T) error {
	if k.validate != nil {
		if err := k.validate(v); err != nil {
			return fmt.Errorf("%s: %w", k.name, err)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.put(ctx, k.name, b)
}

// SetJSON validates and stores a JSON-encoded value (used by the admin CLI and UI).
func (k *Key[T]) SetJSON(ctx context.Context, s *Store, raw []byte) error {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s: %w", k.name, err)
	}
	return k.Set(ctx, s, v)
}

// Definition is the untyped view of a key for listing.
type Definition interface {
	Name() string
	DefaultJSON() string
	SetJSON(ctx context.Context, s *Store, raw []byte) error
}

// DefaultJSON returns the default as JSON.
func (k *Key[T]) DefaultJSON() string {
	b, _ := json.Marshal(k.def)
	return string(b)
}

var (
	regMu    sync.Mutex
	registry = map[string]Definition{}
)

// Define registers a key. Call it from a package-level var; duplicate names panic.
func Define[T any](name string, def T, validate func(T) error) *Key[T] {
	if validate != nil {
		if err := validate(def); err != nil {
			panic(fmt.Sprintf("settings: default for %s is invalid: %v", name, err))
		}
	}
	k := &Key[T]{name: name, def: def, validate: validate}
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("settings: duplicate key " + name)
	}
	registry[name] = k
	return k
}

// Definitions returns every registered key, sorted by name.
func Definitions() []Definition {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]Definition, 0, len(registry))
	for _, d := range registry {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Lookup finds a registered key by name.
func Lookup(name string) (Definition, bool) {
	regMu.Lock()
	defer regMu.Unlock()
	d, ok := registry[name]
	return d, ok
}

// Store caches the settings table.
type Store struct {
	db    *db.DB
	mu    sync.RWMutex
	cache map[string]json.RawMessage
}

// Open loads all settings into memory.
func Open(ctx context.Context, d *db.DB) (*Store, error) {
	s := &Store{db: d, cache: map[string]json.RawMessage{}}
	return s, s.Reload(ctx)
}

// Reload re-reads the table (e.g. after the admin CLI changed a value).
func (s *Store) Reload(ctx context.Context) error {
	next, err := s.load(ctx)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	s.mu.Lock()
	s.cache = next
	s.mu.Unlock()
	return nil
}

func (s *Store) load(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	next := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		next[k] = json.RawMessage(v)
	}
	return next, rows.Err()
}

func (s *Store) raw(name string) (json.RawMessage, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[name]
	return v, ok
}

func (s *Store) put(ctx context.Context, name string, v []byte) error {
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			name, string(v), db.Millis(time.Now()))
		return err
	})
	if err != nil {
		return fmt.Errorf("save %s: %w", name, err)
	}
	s.mu.Lock()
	s.cache[name] = json.RawMessage(v)
	s.mu.Unlock()
	return nil
}

// Validators.

// OneOf accepts only the listed values.
func OneOf[T comparable](allowed ...T) func(T) error {
	return func(v T) error {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("must be one of %v", allowed)
		}
		return nil
	}
}

// Range accepts values in [lo, hi].
func Range[T int | int64 | float64](lo, hi T) func(T) error {
	return func(v T) error {
		if v < lo || v > hi {
			return fmt.Errorf("must be between %v and %v", lo, hi)
		}
		return nil
	}
}
