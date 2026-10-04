package main

import (
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/remote"
)

// An unusable sidecar (here a public address, refused at dial time) does not stop serve: it is
// registered as an unavailable placeholder.
func TestSidecarsNeverBlock(t *testing.T) {
	u, _ := url.Parse("http://192.0.2.1:9")
	reg, _ := connectors.NewRegistry()
	start := time.Now()
	m := remote.NewManager(nil, nil, reg, slog.New(slog.DiscardHandler))
	if err := m.Start(t.Context(), envSidecars([]config.Sidecar{{Name: "example_sidecar", URL: u}})); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("startup waited for the sidecar")
	}
	c, ok := reg.Get("example_sidecar")
	if !ok || c.Describe().Available() || !c.Describe().Remote {
		t.Fatal("sidecar not registered as an unavailable placeholder")
	}
}
