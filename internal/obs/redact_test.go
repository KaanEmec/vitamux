package obs

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const sentinel = "SENTINEL-VALUE-0451"

func capture() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewLogger(&buf, slog.LevelDebug), &buf
}

func TestSentinelNeverReachesOutput(t *testing.T) {
	log, buf := capture()

	_, urlErr := (&http.Client{}).Get("http://127.0.0.1:1/cb?code=" + sentinel + "&state=" + sentinel) //nolint:noctx // exercising *url.Error
	if urlErr == nil || !strings.Contains(urlErr.Error(), sentinel) {
		t.Fatalf("test setup: expected *url.Error carrying the sentinel, got %v", urlErr)
	}
	u, _ := url.Parse("https://user:" + sentinel + "@example.test/path?foo=" + sentinel)
	hdr := http.Header{"Authorization": {"Bearer " + sentinel}, "X-Api-Key": {sentinel}, "Accept": {"application/json"}}

	log.Info("call to https://example.test/x?foo="+sentinel, "token", sentinel, "Refresh_Token", sentinel)
	log.Error("failed", "err", fmt.Errorf("fetch: %w", urlErr))
	log.Error("failed", "url", "https://example.test/cb?q="+sentinel+"#access="+sentinel, "dsn", "postgres://u:"+sentinel+"@db/x")
	log.Info("parsed", "u", u, "headers", hdr, "query", url.Values{"q": {sentinel}, "password": {sentinel}})
	log.Info("nested", slog.Group("secrets", slog.String("value", sentinel)), slog.Group("req", slog.String("cookie", sentinel), slog.Any("e", errors.New("x?code="+sentinel))))
	log.With("secret_key", sentinel, "u", "https://h/?a="+sentinel).WithGroup("password").Info("grouped", "anything", sentinel)
	log.Info("map", "m", map[string]any{"api_key": sentinel, "inner": map[string]string{"k": "https://h/?z=" + sentinel}})
	log.Info("auth", "h", "Authorization: Bearer "+sentinel)

	if out := buf.String(); strings.Contains(out, sentinel) {
		t.Fatalf("sentinel leaked:\n%s", out)
	}
}

func TestNonSecretsSurvive(t *testing.T) {
	log, buf := capture()
	log.Info("request", "method", "GET", "route", "/api/v1/x", "err", errors.New("boom"),
		"url", "https://example.test/a?b=c", slog.Group("g", slog.Int("n", 3)))
	out := buf.String()
	for _, want := range []string{`"method":"GET"`, `"route":"/api/v1/x"`, `"err":"boom"`, `https://example.test/a?b=[REDACTED]`, `"g":{"n":3}`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	for _, k := range []string{"token", "Access_Token", "client_secret", "password", "passwd", "Authorization", "Set-Cookie", "code", "refresh", "api_key", "X-Api-Key", "apikey"} {
		if !IsSensitiveKey(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	for _, k := range []string{"method", "status", "request_id", "user_id", "route"} {
		if IsSensitiveKey(k) {
			t.Errorf("%q should not be sensitive", k)
		}
	}
}
