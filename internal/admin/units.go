package admin

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Byte units for size fields: SI units match how sizes are shown elsewhere
// (web.HumanBytes); binary ones keep defaults like 256 MiB exact.
var sizeUnits = []struct {
	name string
	n    int64
}{
	{"PiB", 1 << 50}, {"PB", 1e15}, {"TiB", 1 << 40}, {"TB", 1e12}, {"GiB", 1 << 30}, {"GB", 1e9},
	{"MiB", 1 << 20}, {"MB", 1e6}, {"KiB", 1 << 10}, {"KB", 1e3},
}

// formatSize renders n bytes exactly, in the largest unit that divides it:
// "256 MiB", "1 GB", "1500 B", "0".
func formatSize(n int64) string {
	if n == 0 {
		return "0"
	}
	for _, u := range sizeUnits {
		if n%u.n == 0 {
			return strconv.FormatInt(n/u.n, 10) + " " + u.name
		}
	}
	return strconv.FormatInt(n, 10) + " B"
}

var errSize = errors.New("bad size")

// parseSize reads a size such as "256 MiB", "1.5 GB", "500kb" or a plain
// number of bytes.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsLetter(r) })
	num, unit := s, ""
	if i >= 0 {
		num, unit = strings.TrimSpace(s[:i]), strings.ToLower(strings.TrimSpace(s[i:]))
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil || v < 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, errSize
	}
	mult := int64(1)
	if unit != "" && unit != "b" {
		found := false
		for _, u := range sizeUnits {
			if strings.ToLower(u.name) == unit {
				mult, found = u.n, true
				break
			}
		}
		if !found {
			return 0, errSize
		}
	}
	b := v * float64(mult)
	if b > math.MaxInt64/2 {
		return 0, errSize
	}
	return int64(math.Round(b)), nil
}

var durationUnits = []struct {
	name string
	n    int64
}{{"d", 86400}, {"h", 3600}, {"m", 60}, {"s", 1}}

// formatSeconds renders a whole number of seconds in the largest unit that
// divides it: "30d", "12h", "90m", "45s".
func formatSeconds(n int64) string {
	if n == 0 {
		return "0"
	}
	for _, u := range durationUnits {
		if n%u.n == 0 {
			return strconv.FormatInt(n/u.n, 10) + u.name
		}
	}
	return strconv.FormatInt(n, 10) + "s"
}

var errDuration = errors.New("bad duration")

// parseSeconds reads "30d", "12h", "90m", "45s" or a plain number of seconds.
func parseSeconds(s string) (int64, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	mult := int64(1)
	for _, u := range durationUnits {
		if num, ok := strings.CutSuffix(s, u.name); ok {
			s, mult = num, u.n
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/mult {
		return 0, errDuration
	}
	return n * mult, nil
}
