//go:build linux

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

var mp4Type = Type{"video/mp4", KindVideo, "mov"}

// hasRun reports whether want appears in args as a contiguous run.
func hasRun(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestArgsHardened(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{Threads: 3})
	args, _, err := r.args(job{
		tool: toolFFmpeg,
		inputs: []input{
			{path: "/data/a.mp4", typ: mp4Type, pre: []string{"-ss", "1.000"}},
			{path: "/data/b.webm", typ: Type{"video/webm", KindVideo, "matroska"}},
		},
		args:   []string{"-frames:v", "1"},
		output: "/data/out.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"-nostdin", "-hide_banner", "-loglevel", "error"},
		{"-filter_threads", "3", "-filter_complex_threads", "3"},
		{"-ss", "1.000", "-f", "mov", "-format_whitelist", "mov", "-protocol_whitelist", "file",
			"-enable_drefs", "0", "-use_absolute_path", "0", "-threads", "3", "-i", "file:/data/a.mp4"},
		{"-f", "matroska", "-format_whitelist", "matroska", "-protocol_whitelist", "file",
			"-threads", "3", "-i", "file:/data/b.webm"},
		{"-y", "file:/data/out.mp4"},
	} {
		if !hasRun(args, want...) {
			t.Errorf("args lack %q:\n%q", want, args)
		}
	}
	if slices.Contains(args, "-enable_drefs") && strings.Count(strings.Join(args, " "), "-enable_drefs") != 1 {
		t.Errorf("mov options leaked onto the matroska input: %q", args)
	}

	probe, _, err := r.args(job{tool: toolFFprobe, inputs: []input{{path: "/data/a.mp4", typ: mp4Type}}})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(probe, "-nostdin") || !hasRun(probe, "-f", "mov", "-format_whitelist", "mov", "-protocol_whitelist", "file") {
		t.Errorf("ffprobe args: %q", probe)
	}

	bad := []struct {
		name string
		j    job
		is   error
	}{
		{"relative input", job{tool: toolFFmpeg, inputs: []input{{path: "a.mp4", typ: mp4Type}}, output: "/o"}, nil},
		{"dotdot input", job{tool: toolFFmpeg, inputs: []input{{path: "/data/../etc/passwd", typ: mp4Type}}, output: "/o"}, nil},
		{"relative output", job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: mp4Type}}, output: "o.mp4"}, nil},
		{"no container", job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: Type{"text/plain", KindText, ""}}}, output: "/o"}, ErrUnsupported},
		{"hls", job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: Type{"video/mp4", KindVideo, "hls"}}}, output: "/o"}, ErrUnsupported},
		{"concat", job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: Type{"video/mp4", KindVideo, "concat"}}}, output: "/o"}, ErrUnsupported},
		{"image2", job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: Type{"image/png", KindImage, "image2"}}}, output: "/o"}, ErrUnsupported},
		{"ffprobe two inputs", job{tool: toolFFprobe, inputs: []input{{path: "/a", typ: mp4Type}, {path: "/b", typ: mp4Type}}}, nil},
	}
	for _, b := range bad {
		_, _, err := r.args(b.j)
		if err == nil || (b.is != nil && !errors.Is(err, b.is)) {
			t.Errorf("%s: err = %v", b.name, err)
		}
	}
}

func TestNewRunnerMissingBinary(t *testing.T) {
	t.Parallel()
	if _, err := NewRunner(Config{FFmpeg: "/nonexistent/ffmpeg"}); err == nil {
		t.Fatal("want error")
	}
}

// TestPlaylistsRefused: HLS and ffconcat files named .mp4 that point at a
// FIFO. Opening the FIFO blocks until the timeout, so a fast failure proves
// ffmpeg never followed the reference.
func TestPlaylistsRefused(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "secret.ts")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	playlists := map[string]string{
		"hls.mp4": "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n" +
			"#EXTINF:1.0,\nfile://" + fifo + "\n#EXTINF:1.0,\nfile:///etc/passwd\n#EXT-X-ENDLIST\n",
		"concat.mp4": "ffconcat version 1.0\nfile " + fifo + "\nfile /etc/passwd\n",
	}
	const timeout = 10 * time.Second
	r := newTestRunner(t, Config{Timeout: timeout})
	info := Info{HasVideo: true, Duration: 20 * time.Second, Width: 160, Height: 120}

	for name, body := range playlists {
		src := filepath.Join(dir, name)
		if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, name+".out")

		typ := sniffPath(t, src)
		if typ.Kind == KindVideo || typ.Kind == KindAudio || typ.Container != "" {
			t.Fatalf("%s sniffed as %+v", name, typ)
		}
		if err := r.Poster(ctx, src, typ, dst); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Poster = %v, want ErrUnsupported", name, err)
		}
		if err := r.Teaser(ctx, src, typ, info, dst); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Teaser = %v, want ErrUnsupported", name, err)
		}
		if _, err := r.Probe(ctx, src, typ); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: Probe = %v, want ErrUnsupported", name, err)
		}

		// Mislabelled on purpose: every allowed demuxer must reject it
		// without opening the FIFO.
		for c := range demuxers {
			forced := Type{"video/mp4", KindVideo, c}
			start := time.Now()
			if _, err := r.Probe(ctx, src, forced); errors.Is(err, ErrTimeout) {
				t.Errorf("%s forced %s: Probe = %v", name, c, err)
			}
			if err := r.Teaser(ctx, src, forced, info, dst); err == nil || errors.Is(err, ErrTimeout) {
				t.Errorf("%s forced %s: Teaser = %v", name, c, err)
			}
			if d := time.Since(start); d > timeout/2 {
				t.Errorf("%s forced %s took %s", name, c, d)
			}
			if _, err := os.Stat(dst); err == nil {
				t.Errorf("%s forced %s left output behind", name, c)
			}
		}
	}

	// Control: unhardened ffmpeg does follow the concat file and blocks on
	// the FIFO, so the trap above is meaningful.
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, lookTool(t, "ffmpeg"), "-nostdin", "-loglevel", "error",
		"-f", "concat", "-safe", "0", "-i", filepath.Join(dir, "concat.mp4"), "-f", "null", "-")
	_ = cmd.Run()
	if cctx.Err() == nil {
		t.Fatal("control: unhardened ffmpeg did not block on the FIFO")
	}
}

func TestTruncatedVideo(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	full := gen(t, "full.mp4", cat(lavfi("testsrc2=s=160x120:r=10:d=2"), "-c:v", "libx264", "-preset", "ultrafast")...)
	b, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	src := putFile(t, "trunc.mp4", b[:200])
	typ := sniffPath(t, src)
	if typ != mp4Type {
		t.Fatalf("sniff = %+v", typ)
	}
	r := newTestRunner(t, Config{Timeout: 20 * time.Second})
	dst := filepath.Join(t.TempDir(), "out")
	if _, err := r.Probe(ctx, src, typ); err == nil {
		// ffprobe may still read the header; Poster must fail either way.
		t.Log("probe of truncated file succeeded")
	}
	if err := r.Poster(ctx, src, typ, dst); err == nil || errors.Is(err, ErrTimeout) {
		t.Errorf("Poster = %v", err)
	}
	if err := r.Teaser(ctx, src, typ, Info{HasVideo: true, Duration: 2 * time.Second}, dst); err == nil || errors.Is(err, ErrTimeout) {
		t.Errorf("Teaser = %v", err)
	}
}

// fakeTool writes a shell script to stand in for ffmpeg/ffprobe. It runs
// with an empty environment, so it uses absolute paths.
func fakeTool(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o700); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	return p
}

// processGone reports whether pid has exited (or is an unreaped zombie).
func processGone(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(b, ')')
	return i < 0 || i+2 >= len(b) || b[i+2] == 'Z' || b[i+2] == 'X'
}

// modes are the two ways a Runner starts a child: through the rlimit
// helper (the default) and directly (Config.Direct).
var modes = []struct {
	name   string
	direct bool
}{{"helper", false}, {"direct", true}}

func TestRunnerTimeoutKillsProcessGroup(t *testing.T) {
	t.Parallel()
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			pidFile := filepath.Join(t.TempDir(), "pid")
			fake := fakeTool(t, fmt.Sprintf("%s 300 &\necho $! > %s\nwait\n", lookTool(t, "sleep"), pidFile))
			r := newTestRunner(t, Config{FFmpeg: fake, Timeout: 500 * time.Millisecond, Direct: m.direct})
			start := time.Now()
			err := r.run(ctx, job{tool: toolFFmpeg, output: "-"})
			// Our own SIGKILL is a timeout, not an out-of-memory kill.
			if !errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %s", d)
			}
			b, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for !processGone(pid) {
				if time.Now().After(deadline) {
					_ = unix.Kill(pid, unix.SIGKILL)
					t.Fatalf("grandchild %d survived the timeout", pid)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func TestRunnerParentCancel(t *testing.T) {
	t.Parallel()
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			fake := fakeTool(t, lookTool(t, "sleep")+" 300\n")
			r := newTestRunner(t, Config{FFmpeg: fake, Direct: m.direct})
			ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
			defer cancel()
			err := r.run(ctx, job{tool: toolFFmpeg, output: "-"})
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, want the parent's context error", err)
			}
		})
	}
}

// TestRunnerExternalKill: a SIGKILL that the Runner did not send (the cgroup
// OOM killer, in a container) is ErrTooLarge, which mediajobs treats as
// permanent.
func TestRunnerExternalKill(t *testing.T) {
	t.Parallel()
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()
			pidFile := filepath.Join(t.TempDir(), "pid")
			// exec keeps the pid, so the test kills the tool itself.
			fake := fakeTool(t, fmt.Sprintf("echo $$ > %s.tmp\n%s %s.tmp %s\nexec %s 300\n",
				pidFile, lookTool(t, "mv"), pidFile, pidFile, lookTool(t, "sleep")))
			r := newTestRunner(t, Config{FFmpeg: fake, Timeout: 30 * time.Second, Direct: m.direct})
			ctx := testCtx(t)
			done := make(chan error, 1)
			go func() { done <- r.run(ctx, job{tool: toolFFmpeg, output: "-"}) }()
			var pid int
			for deadline := time.Now().Add(10 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("tool did not start")
				}
				if b, err := os.ReadFile(pidFile); err == nil {
					pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				}
			}
			if err := unix.Kill(pid, unix.SIGKILL); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if !errors.Is(err, ErrTooLarge) || errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTooLarge", err)
			}
		})
	}
}

func TestRunnerChildEnvironmentAndLimits(t *testing.T) {
	t.Setenv("SNAP_MEDIA_CANARY", "leaked")
	script := `/usr/bin/env
echo "as=$(ulimit -v)"
echo "cpu=$(ulimit -t)"
echo "nofile=$(ulimit -n)"
echo "core=$(ulimit -c)"
echo "fsize=$(ulimit -f)"
echo "ashard=$(ulimit -H -v)"
echo "cpuhard=$(ulimit -H -t)"
echo "pid=$$"
read -r _ _ _ _ pgrp _ < /proc/$$/stat
echo "pgrp=$pgrp"
while read -r k v; do [ "$k" = "NoNewPrivs:" ] && echo "nnp=$v"; done < /proc/self/status
echo "cwd=$(pwd)"
`
	r := newTestRunner(t, Config{FFprobe: fakeTool(t, script), MemLimit: 512 << 20, CPULimit: 7 * time.Second})
	var out bytes.Buffer
	err := r.run(testCtx(t), job{
		tool:   toolFFprobe,
		inputs: []input{{path: "/dev/null", typ: mp4Type}},
		stdout: &out, stdoutMax: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	var env []string
	for line := range strings.Lines(out.String()) {
		line = strings.TrimSpace(line)
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "as", "ashard", "cpu", "cpuhard", "fsize", "nofile", "core", "pid", "pgrp", "nnp", "cwd":
			got[k] = v
		default:
			env = append(env, line)
		}
	}
	t.Logf("child environment: %q", env)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		// /bin/sh itself sets PWD (and may set SHLVL); nothing may come
		// from the parent or from the helper's own environment (helperEnv).
		if k != "PWD" && k != "SHLVL" && k != "" {
			t.Errorf("unexpected variable in child: %q", kv)
		}
	}
	want := map[string]string{
		"as": strconv.Itoa(512 << 10), "ashard": strconv.Itoa(512 << 10), "cpu": "7", "cpuhard": "8",
		"fsize": strconv.Itoa(64 << 20 / 512), "nofile": strconv.Itoa(childNoFile),
		"core": "0", "nnp": "1", "pgrp": got["pid"], "cwd": "/",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestRunnerCPULimit(t *testing.T) {
	t.Parallel()
	fake := fakeTool(t, "while :; do :; done\n")
	r := newTestRunner(t, Config{FFmpeg: fake, CPULimit: time.Second, Timeout: 30 * time.Second})
	start := time.Now()
	err := r.run(testCtx(t), job{tool: toolFFmpeg, output: "-"})
	if !errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "CPU") {
		t.Fatalf("err = %v, want CPU limit", err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("took %s; the wall-clock timeout fired, not RLIMIT_CPU", d)
	}
}

func TestRunnerMemLimit(t *testing.T) {
	t.Parallel()
	ctx := testCtx(t)
	src := gen(t, "clip.mp4", cat(lavfi("testsrc2=s=320x240:r=10:d=2"), "-c:v", "libx264", "-preset", "ultrafast")...)
	dst := filepath.Join(t.TempDir(), "poster.jpg")
	tiny := newTestRunner(t, Config{MemLimit: 64 << 20})
	err := tiny.Poster(ctx, src, mp4Type, dst)
	if err == nil || errors.Is(err, ErrTimeout) {
		t.Fatalf("Poster under 64 MiB RLIMIT_AS = %v, want a clean failure", err)
	}
	t.Logf("64 MiB: %v", err)
	if err := newTestRunner(t, Config{}).Poster(ctx, src, mp4Type, dst); err != nil {
		t.Fatalf("control with default limit: %v", err)
	}

	// Enough to start but not to decode an 8192x8192 frame. ffmpeg 8.1
	// sometimes spins after a failed frame allocation instead of exiting;
	// the CPU and wall-clock limits must end it either way.
	big := gen(t, "big.mp4", cat(lavfi("color=c=red:s=8192x8192:r=10:d=0.1"), "-frames:v", "1", "-c:v", "libx264", "-preset", "ultrafast")...)
	r := newTestRunner(t, Config{MemLimit: 200 << 20, CPULimit: 3 * time.Second, Timeout: 5 * time.Second})
	start := time.Now()
	err = r.Poster(ctx, big, mp4Type, dst)
	if err == nil {
		t.Fatal("Poster of an 8192x8192 frame under 200 MiB succeeded")
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("took %s", d)
	}
	t.Logf("200 MiB, 8192x8192: %v", err)
}

func TestRunnerStdoutLimit(t *testing.T) {
	t.Parallel()
	fake := fakeTool(t, fmt.Sprintf("%s | %s -c 1000000\n", lookTool(t, "yes"), lookTool(t, "head")))
	r := newTestRunner(t, Config{FFprobe: fake})
	var out bytes.Buffer
	err := r.run(testCtx(t), job{tool: toolFFprobe, inputs: []input{{path: "/dev/null", typ: mp4Type}}, stdout: &out, stdoutMax: 1000})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if out.Len() > 1000 {
		t.Fatalf("buffered %d bytes", out.Len())
	}
}

func TestRunnerMaxParallel(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "lock")
	fake := fakeTool(t, fmt.Sprintf("%s %s || exit 3\n%s 0.2\n%s %s\n",
		lookTool(t, "mkdir"), lock, lookTool(t, "sleep"), lookTool(t, "rmdir"), lock))
	r := newTestRunner(t, Config{FFmpeg: fake, MaxParallel: 1})
	ctx := testCtx(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- r.run(ctx, job{tool: toolFFmpeg, output: "-"}) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("overlapping run: %v", err)
		}
	}
}

// The helper starts with the environment that keeps its runtime idle while
// RLIMIT_AS is in force (see helperEnv), and with nothing else.
func TestRunnerHelperEnvironment(t *testing.T) {
	t.Setenv("SNAP_MEDIA_CANARY", "leaked")
	r := newTestRunner(t, Config{})
	cmd, err := r.command(t.Context(), r.ffprobeBin, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GOGC=off", "GOMAXPROCS=1", "GODEBUG=updatemaxprocs=0,asyncpreemptoff=1"}
	if !slices.Equal(cmd.Env, want) {
		t.Fatalf("helper environment %q, want %q", cmd.Env, want)
	}
}
