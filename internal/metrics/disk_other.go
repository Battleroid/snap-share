//go:build !linux

package metrics

func diskFree(string) (int64, bool) { return 0, false }
