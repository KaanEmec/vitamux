package withings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/ingest"
)

const tokenPath = "/v2/oauth2" //nolint:gosec // an endpoint path, not a credential

// Begin returns the consent URL (docs/providers/withings.md#oauth-20).
func (w *Connector) Begin(_ context.Context, in connectors.AuthInput) (connectors.AuthStep, error) {
	if w.cfg.ClientID == "" || w.cfg.ClientSecret == "" {
		return connectors.AuthStep{}, connectors.ErrAuthUnavailable
	}
	u, err := url.Parse(w.cfg.AuthURL)
	if err != nil {
		return connectors.AuthStep{}, err
	}
	u.RawQuery = url.Values{
		"response_type": {"code"}, "client_id": {w.cfg.ClientID}, "scope": {scope},
		"redirect_uri": {in.RedirectURL}, "state": {in.State},
	}.Encode()
	return connectors.AuthStep{RedirectURL: u.String()}, nil
}

// Continue exchanges the callback's code (valid 30 seconds) for tokens and the Withings userid.
func (w *Connector) Continue(ctx context.Context, c connectors.Conn, in connectors.AuthInput) (connectors.Authorized, error) {
	code := in.Callback.Get("code")
	if code == "" {
		return connectors.Authorized{}, connectors.ErrAuthDenied
	}
	tok, err := w.token(ctx, c.HTTP, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {in.RedirectURL}})
	if err != nil {
		return connectors.Authorized{}, err
	}
	return connectors.Authorized{AccountID: string(tok.UserID), Credentials: tok.credentials()}, nil
}

// Refresh rotates the token pair. The runtime persists the result before using it, because
// the old refresh token dies once the new access token is used.
func (w *Connector) Refresh(ctx context.Context, c connectors.Conn, cred connectors.Credentials) (connectors.Credentials, error) {
	if cred.RefreshToken == "" {
		return connectors.Credentials{}, fmt.Errorf("withings: no refresh token: %w", connectors.ErrReauthRequired)
	}
	tok, err := w.token(ctx, c.HTTP, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cred.RefreshToken}})
	if err != nil {
		return connectors.Credentials{}, err
	}
	return tok.credentials(), nil
}

type tokenBody struct {
	UserID       flexID `json:"userid"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (t tokenBody) credentials() connectors.Credentials {
	c := connectors.Credentials{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken}
	if t.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return c
}

// token calls requesttoken. A refused grant (HTTP 400/401, status 401, 342, 343 or 503
// "invalid params", or an invalid_grant error) is ErrReauthRequired.
func (w *Connector) token(ctx context.Context, h *connectors.HTTPClient, form url.Values) (tokenBody, error) {
	if w.cfg.ClientID == "" || w.cfg.ClientSecret == "" {
		return tokenBody{}, fmt.Errorf("withings: client id and secret are not configured: %w", connectors.ErrPermanent)
	}
	form.Set("action", "requesttoken")
	form.Set("client_id", w.cfg.ClientID)
	form.Set("client_secret", w.cfg.ClientSecret)
	env, err := w.post(ctx, h, tokenPath, "", form)
	switch {
	case err != nil:
		return tokenBody{}, err
	case env.http == 400 || env.Status == 503 || strings.Contains(env.Error, "invalid_grant"):
		return tokenBody{}, fmt.Errorf("withings token: grant refused: %w", connectors.ErrReauthRequired)
	case env.http != 0:
		return tokenBody{}, fmt.Errorf("withings token: HTTP %d: %w", env.http, connectors.ErrPermanent)
	case env.Status != 0:
		return tokenBody{}, statusError("token", env.Status)
	}
	var t tokenBody
	if err := json.Unmarshal(env.Body, &t); err != nil || t.AccessToken == "" || t.RefreshToken == "" || t.UserID == "" {
		fp, _ := ingest.ShapeFingerprint(env.Body)
		return tokenBody{}, &connectors.SchemaDriftError{Endpoint: "v2/oauth2 requesttoken", Fingerprint: fp}
	}
	return t, nil
}

// flexID accepts an id sent as a JSON number or string (the spec and examples disagree).
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(b, `"`)
	if _, err := strconv.ParseInt(string(b), 10, 64); err != nil {
		return fmt.Errorf("withings: id is not an integer")
	}
	*f = flexID(b)
	return nil
}
