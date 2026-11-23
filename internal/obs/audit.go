package obs

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Battleroid/snap-share/internal/db"
)

// AuditVersion is the audit line schema version (Line.V). It changes only
// when a field is removed or changes meaning; new fields may be added.
const AuditVersion = 1

// Line is one audit entry as the stream emits it: one JSON object per line.
// Empty actor, ip and target fields are null.
type Line struct {
	V         int            `json:"v"`
	ID        int64          `json:"id"` // audit_log row id, increasing; lets a shipper drop duplicates
	TS        string         `json:"ts"` // RFC 3339 UTC with milliseconds
	Action    string         `json:"action"`
	ActorUser *int64         `json:"actor_user"`
	ActorKey  *int64         `json:"actor_key"`
	IP        *string        `json:"ip"`
	Target    *string        `json:"target"`
	Details   map[string]any `json:"details"`
}

// AuditSink receives audit lines.
type AuditSink interface {
	WriteAudit(Line) error
}

// JSONLines writes each line as a JSON object and a newline, in one Write.
type JSONLines struct{ W io.Writer }

// WriteAudit implements AuditSink.
func (j JSONLines) WriteAudit(l Line) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = j.W.Write(append(b, '\n'))
	return err
}

// LogRecords writes each line as a slog record "audit" with log=audit and
// the Line fields as attributes. The handler should not filter by level
// (audit lines are not subject to SNAP_LOG_LEVEL).
type LogRecords struct{ H slog.Handler }

// WriteAudit implements AuditSink.
func (s LogRecords) WriteAudit(l Line) error {
	r := slog.NewRecord(time.Time{}, slog.LevelInfo, "audit", 0)
	r.AddAttrs(
		slog.String("log", "audit"),
		slog.Int("v", l.V),
		slog.Int64("id", l.ID),
		slog.String("ts", l.TS),
		slog.String("action", l.Action),
		slog.Any("actor_user", nullable(l.ActorUser)),
		slog.Any("actor_key", nullable(l.ActorKey)),
		slog.Any("ip", nullable(l.IP)),
		slog.Any("target", nullable(l.Target)),
		slog.Any("details", l.Details),
	)
	return s.H.Handle(context.Background(), r)
}

func nullable[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

const redacted = "[redacted]"

// sensitiveKeys are detail keys whose values are never emitted.
var sensitiveKeys = []string{"token", "secret", "password", "passphrase", "cookie", "authorization", "credential", "api_key", "recovery", "code", "otp"}

// Redact applies the never-log rules to a line: secret ids in the target,
// values under sensitive keys, email addresses, and query strings and
// fragments of URLs. The audit_log table keeps the full entry for
// Admin › Audit.
func Redact(l Line) Line {
	if l.Target != nil {
		t := *l.Target
		if kind, _, ok := strings.Cut(t, ":"); ok && kind == "secret" {
			t = "secret:" + redacted
		}
		l.Target = &t
	}
	if l.Details != nil {
		l.Details, _ = redactValue("", l.Details).(map[string]any)
	}
	return l
}

func redactValue(key string, v any) any {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			if _, isBool := v.(bool); isBool {
				return v // e.g. "password_set": true says nothing secret
			}
			return redacted
		}
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for kk, vv := range x {
			out[kk] = redactValue(kk, vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = redactValue(key, vv)
		}
		return out
	case string:
		return redactString(x)
	}
	return v
}

func redactString(s string) string {
	if strings.Contains(s, "@") && !strings.ContainsAny(s, " <>") {
		if _, err := mail.ParseAddress(s); err == nil {
			return "[email]"
		}
	}
	if strings.Contains(s, "://") && (strings.ContainsAny(s, "?#") || strings.Contains(s, "/s/")) {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			u.RawQuery, u.Fragment, u.RawFragment, u.User = "", "", "", nil
			if strings.HasPrefix(u.Path, "/s/") {
				u.Path, u.RawPath = "/s/"+redacted, "" // a secret link
			}
			return u.String()
		}
		return redacted
	}
	return s
}

// Stream emits committed audit_log rows to a sink. It reads them back from
// the database (ids above its cursor), so an entry is emitted only after
// its transaction commits and never for one that rolled back, and entries
// written by the admin CLI (another process) are emitted too. An entry is
// emitted as it was inserted; later edits to the row are not.
type Stream struct {
	r    *sql.DB
	sink AuditSink
	log  *slog.Logger

	mu      sync.Mutex
	cursor  int64
	failing bool
}

// batch is the most rows one query reads.
const batch = 500

// NewStream starts a stream at the newest existing row: older entries are
// not emitted again.
func NewStream(ctx context.Context, d *db.DB, sink AuditSink, log *slog.Logger) (*Stream, error) {
	s := &Stream{r: d.R, sink: sink, log: log}
	if err := d.R.QueryRowContext(ctx, `SELECT coalesce(max(id), 0) FROM audit_log`).Scan(&s.cursor); err != nil {
		return nil, err
	}
	return s, nil
}

// Poll emits every row committed since the last poll and returns how many.
func (s *Stream) Poll(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for {
		lines, err := s.read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.fail("read", err)
			}
			return total, err
		}
		for _, l := range lines {
			if err := s.sink.WriteAudit(Redact(l)); err != nil {
				s.fail("write", err)
				return total, err
			}
			s.failing = false
			s.cursor = l.ID
			total++
		}
		if len(lines) < batch {
			return total, nil
		}
	}
}

// fail logs the first error of a run of failures. Call with s.mu held.
func (s *Stream) fail(op string, err error) {
	if !s.failing {
		s.log.Error("audit stream: "+op+" failed; will retry", "err", err)
		s.failing = true
	}
}

// read scans one batch and closes the rows before anything is written.
func (s *Stream) read(ctx context.Context) ([]Line, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, at, actor_user_id, actor_key_id, ip, action, target, details
		FROM audit_log WHERE id > ? ORDER BY id LIMIT ?`, s.cursor, batch)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Line
	for rows.Next() {
		var (
			l          Line
			at         int64
			user, key  sql.NullInt64
			ip, target sql.NullString
			details    string
		)
		if err := rows.Scan(&l.ID, &at, &user, &key, &ip, &l.Action, &target, &details); err != nil {
			return nil, err
		}
		l.V = AuditVersion
		l.TS = db.Time(at).Format("2006-01-02T15:04:05.000Z07:00")
		if user.Valid {
			l.ActorUser = &user.Int64
		}
		if key.Valid {
			l.ActorKey = &key.Int64
		}
		if ip.Valid && ip.String != "" {
			l.IP = &ip.String
		}
		if target.Valid && target.String != "" {
			l.Target = &target.String
		}
		l.Details = map[string]any{}
		if err := json.Unmarshal([]byte(details), &l.Details); err != nil || l.Details == nil {
			l.Details = map[string]any{}
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Run polls every interval until ctx ends, then once more so entries made
// during shutdown are not lost.
func (s *Stream) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, _ = s.Poll(fctx) // Poll logs failures
			return
		case <-t.C:
			_, _ = s.Poll(ctx)
		}
	}
}
