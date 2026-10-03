package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheck(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			t.Errorf("path %q", r.URL.Path)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	t.Setenv("VITAMUX_HTTP_ADDR", srv.Listener.Addr().String())

	if code := healthcheck(&bytes.Buffer{}); code != 0 {
		t.Fatalf("ready server: exit %d", code)
	}
	status = http.StatusServiceUnavailable
	if code := healthcheck(&bytes.Buffer{}); code == 0 {
		t.Fatal("503 must fail the check")
	}
	t.Setenv("VITAMUX_HTTP_ADDR", "127.0.0.1:1") // nothing listens
	if code := healthcheck(&bytes.Buffer{}); code == 0 {
		t.Fatal("an unreachable server must fail the check")
	}
}
