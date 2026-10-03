package resolve

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Property: a denser source does not weigh more. Repeating a source's samples inside their
// buckets (any factor, any order) leaves every result's value, coverage, statuses and
// selection unchanged, for pooling and selecting ops alike (resolution.md#within-source-aggregation).
func TestPropertyDensityInvariance(t *testing.T) {
	rng := rand.New(rand.NewPCG(8, 9))
	hours, _ := Buckets(date("2026-09-14"), time.Hour, berlin)
	srcs := []Source{exWatch, exRing, exGarmin}
	for iter := range 150 {
		op := []Op{OpMean, OpMin, OpMax, OpFirstAvailable, OpLatest}[rng.IntN(5)]
		r := &Rule{Schema: SchemaV1, Metric: "heart_rate", Window: RuleWindow{Kind: catalog.WindowHour}, Strategy: Strategy{Op: op},
			Groups: []Group{{ID: "watch", Match: []Selector{{DeviceType: "watch", Provider: "apple_health"}}},
				{ID: "ring", Match: []Selector{{DeviceType: "ring"}}}, {ID: "garmin", Match: []Selector{{Provider: "garmin"}}}},
			Quality: &Quality{MinCoverage: biRatio(rng.Float64() * 0.6)}}
		var in []Input
		for _, src := range srcs {
			for range rng.IntN(60) {
				h := hours[8+rng.IntN(4)]
				in = append(in, exSample(src, h.Start.Add(time.Duration(rng.Int64N(int64(time.Hour)))), float64(40+rng.IntN(120))))
			}
		}
		dense := slices.Clone(in)
		heavy := srcs[rng.IntN(len(srcs))]
		k := 2 + rng.IntN(30)
		for _, x := range in {
			if x.Source == heavy {
				for range k - 1 {
					dense = append(dense, x)
				}
			}
		}
		rng.Shuffle(len(dense), func(i, j int) { dense[i], dense[j] = dense[j], dense[i] })
		ws := hours[6:14]
		a, err := r.ResolveWindows(ws, Series{"heart_rate": in}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.ResolveWindows(ws, Series{"heart_rate": dense}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		for i := range a {
			ra := BuildResult("heart_rate", testVersion(r), Resolved{WindowResult: a[i]}, time.Time{})
			rb := BuildResult("heart_rate", testVersion(r), Resolved{WindowResult: b[i]}, time.Time{})
			if ra.Status != rb.Status || (ra.Value == nil) != (rb.Value == nil) || ra.Value != nil && !near(*ra.Value, *rb.Value) ||
				!near(ra.Coverage, rb.Coverage) || ra.Selected != rb.Selected {
				t.Fatalf("iteration %d (%s, %s x%d) window %s:\n %s\n %s", iter, op, heavy.DeviceType, k, ws[i].Key, resultText(ra), resultText(rb))
			}
			for j := range ra.Inputs {
				x, y := ra.Inputs[j], rb.Inputs[j]
				if x.Group != y.Group || x.Status != y.Status || x.Selected != y.Selected || !near(x.Coverage, y.Coverage) {
					t.Fatalf("iteration %d window %s input %d: %+v vs %+v", iter, ws[i].Key, j, x, y)
				}
			}
		}
	}
}

// Property: every result lists every rule group exactly once, in the result vocabulary, then
// one entry per excluded or unmatched source, and the counts agree with the inputs.
func TestPropertyEveryGroupListed(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	srcs := []Source{exWatch, exPhone, exRing, exGarmin, rsRelay, {Provider: "manual", Manual: true}}
	sels := []Selector{{Provider: "garmin"}, {DeviceType: "watch"}, {DeviceType: "ring"}, {Provider: "apple_health"},
		{Relayed: relayed(true)}, {Entry: EntryManual}, {DeviceType: "phone"}, {OriginKeyPrefix: "com.apple.health"}}
	vocab := []GroupStatus{StatusUsed, StatusUnused, StatusNoData, StatusBelowQuality, StatusStale}
	day, _ := LocalDay(date("2026-09-14"), berlin)
	hours, _ := Buckets(date("2026-09-14"), time.Hour, berlin)
	for iter := range 300 {
		metric, kind := "steps", catalog.WindowLocalDay
		op := []Op{OpFirstAvailable, OpMax, OpMin, OpMean, OpLatest, OpEarliest}[rng.IntN(6)]
		if rng.IntN(2) == 0 {
			metric, kind = "heart_rate", catalog.WindowHour
		}
		r := &Rule{Schema: SchemaV1, Metric: metric, Window: RuleWindow{Kind: kind}, Strategy: Strategy{Op: op, MinSources: rng.IntN(3)},
			Quality: &Quality{MinCoverage: biRatio(rng.Float64() * 0.3)}}
		for g := range 1 + rng.IntN(4) {
			r.Groups = append(r.Groups, Group{ID: string(rune('a' + g)), Match: []Selector{sels[rng.IntN(len(sels))]}})
		}
		if rng.IntN(2) == 0 {
			r.Exclude = []Selector{sels[rng.IntN(len(sels))]}
		}
		var in []Input
		for range rng.IntN(80) {
			src := srcs[rng.IntN(len(srcs))]
			at := day.Start.Add(time.Duration(rng.Int64N(int64(24 * time.Hour))))
			if metric == "steps" {
				in = append(in, exInterval(src, at, time.Duration(1+rng.IntN(40))*time.Minute, float64(rng.IntN(300))))
			} else {
				in = append(in, exSample(src, at, float64(40+rng.IntN(100))))
			}
		}
		w := day
		if kind == catalog.WindowHour {
			w = hours[rng.IntN(len(hours))]
		}
		res, err := r.ResolveWindowOverridden(w, Series{metric: in}, Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		out := BuildResult(metric, testVersion(r), res, time.Time{})
		if len(out.Inputs) < len(r.Groups) {
			t.Fatalf("iteration %d: %d inputs for %d groups", iter, len(out.Inputs), len(r.Groups))
		}
		seen := map[string]int{}
		excluded, unmatched := 0, 0
		for i, x := range out.Inputs {
			switch {
			case i < len(r.Groups):
				seen[x.Group]++
				if !slices.Contains(vocab, x.Status) {
					t.Fatalf("iteration %d: group %s status %q", iter, x.Group, x.Status)
				}
			case x.Status == StatusExcluded:
				excluded += x.Count
			case x.Status == StatusNotInRule:
				unmatched += x.Count
			default:
				t.Fatalf("iteration %d: source entry %+v", iter, x)
			}
		}
		for _, g := range r.Groups {
			if seen[g.ID] != 1 {
				t.Fatalf("iteration %d: group %s listed %d times", iter, g.ID, seen[g.ID])
			}
		}
		if excluded != out.Counts.Excluded || unmatched != out.Counts.NotInRule {
			t.Fatalf("iteration %d: counts %+v vs excluded %d unmatched %d", iter, out.Counts, excluded, unmatched)
		}
		if out.Explanation == "" || (out.Status == ResultNoData) != (out.Value == nil) {
			t.Fatalf("iteration %d: %s", iter, resultText(out))
		}
	}
}
