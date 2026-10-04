package withings

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

const signaturePath = "/v2/signature"

var _ connectors.AppConnector = (*Connector)(nil)

// VerifyApp checks a client id and secret without a user grant: a signed getnonce
// (docs/providers/withings.md#app-registration-and-callback) answers status 0 only for a
// known client id whose secret made the signature. The nonce itself is not used.
func (w *Connector) VerifyApp(ctx context.Context, h *connectors.HTTPClient, app connectors.AppCredentials) error {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(app.ClientSecret))
	mac.Write([]byte("getnonce," + app.ClientID + "," + ts)) // values of action, client_id, timestamp, sorted by key
	env, err := w.post(ctx, h, signaturePath, "", url.Values{
		"action": {"getnonce"}, "client_id": {app.ClientID}, "timestamp": {ts}, "signature": {hex.EncodeToString(mac.Sum(nil))},
	})
	switch {
	case errors.Is(err, connectors.ErrReauthRequired): // HTTP 401
		return fmt.Errorf("withings getnonce: %w", connectors.ErrAppRejected)
	case err != nil:
		return err
	case env.http != 0:
		return fmt.Errorf("withings getnonce: HTTP %d: %w", env.http, connectors.ErrAppRejected)
	case env.Status == 0:
		return nil
	case env.Status == 601, env.Status == 2554, env.Status == 2555:
		return statusError("getnonce", env.Status)
	}
	return fmt.Errorf("withings getnonce: status %d: %w", env.Status, connectors.ErrAppRejected)
}
