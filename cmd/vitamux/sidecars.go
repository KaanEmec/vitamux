package main

import (
	"github.com/KaanEmec/vitamux/internal/config"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/remote"
)

// envSidecars converts VITAMUX_SIDECARS for the sidecar manager, which registers them next to
// the panel's (docs/architecture/connectors.md#remote-sidecar-mode).
func envSidecars(sidecars []config.Sidecar) []remote.EnvSidecar {
	out := make([]remote.EnvSidecar, len(sidecars))
	for i, s := range sidecars {
		out[i] = remote.EnvSidecar{Name: s.Name, URL: s.URL, Secret: s.Secret.Value(), SecretFile: s.SecretFile}
	}
	return out
}

// envApps returns the provider app credentials the environment sets (ADR-0021: they win).
func envApps(cfg config.Config) map[string]connectors.AppCredentials {
	m := map[string]connectors.AppCredentials{}
	if cfg.WithingsClientID != "" && cfg.WithingsClientSecret.IsSet() {
		m["withings"] = connectors.AppCredentials{ClientID: cfg.WithingsClientID, ClientSecret: cfg.WithingsClientSecret.Value()}
	}
	return m
}
