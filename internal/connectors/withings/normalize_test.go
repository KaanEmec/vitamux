package withings

import (
	"testing"

	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestGoldenMeasures(t *testing.T) {
	normtest.Golden(t, Normalizer{}, normalize.RawPayload{Stream: StreamMeasures, ContentType: "application/json"},
		normalize.Env{Provider: Provider})
}

func TestDecimal(t *testing.T) {
	for _, c := range []struct {
		v    int64
		unit int
		want float64
	}{{7500, -2, 75}, {799, -1, 79.9}, {79950, -3, 79.95}, {132, 0, 132}, {3, 2, 300}} {
		if got := decimal(c.v, c.unit); got != c.want {
			t.Errorf("decimal(%d, %d) = %v, want %v", c.v, c.unit, got, c.want)
		}
	}
}
