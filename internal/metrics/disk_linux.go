//go:build linux

package metrics

import "syscall"

// diskFree returns the bytes available to the server on dir's filesystem.
func diskFree(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * st.Bsize, true // #nosec G115 -- block counts fit in int64
}
