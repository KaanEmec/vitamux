package config

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

func envOf(m map[string]string) Lookup {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func filesOf(m map[string]string) ReadFile {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
}

const sentinel = "postgres://user:SENTINEL-SECRET@db/vitamux"

func TestDefaults(t *testing.T) {
	c, err := load(envOf(nil), filesOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Env != Production || c.HTTPAddr != "127.0.0.1:8080" || c.PublicURL.String() != "http://127.0.0.1:8080" || c.DatabaseURL.IsSet() {
		t.Fatalf("unexpected defaults: %s", c)
	}
}

func TestSecretFromFileWins(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_DATABASE_URL_FILE": "/run/secrets/db"}),
		filesOf(map[string]string{"/run/secrets/db": sentinel + "\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL.Value() != sentinel {
		t.Fatalf("got %q", c.DatabaseURL.Value())
	}
}

func TestSecretRules(t *testing.T) {
	cases := map[string]map[string]string{
		"both file and plain": {"VITAMUX_DATABASE_URL_FILE": "/x", "VITAMUX_DATABASE_URL": sentinel, "VITAMUX_ENV": "development"},
		"plain in production": {"VITAMUX_DATABASE_URL": sentinel},
		"missing file":        {"VITAMUX_DATABASE_URL_FILE": "/missing"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(envOf(env), filesOf(map[string]string{"/x": sentinel}))
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("error leaks secret: %v", err)
			}
		})
	}
}

func TestPlainSecretAllowedInDevelopment(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_ENV": "development", "VITAMUX_DATABASE_URL": sentinel}), filesOf(nil))
	if err != nil || c.DatabaseURL.Value() != sentinel {
		t.Fatalf("err=%v", err)
	}
}

func TestValidationErrorsAreJoined(t *testing.T) {
	_, err := load(envOf(map[string]string{"VITAMUX_ENV": "staging", "VITAMUX_LOG_LEVEL": "loud", "VITAMUX_PUBLIC_URL": "/relative"}), filesOf(nil))
	for _, want := range []string{"VITAMUX_ENV", "VITAMUX_LOG_LEVEL", "VITAMUX_PUBLIC_URL"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %s in %v", want, err)
		}
	}
}

func TestProductionRequiresHTTPSForNonLoopback(t *testing.T) {
	_, err := load(envOf(map[string]string{"VITAMUX_PUBLIC_URL": "http://health.example.org"}), filesOf(nil))
	if err == nil {
		t.Fatal("expected https error")
	}
}

func TestRedactedRendering(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_ENV": "development", "VITAMUX_DATABASE_URL": sentinel}), filesOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(out, "SENTINEL") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_TRUSTED_PROXIES": "172.18.0.0/16, 10.0.0.5 ,"}), filesOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(c.TrustedProxies); got != "[172.18.0.0/16 10.0.0.5/32]" {
		t.Fatalf("got %s", got)
	}
	if c, err := load(envOf(nil), filesOf(nil)); err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("default must trust none: %v %v", c.TrustedProxies, err)
	}
	if _, err := load(envOf(map[string]string{"VITAMUX_TRUSTED_PROXIES": "not-a-cidr"}), filesOf(nil)); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestPreviousMasterKeyFiles(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_PREVIOUS_MASTER_KEY_FILES": "/k/old1, /k/old2 ,"}), filesOf(nil))
	if err != nil || fmt.Sprint(c.PreviousMasterKeyFiles) != "[/k/old1 /k/old2]" {
		t.Fatalf("got %v, %v", c.PreviousMasterKeyFiles, err)
	}
}

func TestMetricsAddr(t *testing.T) {
	c, err := load(envOf(nil), filesOf(nil))
	if err != nil || c.MetricsAddr != "" {
		t.Fatalf("default must be disabled: %q, %v", c.MetricsAddr, err)
	}
	c, err = load(envOf(map[string]string{"VITAMUX_METRICS_ADDR": "127.0.0.1:9090"}), filesOf(nil))
	if err != nil || c.MetricsAddr != "127.0.0.1:9090" {
		t.Fatalf("got %q, %v", c.MetricsAddr, err)
	}
	for _, bad := range []string{"9090", "127.0.0.1:8080"} { // not host:port; same as the public listener
		if _, err := load(envOf(map[string]string{"VITAMUX_METRICS_ADDR": bad}), filesOf(nil)); err == nil {
			t.Errorf("VITAMUX_METRICS_ADDR=%q accepted", bad)
		}
	}
}
