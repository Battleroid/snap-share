package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workerBase() map[string]string {
	return map[string]string{
		"SNAP_MEDIA_WORKER_LISTEN": "unix:///run/snap-media/media.sock",
		"SNAP_MEDIA_WORKER_TOKEN":  strings.Repeat("t", 32),
	}
}

func TestLoadWorkerDefaults(t *testing.T) {
	c, err := LoadWorker(env(workerBase()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Network != "unix" || c.Address != "/run/snap-media/media.sock" {
		t.Errorf("listen %s %s", c.Network, c.Address)
	}
	if c.FilesDir != "/files" || c.Parallel != 2 || c.LogLevel != slog.LevelInfo || c.LogFormat != "json" {
		t.Errorf("defaults: %+v", c)
	}
}

func TestLoadWorker(t *testing.T) {
	tok := strings.Repeat("f", 40)
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadWorker(env(map[string]string{
		"SNAP_MEDIA_WORKER_LISTEN":     "127.0.0.1:8091",
		"SNAP_MEDIA_WORKER_TOKEN_FILE": file,
		"SNAP_FILES_DIR":               "/srv/files",
		"SNAP_MEDIA_WORKER_PARALLEL":   "4",
		"SNAP_LOG_LEVEL":               "debug",
		"SNAP_LOG_FORMAT":              "text",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Network != "tcp" || c.Address != "127.0.0.1:8091" || c.Token != tok || c.FilesDir != "/srv/files" ||
		c.Parallel != 4 || c.LogLevel != slog.LevelDebug || c.LogFormat != "text" {
		t.Errorf("got %+v", c)
	}
}

func TestLoadWorkerErrors(t *testing.T) {
	tok := strings.Repeat("s", 31) + "X"
	cases := map[string]map[string]string{
		"no listen":      {"SNAP_MEDIA_WORKER_TOKEN": strings.Repeat("t", 32)},
		"no token":       {"SNAP_MEDIA_WORKER_LISTEN": "unix:///s.sock"},
		"short token":    merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_TOKEN": tok[:31]}),
		"relative unix":  merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_LISTEN": "unix://run/s.sock"}),
		"unclean unix":   merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_LISTEN": "unix:///run/../s.sock"}),
		"url":            merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_LISTEN": "http://127.0.0.1:8091"}),
		"no port":        merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_LISTEN": "127.0.0.1"}),
		"parallel":       merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_PARALLEL": "0"}),
		"log level":      merge(workerBase(), map[string]string{"SNAP_LOG_LEVEL": "loud"}),
		"token and file": merge(workerBase(), map[string]string{"SNAP_MEDIA_WORKER_TOKEN_FILE": "/x"}),
	}
	for name, m := range cases {
		_, err := LoadWorker(env(m))
		if err == nil {
			t.Errorf("%s: want error", name)
			continue
		}
		// A configuration error never repeats the token.
		if v := m["SNAP_MEDIA_WORKER_TOKEN"]; v != "" && strings.Contains(err.Error(), v) {
			t.Errorf("%s: error contains the token: %v", name, err)
		}
	}
}

// The worker reads nothing it does not need: no secret key, no public URL,
// no data directory.
func TestLoadWorkerReadsNoServerSecrets(t *testing.T) {
	seen := map[string]bool{}
	_, _ = LoadWorker(func(k string) string { seen[k] = true; return "" })
	for _, k := range []string{"SNAP_SECRET_KEY", "SNAP_SECRET_KEY_FILE", "SNAP_PUBLIC_URL", "SNAP_DATA_DIR", "SNAP_SECRETS_DIR"} {
		if seen[k] {
			t.Errorf("LoadWorker reads %s", k)
		}
	}
}
