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
