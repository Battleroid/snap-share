package keys

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"slices"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/sharex"
	"github.com/Battleroid/snap-share/internal/web"
)

// sharexKeyNames label the keys minted for each download.
var sharexKeyNames = map[sharex.Kind]string{
	sharex.Files: "ShareX uploader",
	sharex.Text:  "ShareX text uploader",
	sharex.Links: "ShareX URL shortener",
}

type sharexDownload struct {
	Kind     sharex.Kind
	Title    string
	Endpoint string
	Button   string
}

var sharexDownloads = []sharexDownload{
	{sharex.Files, "Images and files", "/api/v1/upload", "Download uploader"},
	{sharex.Text, "Text", "/api/v1/pastes", "Download text uploader"},
	{sharex.Links, "Short links", "/api/v1/links", "Download URL shortener"},
}

// cliName is the release file pattern served under /dl/.
var cliName = regexp.MustCompile(`^snap-cli-(linux|darwin|freebsd|windows)-(amd64|arm64)(\.exe)?$`)

func validCLIName(name string) bool {
	m := cliName.FindStringSubmatch(name)
	return m != nil && (m[3] == "") == (m[1] != "windows")
}

type cliBuild struct {
	Name, OS, Arch, Size string
}

// cliBuilds lists the release files present in the CLI directory.
func (s *Service) cliBuilds() []cliBuild {
	entries, err := os.ReadDir(s.d.Cfg.CLIDir)
	if err != nil {
		return nil
	}
	var out []cliBuild
	for _, e := range entries {
		if !e.Type().IsRegular() || !validCLIName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		m := cliName.FindStringSubmatch(e.Name())
		out = append(out, cliBuild{Name: e.Name(), OS: m[1], Arch: m[2], Size: web.HumanBytes(info.Size())})
	}
	return out
}

type curlLine struct{ Label, Command string }

// uploadOption is one row of the per-upload options table.
type uploadOption struct{ Name, Values, Effect string }

// uploadOptions are the creation options every upload, paste and link takes
// (items.optionFields plus the file name). Leaving one out uses Settings › Defaults.
var uploadOptions = []uploadOption{
	{"expires", "never · 1h · 1d · 7d · 30d · 90d · an RFC 3339 time", "when it stops working"},
	{"expiry_action", "delete · private", "delete it at expiry, or hide it (private) instead"},
	{"visibility", "unlisted · private", "unlisted: anyone with the link. private: only you, signed in; everyone else gets not found"},
	{"password", "8 or more characters", "visitors must enter it before the item opens"},
	{"naming", "random · words · original", "how the link name is made: random letters and digits, words, or from the file name"},
	{"length", "10–32", "length of a random name"},
	{"slug", "1–64 of A-Z a-z 0-9 - _", "a custom link name instead of naming"},
	{"name", "a file name", "the stored file name (files and pastes); original naming uses it"},
	{"title", "text", "a display title"},
}

// sharexExamples show where the options go in a .sxcu file. Files use
// Arguments (multipart form fields), where ShareX also runs {select:…} and
// {inputbox:…}. The text and link files send a JSON Data body, in which ShareX
// fills in only {input} and {filename}, so prompts there need a form body.
var sharexExamples = []curlLine{
	{"uploader · fixed options", `"Arguments": {
  "expires": "7d",
  "visibility": "private",
  "naming": "words"
}`},
	{"uploader · asked on each upload", `"Arguments": {
  "expires": "{select:1d|7d|30d|never}",
  "visibility": "{select:unlisted|private}",
  "password": "{inputbox:Password, or leave blank}"
}`},
	{"text uploader · fixed options", `"Data": "{\"content\":\"{input}\",\"expires\":\"1d\",\"visibility\":\"private\"}"`},
	{"text uploader · asked on each upload", `"Body": "FormURLEncoded",
"Arguments": {
  "content": "{input}",
  "expires": "{select:1h|1d|7d|never}"
}`},
	{"URL shortener · fixed options", `"Data": "{\"url\":\"{input}\",\"expires\":\"30d\"}"`},
}

// integrationsPage handles GET /settings/integrations.
func (s *Service) integrationsPage(w http.ResponseWriter, r *http.Request) {
	origin := s.d.Cfg.Origin()
	hdr := `-H "Authorization: Bearer $SNAP_KEY"`
	curl := []curlLine{
		{"upload", `curl ` + hdr + ` -F file=@photo.png "` + origin + `/api/v1/upload?format=url"`},
		{"paste from stdin", `cat notes.txt | curl ` + hdr + ` -H "Content-Type: text/plain" --data-binary @- "` + origin + `/api/v1/pastes?format=url"`},
		{"shorten", `curl ` + hdr + ` -d url=https://example.com/ "` + origin + `/api/v1/links?format=url"`},
		{"raw upload", `curl ` + hdr + ` -T backup.tar.zst "` + origin + `/api/v1/upload/backup.tar.zst?format=url"`},
	}
	optCurl := []curlLine{
		{"upload: options as form fields", `curl ` + hdr + ` -F file=@photo.png -F expires=7d -F visibility=private -F naming=words "` + origin + `/api/v1/upload?format=url"`},
		{"upload with a password", `curl ` + hdr + ` -F file=@report.pdf --form-string "password=$ITEM_PASSWORD" "` + origin + `/api/v1/upload?format=url"`},
		{"raw upload: options in the query, password in a header", `curl ` + hdr + ` -H "X-Snap-Password: $ITEM_PASSWORD" -T backup.tar.zst "` + origin + `/api/v1/upload/backup.tar.zst?expires=1d&naming=original&format=url"`},
		{"paste from stdin", `cat notes.txt | curl ` + hdr + ` -H "Content-Type: text/plain" --data-binary @- "` + origin + `/api/v1/pastes?name=notes.txt&expires=1d&visibility=private&format=url"`},
		{"shorten with a custom name", `curl ` + hdr + ` -d url=https://example.com/ -d slug=docs -d expires=30d "` + origin + `/api/v1/links?format=url"`},
	}
	optCLI := []curlLine{
		{"sp up", `sp up --expire 7d --private --name words photo.png
sp up --password-file pw.txt --slug q3-report report.pdf`},
		{"sp paste", `sp paste --expire 1d --title "Build log" build.log`},
		{"sp short", `sp short --slug docs --expire 30d https://example.com/`},
	}
	builds := s.cliBuilds()
	install := ""
	if len(builds) > 0 {
		name := builds[0].Name
		if slices.ContainsFunc(builds, func(b cliBuild) bool { return b.Name == "snap-cli-linux-amd64" }) {
			name = "snap-cli-linux-amd64"
		}
		install = `curl -fLo sp ` + origin + `/dl/` + name + ` && chmod +x sp && ./sp login ` + origin
	}
	s.page(w, r, http.StatusOK, "integrations/integrations", "Integrations", map[string]any{
		"ShareX": sharexDownloads, "Curl": curl, "Builds": builds, "Install": install, "Origin": origin,
		"Env":     "export SNAP_URL=" + origin + "\nexport SNAP_KEY=snap_…",
		"Options": uploadOptions, "OptShareX": sharexExamples, "OptCurl": optCurl, "OptCLI": optCLI,
	})
}

// sharexDownload handles POST /settings/integrations/sharex/{kind}: it mints a
// key that can only create items (deletion goes through the deletion URL) and
// returns the .sxcu that carries it.
func (s *Service) sharexDownload(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	kind := sharex.Kind(r.PathValue("kind"))
	if !kind.Valid() {
		s.d.Errors.NotFound(w, r)
		return
	}
	now := s.now()
	nk := NewKey{
		Name:   sharexKeyNames[kind] + " " + now.UTC().Format("2006-01-02"),
		Scopes: []auth.Scope{auth.ScopeItemsWrite},
	}
	token, _, err := s.store.Create(r.Context(), p.UserID, nk, accounts.Actor(r))
	if errors.Is(err, ErrTooManyKeys) {
		s.d.Errors.Error(w, r, http.StatusConflict, tooManyMsg())
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	host := s.d.Cfg.PublicURL.Host
	up, err := sharex.New(kind, s.d.Cfg.Origin(), s.d.SiteName()+" "+string(kind)+" ("+host+")", token)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	b, err := up.Marshal()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="`+sharex.Filename(host, kind)+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// download handles GET /dl/{name}: a CLI build from the CLI directory.
// Only names matching the release pattern are looked up.
func (s *Service) download(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validCLIName(name) {
		s.d.Errors.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.d.Cfg.CLIDir)
	if err != nil {
		s.d.Errors.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(name)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("cli download", "err", err)
		}
		s.d.Errors.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		s.d.Errors.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
