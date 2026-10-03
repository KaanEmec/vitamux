package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// healthcheck probes this container's own /readyz for the Compose healthcheck (the
// distroless image has no curl). It reads only VITAMUX_HTTP_ADDR and never logs headers or bodies.
func healthcheck(stderr io.Writer) int {
	addr := os.Getenv("VITAMUX_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: bad VITAMUX_HTTP_ADDR %q\n", addr)
		return 1
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1" // a wildcard listen address is reachable on loopback
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/readyz", nil) //nolint:gosec // probes this process's own listener from VITAMUX_HTTP_ADDR
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // same: our own listener
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: /readyz answered %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
