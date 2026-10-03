package httpx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/obs"
	"github.com/KaanEmec/vitamux/internal/version"
)

const sentinel = "SENTINEL-VALUE-0451"

func newReq(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestUserAgentAndCap(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		if r.URL.Path == "/big-chunked" {
			for range 10 {
				_, _ = w.Write(bytes.Repeat([]byte("x"), 100))
				w.(http.Flusher).Flush()
			}
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte("x"), 1000))
	}))
	defer srv.Close()
	c := New(Options{MaxResponseBytes: 500})

	res, err := c.Do(newReq(t, srv.URL+"/big-chunked"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("chunked: want ErrResponseTooLarge, got %v", err)
	}
	if want := "vitamux/" + version.Version; ua != want {
		t.Fatalf("user agent %q, want %q", ua, want)
	}

	if _, err = c.Do(newReq(t, srv.URL+"/declared")); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("declared length: want ErrResponseTooLarge, got %v", err)
	}

	res, err = New(Options{MaxResponseBytes: 1000}).Do(newReq(t, srv.URL+"/exact"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || len(body) != 1000 {
		t.Fatalf("body at the cap must pass: %d bytes, %v", len(body), err)
	}
}

func TestErrorsAndLogsNeverContainSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	var logs bytes.Buffer
	log := slog.New(obs.NewRedactingHandler(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	c := New(Options{Timeout: 100 * time.Millisecond, Logger: log})

	withSecrets := func(base string) *http.Request {
		req := newReq(t, strings.Replace(base, "://", "://user:"+sentinel+"@", 1)+"/cb?code="+sentinel)
		req.Header.Set("Authorization", "Bearer "+sentinel)
		return req
	}

	_, timeoutErr := c.Do(withSecrets(srv.URL))
	_, refusedErr := c.Do(withSecrets(closedURL))
	for name, err := range map[string]error{"timeout": timeoutErr, "refused": refusedErr} {
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "code=") {
			t.Fatalf("%s: error leaks request URL: %v", name, err)
		}
		var he *Error
		if !errors.As(err, &he) || he.Method != http.MethodGet {
			t.Fatalf("%s: want *Error, got %T", name, err)
		}
	}
	if !errors.Is(timeoutErr, context.DeadlineExceeded) {
		t.Fatalf("timeout cause should stay inspectable: %v", timeoutErr)
	}
	if strings.Contains(logs.String(), sentinel) || !strings.Contains(logs.String(), "outbound http") {
		t.Fatalf("logs leak or are missing:\n%s", logs.String())
	}
}

func TestRedirects(t *testing.T) {
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again?code="+sentinel, http.StatusFound)
	}))
	defer loop.Close()
	_, err := New(Options{}).Do(newReq(t, loop.URL))
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("want sanitised redirect error, got %v", err)
	}

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loop.URL, http.StatusFound)
	}))
	defer tlsSrv.Close()
	_, err = New(Options{Transport: tlsSrv.Client().Transport}).Do(newReq(t, tlsSrv.URL))
	if err == nil || !strings.Contains(err.Error(), "https to http") {
		t.Fatalf("downgrade must be refused, got %v", err)
	}
}
