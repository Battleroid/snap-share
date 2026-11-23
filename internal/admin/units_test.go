package admin

import (
	"strings"
	"testing"
)

func TestFormatParseSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 1: "1 B", 1500: "1500 B", 1000: "1 KB", 1024: "1 KiB", 256 << 20: "256 MiB",
		1_000_000_000: "1 GB", 1 << 30: "1 GiB", 1 << 50: "1 PiB", 5e15: "5 PB",
	} {
		if got := formatSize(n); got != want {
			t.Errorf("formatSize(%d) = %q want %q", n, got, want)
		}
		if back, err := parseSize(want); err != nil || back != n {
			t.Errorf("parseSize(%q) = %d, %v", want, back, err)
		}
	}
	for in, want := range map[string]int64{"1.5 GB": 1_500_000_000, "500kb": 500_000, " 2 gib ": 2 << 30, "10 b": 10, "0.5 KiB": 512, "42": 42} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1", "1 parsec", "1e3", "NaN", "inf", "1 GB GB", strings.Repeat("9", 30) + " PB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted", bad)
		}
	}
}

func TestFormatParseSeconds(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 45: "45s", 90 * 60: "90m", 12 * 3600: "12h", 30 * 86400: "30d", 3661: "3661s"} {
		if got := formatSeconds(n); got != want {
			t.Errorf("formatSeconds(%d) = %q want %q", n, got, want)
		}
		if back, err := parseSeconds(want); err != nil || back != n {
			t.Errorf("parseSeconds(%q) = %d, %v", want, back, err)
		}
	}
	for _, bad := range []string{"", "soon", "1.5h", "-1d", "1w", "d"} {
		if _, err := parseSeconds(bad); err == nil {
			t.Errorf("parseSeconds(%q) accepted", bad)
		}
	}
}

func TestSecretID(t *testing.T) {
	const id = "AbCdEfGhIjKlMnOpQrStUg"
	const key = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFE"
	for _, in := range []string{
		"https://snap.example/s/" + id + "#" + key,
		" https://snap.example/s/" + id + " ",
		"https://snap.example/s/" + id + "/",
		"https://snap.example/s/" + id + "#broken",
		"/s/" + id,
		id,
	} {
		got, ok := secretID(in)
		if !ok || got != id {
			t.Errorf("secretID(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "nope", "https://snap.example/" + id + "x", "https://snap.example/s/short#" + key} {
		if got, ok := secretID(in); ok {
			t.Errorf("secretID(%q) = %q", in, got)
		}
	}
}

func TestSparkPath(t *testing.T) {
	sp := sparkPath([]int{0, 2, 1}, 200, 48, 6)
	if sp.Line != "M0.0 42.0 L100.0 6.0 L200.0 24.0" || !strings.HasSuffix(sp.Area, "L200 48 L0 48 Z") {
		t.Errorf("path %q / %q", sp.Line, sp.Area)
	}
	if flat := sparkPath([]int{0, 0}, 10, 10, 2); flat.Line != "M0.0 8.0 L10.0 8.0" {
		t.Errorf("flat %q", flat.Line)
	}
	if empty := sparkPath([]int{3}, 10, 10, 2); empty.Line != "" {
		t.Error("one point drew a line")
	}
}

func TestLabelAndKind(t *testing.T) {
	for name, want := range map[string]string{"notify.daily_cap": "Daily cap", "zz": "Zz", "a.b.c_d": "B c d"} {
		if got := labelFor(name); got != want {
			t.Errorf("labelFor(%q) = %q", name, got)
		}
	}
	for _, c := range []struct {
		name, def string
		want      fieldKind
	}{
		{"x.on", "true", kindBool}, {"x.name", `"a"`, kindText}, {"x.list", "[]", kindJSON}, {"x.max_bytes", "10", kindBytes},
		{"x.ttl_seconds", "10", kindSeconds}, {"x.count", "3", kindNumber}, {"x.obj", "{}", kindJSON},
	} {
		if got := inferKind(c.name, c.def); got != c.want {
			t.Errorf("inferKind(%q, %s) = %s", c.name, c.def, got)
		}
	}
}

func TestSizeText(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 10 << 20: "10 MiB", 1_500_000_000: "1.5 GB", 1_000_000_000: "1 GB", 1 << 30: "1 GiB", 1500: "1.5 KB"} {
		if got := sizeText(n); got != want {
			t.Errorf("sizeText(%d) = %q want %q", n, got, want)
		}
	}
}
