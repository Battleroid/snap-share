package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/mail"
	"github.com/Battleroid/snap-share/internal/settings"
	"github.com/Battleroid/snap-share/internal/web"
)

// fieldKind is how a setting is edited.
type fieldKind string

const (
	kindBool    fieldKind = "bool"    // Switch; applies at once
	kindNumber  fieldKind = "number"  // integer or decimal
	kindBytes   fieldKind = "bytes"   // stored in bytes, edited as "256 MiB"
	kindSeconds fieldKind = "seconds" // stored in seconds, edited as "30d"
	kindSelect  fieldKind = "select"  // one of Options
	kindText    fieldKind = "text"
	kindLines   fieldKind = "lines" // list of strings, one per line
	kindJSON    fieldKind = "json"  // anything else, as JSON
)

// hint is how the editor presents a key. Keys without one get a label from
// their name and a kind from their default value.
type hint struct {
	Label   string
	Kind    fieldKind // "": inferred from the default
	Unit    string
	Options []string
	Help    string // only where it prevents a mistake
}

var hints = map[string]hint{
	"auth.signup_mode":            {Label: "Sign-up mode", Kind: kindSelect, Options: []string{accounts.SignupClosed, accounts.SignupInvite, accounts.SignupOpen}},
	"auth.password_login":         {Label: "Password sign-in"},
	"auth.require_2fa":            {Label: "Require two-factor", Kind: kindSelect, Options: []string{accounts.Require2FAOff, accounts.Require2FAAdmins, accounts.Require2FAEveryone}},
	"uploads.max_bytes":           {Label: "Max file size", Kind: kindBytes},
	"uploads.default_quota_bytes": {Label: "Default quota", Kind: kindBytes, Help: "0 means no limit"},
	"uploads.blocked_extensions":  {Label: "Blocked extensions", Kind: kindLines, Help: "one per line, e.g. exe"},
	"uploads.blocked_types":       {Label: "Blocked types", Kind: kindLines, Help: "one per line, e.g. application/x-msdownload or video/*"},
	"items.max_expiry_days":       {Label: "Max expiry", Unit: "days", Help: "0 means no limit"},
	"secrets.max_bytes":           {Label: "Max file size", Kind: kindBytes},
	"secrets.max_views":           {Label: "Max views"},
	"secrets.max_expiry":          {Label: "Max expiry", Kind: kindSeconds, Help: "e.g. 30d, 12h or 90m"},
	"secrets.max_failures":        {Label: "Wrong passphrases before burning"},
	"secrets.anonymous":           {Label: "Anonymous text secrets"},
	"secrets.anonymous_max_bytes": {Label: "Anonymous max size", Kind: kindBytes},
	"media.teasers":               {Label: "Video hover teasers"},
	"stats.retention_days":        {Label: "Keep raw visitor events", Unit: "days"},
	"storage.min_free_bytes":      {Label: "Free disk to keep", Kind: kindBytes},
	"site.name":                   {Label: "Site name"},
	"metrics.per_user":            {Label: "Per-user series", Help: "one series per user id: storage, items and requests"},
	"metrics.per_key":             {Label: "Per-key series", Help: "one series per API key id: requests"},
	"metrics.referrers":           {Label: "Top referrer hosts", Help: "the 20 hosts that sent the most visitors in the last day"},
}

// Groups by key prefix, in this order; others follow alphabetically.
var groupOrder = []string{"auth", "uploads", "items", "secrets", "media", "stats", "storage", "email", "notify", "metrics", "site"}

var groupTitles = map[string]string{
	"auth": "Sign-in", "uploads": "Uploads", "items": "Items", "secrets": "Secrets", "media": "Media",
	"stats": "Stats", "storage": "Storage", "email": "Email sending", "notify": "Notifications", "metrics": "Metrics", "site": "Site",
}

// setting is a registered key with its presentation.
type setting struct {
	def settings.Definition
	hint
}

func describe(d settings.Definition) setting {
	s := setting{def: d, hint: hints[d.Name()]}
	if s.Label == "" {
		s.Label = labelFor(d.Name())
	}
	if s.Kind == "" {
		s.Kind = inferKind(d.Name(), d.DefaultJSON())
	}
	if s.Unit == "" && strings.HasSuffix(d.Name(), "_days") && s.Kind == kindNumber {
		s.Unit = "days"
	}
	return s
}

func inferKind(name, def string) fieldKind {
	switch {
	case def == "true" || def == "false":
		return kindBool
	case strings.HasPrefix(def, `"`):
		return kindText
	case strings.HasPrefix(def, "[") || strings.HasPrefix(def, "{") || def == "null":
		return kindJSON
	case strings.HasSuffix(name, "_bytes"):
		return kindBytes
	case strings.HasSuffix(name, "_seconds"):
		return kindSeconds
	}
	return kindNumber
}

// labelFor turns "notify.daily_cap" into "Daily cap".
func labelFor(name string) string {
	_, rest, ok := strings.Cut(name, ".")
	if !ok {
		rest = name
	}
	rest = strings.ReplaceAll(strings.ReplaceAll(rest, "_", " "), ".", " ")
	r, n := utf8.DecodeRuneInString(rest)
	return string(unicode.ToUpper(r)) + rest[n:]
}

func groupOf(name string) string {
	g, _, _ := strings.Cut(name, ".")
	return g
}

// display renders a stored JSON value for the form.
func (s setting) display(raw json.RawMessage) (value string, checked bool) {
	switch s.Kind {
	case kindBool:
		var b bool
		_ = json.Unmarshal(raw, &b)
		return "", b
	case kindBytes, kindSeconds:
		var n int64
		if json.Unmarshal(raw, &n) != nil {
			return string(raw), false
		}
		if s.Kind == kindBytes {
			return formatSize(n), false
		}
		return formatSeconds(n), false
	case kindSelect, kindText:
		var v string
		_ = json.Unmarshal(raw, &v)
		return v, false
	case kindLines:
		var l []string
		_ = json.Unmarshal(raw, &l)
		return strings.Join(l, "\n"), false
	}
	return string(raw), false
}

// defaultText is the default as the page shows it.
func (s setting) defaultText() string {
	v, on := s.display(json.RawMessage(s.def.DefaultJSON()))
	switch s.Kind {
	case kindBool:
		if on {
			return "on"
		}
		return "off"
	case kindLines:
		if v == "" {
			return "none"
		}
		return strings.ReplaceAll(v, "\n", ", ")
	}
	if s.Unit != "" {
		v += " " + s.Unit
	}
	return dash(v)
}

// parse turns a submitted form value into JSON for SetJSON. It returns a
// message when the value cannot be read.
func (s setting) parse(v string) (json.RawMessage, string) {
	switch s.Kind {
	case kindBool:
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "on", "true", "1":
			return json.RawMessage("true"), ""
		}
		return json.RawMessage("false"), ""
	case kindNumber:
		v = strings.TrimSpace(v)
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return json.RawMessage(strconv.FormatInt(n, 10)), ""
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil && !strings.ContainsAny(v, "eEnN") {
			return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64)), ""
		}
		return nil, "Enter a number."
	case kindBytes:
		n, err := parseSize(v)
		if err != nil {
			return nil, "Use a size like 500 MB or 2 GiB."
		}
		return json.RawMessage(strconv.FormatInt(n, 10)), ""
	case kindSeconds:
		n, err := parseSeconds(v)
		if err != nil {
			return nil, "Use a duration like 30d, 12h or 90m."
		}
		return json.RawMessage(strconv.FormatInt(n, 10)), ""
	case kindSelect:
		if !slices.Contains(s.Options, v) {
			return nil, "Pick one of " + strings.Join(s.Options, ", ") + "."
		}
		b, _ := json.Marshal(v)
		return b, ""
	case kindText:
		b, _ := json.Marshal(strings.TrimSpace(v))
		return b, ""
	case kindLines:
		list := []string{}
		for _, line := range strings.Split(v, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				list = append(list, line)
			}
		}
		b, _ := json.Marshal(list)
		return b, ""
	}
	v = strings.TrimSpace(v)
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(v)) != nil {
		return nil, "Enter valid JSON."
	}
	return buf.Bytes(), ""
}

var numberRE = regexp.MustCompile(`-?\d+`)

// humanize turns a validator error into a sentence, with byte and second
// limits in the units the field uses.
func (s setting) humanize(err error) string {
	msg := strings.TrimPrefix(err.Error(), s.def.Name()+": ")
	if strings.HasPrefix(msg, "json: ") {
		return "That is not the right kind of value for this setting."
	}
	if s.Kind == kindBytes || s.Kind == kindSeconds {
		msg = numberRE.ReplaceAllStringFunc(msg, func(d string) string {
			n, err := strconv.ParseInt(d, 10, 64)
			if err != nil {
				return d
			}
			if s.Kind == kindBytes {
				return formatSize(n)
			}
			return formatSeconds(n)
		})
	}
	return sentence(msg)
}

func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Invalid value."
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// guard checks rules that span settings or configuration. It returns a
// message when raw must not be saved for key.
func (a *Admin) guard(key string, raw json.RawMessage) string {
	switch key {
	case accounts.PasswordLogin.Name():
		if string(raw) == "false" && !a.d.Accounts.OIDCEnabled() {
			return "Without SSO configured, nobody could sign in."
		}
	case accounts.SignupMode.Name():
		if string(raw) == `"`+accounts.SignupOpen+`"` && !a.d.Accounts.MailEnabled() {
			return "Open sign-up needs SMTP, which is not set up."
		}
	}
	return ""
}

// stored reads the settings table: what is saved, by key.
func (a *Admin) stored(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := a.d.DB.R.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

func current(stored map[string]json.RawMessage, d settings.Definition) json.RawMessage {
	if v, ok := stored[d.Name()]; ok {
		return v
	}
	return json.RawMessage(d.DefaultJSON())
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// change is one setting to save.
type change struct {
	s        setting
	raw, old json.RawMessage
	existed  bool
}

// save stores the changes, all or none: when a validator refuses one, the
// ones already stored are put back. It audits each change and returns
// messages by key for the refused ones.
func (a *Admin) save(ctx context.Context, r *http.Request, changes []change) (map[string]string, error) {
	for i, c := range changes {
		if err := c.s.def.SetJSON(ctx, a.d.Settings, c.raw); err != nil {
			if rerr := a.restore(ctx, changes[:i]); rerr != nil {
				return nil, rerr
			}
			return map[string]string{c.s.def.Name(): c.s.humanize(err)}, nil
		}
	}
	for _, c := range changes {
		e := accounts.Actor(r)
		e.Action, e.Target = "settings.change", c.s.def.Name()
		e.Details = map[string]any{"from": c.old, "to": c.raw}
		if err := a.d.Accounts.Store().Audit(ctx, e); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// restore puts settings back as they were before a refused save.
func (a *Admin) restore(ctx context.Context, done []change) error {
	if len(done) == 0 {
		return nil
	}
	err := a.d.DB.Tx(ctx, func(tx *sql.Tx) error {
		for _, c := range done {
			var err error
			if c.existed {
				_, err = tx.ExecContext(ctx, `UPDATE settings SET value = ?, updated_at = ? WHERE key = ?`, string(c.old), db.Millis(time.Now()), c.s.def.Name())
			} else {
				_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, c.s.def.Name())
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return a.d.Settings.Reload(ctx)
}

// fieldView is one control on the settings page.
type fieldView struct {
	Key, ID, Label, Kind, Unit string
	Value                      string
	Checked                    bool
	Options                    []map[string]any
	Default, Help, Error       string
}

// groupIcons are the kit icons beside the settings page's group links.
var groupIcons = map[string]string{
	"auth": "key", "uploads": "upload", "items": "folder", "secrets": "lock", "media": "play",
	"stats": "chart", "storage": "database", "email": "inbox", "notify": "bell", "metrics": "activity", "site": "home",
}

type groupView struct {
	ID, Title, Icon string
	Fields          []fieldView
	Switches        bool // every field is a Switch: no Save button while JS runs
}

type settingsData struct {
	base
	Groups      []groupView
	MailEnabled bool
	OIDCEnabled bool
	TestTo      string
	TestError   string
}

func fieldID(key string) string { return "set-" + strings.ReplaceAll(key, ".", "-") }

// settingsData builds the page. Submitted values and errors (by key) replace
// what is stored for the fields they name.
func (a *Admin) settingsData(r *http.Request, submitted map[string]string, errs map[string]string) (*settingsData, error) {
	stored, err := a.stored(r.Context())
	if err != nil {
		return nil, err
	}
	d := &settingsData{base: a.base(r, "/admin/settings"), MailEnabled: a.d.Accounts.MailEnabled(), OIDCEnabled: a.d.Accounts.OIDCEnabled()}
	if u, err := a.d.Accounts.CurrentUser(r); err == nil {
		d.TestTo = u.Email
	}
	byGroup := map[string]*groupView{}
	var order []string
	for _, def := range settings.Definitions() {
		s := describe(def)
		f := fieldView{
			Key: def.Name(), ID: fieldID(def.Name()), Label: s.Label, Kind: string(s.Kind), Unit: s.Unit,
			Default: s.defaultText(), Help: s.Help, Error: errs[def.Name()],
		}
		f.Value, f.Checked = s.display(current(stored, def))
		if v, ok := submitted[def.Name()]; ok {
			if s.Kind == kindBool {
				raw, _ := s.parse(v)
				f.Checked = string(raw) == "true"
			} else {
				f.Value = v
			}
		}
		for _, o := range s.Options {
			f.Options = append(f.Options, map[string]any{"value": o, "label": o})
		}
		switch {
		case def.Name() == accounts.PasswordLogin.Name() && !d.OIDCEnabled:
			f.Help = "stays on while no SSO is configured"
		case def.Name() == accounts.SignupMode.Name() && !d.MailEnabled:
			f.Help = "open needs SMTP, which is not set up"
		}
		g := groupOf(def.Name())
		gv, ok := byGroup[g]
		if !ok {
			title := groupTitles[g]
			if title == "" {
				title = labelFor(g)
			}
			gv = &groupView{ID: "group-" + g, Title: title, Icon: groupIcons[g], Switches: true}
			byGroup[g] = gv
			order = append(order, g)
		}
		gv.Fields = append(gv.Fields, f)
		if s.Kind != kindBool {
			gv.Switches = false
		}
	}
	rank := func(g string) int {
		if i := slices.Index(groupOrder, g); i >= 0 {
			return i
		}
		return len(groupOrder)
	}
	sort.SliceStable(order, func(i, j int) bool {
		ri, rj := rank(order[i]), rank(order[j])
		if ri != rj {
			return ri < rj
		}
		return order[i] < order[j]
	})
	for _, g := range order {
		d.Groups = append(d.Groups, *byGroup[g])
	}
	return d, nil
}

func (a *Admin) renderSettings(w http.ResponseWriter, r *http.Request, status int, d *settingsData) {
	a.page(w, r, status, "admin/settings", "Settings", d)
}

func (a *Admin) settingsPage(w http.ResponseWriter, r *http.Request) {
	d, err := a.settingsData(r, nil, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.renderSettings(w, r, http.StatusOK, d)
}

// prepare reads key=value pairs into changes against what is stored. It
// returns field messages for values that cannot be read or are not allowed.
func (a *Admin) prepare(ctx context.Context, values map[string]string) ([]change, map[string]string, error) {
	stored, err := a.stored(ctx)
	if err != nil {
		return nil, nil, err
	}
	errs := map[string]string{}
	var out []change
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		def, ok := settings.Lookup(k)
		if !ok {
			errs[k] = "Unknown setting."
			continue
		}
		s := describe(def)
		raw, msg := s.parse(values[k])
		if msg == "" {
			msg = a.guard(k, raw)
		}
		if msg != "" {
			errs[k] = msg
			continue
		}
		old, existed := stored[k]
		if !existed {
			old = json.RawMessage(def.DefaultJSON())
		}
		if sameJSON(old, raw) {
			continue
		}
		out = append(out, change{s: s, raw: raw, old: old, existed: existed})
	}
	return out, errs, nil
}

// saveSettings handles POST /admin/settings: one group's form. "keys" lists
// every setting on the form, so a Switch left off reads as false.
func (a *Admin) saveSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		a.d.Errors.Error(w, r, http.StatusBadRequest, "Invalid form.")
		return
	}
	values := map[string]string{}
	for _, k := range r.PostForm["keys"] {
		values[k] = r.PostForm.Get(k)
	}
	changes, errs, err := a.prepare(ctx, values)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(errs) == 0 {
		if errs, err = a.save(ctx, r, changes); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if len(errs) > 0 {
		d, err := a.settingsData(r, values, errs)
		if err != nil {
			a.fail(w, r, err)
			return
		}
		d.Error = "Nothing was saved. Fix the fields marked below."
		a.renderSettings(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	group := strings.TrimPrefix(r.PostFormValue("group"), "group-")
	to := "/admin/settings?done=settings"
	if group != "" && !strings.ContainsAny(group, "#?/&") {
		to += "#group-" + group
	}
	redirect(w, r, to)
}

// applySetting handles POST /admin/settings/apply (key, value): one setting,
// at once, answered with JSON. The settings page's Switches use it.
func (a *Admin) applySetting(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PostFormValue("key")
	def, ok := settings.Lookup(key)
	if !ok {
		web.JSON(w, http.StatusNotFound, map[string]string{"error": "Unknown setting."})
		return
	}
	changes, errs, err := a.prepare(ctx, map[string]string{key: r.PostFormValue("value")})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if len(errs) == 0 {
		if errs, err = a.save(ctx, r, changes); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if msg := errs[key]; msg != "" {
		web.JSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
		return
	}
	stored, err := a.stored(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	raw := current(stored, def)
	text, on := describe(def).display(raw)
	web.JSON(w, http.StatusOK, map[string]any{"key": key, "value": raw, "text": text, "on": on})
}

// testEmail handles POST /admin/settings/test-email.
func (a *Admin) testEmail(w http.ResponseWriter, r *http.Request) {
	d, err := a.settingsData(r, nil, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	to, err := accounts.NormalizeEmail(r.PostFormValue("to"))
	if err != nil {
		d.TestTo, d.TestError = r.PostFormValue("to"), "Enter a valid email address."
		a.renderSettings(w, r, http.StatusUnprocessableEntity, d)
		return
	}
	d.TestTo = to
	p := auth.FromContext(r.Context())
	if err := a.d.Accounts.SendMail(r.Context(), mail.KindTest, to, mail.Data{By: p.Username}); err != nil {
		d.Error = "Sending failed: " + err.Error()
		a.renderSettings(w, r, http.StatusBadGateway, d)
		return
	}
	d.Notice = "Test email sent to " + to + "."
	a.renderSettings(w, r, http.StatusOK, d)
}
