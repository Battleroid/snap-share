//go:build linux

package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// execSentinel is os.Args[1] of the re-exec helper. Private to this package.
const execSentinel = "__snap-media-exec"

// MaybeRunLimited turns the current process into a resource-limited
// ffmpeg/ffprobe when it was started by a Runner, and never returns in that
// case. Otherwise it returns immediately.
//
// Go cannot set rlimits on a child between fork and exec, so a Runner starts
// its own executable as
//
//	<self> __snap-media-exec <as> <cpu> <fsize> <nofile> <binary> <args...>
//
// and this function applies the limits and execs the binary. Call it first
// thing in main (and in TestMain of tests that use a Runner).
func MaybeRunLimited() {
	if len(os.Args) < 2 || os.Args[1] != execSentinel {
		return
	}
	err := execLimited(os.Args[2:])
	fmt.Fprintf(os.Stderr, "media exec: %v\n", err)
	os.Exit(127)
}

// helperEnv is the environment of the re-exec helper. A Go process maps more
// address space at start (about 1.2 GiB) than RLIMIT_AS allows ffmpeg, so
// once that limit is set any thread that maps memory (a stack copy, a new M,
// a GC cycle, a finalizer) crashes the helper. These keep the runtime idle
// until execve: no GC (so no finalizers or cleanups either), one P, no
// GOMAXPROCS updater and no preemption signals. ffmpeg gets none of it:
// execLimited execs it with an empty environment.
func helperEnv() []string {
	return []string{"GOGC=off", "GOMAXPROCS=1", "GODEBUG=updatemaxprocs=0,asyncpreemptoff=1"}
}

func execLimited(args []string) error {
	if len(args) < 5 {
		return errors.New("missing arguments")
	}
	var lim [4]uint64
	for i := range lim {
		v, err := strconv.ParseUint(args[i], 10, 64)
		if err != nil {
			return fmt.Errorf("limit %d: %w", i, err)
		}
		lim[i] = v
	}
	bin := args[4]
	if !filepath.IsAbs(bin) {
		return fmt.Errorf("binary must be absolute: %q", bin)
	}
	// Everything prlimit and execve need is allocated now, on the heap
	// (which does not move), so nothing is allocated once RLIMIT_AS is set.
	argv0, err := unix.BytePtrFromString(bin)
	if err != nil {
		return err
	}
	argv, err := cStrings(args[4:])
	if err != nil {
		return err
	}
	envv := []*byte{nil} // empty environment
	as, cpu, fsize, nofile := lim[0], lim[1], lim[2], lim[3]
	var rlimAS *unix.Rlimit // nil: no limit
	if as != 0 {
		rlimAS = &unix.Rlimit{Cur: as, Max: as}
	}
	debug.SetGCPercent(-1) // already off through helperEnv; in case it was not used
	runtime.LockOSThread()

	// These limits leave the runtime's memory alone, so they come first.
	set := []struct {
		res      int
		cur, max uint64
	}{
		{unix.RLIMIT_CORE, 0, 0},
		// SIGXCPU at the soft limit, SIGKILL one second later.
		{unix.RLIMIT_CPU, cpu, cpu + 1},
		{unix.RLIMIT_FSIZE, fsize, fsize},
		{unix.RLIMIT_NOFILE, nofile, nofile},
	}
	for _, s := range set {
		if s.max == 0 && s.res != unix.RLIMIT_CORE {
			continue // 0 means no limit
		}
		if err := unix.Setrlimit(s.res, &unix.Rlimit{Cur: s.cur, Max: s.max}); err != nil {
			return fmt.Errorf("setrlimit %d: %w", s.res, err)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	// #nosec G103 -- prlimit and execve take C pointers; all of them point at heap memory allocated above.
	errno, asFailed := limitASAndExec(uintptr(unsafe.Pointer(rlimAS)), uintptr(unsafe.Pointer(argv0)),
		uintptr(unsafe.Pointer(&argv[0])), uintptr(unsafe.Pointer(&envv[0])))
	runtime.KeepAlive(rlimAS)
	runtime.KeepAlive(argv0)
	runtime.KeepAlive(argv)
	runtime.KeepAlive(envv)
	if asFailed {
		// RLIMIT_AS is unchanged, so an ordinary error is safe.
		return fmt.Errorf("setrlimit AS: %w", errno)
	}
	// RLIMIT_AS is in force and formatting an error could crash: write a
	// fixed message without allocating.
	rawWrite("media exec: execve: ")
	rawWrite(errno.Error()) // a string from the errno table
	rawWrite("\n")
	_, _, _ = syscall.RawSyscall(unix.SYS_EXIT_GROUP, 127, 0, 0)
	return errno
}

// limitASAndExec sets RLIMIT_AS (unless rlim is 0) and calls execve, with no
// Go code in between that could allocate, grow the stack or be preempted. It
// returns only when one of the two calls fails; asFailed reports that it was
// the first.
//
//go:nosplit
//go:norace
func limitASAndExec(rlim, path, argv, envv uintptr) (errno unix.Errno, asFailed bool) {
	if rlim != 0 {
		if _, _, e := syscall.RawSyscall6(unix.SYS_PRLIMIT64, 0, unix.RLIMIT_AS, rlim, 0, 0, 0); e != 0 {
			return e, true
		}
	}
	_, _, e := syscall.RawSyscall(unix.SYS_EXECVE, path, argv, envv)
	return e, false
}

// rawWrite writes s to stderr without allocating.
func rawWrite(s string) {
	// #nosec G103 -- write(2) of a string's bytes.
	_, _, _ = syscall.RawSyscall(unix.SYS_WRITE, 2, uintptr(unsafe.Pointer(unsafe.StringData(s))), uintptr(len(s)))
}

// cStrings is a NULL-terminated array of C strings for execve.
func cStrings(ss []string) ([]*byte, error) {
	out := make([]*byte, 0, len(ss)+1)
	for _, s := range ss {
		p, err := unix.BytePtrFromString(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return append(out, nil), nil
}

// command starts bin in its own process group, which is SIGKILLed when ctx
// ends. By default it goes through the re-exec helper (environment:
// helperEnv), which applies childLimits. With Config.Direct it execs bin
// itself, with an empty environment, under the container's limits.
func (r *Runner) command(ctx context.Context, bin string, args []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if r.cfg.Direct {
		cmd = exec.CommandContext(ctx, bin, args...) //nolint:gosec // fixed binaries; args built by Runner.args
		cmd.Env = []string{}                         // empty, not nil: nil inherits ours
	} else {
		argv := append([]string{r.self, execSentinel}, r.childLimits()...)
		argv = append(argv, bin)
		argv = append(argv, args...)
		cmd = exec.CommandContext(ctx, r.self) //nolint:gosec // fixed binaries; args built by Runner.args
		cmd.Args = argv
		cmd.Env = helperEnv()
	}
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

// cpuKilled reports whether the child died from RLIMIT_CPU.
func cpuKilled(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGXCPU
}

// sigKilled reports whether the child died from SIGKILL.
func sigKilled(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}
