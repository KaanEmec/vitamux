package remote

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bundled sidecar that is off is a normal state: debug, not a warning. Any other sidecar warns.
func TestUnreachableLogLevel(t *testing.T) {
	off, _ := url.Parse("http://127.0.0.1:1")
	for _, optional := range []bool{true, false} {
		var buf bytes.Buffer
		c := New(Options{Name: "garmin", URL: off, Secret: "synthetic-secret", Optional: optional,
			Log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))})
		if err := c.Discover(t.Context()); err == nil {
			t.Fatal("describe of a closed port succeeded")
		}
		if c.LastProblem() != ProblemUnreachable {
			t.Fatalf("problem %q", c.LastProblem())
		}
		if warned := strings.Contains(buf.String(), `"level":"WARN"`); warned == optional {
			t.Fatalf("optional=%v: log %s", optional, buf.String())
		}
	}
}

// The secret file of a bundled sidecar may appear after startup; it is read on use.
func TestSecretFileReadOnUse(t *testing.T) {
	describe, err := os.ReadFile("../../../schemas/examples/connector-sidecar/describe.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-file-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set(ProtocolHeader, Protocol)
		_, _ = w.Write(describe)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	file := filepath.Join(t.TempDir(), "secret")
	c := New(Options{Name: "example_sidecar", URL: u, SecretFile: file, Optional: true})
	if err := c.Probe(t.Context()); err == nil || c.LastProblem() != ProblemSecretMissing {
		t.Fatalf("without the file: %v, %q", err, c.LastProblem())
	}
	if err := os.WriteFile(file, []byte("synthetic-file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Probe(t.Context()); err != nil || c.LastProblem() != "" || !c.Describe().Available() {
		t.Fatalf("with the file: %v, %q", err, c.LastProblem())
	}
}

func TestEnableSteps(t *testing.T) {
	if s := EnableSteps("garmin", "compose"); len(s) != 1 || s[0].Line != "COMPOSE_PROFILES=garmin" || s[0].Apply != "docker compose up -d" {
		t.Fatalf("compose: %+v", s)
	}
	if s := EnableSteps("whoop", "coolify"); len(s) != 1 || s[0].Line != "WHOOP_SIDECAR=1" {
		t.Fatalf("coolify: %+v", s)
	}
	if len(EnableSteps("garmin", "")) != 2 || EnableSteps("example_sidecar", "") != nil {
		t.Fatal("unknown install lists both; a custom sidecar has none")
	}
}
