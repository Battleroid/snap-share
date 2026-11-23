package metrics

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/db"
	"github.com/Battleroid/snap-share/internal/settings"
)

// CacheFor is how long one read of the database serves scrapes.
const CacheFor = 30 * time.Second

// TopReferrers is how many referrer hosts metrics.referrers exports.
const TopReferrers = 20

// maxHostLen cuts referrer hosts (they are chosen by whoever links to an item).
const maxHostLen = 64

// InstanceOptions configure the database-derived gauges.
type InstanceOptions struct {
	DB         *db.DB
	Settings   *settings.Store
	Log        *slog.Logger
	DataDir    string // snap.db and its WAL
	FilesDir   string
	SecretsDir string
	// JobKinds lists the registered job kinds; rows of any other kind are
	// counted as "other".
	JobKinds func() []string
	Now      func() time.Time
}

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(name, help, labels, nil)
}

var (
	usersDesc        = desc("snap_users", "Accounts by status and role.", "status", "role")
	itemsDesc        = desc("snap_items", "Items by kind.", "kind")
	itemBytesDesc    = desc("snap_item_bytes", "Bytes stored by items, by kind.", "kind")
	secretsDesc      = desc("snap_secrets", "Secrets by state.", "state")
	activeSecretDesc = desc("snap_secrets_active", "Secrets that can still be opened (ready and not expired).")
	secretBytesDesc  = desc("snap_secret_bytes", "Ciphertext bytes held for secrets that are uploading or ready.")
	sessionsDesc     = desc("snap_sessions_active", "Browser sessions that have not expired.")
	keysDesc         = desc("snap_api_keys_active", "API keys neither revoked nor expired.")
	jobsDesc         = desc("snap_jobs", "Queued jobs by kind and state (finished jobs are kept for a week).", "kind", "state")
	usedDesc         = desc("snap_storage_used_bytes", "Bytes charged to user quotas.")
	freeDesc         = desc("snap_disk_free_bytes", "Free disk space available to the server, by store.", "store")
	dbSizeDesc       = desc("snap_db_size_bytes", "Size of the SQLite database file.")
	walSizeDesc      = desc("snap_db_wal_size_bytes", "Size of the SQLite write-ahead log.")
	scrapeErrDesc    = desc("snap_instance_scrape_errors_total", "Failed reads of the instance gauges (the last good values are served).")
	scrapeAgeDesc    = desc("snap_instance_scrape_timestamp_seconds", "When the instance gauges were last read from the database.")

	userBytesDesc = desc("snap_user_storage_bytes", "Bytes charged to each user's quota (metrics.per_user; the largest users).", "user_id")
	userItemsDesc = desc("snap_user_items", "Items per user (metrics.per_user; the same users as snap_user_storage_bytes).", "user_id")
	referrerDesc  = desc("snap_referrer_events_24h", "Visitor events in the last 24 hours by referrer host (metrics.referrers; the top hosts, the rest as other).", "host")
)

type sample struct {
	d      *prometheus.Desc
	v      float64
	labels []string
}

// Instance is a collector of gauges read from the database (db.R) and the
// filesystem. A read serves scrapes for CacheFor; one read runs at a time,
// and a failed read keeps the last good values.
type Instance struct {
	o InstanceOptions

	mu      sync.Mutex
	at      time.Time // last read, good or not
	okAt    time.Time // last good read
	samples []sample
	optUser []sample // metrics.per_user
	optRef  []sample // metrics.referrers
	errors  float64
}

// NewInstance builds the collector; register it on the metrics registry.
func NewInstance(o InstanceOptions) *Instance {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.JobKinds == nil {
		o.JobKinds = func() []string { return nil }
	}
	return &Instance{o: o}
}

// Describe sends nothing: the collector is unchecked, as its opt-in series
// come and go with the settings.
func (c *Instance) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (c *Instance) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.o.Now()
	if c.at.IsZero() || now.Sub(c.at) >= CacheFor {
		c.refresh(now)
	}
	send := func(ss []sample) {
		for _, s := range ss {
			ch <- prometheus.MustNewConstMetric(s.d, prometheus.GaugeValue, s.v, s.labels...)
		}
	}
	send(c.samples)
	if c.o.Settings != nil && PerUser.Get(c.o.Settings) {
		send(c.optUser)
	}
	if c.o.Settings != nil && Referrers.Get(c.o.Settings) {
		send(c.optRef)
	}
	ch <- prometheus.MustNewConstMetric(scrapeErrDesc, prometheus.CounterValue, c.errors)
	if !c.okAt.IsZero() {
		ch <- prometheus.MustNewConstMetric(scrapeAgeDesc, prometheus.GaugeValue, float64(c.okAt.Unix()))
	}
}

// Refresh forces the next scrape to read the database (tests).
func (c *Instance) Refresh() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// refresh reads everything. Call with c.mu held.
func (c *Instance) refresh(now time.Time) {
	// Well inside Prometheus's default 10 s scrape timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ss, err := c.read(ctx, now)
	if err != nil {
		c.errors++
		c.o.Log.Warn("metrics: reading instance gauges failed", "err", err)
		c.at = now // serve the last values until the next try, not a retry per scrape
		return
	}
	c.samples = ss
	c.optUser, c.optRef = nil, nil
	if c.o.Settings != nil && PerUser.Get(c.o.Settings) {
		if c.optUser, err = c.perUser(ctx); err != nil {
			c.errors++
			c.o.Log.Warn("metrics: reading per-user gauges failed", "err", err)
		}
	}
	if c.o.Settings != nil && Referrers.Get(c.o.Settings) {
		if c.optRef, err = c.referrers(ctx, now); err != nil {
			c.errors++
			c.o.Log.Warn("metrics: reading referrers failed", "err", err)
		}
	}
	c.at, c.okAt = now, now
}

// query scans every row with scan and closes the rows before returning.
func (c *Instance) query(ctx context.Context, scan func(*sql.Rows) error, q string, args ...any) error {
	rows, err := c.o.DB.R.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (c *Instance) read(ctx context.Context, now time.Time) ([]sample, error) {
	var out []sample
	ms := db.Millis(now)

	// Every known label combination is sent, zero or not, so series don't
	// appear and vanish.
	users := map[[2]string]float64{}
	for _, s := range UserStatuses {
		for _, r := range UserRoles {
			users[[2]string{s, r}] = 0
		}
	}
	if err := c.query(ctx, func(rows *sql.Rows) error {
		var st, role string
		var n float64
		if err := rows.Scan(&st, &role, &n); err != nil {
			return err
		}
		users[[2]string{oneOf(st, UserStatuses), oneOf(role, UserRoles)}] += n
		return nil
	}, `SELECT status, role, count(*) FROM users GROUP BY status, role`); err != nil {
		return nil, err
	}
	for k, n := range users {
		out = append(out, sample{usersDesc, n, []string{k[0], k[1]}})
	}

	items, bytes := zeros(ItemKinds), zeros(ItemKinds)
	if err := c.query(ctx, func(rows *sql.Rows) error {
		var kind string
		var n, b float64
		if err := rows.Scan(&kind, &n, &b); err != nil {
			return err
		}
		k := oneOf(kind, ItemKinds)
		items[k] += n
		bytes[k] += b
		return nil
	}, `SELECT kind, count(*), coalesce(sum(size), 0) FROM items GROUP BY kind`); err != nil {
		return nil, err
	}
	for k, n := range items {
		out = append(out, sample{itemsDesc, n, []string{k}}, sample{itemBytesDesc, bytes[k], []string{k}})
	}

	secrets := zeros(SecretStates)
	if err := c.query(ctx, func(rows *sql.Rows) error {
		var st string
		var n float64
		if err := rows.Scan(&st, &n); err != nil {
			return err
		}
		secrets[oneOf(st, SecretStates)] += n
		return nil
	}, `SELECT state, count(*) FROM secrets GROUP BY state`); err != nil {
		return nil, err
	}
	for k, n := range secrets {
		out = append(out, sample{secretsDesc, n, []string{k}})
	}

	single := []struct {
		d    *prometheus.Desc
		q    string
		args []any
	}{
		{activeSecretDesc, `SELECT count(*) FROM secrets WHERE state = 'ready' AND expires_at > ?`, []any{ms}},
		{secretBytesDesc, `SELECT coalesce(sum(ct_size), 0) FROM secrets WHERE state IN ('uploading', 'ready')`, nil},
		{sessionsDesc, `SELECT count(*) FROM sessions WHERE expires_at > ?`, []any{ms}},
		{keysDesc, `SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, []any{ms}},
		{usedDesc, `SELECT coalesce(sum(used_bytes), 0) FROM users`, nil},
	}
	for _, s := range single {
		var v float64
		if err := c.o.DB.R.QueryRowContext(ctx, s.q, s.args...).Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, sample{s.d, v, nil})
	}

	kinds := c.o.JobKinds()
	jobs := map[[2]string]float64{}
	for _, k := range append(slices.Clone(kinds), Other) {
		for _, st := range JobStates {
			jobs[[2]string{k, st}] = 0
		}
	}
	if err := c.query(ctx, func(rows *sql.Rows) error {
		var kind, st string
		var n float64
		if err := rows.Scan(&kind, &st, &n); err != nil {
			return err
		}
		jobs[[2]string{oneOf(kind, kinds), oneOf(st, JobStates)}] += n
		return nil
	}, `SELECT kind, state, count(*) FROM jobs GROUP BY kind, state`); err != nil {
		return nil, err
	}
	for k, n := range jobs {
		out = append(out, sample{jobsDesc, n, []string{k[0], k[1]}})
	}

	for store, dir := range map[string]string{"files": c.o.FilesDir, "secrets": c.o.SecretsDir, "data": c.o.DataDir} {
		if dir == "" {
			continue
		}
		if free, ok := diskFree(dir); ok {
			out = append(out, sample{freeDesc, float64(free), []string{store}})
		}
	}
	for d, name := range map[*prometheus.Desc]string{dbSizeDesc: "snap.db", walSizeDesc: "snap.db-wal"} {
		var size float64
		if st, err := os.Stat(filepath.Join(c.o.DataDir, name)); err == nil {
			size = float64(st.Size())
		}
		out = append(out, sample{d, size, nil})
	}
	return out, nil
}

func zeros(set []string) map[string]float64 {
	m := make(map[string]float64, len(set)+1)
	for _, k := range set {
		m[k] = 0
	}
	m[Other] = 0
	return m
}

// perUser reads the MaxSeries users with the most stored bytes.
func (c *Instance) perUser(ctx context.Context) ([]sample, error) {
	var out []sample
	err := c.query(ctx, func(rows *sql.Rows) error {
		var id int64
		var b, n float64
		if err := rows.Scan(&id, &b, &n); err != nil {
			return err
		}
		l := []string{strconv.FormatInt(id, 10)}
		out = append(out, sample{userBytesDesc, b, l}, sample{userItemsDesc, n, l})
		return nil
	}, `SELECT u.id, u.used_bytes, (SELECT count(*) FROM items i WHERE i.owner_id = u.id)
		FROM users u ORDER BY u.used_bytes DESC, u.id LIMIT ?`, MaxSeries)
	return out, err
}

// referrers reads the TopReferrers referrer hosts of the last day; the
// rest are summed as "other".
func (c *Instance) referrers(ctx context.Context, now time.Time) ([]sample, error) {
	var out []sample
	var other float64
	err := c.query(ctx, func(rows *sql.Rows) error {
		var host string
		var n float64
		if err := rows.Scan(&host, &n); err != nil {
			return err
		}
		if len(out) >= TopReferrers || len(host) > maxHostLen || !hostname(host) {
			other += n
			return nil
		}
		out = append(out, sample{referrerDesc, n, []string{host}})
		return nil
	}, `SELECT referrer_host, count(*) AS n FROM item_events WHERE at > ? AND referrer_host != ''
		GROUP BY referrer_host ORDER BY n DESC, referrer_host LIMIT ?`,
		db.Millis(now.Add(-24*time.Hour)), TopReferrers*5)
	if err != nil {
		return nil, err
	}
	// Hosts beyond the query's limit are not counted in other: it is the
	// remainder of the hosts read, a lower bound.
	return append(out, sample{referrerDesc, other, []string{Other}}), nil
}

// hostname accepts letters, digits, dots, hyphens and colons (a port or IPv6).
func hostname(h string) bool {
	for i := 0; i < len(h); i++ {
		switch c := h[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(".-:[]", c) >= 0:
		default:
			return false
		}
	}
	return h != Other
}
