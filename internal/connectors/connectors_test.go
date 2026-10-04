package connectors

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"120", 120 * time.Second},
		{" 0 ", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0}, // date in the past
		{"999999999", 24 * time.Hour},                    // clamped
		{now.Add(72 * time.Hour).Format(http.TimeFormat), 24 * time.Hour},
		{"", time.Minute},
		{"-5", time.Minute},
		{"soon", time.Minute},
	} {
		if got := parseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestBucketLimitsRate(t *testing.T) {
	b := newBucket(RateLimitSpec{Requests: 5, Per: 100 * time.Millisecond}) // burst 5, then one per 20 ms
	start := time.Now()
	for range 10 {
		if err := b.wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 90*time.Millisecond {
		t.Fatalf("10 tokens in %s; want about 100 ms (5 burst + 5 × 20 ms)", el)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	slow := newBucket(RateLimitSpec{Requests: 1, Per: time.Hour})
	_ = slow.wait(ctx)
	if err := slow.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("empty bucket wait = %v, want deadline exceeded", err)
	}
}

// A sidecar's client is first handed out for its placeholder (no limits); once the sidecar is
// described, the same client (same block) gets the described buckets. Unchanged limits keep
// the buckets' state.
func TestProviderClientFollowsLimits(t *testing.T) {
	p := providerClients{m: map[string]*HTTPClient{}}
	h := p.get(Descriptor{Provider: "example_sidecar", Remote: true})
	if err := h.Wait(t.Context()); err != nil || len(h.buckets) != 0 {
		t.Fatalf("placeholder: %v, %d buckets", err, len(h.buckets))
	}
	h.block(time.Now().Add(time.Hour))
	limits := []RateLimitSpec{{Requests: 1, Per: time.Hour}}
	if p.get(Descriptor{Provider: "example_sidecar", RateLimits: limits}) != h || len(h.buckets) != 1 {
		t.Fatal("described limits not applied to the provider's client")
	}
	var rl *RateLimitedError
	if err := h.Wait(t.Context()); !errors.As(err, &rl) {
		t.Fatalf("blocked provider admitted: %v", err)
	}
	h.blockedUntil = time.Time{}
	if err := h.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	p.get(Descriptor{Provider: "example_sidecar", RateLimits: slices.Clone(limits)})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()
	if err := h.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unchanged limits refilled the bucket: %v", err)
	}
}

type stub struct{ d Descriptor }

func (s stub) Describe() Descriptor { return s.d }
func (stub) Plan(context.Context, Conn, PlanRequest) ([]WorkUnit, error) {
	return nil, nil
}
func (stub) Fetch(context.Context, Conn, Credentials, WorkUnit, *RawSink) (FetchResult, error) {
	return FetchResult{Done: true}, nil
}

type stubAuth struct{ stub }

func (stubAuth) Refresh(_ context.Context, _ Conn, c Credentials) (Credentials, error) { return c, nil }

func TestRegistryValidation(t *testing.T) {
	good := func() Descriptor {
		return Descriptor{
			Provider: "withings", Version: "1", AuthKind: AuthNone,
			Streams:      []StreamSpec{{Name: "withings.measures", Interval: time.Hour, Lookback: 7 * 24 * time.Hour, MaxBackfill: 365 * 24 * time.Hour, UnitSize: 30 * 24 * time.Hour}},
			RateLimits:   []RateLimitSpec{{Requests: 120, Per: time.Minute}},
			Capabilities: Capabilities{Incremental: true, Backfill: true},
		}
	}
	if _, err := NewRegistry(stub{good()}); err != nil {
		t.Fatalf("valid descriptor: %v", err)
	}
	onDemand := good() // a backfill-only stream (the Garmin reload) has no interval and no lookback
	onDemand.Streams = append(onDemand.Streams, StreamSpec{Name: "withings.reload", MaxBackfill: 365 * 24 * time.Hour, UnitSize: 24 * time.Hour})
	if _, err := NewRegistry(stub{onDemand}); err != nil {
		t.Fatalf("on-demand stream: %v", err)
	}
	oauth := good()
	oauth.AuthKind = AuthOAuth2
	if _, err := NewRegistry(stubAuth{stub{oauth}}); err != nil {
		t.Fatalf("oauth with authenticator: %v", err)
	}
	for name, mutate := range map[string]func(*Descriptor){
		"provider code":     func(d *Descriptor) { d.Provider = "With-ings" },
		"version":           func(d *Descriptor) { d.Version = "" },
		"no streams":        func(d *Descriptor) { d.Streams = nil },
		"unknown auth":      func(d *Descriptor) { d.AuthKind = "magic" },
		"oauth without":     func(d *Descriptor) { d.AuthKind = AuthOAuth2 },
		"stream name":       func(d *Descriptor) { d.Streams[0].Name = "Measures!" },
		"duplicate stream":  func(d *Descriptor) { d.Streams = append(d.Streams, d.Streams[0]) },
		"short interval":    func(d *Descriptor) { d.Streams[0].Interval = time.Second },
		"incremental no iv": func(d *Descriptor) { d.Streams[0].Interval = 0 },
		"negative lookback": func(d *Descriptor) { d.Streams[0].Lookback = -time.Hour },
		"backfill unit":     func(d *Descriptor) { d.Streams[0].UnitSize = 0 },
		"rate limit":        func(d *Descriptor) { d.RateLimits[0].Per = 0 },
	} {
		d := good()
		mutate(&d)
		if _, err := NewRegistry(stub{d}); !errors.Is(err, ErrInvalidDescriptor) {
			t.Errorf("%s: err = %v, want ErrInvalidDescriptor", name, err)
		}
	}
	if _, err := NewRegistry(stub{good()}, stub{good()}); !errors.Is(err, ErrInvalidDescriptor) {
		t.Errorf("duplicate provider: err = %v", err)
	}
}

func TestCredentialsNeverPrint(t *testing.T) {
	c := Credentials{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh"}
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("x", "cred", c)
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%+v %#v", c, c), logged.String()} {
		if strings.Contains(s, "synthetic") {
			t.Fatalf("credentials leaked: %s", s)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("refresh: %w", ErrReauthRequired): ClassReauthRequired,
		&RateLimitedError{RetryAfter: time.Minute}:   ClassRateLimited,
		ErrTransient:                                ClassTransient,
		&SchemaDriftError{Endpoint: "/v1/x"}:        ClassSchemaDrift,
		fmt.Errorf("bad request: %w", ErrPermanent): ClassPermanent,
	} {
		var c interface{ ErrorClass() string }
		if !errors.As(err, &c) || c.ErrorClass() != want {
			t.Errorf("%v: class mismatch, want %s", err, want)
		}
	}
}
