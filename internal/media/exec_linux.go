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
	// Everything execve needs is allocated now: once RLIMIT_AS is lowered,
	// any Go heap growth (an allocation, a GC cycle) can fail and crash the
	// runtime before ffmpeg starts. So: build the argv/envp pointers first,
	// stop the GC, pin the thread, then set limits and call execve directly.
	argv0, err := unix.BytePtrFromString(bin)
	if err != nil {
		return err
	}
	argv, err := cStrings(args[4:])
	if err != nil {
		return err
	}
	envv := []*byte{nil} // empty environment
	debug.SetGCPercent(-1)
	runtime.LockOSThread()

	as, cpu, fsize, nofile := lim[0], lim[1], lim[2], lim[3]
	set := []struct {
		res      int
		cur, max uint64
	}{
		{unix.RLIMIT_CORE, 0, 0},
		{unix.RLIMIT_AS, as, as},
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
	// #nosec G103 -- execve takes C pointers; all three point at memory allocated above.
	_, _, errno := unix.RawSyscall(unix.SYS_EXECVE, uintptr(unsafe.Pointer(argv0)), uintptr(unsafe.Pointer(&argv[0])), uintptr(unsafe.Pointer(&envv[0])))
	return fmt.Errorf("execve %s: %w", bin, errno)
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

// command starts bin through the re-exec helper with an empty environment,
// in its own process group that is SIGKILLed when ctx ends.
func (r *Runner) command(ctx context.Context, bin string, args []string) (*exec.Cmd, error) {
	argv := append([]string{r.self, execSentinel}, r.childLimits()...)
	argv = append(argv, bin)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, r.self) //nolint:gosec // fixed binaries; args built by Runner.args
	cmd.Args = argv
	cmd.Env = []string{}
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
