package connectors

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

const (
	minInterval            = time.Minute // jobs.EnsureSchedule refuses less
	defaultCorrectionEvery = 24 * time.Hour
)

var (
	providerRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`) // providers.code
	streamRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*$`)
)

// ErrInvalidDescriptor wraps descriptor validation failures.
var ErrInvalidDescriptor = errors.New("connectors: invalid descriptor")

// Registry maps provider codes to connectors.
type Registry struct{ m map[string]Connector }

// NewRegistry validates and registers cs; adding a connector is one argument here.
func NewRegistry(cs ...Connector) (*Registry, error) {
	r := &Registry{m: map[string]Connector{}}
	for _, c := range cs {
		d := c.Describe()
		if err := Validate(c, d); err != nil {
			return nil, err
		}
		if _, dup := r.m[d.Provider]; dup {
			return nil, fmt.Errorf("%w: provider %q registered twice", ErrInvalidDescriptor, d.Provider)
		}
		r.m[d.Provider] = c
	}
	return r, nil
}

// Get returns the connector for a provider code.
func (r *Registry) Get(provider string) (Connector, bool) {
	c, ok := r.m[provider]
	return c, ok
}

// Descriptors returns every registered connector's current descriptor, by provider code.
func (r *Registry) Descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(r.m))
	for _, c := range r.m {
		out = append(out, c.Describe())
	}
	slices.SortFunc(out, func(a, b Descriptor) int { return strings.Compare(a.Provider, b.Provider) })
	return out
}

// Validate checks a descriptor as NewRegistry does; a sidecar connector checks the descriptor
// its sidecar sends with it before using it.
func Validate(c Connector, d Descriptor) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidDescriptor, d.Provider, fmt.Sprintf(format, args...))
	}
	switch {
	case !providerRe.MatchString(d.Provider):
		return bad("provider code must match %s", providerRe)
	case !d.Available():
		return nil // sidecar placeholder: nothing is known yet
	case d.Version == "":
		return bad("version is required")
	case len(d.Streams) == 0:
		return bad("at least one stream is required")
	}
	switch d.AuthKind {
	case AuthNone, AuthDevicePairing:
	case AuthOAuth2, AuthInteractiveMFA:
		if _, ok := c.(Authenticator); !ok {
			return bad("auth kind %s needs an Authenticator", d.AuthKind)
		}
	default:
		return bad("unknown auth kind %q", d.AuthKind)
	}
	seen := map[string]bool{}
	for _, s := range d.Streams {
		switch {
		case !streamRe.MatchString(s.Name):
			return bad("stream %q must match %s", s.Name, streamRe)
		case seen[s.Name]:
			return bad("stream %q declared twice", s.Name)
		case s.Interval != 0 && s.Interval < minInterval:
			return bad("stream %s: interval must be 0 or at least %s", s.Name, minInterval)
		case d.Capabilities.Incremental && s.Interval == 0:
			return bad("stream %s: incremental streams need an interval", s.Name)
		case s.Lookback < 0:
			return bad("stream %s: lookback must not be negative", s.Name)
		case s.CorrectionEvery != 0 && s.CorrectionEvery < minInterval:
			return bad("stream %s: correction interval must be 0 or at least %s", s.Name, minInterval)
		case d.Capabilities.Backfill && (s.MaxBackfill <= 0 || s.UnitSize <= 0 || s.UnitSize > s.MaxBackfill):
			return bad("stream %s: backfill needs 0 < unit size <= max backfill", s.Name)
		}
		seen[s.Name] = true
	}
	for _, l := range d.RateLimits {
		if l.Requests <= 0 || l.Per <= 0 {
			return bad("rate limits need positive requests and period")
		}
	}
	return nil
}

func (d Descriptor) stream(name string) (StreamSpec, bool) {
	for _, s := range d.Streams {
		if s.Name == name {
			return s, true
		}
	}
	return StreamSpec{}, false
}

// EnsureSchedules creates the descriptor's default schedules for a connection: incremental
// per stream with an Interval, correction per stream with a Lookback. Existing schedules keep
// their owner-edited settings, so call it on every connect.
func EnsureSchedules(ctx context.Context, q *dbq.Queries, connectionID uuid.UUID, d Descriptor) error {
	for _, s := range d.Streams {
		if s.Interval > 0 {
			if _, err := jobs.EnsureSchedule(ctx, q, jobs.ScheduleSpec{
				ConnectionID: connectionID, Stream: s.Name, Mode: ModeIncremental, Interval: s.Interval,
			}); err != nil {
				return err
			}
		}
		if s.Lookback > 0 {
			if _, err := jobs.EnsureSchedule(ctx, q, jobs.ScheduleSpec{
				ConnectionID: connectionID, Stream: s.Name, Mode: ModeCorrection,
				Interval: cmp.Or(s.CorrectionEvery, defaultCorrectionEvery), Lookback: s.Lookback,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
