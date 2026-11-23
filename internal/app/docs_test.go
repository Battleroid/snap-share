package app_test

// Docs consistency: the configuration reference matches the code, relative
// links resolve, and the CI workflow and example deployment agree with the
// toolchain and with each other.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	_ "github.com/Battleroid/snap-share/internal/app" // registers every settings key
	"github.com/Battleroid/snap-share/internal/config"
	"github.com/Battleroid/snap-share/internal/settings"
)

const repoRoot = "../.."

// Variables that are not server configuration but appear in the docs.
var otherEnv = map[string]string{
	"SNAP_URL":        "sp",
	"SNAP_KEY":        "sp",
	"SNAP_IMAGE":      "deploy/compose.yml",
	"SNAP_DOMAIN":     "deploy/compose.yml",
	"SNAP_RAW_DOMAIN": "deploy/compose.yml",
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// configEnv returns every variable config.Load reads.
func configEnv() map[string]bool {
	seen := map[string]bool{}
	_, _ = config.Load(func(k string) string { seen[k] = true; return "" })
	return seen
}

// section returns the lines of a markdown file from heading to the next
// heading of the same or a higher level.
func section(t *testing.T, doc, heading string) []string {
	t.Helper()
	level := strings.Index(heading, " ")
	var out []string
	in := false
	for _, line := range strings.Split(doc, "\n") {
		if in {
			if n := headingLevel(line); n > 0 && n <= level {
				break
			}
			out = append(out, line)
		} else if line == heading {
			in = true
		}
	}
	if !in {
		t.Fatalf("heading %q not found", heading)
	}
	return out
}

func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// tableRows returns the cells of the table rows whose first cell starts with a backtick.
func tableRows(lines []string) [][]string {
	var rows [][]string
	for _, line := range lines {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		rows = append(rows, cells)
	}
	return rows
}

var codeSpan = regexp.MustCompile("`([^`]+)`")

func TestDocsEnvironmentVariables(t *testing.T) {
	seen := configEnv()
	doc := readFile(t, "docs/configuration.md")

	type entry struct{ file bool }
	documented := map[string]entry{}
	for _, row := range tableRows(section(t, doc, "## Environment variables")) {
		m := codeSpan.FindStringSubmatch(row[0])
		documented[m[1]] = entry{file: strings.Contains(row[0], "`_FILE`")}
	}

	for name := range seen {
		if base, ok := strings.CutSuffix(name, "_FILE"); ok && seen[base] {
			if !documented[base].file {
				t.Errorf("docs/configuration.md: %s is not marked / `_FILE`", base)
			}
			continue
		}
		if _, ok := documented[name]; !ok {
			t.Errorf("docs/configuration.md: %s is read by internal/config but missing from the environment table", name)
		}
	}
	for name, e := range documented {
		if !seen[name] {
			t.Errorf("docs/configuration.md documents %s, which internal/config does not read", name)
		}
		if e.file && !seen[name+"_FILE"] {
			t.Errorf("docs/configuration.md marks %s / `_FILE`, but internal/config does not read %s_FILE", name, name)
		}
	}
}

func TestDocsSettings(t *testing.T) {
	doc := readFile(t, "docs/configuration.md")
	documented := map[string]string{} // key -> default cell
	for _, row := range tableRows(section(t, doc, "## Admin settings")) {
		if len(row) < 4 {
			t.Errorf("docs/configuration.md: settings row %q needs key, default, meaning and where", row)
			continue
		}
		documented[strings.Trim(row[0], "`")] = row[1]
	}
	for _, d := range settings.Definitions() {
		def, ok := documented[d.Name()]
		if !ok {
			t.Errorf("docs/configuration.md: setting %s is missing from the admin settings table", d.Name())
			continue
		}
		if want := "`" + d.DefaultJSON() + "`"; def != want {
			t.Errorf("docs/configuration.md: %s default is %s, the code says %s", d.Name(), def, want)
		}
	}
	for name := range documented {
		if _, ok := settings.Lookup(name); !ok {
			t.Errorf("docs/configuration.md documents setting %s, which is not registered", name)
		}
	}
}

// docFiles are the markdown files written for this repo (not the vendored kit docs).
func docFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"README.md", "CONTRIBUTING.md", "docs/dev/conventions.md"}
	matches, err := filepath.Glob(filepath.Join(repoRoot, "docs", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range matches {
		rel, _ := filepath.Rel(repoRoot, m)
		files = append(files, filepath.ToSlash(rel))
	}
	slices.Sort(files)
	return files
}

var envName = regexp.MustCompile(`\bSNAP_[A-Z0-9_]+\b`)

// Every SNAP_ variable named in the docs exists.
func TestDocsMentionKnownVariables(t *testing.T) {
	seen := configEnv()
	files := append(docFiles(t), "deploy/.env.example", "deploy/compose.yml", "deploy/compose.local-test.yml")
	for _, f := range files {
		for _, name := range envName.FindAllString(readFile(t, f), -1) {
			if strings.HasSuffix(name, "_") { // a prefix: SNAP_OIDC_*
				continue
			}
			if !seen[name] && otherEnv[name] == "" {
				t.Errorf("%s mentions %s, which nothing reads", f, name)
			}
		}
	}
}

var (
	mdLink    = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)\)`)
	fence     = regexp.MustCompile("(?ms)^```.*?^```")
	mdInline  = regexp.MustCompile("`[^`\n]*`")
	headingMD = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// prose drops fenced code blocks and code spans, where brackets are not links.
func prose(doc string) string {
	return mdInline.ReplaceAllString(fence.ReplaceAllString(doc, ""), "")
}

// anchors returns the heading ids GitHub and Gitea generate for doc.
func anchors(doc string) map[string]bool {
	out := map[string]bool{}
	count := map[string]int{}
	for _, line := range strings.Split(fence.ReplaceAllString(doc, ""), "\n") {
		n := headingLevel(line)
		if n == 0 {
			continue
		}
		text := headingMD.ReplaceAllString(line[n+1:], "$1")
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(text)) {
			switch {
			case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
				b.WriteRune(r)
			case r == ' ':
				b.WriteByte('-')
			}
		}
		id := b.String()
		if c := count[id]; c > 0 {
			count[id]++
			id += "-" + strconv.Itoa(c)
		} else {
			count[id] = 1
		}
		out[id] = true
	}
	return out
}

func TestDocsLinks(t *testing.T) {
	for _, f := range docFiles(t) {
		doc := readFile(t, f)
		for _, m := range mdLink.FindAllStringSubmatch(prose(doc), -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			path, frag, _ := strings.Cut(target, "#")
			rel := f
			if path != "" {
				rel = filepath.ToSlash(filepath.Join(filepath.Dir(f), path))
				if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
					t.Errorf("%s: link %s: no such file", f, target)
					continue
				}
			}
			if frag != "" && strings.HasSuffix(rel, ".md") && !anchors(readFile(t, rel))[frag] {
				t.Errorf("%s: link %s: no heading #%s in %s", f, target, frag, rel)
			}
		}
	}
}

// docs/README.md lists every page in docs/.
func TestDocsIndex(t *testing.T) {
	index := readFile(t, "docs/README.md")
	for _, f := range docFiles(t) {
		if !strings.HasPrefix(f, "docs/") || f == "docs/README.md" {
			continue
		}
		if !strings.Contains(index, "("+strings.TrimPrefix(f, "docs/")+")") {
			t.Errorf("docs/README.md does not link %s", f)
		}
	}
}

// The CI workflow runs the toolchain the Makefile's containers pin: it calls the
// Makefile targets instead of naming images of its own, so the versions cannot
// drift. This checks that it still does, that the pins are there to be used, and
// that the workflow's actions and token scope stay locked down.
func TestCIMatchesToolchain(t *testing.T) {
	const wf = ".github/workflows/ci.yml"
	ci := readFile(t, wf)
	dev := readFile(t, "docker/dev.Dockerfile")
	mk := readFile(t, "Makefile")

	// The pins the make targets run in.
	var base, node string
	if m := regexp.MustCompile(`(?m)^FROM (\S+)`).FindStringSubmatch(dev); m != nil {
		base = m[1]
	}
	tools := regexp.MustCompile(`@v[0-9][^\s]*`).FindAllString(dev, -1)
	if m := regexp.MustCompile(`(?m)^NODE_IMAGE\s*:=\s*(\S+)`).FindStringSubmatch(mk); m != nil {
		node = m[1]
	}
	if base == "" || node == "" || len(tools) < 2 {
		t.Fatalf("could not read the toolchain versions: base %q, node %q, tools %q", base, node, tools)
	}
	if !strings.Contains(readFile(t, "Dockerfile"), "FROM "+base+" AS build") {
		t.Errorf("Dockerfile does not build with %s, the image docker/dev.Dockerfile tests with", base)
	}

	// The jobs call the targets that use them, and nothing swaps in another image.
	for _, target := range []string{"make test-go", "make lint", "make vuln", "make test-js"} {
		if !strings.Contains(ci, target) {
			t.Errorf("%s does not run `%s`", wf, target)
		}
	}
	if regexp.MustCompile(`(?m)^[ \t]*(container:|image:[ \t]*\S)`).MatchString(ci) {
		t.Errorf("%s names a container image; the toolchain comes from the Makefile's pins", wf)
	}

	// Every action is pinned to a full commit SHA with a version comment.
	uses := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*(\S+)(.*)$`).FindAllStringSubmatch(ci, -1)
	if len(uses) == 0 {
		t.Errorf("%s uses no actions; this check expects checkout", wf)
	}
	for _, u := range uses {
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(u[1]) || !regexp.MustCompile(`^\s*#\s*v\d`).MatchString(u[2]) {
			t.Errorf("%s: %q is not pinned to a commit SHA followed by a `# vX.Y.Z` comment", wf, strings.TrimSpace(u[0]))
		}
	}

	// Least privilege: read-only by default, package writes only in the image job,
	// and no secret but the workflow's own token.
	if !regexp.MustCompile(`(?m)^permissions:\n  contents: read\n`).MatchString(ci) {
		t.Errorf("%s: the top-level permissions must be `contents: read`", wf)
	}
	if n := strings.Count(ci, "packages: write"); n != 1 {
		t.Errorf("%s: want exactly one `packages: write` (the image job), got %d", wf, n)
	}
	for _, s := range regexp.MustCompile(`secrets\.\w+`).FindAllString(ci, -1) {
		if s != "secrets.GITHUB_TOKEN" {
			t.Errorf("%s uses %s; only GITHUB_TOKEN is allowed", wf, s)
		}
	}

	// The published image name agrees across the workflow, the example and the docs.
	const image = "ghcr.io/battleroid/snap-share"
	for _, f := range []string{wf, "deploy/.env.example", "docs/deployment.md"} {
		if !strings.Contains(readFile(t, f), image) {
			t.Errorf("%s does not mention %s", f, image)
		}
	}
}

// SNAP_TRUSTED_PROXIES in the example .env names the compose network's subnet.
func TestDeployTrustedProxies(t *testing.T) {
	compose := readFile(t, "deploy/compose.yml")
	env := readFile(t, "deploy/.env.example")
	subnet := regexp.MustCompile(`(?m)^\s*- subnet: (\S+)`).FindStringSubmatch(compose)
	trusted := regexp.MustCompile(`(?m)^SNAP_TRUSTED_PROXIES=(\S+)`).FindStringSubmatch(env)
	if subnet == nil || trusted == nil || subnet[1] != trusted[1] {
		t.Errorf("deploy/.env.example SNAP_TRUSTED_PROXIES %q differs from the subnet in deploy/compose.yml %q", trusted, subnet)
	}
}
