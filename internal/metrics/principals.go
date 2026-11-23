package metrics

import (
	"strconv"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Battleroid/snap-share/internal/settings"
)

// MaxSeries caps each opt-in family (per user, per key, referrers): ids
// beyond it are counted under "other".
const MaxSeries = 500

var (
	userRequestsDesc = prometheus.NewDesc("snap_user_requests_total",
		"Requests by user id (metrics.per_user). Since the setting was last turned on or the server started.", []string{"user_id"}, nil)
	keyRequestsDesc = prometheus.NewDesc("snap_key_requests_total",
		"Requests by API key id (metrics.per_key). Since the setting was last turned on or the server started.", []string{"key_id"}, nil)
)

// principals counts requests per user and per key while the settings are
// on. It is a collector rather than a CounterVec so turning a setting off
// drops its series at once, and so the number of series stays capped.
type principals struct {
	settings *settings.Store
	max      int

	mu    sync.Mutex
	users map[string]float64
	keys  map[string]float64
}

func newPrincipals(st *settings.Store) *principals {
	return &principals{settings: st, max: MaxSeries, users: map[string]float64{}, keys: map[string]float64{}}
}

func (p *principals) on(k interface{ Get(*settings.Store) bool }) bool {
	return p.settings != nil && k.Get(p.settings)
}

func (p *principals) request(userID, keyID int64) {
	perUser, perKey := p.on(PerUser), p.on(PerKey)
	if !perUser && !perKey {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if perUser && userID != 0 {
		add(p.users, strconv.FormatInt(userID, 10), p.max)
	}
	if perKey && keyID != 0 {
		add(p.keys, strconv.FormatInt(keyID, 10), p.max)
	}
}

// add counts id, or Other once the map holds max ids.
func add(m map[string]float64, id string, max int) {
	if _, ok := m[id]; !ok && len(m) >= max {
		id = Other
	}
	m[id]++
}

func (p *principals) Describe(ch chan<- *prometheus.Desc) {
	ch <- userRequestsDesc
	ch <- keyRequestsDesc
}

func (p *principals) Collect(ch chan<- prometheus.Metric) {
	perUser, perKey := p.on(PerUser), p.on(PerKey)
	p.mu.Lock()
	defer p.mu.Unlock()
	emit := func(on bool, m *map[string]float64, d *prometheus.Desc) {
		if !on {
			if len(*m) > 0 {
				*m = map[string]float64{}
			}
			return
		}
		for id, n := range *m {
			ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, n, id)
		}
	}
	emit(perUser, &p.users, userRequestsDesc)
	emit(perKey, &p.keys, keyRequestsDesc)
}
