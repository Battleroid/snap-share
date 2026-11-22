//go:build !linux

package local

// statfs is not implemented here; the free-space guard is effectively off.
func statfs(string) (uint64, error) { return 1 << 62, nil }
