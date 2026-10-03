package withings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const notifyPath = "/notify"

// applis are the notify categories that touch the measures stream: 1 weight and body
// composition, 2 temperature, 4 blood pressure, heart rate and SpO2.
var applis = []int{1, 2, 4}

// subscription is one notify profile of the Withings user the bearer token belongs to.
type subscription struct {
	Appli       int    `json:"appli"`
	CallbackURL string `json:"callbackurl"`
}

// subscriptions lists the user's notify profiles (action=list).
func (w *Connector) subscriptions(ctx context.Context, h *connectors.HTTPClient, bearer string) ([]subscription, error) {
	body, err := w.notify(ctx, h, bearer, url.Values{"action": {"list"}})
	if err != nil {
		return nil, err
	}
	var b struct {
		Profiles *[]struct {
			Appli       *int    `json:"appli"`
			CallbackURL *string `json:"callbackurl"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(body, &b); err != nil || b.Profiles == nil {
		fp, _ := ingest.ShapeFingerprint(body)
		return nil, &connectors.SchemaDriftError{Endpoint: "notify list", Fingerprint: fp}
	}
	out := make([]subscription, 0, len(*b.Profiles))
	for _, p := range *b.Profiles {
		if p.Appli == nil || p.CallbackURL == nil {
			fp, _ := ingest.ShapeFingerprint(body)
			return nil, &connectors.SchemaDriftError{Endpoint: "notify list", Fingerprint: fp}
		}
		out = append(out, subscription{Appli: *p.Appli, CallbackURL: *p.CallbackURL})
	}
	return out, nil
}

// subscribe adds a notify profile. Withings first sends HEAD to callback and needs a 2xx.
func (w *Connector) subscribe(ctx context.Context, h *connectors.HTTPClient, bearer, callback string, appli int) error {
	_, err := w.notify(ctx, h, bearer, url.Values{"action": {"subscribe"}, "callbackurl": {callback},
		"appli": {strconv.Itoa(appli)}, "comment": {"Vitamux"}})
	return err
}

// unsubscribe removes a notify profile (action=revoke).
func (w *Connector) unsubscribe(ctx context.Context, h *connectors.HTTPClient, bearer, callback string, appli int) error {
	_, err := w.notify(ctx, h, bearer, url.Values{"action": {"revoke"}, "callbackurl": {callback}, "appli": {strconv.Itoa(appli)}})
	return err
}

// notify calls the notify endpoint with the bearer token (no signature needed) and returns the body.
func (w *Connector) notify(ctx context.Context, h *connectors.HTTPClient, bearer string, form url.Values) (json.RawMessage, error) {
	env, err := w.post(ctx, h, notifyPath, bearer, form)
	switch {
	case err != nil:
		return nil, err
	case env.http != 0:
		return nil, fmt.Errorf("withings notify %s: HTTP %d: %w", form.Get("action"), env.http, connectors.ErrPermanent)
	case env.Status != 0:
		return nil, statusError("notify "+form.Get("action"), env.Status)
	}
	return env.Body, nil
}
