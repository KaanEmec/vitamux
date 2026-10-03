package api

import (
	"errors"
	"net/http"

	"github.com/KaanEmec/vitamux/internal/connectors/withings"
)

// Provider notification callbacks (docs/providers/withings.md#notifications). Public: the
// unguessable per-connection token in the path authorizes them, and a payload is only a hint
// to fetch. The access log records the route pattern, never the token.
func (rt *router) webhookRoutes() {
	rt.handle("GET /webhooks/withings/{hook_token}", public, rt.webhookProbe) // and HEAD
	rt.handle("POST /webhooks/withings/{hook_token}", public, rt.withingsNotify)
}

// webhookProbe answers the provider's validation request (Withings sends HEAD on subscribe).
func (rt *router) webhookProbe(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// withingsNotify enqueues at most one deduplicated window sync. An unknown token is 404 with no
// job; an ignored notification is still 204, because Withings retries and eventually cancels
// callbacks that do not answer 2xx.
func (rt *router) withingsNotify(w http.ResponseWriter, r *http.Request) {
	if rt.opts.Withings == nil {
		writeProblem(w, r, CodeNotFound, "no such hook")
		return
	}
	_ = r.ParseForm() // a garbled body leaves fields empty and is ignored as a hint
	switch err := rt.opts.Withings.Notify(r.Context(), r.PathValue("hook_token"), r.PostForm); {
	case errors.Is(err, withings.ErrUnknownHook):
		writeProblem(w, r, CodeNotFound, "no such hook")
	case err != nil:
		rt.internal(w, r, "withings notification", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
