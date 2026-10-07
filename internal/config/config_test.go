package config

import (
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"
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

func TestAppSessionLifetimes(t *testing.T) {
	c, err := load(envOf(nil), filesOf(nil))
	if err != nil || c.AppSessionIdle != 30*24*time.Hour || c.AppSessionMax != 90*24*time.Hour {
		t.Fatalf("defaults: %v %v, %v", c.AppSessionIdle, c.AppSessionMax, err)
	}
	c, err = load(envOf(map[string]string{"VITAMUX_APP_SESSION_IDLE": "24h", "VITAMUX_APP_SESSION_MAX": "168h"}), filesOf(nil))
	if err != nil || c.AppSessionIdle != 24*time.Hour || c.AppSessionMax != 168*time.Hour {
		t.Fatalf("got %v %v, %v", c.AppSessionIdle, c.AppSessionMax, err)
	}
	for _, bad := range []map[string]string{
		{"VITAMUX_APP_SESSION_IDLE": "30d"}, // not a Go duration
		{"VITAMUX_APP_SESSION_IDLE": "-1h"},
		{"VITAMUX_APP_SESSION_MAX": "0s"},
		{"VITAMUX_APP_SESSION_IDLE": "100h", "VITAMUX_APP_SESSION_MAX": "10h"}, // idle beyond the absolute end
	} {
		if _, err := load(envOf(bad), filesOf(nil)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestWithingsClient(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_WITHINGS_CLIENT_ID": "synthetic-client", "VITAMUX_WITHINGS_CLIENT_SECRET_FILE": "/run/secrets/w"}),
		filesOf(map[string]string{"/run/secrets/w": "SENTINEL-SECRET\n"}))
	if err != nil || c.WithingsClientID != "synthetic-client" || c.WithingsClientSecret.Value() != "SENTINEL-SECRET" {
		t.Fatalf("got %v", err)
	}
	if _, err := load(envOf(map[string]string{"VITAMUX_WITHINGS_CLIENT_ID": "synthetic-client"}), filesOf(nil)); err == nil {
		t.Fatal("id without secret accepted")
	}
}

func TestExtractors(t *testing.T) {
	files := filesOf(map[string]string{"/k": "SENTINEL-AI-KEY\n"})
	c, err := load(envOf(map[string]string{
		"VITAMUX_GEMINI_API_KEY_FILE": "/k", "VITAMUX_GEMINI_MODEL": "m1",
		"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "http://127.0.0.1:8000/v1", "VITAMUX_OPENAI_COMPATIBLE_MODEL": "m2",
		"VITAMUX_OPENAI_COMPATIBLE_ALLOW_PRIVATE": "true",
	}), files)
	if err != nil {
		t.Fatal(err)
	}
	if c.GeminiAPIKey.Value() != "SENTINEL-AI-KEY" || c.GeminiModel != "m1" || c.OpenAICompatibleBaseURL.String() != "http://127.0.0.1:8000/v1" {
		t.Fatalf("unexpected: %+v", c)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", c, c, c), "SENTINEL") {
		t.Fatal("config rendering leaks the AI key")
	}
	for name, env := range map[string]map[string]string{
		"key without model":   {"VITAMUX_OPENAI_API_KEY_FILE": "/k"},
		"model without key":   {"VITAMUX_GEMINI_MODEL": "m"},
		"url without model":   {"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "https://ai.example.com/v1"},
		"http public":         {"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "http://ai.example.com/v1", "VITAMUX_OPENAI_COMPATIBLE_MODEL": "m"},
		"private not allowed": {"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "https://10.0.0.5/v1", "VITAMUX_OPENAI_COMPATIBLE_MODEL": "m"},
		"localhost":           {"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "https://localhost/v1", "VITAMUX_OPENAI_COMPATIBLE_MODEL": "m"},
		"credentials in url":  {"VITAMUX_OPENAI_COMPATIBLE_BASE_URL": "https://u:p@ai.example.com/v1", "VITAMUX_OPENAI_COMPATIBLE_MODEL": "m"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := load(envOf(env), files); err == nil {
				t.Fatal("expected error")
			} else if strings.Contains(err.Error(), "SENTINEL") {
				t.Fatalf("error leaks the key: %v", err)
			}
		})
	}
}

func TestBackup(t *testing.T) {
	c, err := load(envOf(map[string]string{"VITAMUX_BACKUP_DIR": "/backups"}), filesOf(nil))
	if err != nil || c.BackupDir != "/backups" || c.BackupKeep != 3 {
		t.Fatalf("dir %q keep %d, %v", c.BackupDir, c.BackupKeep, err)
	}
	for _, keep := range []string{"0", "-1", "three"} {
		if _, err := load(envOf(map[string]string{"VITAMUX_BACKUP_KEEP": keep}), filesOf(nil)); err == nil {
			t.Errorf("VITAMUX_BACKUP_KEEP=%s accepted", keep)
		}
	}
}
