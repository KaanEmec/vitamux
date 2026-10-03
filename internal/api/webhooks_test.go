package api

import (
	"log/slog"
	"net/http"
	"testing"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

// Webhook callbacks are public: probes answer 204 and touch nothing; a notification without a
// known hook is 404. The service behind it is tested in internal/connectors/withings.
func TestWithingsWebhookRoutes(t *testing.T) {
	h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodHead, http.MethodGet} {
		if res := serve(t, h, request(t, m, "/webhooks/withings/synthetic-hook", nil)); res.StatusCode != http.StatusNoContent {
			t.Fatalf("%s: %d", m, res.StatusCode)
		}
	}
	res := serve(t, h, request(t, http.MethodPost, "/webhooks/withings/synthetic-hook", nil))
	if res.StatusCode != http.StatusNotFound || res.Header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("POST without a hook: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

// A callback for a provider without a connector is 404, not a redirect.
func TestOAuthCallbackUnknownProvider(t *testing.T) {
	h, err := NewHandler(slog.New(slog.DiscardHandler), newUITestFS(), Options{Connectors: connectors.New(connectors.Config{})})
	if err != nil {
		t.Fatal(err)
	}
	if res := serve(t, h, request(t, http.MethodGet, "/oauth/oura/callback?code=synthetic-code&state=x", nil)); res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown provider: %d", res.StatusCode)
	}
}
