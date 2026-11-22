package stats

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestClassifyRealUserAgents(t *testing.T) {
	f, err := os.Open("testdata/ua.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " | ", 3)
		if len(parts) != 3 {
			t.Fatalf("bad testdata line %q", line)
		}
		wantSummary, wantClass, ua := parts[0], parts[1], parts[2]
		n++

		r := httptest.NewRequest(http.MethodGet, "/abc", nil)
		r.Header.Set("User-Agent", ua)
		c := Classify(r)
		var got string
		switch {
		case c.Drop:
			got = "drop"
		case c.Preview:
			got = "preview:" + c.Bot
		default:
			got = "human"
		}
		if got != wantClass {
			t.Errorf("Classify(%q) = %s, want %s", ua, got, wantClass)
		}
		if s := SummarizeUA(ua); s != wantSummary {
			t.Errorf("SummarizeUA(%q) = %q, want %q", ua, s, wantSummary)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 30 {
		t.Errorf("only %d user agents in testdata", n)
	}
}

func TestClassifyRequests(t *testing.T) {
	const firefox = "Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0"
	tests := []struct {
		name    string
		method  string
		headers map[string]string
		want    Class
	}{
		{"person", http.MethodGet, map[string]string{"User-Agent": firefox}, Class{}},
		{"no user agent", http.MethodGet, nil, Class{Preview: true, Bot: UnknownBot}},
		{"blank user agent", http.MethodGet, map[string]string{"User-Agent": "  "}, Class{Preview: true, Bot: UnknownBot}},
		{"head", http.MethodHead, map[string]string{"User-Agent": firefox}, Class{Drop: true}},
		{"options", http.MethodOptions, map[string]string{"User-Agent": firefox}, Class{Drop: true}},
		{"chrome prefetch", http.MethodGet, map[string]string{"User-Agent": firefox, "Sec-Purpose": "prefetch;prerender"}, Class{Drop: true}},
		{"old purpose", http.MethodGet, map[string]string{"User-Agent": firefox, "Purpose": "prefetch"}, Class{Drop: true}},
		{"firefox prefetch", http.MethodGet, map[string]string{"User-Agent": firefox, "X-Moz": "prefetch"}, Class{Drop: true}},
		{"head from bot", http.MethodHead, map[string]string{"User-Agent": "Discordbot/2.0"}, Class{Drop: true}},
		{"crawler named", http.MethodGet, map[string]string{"User-Agent": "Googlebot/2.1"}, Class{Drop: true, Bot: "Google"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "/abc", nil)
			r.Header.Del("User-Agent")
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			if got := Classify(r); got != tt.want {
				t.Errorf("Classify = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBotsFile(t *testing.T) {
	rs, err := parseRules(botsTxt)
	if err != nil {
		t.Fatal(err)
	}
	previews := map[string]bool{}
	for _, r := range rs {
		if r.preview {
			previews[r.name] = true
		}
	}
	for _, want := range []string{"Discord", "Slack", "Telegram", "Twitter", "Facebook", "WhatsApp", "iMessage",
		"LinkedIn", "Mattermost", "Iframely", "Teams", "Pinterest", "Reddit", "Embedly"} {
		if !previews[want] {
			t.Errorf("no preview rule for %s", want)
		}
	}
	for _, bad := range []string{
		"preview | x",
		"keep | x | y",
		"drop |  | y",
		"drop | x |  & ",
	} {
		if _, err := parseRules(bad); err == nil {
			t.Errorf("parseRules(%q): no error", bad)
		}
	}
	// A plain browser string must not match any rule.
	if r, ok := matchRule("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"); ok {
		t.Errorf("browser matched rule %+v", r)
	}
}

func TestSummarizeUAEdges(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		"   ":                      "",
		"Mozilla/5.0":              "unknown",
		"(((":                      "unknown",
		"<script>/1":               "script",
		strings.Repeat("A", 100):   strings.Repeat("A", 32),
		"Mozilla/5.0 (X11; Linux)": "Linux",
	}
	for ua, want := range tests {
		if got := SummarizeUA(ua); got != want {
			t.Errorf("SummarizeUA(%q) = %q, want %q", ua, got, want)
		}
	}
}
