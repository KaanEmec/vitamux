package remote

import (
	"cmp"
	"context"
	"encoding/json"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// Recorder is the Options.OnDescribe of serve: it registers the sidecar's provider (a known
// code keeps its row) and records its upstream package on the provider's remote connections,
// auditing each change as connection.upstream_changed. It also reconciles the connections'
// streams with the descriptor (connectors.ReconcileStreams).
func Recorder(d *db.DB) func(context.Context, connectors.Descriptor) error {
	return func(ctx context.Context, desc connectors.Descriptor) error {
		var upstream []byte
		if desc.Upstream != nil {
			var err error
			if upstream, err = json.Marshal(desc.Upstream); err != nil { // as connectors' finalize stores it
				return err
			}
		}
		return d.Tx(ctx, func(q *dbq.Queries) error {
			if err := q.RegisterProvider(ctx, dbq.RegisterProviderParams{Code: desc.Provider, Name: cmp.Or(desc.Name, desc.Provider)}); err != nil {
				return err
			}
			changed, err := q.UpdateConnectionUpstream(ctx, dbq.UpdateConnectionUpstreamParams{Upstream: upstream, Provider: desc.Provider})
			if err != nil {
				return err
			}
			for _, c := range changed {
				if err := audit.Record(ctx, q, audit.Event{
					UserID: &c.UserID, Actor: audit.System, Action: "connection.upstream_changed",
					TargetType: "connection", TargetID: c.ID.String(),
					Detail: map[string]any{"provider": desc.Provider, "from": json.RawMessage(c.Previous), "to": json.RawMessage(upstream)},
				}); err != nil {
					return err
				}
			}
			return connectors.ReconcileStreams(ctx, q, desc)
		})
	}
}
