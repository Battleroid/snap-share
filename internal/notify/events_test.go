package notify_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Battleroid/snap-share/internal/items"
	"github.com/Battleroid/snap-share/internal/notify"
	"github.com/Battleroid/snap-share/internal/secrets"
	"github.com/Battleroid/snap-share/internal/stats"
)

// bothChannels turns on email and ntfy for ann.
func (e *env) bothChannels() {
	e.t.Helper()
	if err := notify.OutboundAllow.Set(e.ctx, e.s.App.Settings, []string{"127.0.0.1"}); err != nil {
		e.t.Fatal(err)
	}
	for _, err := range []error{
		e.s.App.Notify.SaveNtfy(e.ctx, 1, notify.NtfyInput{Server: e.ntfy.srv.URL, Topic: "ann"}),
		e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelNtfy, true),
		e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true),
	} {
		if err != nil {
			e.t.Fatal(err)
		}
	}
}

// sends returns the queued notify.send jobs as channel → payloads.
func (e *env) sends() map[string][]string {
	e.t.Helper()
	out := map[string][]string{}
	for _, p := range e.jobs(notify.JobSend) {
		var j struct{ Channel string }
		if err := json.Unmarshal([]byte(p), &j); err != nil {
			e.t.Fatal(err)
		}
		out[j.Channel] = append(out[j.Channel], p)
	}
	return out
}

func (e *env) setEvents(channels []string, on ...string) {
	e.t.Helper()
	if err := e.own.SetEvents(e.ctx, 1, channels, on); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) item(id int64) *items.Item {
	e.t.Helper()
	it, err := e.s.App.Items.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return it
}

func countKind(rows []inboxRow, kind string) int {
	n := 0
	for _, r := range rows {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// The defaults are today's behaviour: the bell gets first opens, reveals,
// burns and the new low-volume events; email and ntfy get reveals and
// burns, and item opens only per the old setting.
func TestEventDefaults(t *testing.T) {
	e := newEnv(t)
	p, err := e.own.Prefs(e.ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range notify.Events {
		for _, ch := range notify.AllChannels {
			want := ev.Push
			if ch == notify.ChannelInbox {
				want = ev.Inbox
			}
			if p.Events.On(ev.Name, ch) != want {
				t.Errorf("%s by %s: %v", ev.Name, ch, !want)
			}
		}
	}
	if p.Events.On(notify.EventItemFirstOpen, "email") || p.Events.On(notify.EventItemOpen, "inbox") ||
		!p.Events.On(notify.EventSecretBurned, "ntfy") || !p.Events.On(notify.EventSecretRevealed, "email") {
		t.Fatalf("defaults %+v", p.Events)
	}
	// The old setting still gives the item-open rows until the grid is saved.
	e.exec(`INSERT INTO notify_prefs (user_id, default_event, updated_at) VALUES (2, 'first', 1)`)
	if p, _ := e.own.Prefs(e.ctx, 2); !p.Events.On(notify.EventItemFirstOpen, "email") || p.Events.On(notify.EventItemOpen, "email") {
		t.Fatalf("legacy first %+v", p.Events)
	}
}

// Each event goes once to the channels ticked for it and skips the others;
// saving one column leaves the rest alone; an item set to "off" stays off
// email and ntfy.
func TestEventChannels(t *testing.T) {
	e := newEnv(t)
	e.bothChannels()
	e.setEvents(notify.AllChannels, notify.EventItemExpired+":email", notify.EventItemExpired+":inbox")
	e.own.ItemExpired(e.ctx, e.item(e.photo), false)
	s := e.sends()
	if len(s["email"]) != 1 || len(s["ntfy"]) != 0 || !strings.Contains(s["email"][0], "photo.jpg expired") ||
		countKind(e.inbox(1), notify.InboxItemExpired) != 1 {
		t.Fatalf("expired: %v %+v", s, e.inbox(1))
	}

	// The bell column alone: email keeps its rows.
	e.clearJobs()
	e.setEvents([]string{notify.ChannelInbox})
	e.own.ItemExpired(e.ctx, e.item(e.photo), true)
	if s := e.sends(); len(s["email"]) != 1 || !strings.Contains(s["email"][0], "deleted") || countKind(e.inbox(1), notify.InboxItemExpired) != 1 {
		t.Fatalf("bell off: %v", s)
	}

	// An item set to off gets nothing by email or ntfy.
	e.clearJobs()
	if err := e.own.SetOverride(e.ctx, 1, e.photo, notify.ModeNone); err != nil {
		t.Fatal(err)
	}
	e.own.ItemExpired(e.ctx, e.item(e.photo), false)
	if n := len(e.jobs(notify.JobSend)); n != 0 {
		t.Fatalf("override none: %d sends", n)
	}

	// A deleted item's row is about the account and survives the sweep.
	e.setEvents([]string{notify.ChannelInbox}, notify.EventItemExpired+":inbox")
	e.own.ItemExpired(e.ctx, e.item(e.link), true)
	e.exec(`DELETE FROM items WHERE id = ?`, e.link)
	if err := e.own.SweepInbox(e.ctx); err != nil {
		t.Fatal(err)
	}
	var kept bool
	for _, r := range e.inbox(1) {
		kept = kept || (r.SubjectType == "account" && r.Subject == "1" && r.Link == "/dashboard" && strings.HasSuffix(r.Title, " expired"))
	}
	if !kept {
		t.Fatalf("account row: %+v", e.inbox(1))
	}
}

// An item's own setting keeps deciding whether its opens are wanted: with
// the grid's item-open rows off, "every open" still reaches every channel
// that is on (today's behaviour); with rows ticked, only those channels.
func TestOverridesWinOverTheGrid(t *testing.T) {
	e := newEnv(t)
	e.bothChannels()
	ms := func() int64 { return e.clk.Now().UnixMilli() }
	if err := e.own.SetOverride(e.ctx, 1, e.photo, notify.ModeEvery); err != nil {
		t.Fatal(err)
	}
	e.open(map[string]any{"item_id": e.photo, "first": true, "count": 1, "from": ms(), "to": ms()})
	if s := e.sends(); len(s["email"]) != 1 || len(s["ntfy"]) != 1 {
		t.Fatalf("override every, grid off: %v", s)
	}

	e.clearJobs()
	e.clk.Add(time.Hour)
	e.setEvents(notify.AllChannels, notify.EventItemFirstOpen+":ntfy", notify.EventItemFirstOpen+":inbox")
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	if s := e.sends(); len(s["email"]) != 0 || len(s["ntfy"]) != 1 {
		t.Fatalf("override every, ntfy ticked: %v", s)
	}

	// Without an override the grid decides: first opens by ntfy only, and
	// not other opens.
	e.clearJobs()
	if err := e.own.SetOverride(e.ctx, 1, e.photo, ""); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(time.Hour)
	e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
	e.open(map[string]any{"item_id": e.photo, "first": true, "count": 1, "from": ms(), "to": ms()})
	if s := e.sends(); len(s["email"]) != 0 || len(s["ntfy"]) != 1 {
		t.Fatalf("grid: %v", s)
	}

	// A secret without notify me gets no reveal by email or ntfy, whatever
	// the grid; the bell lists it.
	e.clearJobs()
	e.setEvents(notify.AllChannels, notify.EventSecretRevealed+":email", notify.EventSecretRevealed+":ntfy", notify.EventSecretRevealed+":inbox")
	before := countKind(e.inbox(1), notify.InboxSecretRevealed)
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, SecretID: "QUJDREVGR0hJSktMTU5PUA", Kind: stats.KindRevealed, At: e.clk.Now(), MaxViews: 2})
	e.own.Close()
	if n := len(e.jobs(notify.JobSend)); n != 0 || countKind(e.inbox(1), notify.InboxSecretRevealed) != before+1 {
		t.Fatalf("unrequested reveal: %d sends", n)
	}
}

// Every open to the bell goes through the coalescing window, not a row per
// counter flush.
func TestEveryOpenToTheBellIsCoalesced(t *testing.T) {
	e := newEnv(t)
	e.setEvents([]string{notify.ChannelInbox}, notify.EventItemOpen+":inbox")
	ms := func() int64 { return e.clk.Now().UnixMilli() }
	// With no push channel, the bell alone still queues the open.
	e.own.ItemsOpened(e.ctx, []items.Opened{{ItemID: e.photo, First: true, Count: 1, From: e.clk.Now(), To: e.clk.Now()}})
	if n := len(e.jobs(notify.JobOpen)); n != 1 || countKind(e.inbox(1), notify.InboxItemOpened) != 0 {
		t.Fatalf("flush: %d open jobs, %+v", n, e.inbox(1))
	}
	e.clearJobs()
	for range 3 {
		e.open(map[string]any{"item_id": e.photo, "count": 1, "from": ms(), "to": ms()})
		e.clk.Add(time.Minute)
	}
	if n := countKind(e.inbox(1), notify.InboxItemOpened); n != 1 {
		t.Fatalf("%d rows inside the window", n)
	}
	e.clk.Add(15 * time.Minute)
	e.open(map[string]any{"item_id": e.photo, "summary": true})
	rows := e.inbox(1)
	if countKind(rows, notify.InboxItemOpened) != 2 || !strings.Contains(rows[0].Title, "opened 2 more times") {
		t.Fatalf("summary: %+v", rows)
	}
	if n := len(e.jobs(notify.JobSend)); n != 0 {
		t.Fatalf("%d sends with no channel on", n)
	}
}

// Items expiring within a day are reported once per expiry.
func TestItemExpiringOnce(t *testing.T) {
	e := newEnv(t)
	e.bothChannels()
	e.setEvents([]string{notify.ChannelEmail}, notify.EventItemExpiring+":email")
	soon := time.Now().Add(3 * time.Hour)
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, soon.UnixMilli(), e.photo)
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, time.Now().Add(48*time.Hour).UnixMilli(), e.link) // too far
	for range 2 {
		if err := e.own.SweepExpiring(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	s := e.sends()
	if countKind(e.inbox(1), notify.InboxItemExpiring) != 1 || len(s["email"]) != 1 || len(s["ntfy"]) != 0 ||
		!strings.Contains(s["email"][0], "photo.jpg expires within a day") {
		t.Fatalf("expiring: %v %+v", s, e.inbox(1))
	}
	// A new expiry is reported again.
	e.exec(`UPDATE items SET expires_at = ? WHERE id = ?`, soon.Add(time.Hour).UnixMilli(), e.photo)
	if err := e.own.SweepExpiring(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := countKind(e.inbox(1), notify.InboxItemExpiring); n != 2 {
		t.Fatalf("new expiry: %d rows", n)
	}
}

// Storage fires once per crossing; a jump past both levels fires 95% only,
// and dropping below re-arms it. Users without a quota get nothing.
func TestQuotaOncePerCrossing(t *testing.T) {
	e := newEnv(t)
	e.bothChannels()
	e.setEvents([]string{notify.ChannelEmail}, notify.EventQuota80+":email", notify.EventQuota95+":email")
	use := func(n int64) {
		e.t.Helper()
		e.exec(`UPDATE users SET used_bytes = ? WHERE id = 1`, n)
		e.own.QuotaChanged(e.ctx, 1)
	}
	use(900) // no quota yet
	e.exec(`UPDATE users SET quota_bytes = 1000 WHERE id = 1`)
	for _, n := range []int64{500, 850, 870, 960, 990} {
		use(n)
	}
	rows := e.inbox(1)
	if countKind(rows, notify.InboxQuota) != 2 || rows[0].Status != "down" || rows[1].Status != "warn" ||
		!strings.HasPrefix(rows[1].Title, "Storage 85% used") || rows[0].SubjectType != "account" {
		t.Fatalf("crossings: %+v", rows)
	}
	use(100)
	use(990) // straight past both
	rows = e.inbox(1)
	if countKind(rows, notify.InboxQuota) != 3 || rows[0].Status != "down" {
		t.Fatalf("re-armed: %+v", rows)
	}
	if n := len(e.sends()["email"]); n != 3 {
		t.Fatalf("%d emails", n)
	}
}

// Wrong passphrases are coalesced per secret; a burn ends the window.
func TestWrongPassphraseCoalesced(t *testing.T) {
	e := newEnv(t)
	e.bothChannels()
	e.setEvents([]string{notify.ChannelEmail}, notify.EventSecretWrongPassphrase+":email", notify.EventSecretBurned+":email")
	const id = "QUJDREVGR0hJSktMTU5PUA"
	wrong := secrets.Notification{OwnerID: 1, SecretID: id, Note: "prod db", Kind: stats.KindFailed, Failures: 1, MaxFailures: 5, At: e.clk.Now()}
	e.own.Notify(e.ctx, wrong)
	e.own.Close()
	for i := 2; i <= 3; i++ {
		e.clk.Add(time.Minute)
		wrong.Failures, wrong.At = i, e.clk.Now()
		e.own.Notify(e.ctx, wrong)
		e.own.Close()
	}
	s := e.sends()
	summaries := e.jobs(notify.JobSecret)
	if len(s["email"]) != 1 || !strings.Contains(s["email"][0], "wrong passphrase") || !strings.Contains(s["email"][0], "4 attempts left") ||
		len(summaries) != 1 || countKind(e.inbox(1), notify.InboxSecretWrongPass) != 1 {
		t.Fatalf("first: %v %v", s, summaries)
	}
	e.clk.Add(15 * time.Minute)
	if err := e.own.HandleSecret(e.ctx, json.RawMessage(summaries[0])); err != nil {
		t.Fatal(err)
	}
	s = e.sends()
	if len(s["email"]) != 2 || !strings.Contains(s["email"][1], "2 more wrong passphrases") || countKind(e.inbox(1), notify.InboxSecretWrongPass) != 2 {
		t.Fatalf("summary: %v", s)
	}

	// Another wrong one starts a held window; the burn drops what it held.
	e.clk.Add(time.Minute)
	e.own.Notify(e.ctx, wrong)
	e.own.Close()
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, SecretID: id, Kind: stats.KindBurned, At: e.clk.Now()})
	e.own.Close()
	e.clk.Add(15 * time.Minute)
	for _, p := range e.jobs(notify.JobSecret) {
		if err := e.own.HandleSecret(e.ctx, json.RawMessage(p)); err != nil {
			t.Fatal(err)
		}
	}
	s = e.sends()
	if len(s["email"]) != 3 || !strings.Contains(s["email"][2], "secret_burned") {
		t.Fatalf("after the burn: %v", s["email"])
	}
}

// The hourly cap holds for the new secret events; burns stay uncapped.
func TestCapsHoldForNewEvents(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 1, notify.ChannelEmail, true)
	e.setEvents([]string{notify.ChannelEmail}, notify.EventSecretRevealed+":email", notify.EventSecretRevokedByAdmin+":email",
		notify.EventSecretBurned+":email")
	for range notify.MessagesPerHour {
		e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindRevealed, Requested: true, At: e.clk.Now(), MaxViews: 9})
	}
	e.own.Close() // the reveals use up the hour first
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: secrets.NotifyRemoved, At: e.clk.Now()})
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, Kind: stats.KindBurned, At: e.clk.Now()})
	e.own.Close()
	s := e.sends()["email"]
	removed := 0
	for _, p := range s {
		if strings.Contains(p, "secret_removed") {
			removed++
		}
	}
	if len(s) != notify.MessagesPerHour+1 || removed != 0 {
		t.Fatalf("%d sends, %d removals", len(s), removed)
	}
}

// Expiry and quota events reach only the owner.
func TestEventsReachOnlyTheOwner(t *testing.T) {
	e := newEnv(t)
	_ = e.s.App.Notify.SetChannel(e.ctx, 2, notify.ChannelEmail, true)
	if err := e.own.SetEvents(e.ctx, 2, notify.AllChannels, allOn()); err != nil {
		t.Fatal(err)
	}
	e.own.ItemExpired(e.ctx, e.item(e.photo), false)
	e.exec(`UPDATE users SET quota_bytes = 1000, used_bytes = 990 WHERE id = 1`)
	e.own.QuotaChanged(e.ctx, 1)
	e.own.Notify(e.ctx, secrets.Notification{OwnerID: 1, SecretID: "QUJDREVGR0hJSktMTU5PUA", Kind: secrets.NotifyRemoved, At: e.clk.Now()})
	e.own.Close()
	if rows := e.inbox(2); len(rows) != 0 {
		t.Fatalf("bob's inbox: %+v", rows)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND payload LIKE '%"user_id":2%'`, notify.JobSend); n != 0 {
		t.Fatalf("%d sends to bob", n)
	}
	if len(e.inbox(1)) != 3 {
		t.Fatalf("ann's inbox: %+v", e.inbox(1))
	}
}

func allOn() []string {
	var out []string
	for _, ev := range notify.Events {
		for _, ch := range notify.AllChannels {
			out = append(out, ev.Name+":"+ch)
		}
	}
	return out
}
