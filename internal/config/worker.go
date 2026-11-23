package config

import (
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// Worker is the configuration of `snap-server media-worker`. It holds no
// secret key and no public URL: the worker reads files and runs ffmpeg,
// nothing else.
type Worker struct {
	// Network is "unix" or "tcp", Address the socket path or host:port.
	Network, Address string
	Token            string // bearer token every request must carry, >= 32 characters
	FilesDir         string // the items' files directory, read-only; default /files
	Parallel         int    // concurrent ffmpeg children; default 2

	LogLevel  slog.Level
	LogFormat string // json (default) or text
}

// WorkerTokenMin is the shortest accepted SNAP_MEDIA_WORKER_TOKEN.
const WorkerTokenMin = 32

// LoadWorker reads the media worker's configuration using getenv.
func LoadWorker(getenv func(string) string) (*Worker, error) {
	l := loader{getenv: getenv}
	c := &Worker{
		FilesDir: l.str("FILES_DIR", "/files"),
		Parallel: l.integer("MEDIA_WORKER_PARALLEL", 2),
		Token:    l.secret("MEDIA_WORKER_TOKEN"),
	}
	c.Network, c.Address = l.workerListen()
	if len(c.Token) < WorkerTokenMin {
		l.fail("SNAP_MEDIA_WORKER_TOKEN must be at least %d characters (e.g. openssl rand -hex 32)", WorkerTokenMin)
	}
	if c.Parallel < 1 || c.Parallel > 64 {
		l.fail("SNAP_MEDIA_WORKER_PARALLEL must be between 1 and 64")
	}
	c.LogLevel, c.LogFormat = l.logging()
	if err := l.err(); err != nil {
		return nil, err
	}
	return c, nil
}

// workerListen is SNAP_MEDIA_WORKER_LISTEN: unix:///absolute/path or
// host:port. Required.
func (l *loader) workerListen() (network, address string) {
	v := l.raw("MEDIA_WORKER_LISTEN")
	switch {
	case v == "":
		l.fail("SNAP_MEDIA_WORKER_LISTEN is required (unix:///run/snap-media/media.sock or host:port)")
	case strings.HasPrefix(v, "unix://"):
		p := strings.TrimPrefix(v, "unix://")
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.HasSuffix(v, "/") {
			l.fail("SNAP_MEDIA_WORKER_LISTEN: unix:// needs an absolute socket path, e.g. unix:///run/snap-media/media.sock")
			return "", ""
		}
		return "unix", p
	default:
		if _, port, err := net.SplitHostPort(v); err != nil || port == "" || strings.Contains(v, "/") {
			l.fail("SNAP_MEDIA_WORKER_LISTEN must be unix:///path or host:port")
			return "", ""
		}
		return "tcp", v
	}
	return "", ""
}

// mediaWorker reads SNAP_MEDIA_WORKER and, when it is set, the token the
// worker requires. The token is ignored without a worker, so one env file
// can serve both containers.
func (l *loader) mediaWorker() (workerURL, token string) {
	workerURL = l.raw("MEDIA_WORKER")
	token = l.secret("MEDIA_WORKER_TOKEN")
	if workerURL == "" {
		return "", ""
	}
	switch {
	case strings.HasPrefix(workerURL, "unix://"):
		p := strings.TrimPrefix(workerURL, "unix://")
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			l.fail("SNAP_MEDIA_WORKER: unix:// needs an absolute socket path, e.g. unix:///run/snap-media/media.sock")
		}
	case strings.HasPrefix(workerURL, "http://"):
		u, err := url.Parse(workerURL)
		if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			l.fail("SNAP_MEDIA_WORKER must be unix:///path or http://host:port")
		}
	default:
		l.fail("SNAP_MEDIA_WORKER must be unix:///path or http://host:port")
	}
	if len(token) < WorkerTokenMin {
		l.fail("SNAP_MEDIA_WORKER_TOKEN must be at least %d characters with SNAP_MEDIA_WORKER", WorkerTokenMin)
	}
	return workerURL, token
}
