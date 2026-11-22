//go:build !linux

package admin

// diskFree is not implemented here; the overview shows "—".
func diskFree(string) (int64, bool) { return 0, false }
