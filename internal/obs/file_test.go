package obs_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/obs"
)

func TestOpenFileRelativeAndReopen(t *testing.T) {
	data := t.TempDir()
	f, err := obs.OpenFile(data, "logs/audit.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write([]byte("one\n")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "logs", "audit.jsonl")
	// logrotate: move the file away, then SIGHUP.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("two\n")); err != nil { // still the old file
		t.Fatal(err)
	}
	if err := f.Reopen(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("three\n")); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(path + ".1")
	cur, _ := os.ReadFile(path)
	if string(old) != "one\ntwo\n" || string(cur) != "three\n" {
		t.Fatalf("rotated %q, current %q", old, cur)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
	}
}

func TestOpenFileRefusals(t *testing.T) {
	data := t.TempDir()
	files := filepath.Join(data, "files")
	outside := t.TempDir()
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"blob root, relative": "files/audit.log",
		"blob root, absolute": filepath.Join(files, "x", "audit.log"),
		"escapes data dir":    "../audit.log",
		"a directory":         "files",
		"empty":               "",
	}
	for name, p := range cases {
		if f, err := obs.OpenFile(data, p, files); err == nil {
			_ = f.Close()
			t.Errorf("%s: %q opened", name, p)
		}
	}
	if runtime.GOOS != "windows" {
		// A symlink planted at the log path can't send writes elsewhere.
		target := filepath.Join(outside, "victim")
		if err := os.WriteFile(target, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(data, "audit.log")); err != nil {
			t.Fatal(err)
		}
		if f, err := obs.OpenFile(data, "audit.log"); err == nil {
			_ = f.Close()
			t.Error("followed a symlink out of the data directory")
		}
	}
	// An absolute path outside the data dir is the operator's choice.
	f, err := obs.OpenFile(data, filepath.Join(outside, "app.log"), files)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestSetupRefusesOneFileForBoth(t *testing.T) {
	data := t.TempDir()
	cfg := &config.Config{DataDir: data, FilesDir: filepath.Join(data, "files"), SecretsDir: filepath.Join(data, "secrets"),
		LogFormat: "json", LogFile: "snap.log", AuditLog: filepath.Join(data, "snap.log")}
	if l, err := obs.Setup(cfg, os.Stdout); err == nil {
		_ = l.Close()
		t.Fatal("same file accepted for the app log and the audit stream")
	}
	cfg.AuditLog = "audit.jsonl"
	l, err := obs.Setup(cfg, os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, ok := l.Audit.(obs.JSONLines); !ok {
		t.Fatalf("audit sink %T", l.Audit)
	}
	l.App.Info("hello")
	if err := l.Reopen(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(data, "snap.log"))
	if !strings.Contains(string(b), `"msg":"hello"`) {
		t.Fatalf("app log %q", b)
	}
}

func TestSetupStdoutAuditIgnoresLevelAndFormat(t *testing.T) {
	data := t.TempDir()
	out := &lines{}
	cfg := &config.Config{DataDir: data, LogFormat: "text", LogLevel: 8 /* error */, AuditLog: "stdout"}
	l, err := obs.Setup(cfg, out)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Audit.WriteAudit(obs.Line{V: 1, ID: 1, Action: "x.y", Details: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	l.App.Info("dropped at error level")
	got := out.all(t) // fails unless every line is JSON
	if len(got) != 1 || got[0]["log"] != "audit" || got[0]["action"] != "x.y" {
		t.Fatalf("stdout: %v", got)
	}
}
