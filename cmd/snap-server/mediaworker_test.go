//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The subcommand serves on its socket, its healthcheck passes while it
// runs and fails once it has stopped on SIGTERM.
func TestMediaWorkerCommand(t *testing.T) {
	dir, err := os.MkdirTemp("", "mwc") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	files := filepath.Join(dir, "files")
	if err := os.Mkdir(files, 0o500); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "run", "media.sock")
	t.Setenv("SNAP_MEDIA_WORKER_LISTEN", "unix://"+sock)
	t.Setenv("SNAP_MEDIA_WORKER_TOKEN", strings.Repeat("k", 32))
	t.Setenv("SNAP_FILES_DIR", files)
	t.Setenv("TMPDIR", dir)

	if err := mediaWorkerHealthcheck(); err == nil {
		t.Fatal("healthcheck passed with no worker")
	}
	done := make(chan error, 1)
	go func() { done <- mediaWorker(nil) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := mediaWorkerHealthcheck()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never became healthy: %v", err)
		}
		select {
		case err := <-done:
			t.Fatalf("worker exited: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if _, err := os.Stat(filepath.Join(files, ".tmp")); err == nil {
		t.Error("the worker wrote to the files directory")
	}
	// The worker's signal handler is installed by now.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("worker: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("worker did not stop on SIGTERM")
	}
	if err := mediaWorkerHealthcheck(); err == nil {
		t.Error("healthcheck passed after the worker stopped")
	}
	if err := mediaWorker([]string{"bogus"}); err == nil {
		t.Error("unknown argument accepted")
	}
}
