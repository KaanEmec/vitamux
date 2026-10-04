package main

import (
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors"
)

// An unusable sidecar (here a public address, refused at dial time) does not stop serve: it is
// registered as an unavailable placeholder.
func TestSidecarConnectorsNeverBlock(t *testing.T) {
	u, _ := url.Parse("http://192.0.2.1:9")
	start := time.Now()
	cs := sidecarConnectors(t.Context(), []config.Sidecar{{Name: "example_sidecar", URL: u}}, nil, slog.New(slog.DiscardHandler))
	if time.Since(start) > startupDescribe+time.Second {
		t.Fatal("startup waited for the sidecar")
	}
	reg, err := connectors.NewRegistry(cs...)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := reg.Get("example_sidecar")
	if !ok || c.Describe().Available() || !c.Describe().Remote {
		t.Fatal("sidecar not registered as an unavailable placeholder")
	}
}
