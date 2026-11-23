//go:build linux

package media

import (
	"bytes"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestDirectCommand: Direct execs the binary itself, with no helper, and
// keeps the process group, Pdeathsig, the empty environment and Dir "/".
func TestDirectCommand(t *testing.T) {
	t.Setenv("SNAP_MEDIA_CANARY", "leaked")
	r := newTestRunner(t, Config{Direct: true})
	if r.self != "" {
		t.Errorf("Direct runner resolved a re-exec target %q", r.self)
	}
	args := []string{"-hide_banner", "-i", "file:/a"}
	cmd, err := r.command(t.Context(), r.ffprobeBin, args)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != r.ffprobeBin || len(cmd.Args) == 0 || cmd.Args[0] != r.ffprobeBin || !slices.Equal(cmd.Args[1:], args) {
		t.Errorf("Direct command %q %q, want %q %q", cmd.Path, cmd.Args, r.ffprobeBin, args)
	}
	if slices.Contains(cmd.Args, execSentinel) {
		t.Errorf("Direct command goes through the helper: %q", cmd.Args)
	}
	if cmd.Env == nil || len(cmd.Env) != 0 {
		t.Errorf("Direct environment %q, want empty (not nil, which inherits)", cmd.Env)
	}
	if cmd.Dir != "/" {
		t.Errorf("Dir = %q", cmd.Dir)
	}
	if a := cmd.SysProcAttr; a == nil || !a.Setpgid || a.Pdeathsig != syscall.SIGKILL {
		t.Errorf("SysProcAttr = %+v, want Setpgid and Pdeathsig SIGKILL", a)
	}
	if cmd.Cancel == nil || cmd.WaitDelay == 0 {
		t.Error("no group kill on cancel")
	}
}

// TestDirectArgsMatchHelper: both modes build the arguments with the same
// Runner.args, and both hand ffmpeg exactly those arguments.
func TestDirectArgsMatchHelper(t *testing.T) {
	t.Parallel()
	helper := newTestRunner(t, Config{Threads: 3})
	direct := newTestRunner(t, Config{Threads: 3, Direct: true})
	jobs := []job{
		{tool: toolFFmpeg, inputs: []input{
			{path: "/data/a.mp4", typ: mp4Type, pre: []string{"-ss", "1.000"}},
			{path: "/data/b.webm", typ: Type{"video/webm", KindVideo, "matroska"}},
		}, args: []string{"-frames:v", "1"}, output: "/data/out.mp4"},
		{tool: toolFFmpeg, inputs: []input{{raw: &rawVideo{w: 4, h: 4, fps: 10, r: strings.NewReader("")}}}, output: "-"},
		{tool: toolFFprobe, inputs: []input{{path: "/data/a.mp4", typ: mp4Type}}, args: []string{"-show_streams"}},
	}
	for i, j := range jobs {
		ha, _, err := helper.args(j)
		if err != nil {
			t.Fatal(err)
		}
		da, _, err := direct.args(j)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ha, da) {
			t.Errorf("job %d: args differ:\nhelper %q\ndirect %q", i, ha, da)
		}
		if !slices.Contains(da, "-protocol_whitelist") {
			t.Errorf("job %d: no protocol whitelist: %q", i, da)
		}
		bin := direct.ffmpegBin
		if j.tool == toolFFprobe {
			bin = direct.ffprobeBin
		}
		hc, err := helper.command(t.Context(), bin, ha)
		if err != nil {
			t.Fatal(err)
		}
		dc, err := direct.command(t.Context(), bin, da)
		if err != nil {
			t.Fatal(err)
		}
		// helper argv: self, sentinel, four limits, bin, args...
		const pre = 2 + 4
		if len(hc.Args) < pre+1 || hc.Args[1] != execSentinel || hc.Args[pre] != bin {
			t.Fatalf("job %d: helper argv %q", i, hc.Args)
		}
		if !slices.Equal(hc.Args[pre:], dc.Args) {
			t.Errorf("job %d: child argv differs:\nhelper %q\ndirect %q", i, hc.Args[pre:], dc.Args)
		}
	}
	for _, typ := range []Type{{"video/mp4", KindVideo, "hls"}, {"video/mp4", KindVideo, "concat"}} {
		j := job{tool: toolFFmpeg, inputs: []input{{path: "/a", typ: typ}}, output: "/o"}
		if _, _, err := direct.args(j); err == nil {
			t.Errorf("Direct accepted demuxer %q", typ.Container)
		}
	}
}

// TestDirectChildEnvironment: the child starts with nothing from snap's
// environment, in its own process group, in "/", and with the limits it
// inherits (the container's) instead of MemLimit and CPULimit.
func TestDirectChildEnvironment(t *testing.T) {
	t.Setenv("SNAP_MEDIA_CANARY", "leaked")
	script := `/usr/bin/env
echo "as=$(ulimit -v)"
echo "cpu=$(ulimit -t)"
echo "pid=$$"
read -r _ _ _ _ pgrp _ < /proc/$$/stat
echo "pgrp=$pgrp"
echo "cwd=$(pwd)"
`
	r := newTestRunner(t, Config{FFprobe: fakeTool(t, script), MemLimit: 512 << 20, CPULimit: 7 * time.Second, Direct: true})
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
	for line := range strings.Lines(out.String()) {
		line = strings.TrimSpace(line)
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "as", "cpu", "pid", "pgrp", "cwd":
			got[k] = v
		case "PWD", "SHLVL", "":
			// set by /bin/sh itself
		default:
			t.Errorf("unexpected variable in child: %q", line)
		}
	}
	inherited := func(res int, unit uint64) string {
		var l unix.Rlimit
		if err := unix.Getrlimit(res, &l); err != nil {
			t.Fatal(err)
		}
		if l.Cur == unix.RLIM_INFINITY {
			return "unlimited"
		}
		return strconv.FormatUint(l.Cur/unit, 10)
	}
	want := map[string]string{
		"as": inherited(unix.RLIMIT_AS, 1024), "cpu": inherited(unix.RLIMIT_CPU, 1),
		"pgrp": got["pid"], "cwd": "/",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if got["as"] == strconv.Itoa(512<<10) || got["cpu"] == "7" {
		t.Errorf("Direct child got the helper's rlimits: %v", got)
	}
}

// TestDirectMedia runs every ffmpeg-backed operation in Direct mode on the
// same kind of fixtures as the helper-mode tests.
func TestDirectMedia(t *testing.T) {
	t.Parallel()
	r := newTestRunner(t, Config{Direct: true})
	x264 := []string{"-c:v", "libx264", "-preset", "ultrafast"}
	mp4 := gen(t, "v.mp4", cat(lavfi("testsrc2=s=320x240:r=24:d=3"), lavfi("sine=d=3"), "-shortest", x264, "-c:a", "aac")...)

	t.Run("probe", func(t *testing.T) {
		t.Parallel()
		info, err := r.Probe(testCtx(t), mp4, sniffPath(t, mp4))
		if err != nil {
			t.Fatal(err)
		}
		if info.Width != 320 || info.Height != 240 || !info.HasVideo || !info.HasAudio || info.VideoCodec != "h264" {
			t.Errorf("Probe = %+v", info)
		}
	})
	t.Run("thumbnail avif", func(t *testing.T) {
		t.Parallel()
		// AVIF is always decoded by ffmpeg, never in process.
		avif := gen(t, "s.avif", cat(lavfi("color=c=green:s=160x120"), "-frames:v", "1", "-c:v", "libaom-av1", "-still-picture", "1", "-cpu-used", "8")...)
		th, _ := thumb(t, r, avif)
		if th.Width != 160 || th.Height != 120 {
			t.Errorf("thumb %+v", th)
		}
	})
	t.Run("poster", func(t *testing.T) {
		t.Parallel()
		dst := filepath.Join(t.TempDir(), "poster.jpg")
		if err := r.Poster(testCtx(t), mp4, sniffPath(t, mp4), dst); err != nil {
			t.Fatal(err)
		}
		if b := readJPEG(t, dst).Bounds(); b.Dx() != 320 || b.Dy() != 240 {
			t.Errorf("poster %v", b)
		}
	})
	teasers := map[string]string{
		"teaser mp4": mp4,
		// Composited in Go and piped to ffmpeg on stdin.
		"teaser webp": gen(t, "s.webp", cat(lavfi("testsrc2=s=64x48:r=10:d=2"), "-c:v", "libwebp_anim")...),
	}
	for name, src := range teasers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := testCtx(t)
			typ := sniffPath(t, src)
			info, err := r.Probe(ctx, src, typ)
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "teaser.mp4")
			if err := r.Teaser(ctx, src, typ, info, dst); err != nil {
				t.Fatal(err)
			}
			p := probeFile(t, dst)
			if len(p.Streams) != 1 || p.Streams[0].CodecName != "h264" {
				t.Errorf("teaser streams = %+v", p.Streams)
			}
		})
	}
}
