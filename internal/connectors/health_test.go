package connectors

import (
	"testing"
	"time"
)

func TestDeriveHealth(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-10 * time.Minute)
	hour := time.Hour
	ok := HealthInput{Now: now, ConnectionStatus: "active", StreamStatus: "ok", LastSuccessAt: fresh, Interval: hour}

	tests := []struct {
		name string
		edit func(*HealthInput)
		want HealthState
	}{
		{"healthy", func(*HealthInput) {}, HealthState{HealthOK, ""}},
		{"never synced", func(i *HealthInput) { i.LastSuccessAt = time.Time{} }, HealthState{HealthOK, ReasonAwaitingFirstSync}},
		{"unscheduled is never stale", func(i *HealthInput) { i.Interval, i.LastSuccessAt = 0, now.Add(-1000*time.Hour) }, HealthState{HealthOK, ""}},
		{"disabled", func(i *HealthInput) { i.ConnectionStatus = "disabled"; i.ConsecutiveFailures = 9 }, HealthState{HealthDisabled, ""}},
		{"paused beats failures", func(i *HealthInput) { i.ConnectionStatus = "paused"; i.ConsecutiveFailures = 9 }, HealthState{HealthPaused, ""}},
		{"needs reauth by status", func(i *HealthInput) { i.ConnectionStatus = "needs_reauth" }, HealthState{HealthNeedsReauth, ClassReauthRequired}},
		{"needs reauth by error class", func(i *HealthInput) { i.LastErrorClass = ClassReauthRequired }, HealthState{HealthNeedsReauth, ClassReauthRequired}},
		{"permanent error", func(i *HealthInput) { i.ConnectionStatus, i.LastErrorClass = "error", ClassPermanent }, HealthState{HealthFailing, ClassPermanent}},
		{"error status without class", func(i *HealthInput) { i.ConnectionStatus = "error" }, HealthState{HealthFailing, ClassPermanent}},
		{"consecutive failures", func(i *HealthInput) { i.ConsecutiveFailures, i.LastErrorClass = FailingAfter, ClassTransient }, HealthState{HealthFailing, ClassTransient}},
		{"failures beat schema drift", func(i *HealthInput) {
			i.ConsecutiveFailures, i.StreamStatus, i.StreamReason = FailingAfter, "degraded", ClassSchemaDrift
		}, HealthState{HealthFailing, ClassTransient}},
		{"stream schema drift", func(i *HealthInput) { i.StreamStatus, i.StreamReason = "degraded", ClassSchemaDrift }, HealthState{HealthDegraded, ClassSchemaDrift}},
		{"connection degraded", func(i *HealthInput) { i.ConnectionStatus, i.LastErrorClass = "degraded", ClassSchemaDrift }, HealthState{HealthDegraded, ClassSchemaDrift}},
		{"a failure or two", func(i *HealthInput) { i.ConsecutiveFailures = FailingAfter - 1 }, HealthState{HealthDegraded, ReasonRecentFailures}},
		{"rate limited", func(i *HealthInput) { i.BlockedUntil = now.Add(time.Minute) }, HealthState{HealthDegraded, ReasonRateLimited}},
		{"block over", func(i *HealthInput) { i.BlockedUntil = now.Add(-time.Minute) }, HealthState{HealthOK, ""}},
		{"stale", func(i *HealthInput) { i.LastSuccessAt = now.Add(-StaleIntervals*hour - time.Second) }, HealthState{HealthStale, ""}},
		{"not yet stale", func(i *HealthInput) { i.LastSuccessAt = now.Add(-StaleIntervals * hour) }, HealthState{HealthOK, ""}},
		{"degraded beats stale", func(i *HealthInput) {
			i.LastSuccessAt, i.StreamStatus, i.StreamReason = now.Add(-100*hour), "degraded", ClassSchemaDrift
		}, HealthState{HealthDegraded, ClassSchemaDrift}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := ok
			tc.edit(&in)
			if got := DeriveHealth(in); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
