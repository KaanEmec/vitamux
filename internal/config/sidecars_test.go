package config

import (
	"strings"
	"testing"
)

func TestSidecars(t *testing.T) {
	env := envOf(map[string]string{
		"VITAMUX_DATA_DIR":                  "/data",
		"VITAMUX_SIDECARS":                  "example_sidecar=http://example-sidecar:8090, other=https://10.0.0.5/base",
		"VITAMUX_SIDECAR_OTHER_SECRET_FILE": "/run/secrets/other",
	})
	c, err := load(env, filesOf(map[string]string{
		"/data/secrets/sidecar-example_sidecar.secret": "SENTINEL-A\n",
		"/run/secrets/other":                           "SENTINEL-B",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sidecars) != 2 {
		t.Fatalf("%d sidecars", len(c.Sidecars))
	}
	a, b := c.Sidecars[0], c.Sidecars[1]
	if a.Name != "example_sidecar" || a.URL.Host != "example-sidecar:8090" || a.Secret.Value() != "SENTINEL-A" ||
		b.Name != "other" || b.SecretFile != "/run/secrets/other" || b.Secret.Value() != "SENTINEL-B" {
		t.Fatalf("unexpected sidecars: %+v", c.Sidecars)
	}
	if strings.Contains(c.String(), "SENTINEL") {
		t.Fatal("config rendering leaks a sidecar secret")
	}
	// admin init-secrets sees the paths without reading them.
	s, err := Sidecars(env)
	if err != nil || len(s) != 2 || s[0].SecretFile != "/data/secrets/sidecar-example_sidecar.secret" || s[0].Secret.IsSet() {
		t.Fatalf("Sidecars: %+v, %v", s, err)
	}
}

// A missing secret file is not an error: the connector reads it once init-secrets created it.
func TestSidecarSecretMissing(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_SIDECARS": "garmin=http://sidecar-garmin:8080", "VITAMUX_DATA_DIR": "/data"}), filesOf(nil))
	if err != nil || len(c.Sidecars) != 1 || c.Sidecars[0].Secret.IsSet() || c.Sidecars[0].SecretFile != "/data/secrets/sidecar-garmin.secret" {
		t.Fatalf("got %+v, %v", c.Sidecars, err)
	}
}

func TestSidecarErrors(t *testing.T) {
	for name, tc := range map[string]struct{ sidecars, want string }{
		"bad name":    {"Bad=http://x:1", "must match"},
		"twice":       {"a=http://x:1,a=http://y:1", "listed twice"},
		"no url":      {"a", "absolute http(s) URL"},
		"credentials": {"a=http://u:p@x:1", "absolute http(s) URL"},
		"ftp":         {"a=ftp://x", "absolute http(s) URL"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(envOf(map[string]string{"VITAMUX_SIDECARS": tc.sidecars}), filesOf(nil))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
