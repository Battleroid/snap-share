package obs

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/web"
)

// Logs are the process's log outputs, built from the configuration.
type Logs struct {
	App   *slog.Logger // app log: SNAP_LOG_FORMAT, SNAP_LOG_LEVEL, stdout or SNAP_LOG_FILE
	Audit AuditSink    // SNAP_AUDIT_LOG; nil when off

	files []*File
}

// NewHandler is the app log handler: JSON or text at level, with the
// request id added from the context.
func NewHandler(format string, level slog.Leveler, w io.Writer) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return web.LogHandler(h)
}

// AuditHandler writes audit records as JSON whatever the log format and
// level, without slog's own time (Line.TS is the entry's time).
func AuditHandler(w io.Writer) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
}

// Setup opens the outputs cfg names. stdout is the process's standard
// output; the app log and a stdout audit stream share it line by line.
func Setup(cfg *config.Config, stdout io.Writer) (*Logs, error) {
	l := &Logs{}
	out := &lockedWriter{w: stdout}
	deny := []string{cfg.FilesDir, cfg.SecretsDir}

	var appw io.Writer = out
	if cfg.LogFile != "" {
		f, err := OpenFile(cfg.DataDir, cfg.LogFile, deny...)
		if err != nil {
			return nil, fmt.Errorf("SNAP_LOG_FILE: %w", err)
		}
		l.files = append(l.files, f)
		appw = f
	}
	l.App = slog.New(NewHandler(cfg.LogFormat, cfg.LogLevel, appw))

	switch cfg.AuditLog {
	case "", "off":
	case "stdout":
		l.Audit = LogRecords{H: AuditHandler(out)}
	default:
		f, err := OpenFile(cfg.DataDir, cfg.AuditLog, deny...)
		if err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("SNAP_AUDIT_LOG: %w", err)
		}
		if cfg.LogFile != "" && len(l.files) == 1 && sameFile(l.files[0], f) {
			_ = f.Close()
			_ = l.Close()
			return nil, errors.New("SNAP_AUDIT_LOG and SNAP_LOG_FILE must be different files")
		}
		l.files = append(l.files, f)
		l.Audit = JSONLines{W: f}
	}
	return l, nil
}

func sameFile(a, b *File) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	sa, err1 := a.f.Stat()
	sb, err2 := b.f.Stat()
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// Reopen reopens the log files (after logrotate, on SIGHUP).
func (l *Logs) Reopen() error {
	var errs []error
	for _, f := range l.files {
		errs = append(errs, f.Reopen())
	}
	return errors.Join(errs...)
}

// Close closes the log files.
func (l *Logs) Close() error {
	var errs []error
	for _, f := range l.files {
		errs = append(errs, f.Close())
	}
	return errors.Join(errs...)
}
