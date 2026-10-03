// Package audit records append-only audit events. Events never hold secrets or health values:
// callers pass only identifiers and settings, and sensitive-looking keys are masked regardless.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/obs"
)

// Actors other than the owner are "api_key:<id>", "client:<id>" or "system".
const (
	Owner  = "owner"
	System = "system"
)

// Event is one audit record.
type Event struct {
	UserID     *uuid.UUID
	Actor      string
	Action     string // e.g. "owner.password_reset", "keys.rotate"
	TargetType string
	TargetID   string
	Detail     map[string]any
}

// Record inserts e. Pass the Queries of the transaction that performs the audited change.
func Record(ctx context.Context, q *dbq.Queries, e Event) error {
	detail, err := json.Marshal(mask(e.Detail))
	if err != nil {
		return fmt.Errorf("audit detail: %w", err)
	}
	_, err = q.InsertAuditEvent(ctx, dbq.InsertAuditEventParams{
		UserID:     e.UserID,
		Actor:      e.Actor,
		Action:     e.Action,
		TargetType: optional(e.TargetType),
		TargetID:   optional(e.TargetID),
		Detail:     detail,
	})
	return err
}

// Diff returns {"field": {"from": old, "to": new}} for every changed field. Record replaces
// sensitive fields by "[redacted]", so the event says only that they changed.
func Diff(before, after map[string]any) map[string]any {
	out := map[string]any{}
	for k, b := range before {
		if a, ok := after[k]; !ok || !reflect.DeepEqual(a, b) {
			out[k] = map[string]any{"from": b, "to": after[k]}
		}
	}
	for k, a := range after {
		if _, ok := before[k]; !ok {
			out[k] = map[string]any{"from": nil, "to": a}
		}
	}
	return out
}

func mask(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if obs.IsSensitiveKey(k) {
			out[k] = "[redacted]"
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			v = mask(sub)
		}
		out[k] = v
	}
	return out
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
