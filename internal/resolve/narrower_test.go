package resolve

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/catalog"
)

// Property: cutting the rows to each window (narrower, J26.4) changes nothing. Hour windows over
// sorted samples and long intervals resolve as one window at a time over the whole series.
func TestPropertyNarrowedWindowsEqualWhole(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 6))
	hours, _ := Buckets(date("2026-09-14"), time.Hour, berlin)
	for iter := range 60 {
		code, op := "heart_rate", OpMean
		if iter%2 == 1 {
			code, op = "steps", OpFirstAvailable
		}
		r := agRule(code, catalog.WindowHour, op, agGroup("watch", Selector{DeviceType: "watch"}), agGroup("ring", Selector{DeviceType: "ring"}))
		var in []Input
		for _, src := range []Source{exWatch, exRing} {
			for range 5 + rng.IntN(80) {
				at := hours[2].Start.Add(time.Duration(rng.Int64N(int64(8 * time.Hour))))
				if code == "steps" || rng.IntN(5) == 0 {
					in = append(in, exInterval(src, at, time.Duration(1+rng.IntN(150))*time.Minute, float64(rng.IntN(200))))
					continue
				}
				in = append(in, exSample(src, at, float64(40+rng.IntN(120))))
			}
		}
		slices.SortStableFunc(in, func(a, b Input) int { return a.Start.Compare(b.Start) })
		s := Series{code: in}
		ws := hours[:12]
		got, err := r.ResolveWindowsOverridden(ws, s, Options{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range ws {
			want, err := r.ResolveWindow(w, s, Options{})
			if err != nil {
				t.Fatal(err)
			}
			a := BuildResult(code, testVersion(r), got[i], time.Time{})
			b := BuildResult(code, testVersion(r), Resolved{WindowResult: want}, time.Time{})
			if resultText(a) != resultText(b) || len(a.Inputs) != len(b.Inputs) {
				t.Fatalf("iteration %d window %s:\n %s\n %s", iter, w.Key, resultText(a), resultText(b))
			}
		}
	}
}
