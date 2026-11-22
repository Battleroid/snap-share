package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Battleroid/snap-share/internal/accounts"
	"github.com/Battleroid/snap-share/internal/web"
)

// sparkDays is the overview's upload history.
const sparkDays = 30

type overviewData struct {
	base
	UsersActive, UsersDisabled int
	Items                      int
	Used, UsedUnit             string // "4.1", "MB"
	Free                       string // "" when unknown
	SecretsActive              int
	Uploads                    int // over sparkDays
	Today                      int
	Tile                       spark // StatTile background sparkline, 200×48
	Recent                     []auditRow
}

// spark is an SVG sparkline: the line and the area under it.
type spark struct {
	W, H       int
	Line, Area string
}

// sparkPath draws vals (oldest first) on a w×h box with pad pixels top and
// bottom, from a zero baseline.
func sparkPath(vals []int, w, h, pad int) spark {
	sp := spark{W: w, H: h}
	if len(vals) < 2 {
		return sp
	}
	top := 0
	for _, v := range vals {
		top = max(top, v)
	}
	var b strings.Builder
	for i, v := range vals {
		x := float64(i) * float64(w) / float64(len(vals)-1)
		y := float64(h - pad)
		if top > 0 {
			y = float64(pad) + float64(h-2*pad)*(1-float64(v)/float64(top))
		}
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString(" L")
		}
		b.WriteString(strconv.FormatFloat(x, 'f', 1, 64) + " " + strconv.FormatFloat(y, 'f', 1, 64))
	}
	sp.Line = b.String()
	sp.Area = sp.Line + " L" + strconv.Itoa(w) + " " + strconv.Itoa(h) + " L0 " + strconv.Itoa(h) + " Z"
	return sp
}

func (a *Admin) overviewPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := overviewData{base: a.base(r, "/admin")}
	var err error
	if d.UsersActive, d.UsersDisabled, err = a.d.Accounts.Store().UserCounts(ctx); err != nil {
		a.fail(w, r, err)
		return
	}
	count, used, err := a.d.Items.Totals(ctx)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Items = count
	d.Used, d.UsedUnit, _ = strings.Cut(web.HumanBytes(used), " ")
	if free, ok := diskFree(a.d.Cfg.FilesDir); ok {
		d.Free = web.HumanBytes(free)
	}
	if d.SecretsActive, err = a.d.Secrets.CountActive(ctx); err != nil {
		a.fail(w, r, err)
		return
	}
	perDay, err := a.d.Items.CreatedPerDay(ctx, sparkDays, a.now())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	for _, n := range perDay {
		d.Uploads += n
	}
	d.Today = perDay[len(perDay)-1]
	d.Tile = sparkPath(perDay, 200, 48, 6)
	recent, _, err := a.d.Accounts.Store().ListAudit(ctx, accounts.AuditFilter{Limit: 8})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d.Recent = auditRows(recent)
	a.page(w, r, http.StatusOK, "admin/overview", "Admin", d)
}
