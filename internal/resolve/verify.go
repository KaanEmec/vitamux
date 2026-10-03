package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/catalog"
	"github.com/KaanEmec/vitamux/internal/db"
)

// VerifyReport is what Verify compared and every pair whose cached results differ from live ones.
type VerifyReport struct {
	Windows int
	Diffs   []VerifyDiff
}

// VerifyDiff is one (metric, date) whose cached results differ; Window is the key of the first
// differing window. It holds no values.
type VerifyDiff struct {
	UserID uuid.UUID
	Metric string
	Date   time.Time
	Window string
}

func (v VerifyDiff) String() string {
	return fmt.Sprintf("%s %s %s: window %s differs", v.UserID, v.Metric, v.Date.Format(dateLayout), v.Window)
}

// verifyChunk is how many days Verify fills the cache with per request.
const verifyChunk = 31

// Verify compares resolved_cache with live computation (`vitamux resolve verify`). For every
// owner it fills the cache for each metric with a rule in effect (bucket windows are never cached)
// on the local dates from through to, in its rule's window (dates already cached are kept, so stale rows stay to be found), then
// resolves n random (metric, date) pairs once from the cache and once live and compares them,
// computed_at aside. A date resolved in a range must equal the same date resolved alone.
func Verify(ctx context.Context, d *db.DB, from, to time.Time, n int, rnd *rand.Rand) (VerifyReport, error) {
	from, to = midnightUTC(from), midnightUTC(to)
	users, err := d.Q().ListResolveUsers(ctx)
	if err != nil {
		return VerifyReport{}, db.MapErr(err)
	}
	type pair struct {
		user   uuid.UUID
		metric string
	}
	var metrics []pair
	for _, u := range users {
		set, err := NewStore(d).ActiveSet(ctx, u)
		if err != nil {
			return VerifyReport{}, err
		}
		for _, v := range set {
			if v.Rule.Window.Kind == catalog.WindowBucket { // never cached
				continue
			}
			metrics = append(metrics, pair{u, v.Metric})
			for c := from; !c.After(to); c = c.AddDate(0, 0, verifyChunk) {
				end := c.AddDate(0, 0, verifyChunk-1)
				if end.After(to) {
					end = to
				}
				if _, err := Run(ctx, d, Request{UserID: u, Metric: v.Metric, From: c, To: end}); err != nil {
					return VerifyReport{}, fmt.Errorf("verify: fill %s: %w", v.Metric, err)
				}
			}
		}
	}
	var rep VerifyReport
	if len(metrics) == 0 {
		return rep, nil
	}
	days := int(to.Sub(from)/(24*time.Hour)) + 1
	for range n {
		p := metrics[rnd.IntN(len(metrics))]
		date := from.AddDate(0, 0, rnd.IntN(days))
		req := Request{UserID: p.user, Metric: p.metric, From: date, To: date}
		cached, err := Run(ctx, d, req)
		if err != nil {
			return rep, err
		}
		req.Live = true
		live, err := Run(ctx, d, req)
		if err != nil {
			return rep, err
		}
		rep.Windows++
		if key, same := sameResults(cached, live); !same {
			rep.Diffs = append(rep.Diffs, VerifyDiff{UserID: p.user, Metric: p.metric, Date: date, Window: key})
		}
	}
	return rep, nil
}

// sameResults compares two result lists by their JSON without computed_at; when they differ it
// returns the key of the first differing window.
func sameResults(a, b []Result) (string, bool) {
	for i := range max(len(a), len(b)) {
		if i >= len(a) || i >= len(b) {
			return "(count)", false
		}
		x, y := a[i], b[i]
		x.ComputedAt, y.ComputedAt = time.Time{}, time.Time{}
		jx, errX := json.Marshal(x)
		jy, errY := json.Marshal(y)
		if errX != nil || errY != nil || string(jx) != string(jy) {
			return a[i].Window.Key, false
		}
	}
	return "", true
}
