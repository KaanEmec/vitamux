package connectors

import "time"

// Health is the one-word state the UI and API show for a connection or one of its streams.
type Health string

const (
	HealthOK          Health = "ok"
	HealthDegraded    Health = "degraded"     // working, but see HealthState.Reason
	HealthFailing     Health = "failing"      // repeated failures or a permanent error
	HealthNeedsReauth Health = "needs_reauth" // the owner must reconnect
	HealthPaused      Health = "paused"
	HealthDisabled    Health = "disabled"
	HealthStale       Health = "stale" // no failure, but no success for several intervals
)

// Reasons of a HealthState. Free of provider data, safe to show and log.
const (
	ReasonRateLimited       = "rate_limited"
	ReasonRecentFailures    = "recent_failures"
	ReasonAwaitingFirstSync = "awaiting_first_sync"
)

// Health thresholds: a connection is failing from FailingAfter consecutive failures, and
// stale once its last success is older than StaleIntervals schedule intervals.
const (
	FailingAfter   = 3
	StaleIntervals = 3
)

// HealthInput is what Derive needs; every field comes from the connections, sync_cursors,
// schedules and provider_rate_state rows. Leave Stream* empty to derive a connection's health.
type HealthInput struct {
	Now                 time.Time
	ConnectionStatus    string // connections.status: active|degraded|needs_reauth|paused|error|disabled
	StreamStatus        string // sync_cursors.status: ok|degraded; "" when deriving a connection
	StreamReason        string // sync_cursors.status_reason, e.g. schema_drift
	LastSuccessAt       time.Time
	LastErrorClass      string // connections.last_error_class
	ConsecutiveFailures int
	BlockedUntil        time.Time     // provider_rate_state.blocked_until; zero when not blocked
	Interval            time.Duration // schedule interval; 0 = unscheduled, never stale
}

// HealthState is a derived health with the reason that makes it more than its word.
type HealthState struct {
	Health Health
	Reason string
}

// DeriveHealth is the single place that turns stored sync state into a health, so the API,
// the UI and the metrics agree. First match wins: owner-set states, then reauthorization,
// then failures, then degradation, then staleness.
func DeriveHealth(in HealthInput) HealthState {
	switch {
	case in.ConnectionStatus == "disabled":
		return HealthState{Health: HealthDisabled}
	case in.ConnectionStatus == "paused":
		return HealthState{Health: HealthPaused}
	case in.ConnectionStatus == "needs_reauth" || in.LastErrorClass == ClassReauthRequired:
		return HealthState{Health: HealthNeedsReauth, Reason: ClassReauthRequired}
	case in.ConnectionStatus == "error":
		return HealthState{Health: HealthFailing, Reason: orElse(in.LastErrorClass, ClassPermanent)}
	case in.ConsecutiveFailures >= FailingAfter:
		return HealthState{Health: HealthFailing, Reason: orElse(in.LastErrorClass, ClassTransient)}
	case in.StreamStatus == "degraded":
		return HealthState{Health: HealthDegraded, Reason: orElse(in.StreamReason, "stream_degraded")}
	case in.ConnectionStatus == "degraded":
		return HealthState{Health: HealthDegraded, Reason: orElse(in.LastErrorClass, "connection_degraded")}
	case in.ConsecutiveFailures > 0:
		return HealthState{Health: HealthDegraded, Reason: ReasonRecentFailures}
	case in.BlockedUntil.After(in.Now):
		return HealthState{Health: HealthDegraded, Reason: ReasonRateLimited}
	case in.LastSuccessAt.IsZero():
		return HealthState{Health: HealthOK, Reason: ReasonAwaitingFirstSync}
	case in.Interval > 0 && in.Now.Sub(in.LastSuccessAt) > StaleIntervals*in.Interval:
		return HealthState{Health: HealthStale}
	}
	return HealthState{Health: HealthOK}
}

func orElse(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
