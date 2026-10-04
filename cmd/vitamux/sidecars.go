package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/remote"
	"github.com/KaanEmec/vitamux/internal/db"
)

// startupDescribe bounds the describe of every sidecar at startup; startup never waits longer.
const startupDescribe = 3 * time.Second

// sidecarConnectors returns a connector per configured sidecar. Each is described in
// parallel; an unreachable one is registered as a placeholder and described again on use
// (docs/architecture/connectors.md#remote-sidecar-mode).
func sidecarConnectors(ctx context.Context, sidecars []config.Sidecar, d *db.DB, log *slog.Logger) []connectors.Connector {
	out := make([]connectors.Connector, len(sidecars))
	ctx, cancel := context.WithTimeout(ctx, startupDescribe)
	defer cancel()
	var wg sync.WaitGroup
	for i, s := range sidecars {
		c := remote.New(remote.Options{Name: s.Name, URL: s.URL, Secret: s.Secret.Value(), Log: log, OnDescribe: remote.Recorder(d)})
		out[i] = c
		wg.Go(func() { _ = c.Discover(ctx) }) // failures are logged by the connector
	}
	wg.Wait()
	return out
}
