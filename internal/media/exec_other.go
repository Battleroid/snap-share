//go:build !linux

package media

import (
	"context"
	"fmt"
	"os/exec"
)

// MaybeRunLimited does nothing outside Linux.
func MaybeRunLimited() {}

// command refuses to run: the ffmpeg sandbox needs Linux rlimits and
// process groups.
func (r *Runner) command(context.Context, string, []string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("%w: ffmpeg runner needs linux", ErrUnsupported)
}

func cpuKilled(error) bool { return false }

func sigKilled(error) bool { return false }
