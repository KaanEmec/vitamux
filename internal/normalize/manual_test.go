package normalize_test

import (
	"testing"

	"github.com/KaanEmec/vitamux/internal/normalize"
	"github.com/KaanEmec/vitamux/internal/normalize/normtest"
)

func TestGoldenManual(t *testing.T) {
	normtest.Golden(t, normalize.Manual{}, normalize.RawPayload{Stream: normalize.ManualStream, ContentType: "application/json"},
		normalize.Env{Provider: "manual"})
}
