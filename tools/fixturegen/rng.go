package main

import (
	"hash/fnv"
	"math/rand/v2"
)

// splitmix64 spreads nearby inputs so PCG streams for adjacent days are unrelated.
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ x>>30) * 0xbf58476d1ce4e5b9
	x = (x ^ x>>27) * 0x94d049bb133111eb
	return x ^ x>>31
}

// stream returns the PRNG for one purpose on one day. Streams are keyed by day index, not by
// position in the run, so a 30-day subset equals the same days of the full year.
func stream(seed uint64, purpose string, day int) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(purpose))
	a := splitmix64(seed ^ h.Sum64())
	b := splitmix64(a + uint64(int64(day))) //nolint:gosec // day index may be negative; wrap-around is fine for a seed
	return rand.New(rand.NewPCG(a, b))      //nolint:gosec // deterministic fixtures, not security
}

// between returns an integer in [lo, hi].
func between(r *rand.Rand, lo, hi int) int { return lo + r.IntN(hi-lo+1) }
