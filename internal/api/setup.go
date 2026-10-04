package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"github.com/KaanEmec/vitamux/internal/api/oapi"
	"github.com/KaanEmec/vitamux/internal/auth"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/connectors/remote"
	"github.com/KaanEmec/vitamux/internal/db"
)

// Source setup in the web panel (E20, docs/adr/0021-source-setup.md): providers with their
// setup state, write-only app credentials, sidecar probes and sidecars added in the panel.
// Secrets are changed by the owner session only, and every change is audited.
func (rt *router) setupRoutes() {
	rt.handle("PUT /api/v1/providers/{provider}/app-credentials", session, rt.ops.PutProviderAppCredentials)
	rt.handle("DELETE /api/v1/providers/{provider}/app-credentials", session, rt.ops.DeleteProviderAppCredentials)
	rt.handle("POST /api/v1/providers/{provider}/app-credentials/verify", session, rt.ops.VerifyProviderAppCredentials)
	rt.handle("POST /api/v1/providers/{provider}/probe", scope(auth.WriteConfig), rt.ops.ProbeProvider)
	rt.handle("GET /api/v1/sidecars", scope(auth.ReadConfig), rt.ops.ListSidecars)
	rt.handle("POST /api/v1/sidecars", session, rt.ops.CreateSidecar)
	rt.handle("DELETE /api/v1/sidecars/{name}", session, rt.ops.DeleteSidecar)
}

var errNoProvider = problemErr(CodeNotFound, "no connector serves this provider")

// maxCallbackLen is the longest callback URL Withings accepts (docs/providers/withings.md).
const maxCallbackLen = 255

func (o *owner) setup() (*connectors.Runtime, *connectors.Apps, *remote.Manager, error) {
	rt, err := o.runtime()
	if err != nil {
		return nil, nil, nil, err
	}
	if o.opts.Apps == nil || o.opts.Sidecars == nil {
		return nil, nil, nil, problemErr(CodeUnavailable, "source setup is unavailable")
	}
	return rt, o.opts.Apps, o.opts.Sidecars, nil
}

// ListProviders lists the registered connectors, in-process and sidecars, with their setup state.
func (o *owner) ListProviders(ctx context.Context, _ oapi.ListProvidersRequestObject) (oapi.ListProvidersResponseObject, error) {
	rt, err := o.runtime()
	if err != nil {
		return nil, err
	}
	counts, err := o.connectionCounts(ctx)
	if err != nil {
		return nil, err
	}
	ds := rt.Providers()
	out := oapi.ListProviders200JSONResponse{Providers: make([]oapi.Provider, len(ds))}
	for i, d := range ds {
		if out.Providers[i], err = o.provider(ctx, d, counts[d.Provider]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (o *owner) connectionCounts(ctx context.Context) (map[string]int, error) {
	rows, err := o.opts.DB.Q().ProviderConnectionCounts(ctx, auth.PrincipalFrom(ctx).UserID)
	if err != nil {
		return nil, db.MapErr(err)
	}
	m := make(map[string]int, len(rows))
	for _, r := range rows {
		m[r.Provider] = int(r.Connections)
	}
	return m, nil
}

// providerByCode answers one provider, as listed.
func (o *owner) providerByCode(ctx context.Context, code string) (oapi.Provider, error) {
	rt, err := o.runtime()
	if err != nil {
		return oapi.Provider{}, err
	}
	d, ok := rt.Describe(code)
	if !ok {
		return oapi.Provider{}, errNoProvider
	}
	counts, err := o.connectionCounts(ctx)
	if err != nil {
		return oapi.Provider{}, err
	}
	return o.provider(ctx, d, counts[code])
}

// provider builds a provider with its setup state (ADR-0021: the first state that applies).
func (o *owner) provider(ctx context.Context, d connectors.Descriptor, connections int) (oapi.Provider, error) {
	p := oapi.Provider{Code: d.Provider, Name: cmp.Or(d.Name, d.Provider), Official: d.Official, Remote: d.Remote,
		Available: d.Available(), Connections: connections, Problems: []oapi.SetupProblem{}}
	if d.AuthKind != "" {
		k := oapi.ProviderAuthKind(d.AuthKind)
		p.AuthKind = &k
	}
	if u := d.Upstream; u != nil {
		p.Upstream = &oapi.Upstream{Package: u.Package, Version: u.Version, SourceURL: u.SourceURL}
	}
	if s, ok := o.sidecar(d.Provider); ok {
		p.Sidecar = &oapi.ProviderSidecar{Source: oapi.ProviderSidecarSource(s.Source), Bundled: s.Bundled, Enable: []oapi.SidecarEnable{}}
		if !d.Available() {
			for _, e := range remote.EnableSteps(s.Name, o.opts.Install) {
				p.Sidecar.Enable = append(p.Sidecar.Enable, oapi.SidecarEnable{Install: oapi.SidecarEnableInstall(e.Install), Line: e.Line, Apply: e.Apply})
			}
			code := cmp.Or(s.Conn.LastProblem(), remote.ProblemUnreachable) // "" only in a race with a describe
			p.Problems = append(p.Problems, oapi.SetupProblem{Code: oapi.SetupProblemCode(code), Message: sidecarProblemMessage(code, s)})
		}
	}
	blocking := false
	if d.AuthKind == connectors.AuthOAuth2 && o.opts.PublicURL != nil {
		cb := o.opts.PublicURL.JoinPath("oauth", d.Provider, "callback").String()
		p.CallbackURL = &cb
		probs := publicURLProblems(o.opts.PublicURL, cb)
		p.Problems = append(p.Problems, probs...)
		blocking = len(probs) > 0 && !o.opts.Development
	}
	c, _ := o.opts.Connectors.Connector(d.Provider)
	if _, ok := c.(connectors.AppConnector); ok && o.opts.Apps != nil {
		st, err := o.opts.Apps.Status(ctx, d.Provider)
		if err != nil {
			return oapi.Provider{}, err
		}
		p.AppCredentials = &oapi.AppCredentialsStatus{Set: st.Set, ManagedByEnvironment: st.ManagedByEnvironment, ClientID: optString(st.ClientID), UpdatedAt: st.UpdatedAt}
	}
	switch {
	case !d.Available():
		p.SetupState = oapi.NeedsSidecar
	case connections > 0:
		p.SetupState = oapi.Connected
	case blocking:
		p.SetupState = oapi.NeedsPublicURL
	case p.AppCredentials != nil && !p.AppCredentials.Set:
		p.SetupState = oapi.NeedsAppCredentials
	default:
		p.SetupState = oapi.Ready
	}
	return p, nil
}

func (o *owner) sidecar(name string) (remote.Sidecar, bool) {
	if o.opts.Sidecars == nil {
		return remote.Sidecar{}, false
	}
	return o.opts.Sidecars.Get(name)
}

func sidecarProblemMessage(code string, s remote.Sidecar) string {
	switch code {
	case remote.ProblemSecretMissing:
		return "The shared secret file of this sidecar does not exist yet: run `vitamux admin init-secrets` (Compose: docker compose --profile setup run --rm init-secrets), then check again."
	case remote.ProblemUnreachable:
		if s.Bundled {
			return "The " + s.Name + " sidecar is not running. Turn it on as shown, then check again."
		}
		return "Nothing answers at " + s.URL.String() + ". Start the sidecar on the private network, then check again."
	}
	return "The sidecar answers but its self-description failed. Check that it runs a supported version and has the shared secret Vitamux generated."
}

// publicURLProblems lists what a provider would refuse in callback, derived from
// VITAMUX_PUBLIC_URL: https, a domain name, port 443 or 80, at most 255 characters.
func publicURLProblems(pub *url.URL, callback string) []oapi.SetupProblem {
	var out []oapi.SetupProblem
	add := func(code oapi.SetupProblemCode, msg string) {
		out = append(out, oapi.SetupProblem{Code: code, Message: msg})
	}
	host := strings.ToLower(pub.Hostname())
	if pub.Scheme != "https" {
		add(oapi.PublicURLNotHTTPS, "Set VITAMUX_PUBLIC_URL to the https address of your reverse proxy: providers accept only https callbacks.")
	}
	if _, err := netip.ParseAddr(host); err == nil || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		add(oapi.PublicURLIPHost, "VITAMUX_PUBLIC_URL needs a domain name, not an IP address or localhost.")
	}
	if port := pub.Port(); port != "" && port != "443" && port != "80" {
		add(oapi.PublicURLPort, "VITAMUX_PUBLIC_URL must use the standard port 443 (no :"+port+").")
	}
	if len(callback) > maxCallbackLen {
		add(oapi.PublicURLTooLong, fmt.Sprintf("The callback URL is longer than %d characters; use a shorter VITAMUX_PUBLIC_URL.", maxCallbackLen))
	}
	return out
}

// appProvider checks that provider runs on the owner's own application.
func (o *owner) appProvider(provider string) (*connectors.Apps, error) {
	rt, apps, _, err := o.setup()
	if err != nil {
		return nil, err
	}
	c, ok := rt.Connector(provider)
	if _, isApp := c.(connectors.AppConnector); !ok || !isApp {
		return nil, problemErr(CodeNotFound, "this provider does not use app credentials of your own")
	}
	return apps, nil
}

var errManagedByEnv = problemErr(CodeConflict, "this value is set by the environment, which wins over the panel; change or remove it there")

func (o *owner) PutProviderAppCredentials(ctx context.Context, req oapi.PutProviderAppCredentialsRequestObject) (oapi.PutProviderAppCredentialsResponseObject, error) {
	apps, err := o.appProvider(req.Provider)
	if err != nil {
		return nil, err
	}
	b := req.Body
	app := connectors.AppCredentials{ClientID: strings.TrimSpace(b.ClientID), ClientSecret: strings.TrimSpace(b.ClientSecret)}
	if !app.IsSet() {
		return nil, problemErr(CodeValidationFailed, "client id and secret must not be blank")
	}
	p := auth.PrincipalFrom(ctx)
	if _, err := apps.Put(ctx, req.Provider, app, p.UserID, p.Actor()); errors.Is(err, connectors.ErrManagedByEnvironment) {
		return nil, errManagedByEnv
	} else if err != nil {
		return nil, err
	}
	out, err := o.providerByCode(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	return oapi.PutProviderAppCredentials200JSONResponse(out), nil
}

func (o *owner) DeleteProviderAppCredentials(ctx context.Context, req oapi.DeleteProviderAppCredentialsRequestObject) (oapi.DeleteProviderAppCredentialsResponseObject, error) {
	apps, err := o.appProvider(req.Provider)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	if err := apps.Delete(ctx, req.Provider, ptrVal(req.Params.Confirm), p.UserID, p.Actor()); err != nil {
		return nil, setupErr(err, "no app credentials are stored for this provider", "connect again after setting new ones")
	}
	return oapi.DeleteProviderAppCredentials204Response{}, nil
}

// setupErr maps the setup errors shared by app credentials and sidecars to problems.
func setupErr(err error, notFound, inUse string) error {
	var used *connectors.InUseError
	switch {
	case errors.Is(err, connectors.ErrManagedByEnvironment):
		return errManagedByEnv
	case errors.As(err, &used):
		return problemErr(CodeConflict, fmt.Sprintf("%d connections use this; repeat with confirm=true to remove it anyway (%s)", used.Connections, inUse))
	case errors.Is(err, db.ErrNotFound):
		return problemErr(CodeNotFound, notFound)
	}
	return err
}

var verifyMessages = map[string]string{
	"valid":        "The provider accepted the client id and secret.",
	"invalid":      "The provider refused the client id and secret. Copy both again from its developer dashboard.",
	"unverifiable": "This provider cannot check them before an account connects; connecting will.",
}

func (o *owner) VerifyProviderAppCredentials(ctx context.Context, req oapi.VerifyProviderAppCredentialsRequestObject) (oapi.VerifyProviderAppCredentialsResponseObject, error) {
	apps, err := o.appProvider(req.Provider)
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	result, err := o.opts.Connectors.VerifyApp(ctx, apps, req.Provider, p.UserID, p.Actor())
	var rl *connectors.RateLimitedError
	switch {
	case errors.Is(err, db.ErrNotFound):
		return nil, problemErr(CodeNotFound, "no app credentials are set for this provider")
	case errors.As(err, &rl):
		return nil, problemErr(CodeRateLimited, "the provider is rate limiting; try again in a minute")
	case err != nil:
		o.log.WarnContext(ctx, "verify app credentials", "provider", req.Provider, "request_id", requestIDFrom(ctx), "err", err)
		return nil, problemErr(CodeUnavailable, "the provider could not be reached; try again later")
	}
	return oapi.VerifyProviderAppCredentials200JSONResponse{Result: oapi.AppCredentialsVerificationResult(result), Message: verifyMessages[result]}, nil
}

func (o *owner) ProbeProvider(ctx context.Context, req oapi.ProbeProviderRequestObject) (oapi.ProbeProviderResponseObject, error) {
	_, _, sidecars, err := o.setup()
	if err != nil {
		return nil, err
	}
	if s, ok := sidecars.Get(req.Provider); ok {
		_ = s.Conn.Probe(ctx) // the outcome is the provider's state; failures are logged by the connector
	}
	out, err := o.providerByCode(ctx, req.Provider)
	if err != nil {
		return nil, err
	}
	return oapi.ProbeProvider200JSONResponse(out), nil
}

func sidecarBody(s remote.Sidecar) oapi.Sidecar {
	return oapi.Sidecar{Name: s.Name, URL: s.URL.String(), Source: oapi.SidecarSource(s.Source), Bundled: s.Bundled,
		Available: s.Conn.LastProblem() == "", CreatedAt: s.CreatedAt}
}

func (o *owner) ListSidecars(context.Context, oapi.ListSidecarsRequestObject) (oapi.ListSidecarsResponseObject, error) {
	_, _, sidecars, err := o.setup()
	if err != nil {
		return nil, err
	}
	list := sidecars.List()
	out := oapi.ListSidecars200JSONResponse{Sidecars: make([]oapi.Sidecar, len(list))}
	for i, s := range list {
		out.Sidecars[i] = sidecarBody(s)
	}
	return out, nil
}

func (o *owner) CreateSidecar(ctx context.Context, req oapi.CreateSidecarRequestObject) (oapi.CreateSidecarResponseObject, error) {
	_, _, sidecars, err := o.setup()
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	s, secret, err := sidecars.Add(ctx, req.Body.Name, req.Body.URL, p.UserID, p.Actor())
	switch {
	case errors.Is(err, remote.ErrInvalidName):
		return nil, problemErr(CodeValidationFailed, err.Error(), FieldError{Pointer: "/name", Detail: "not a provider code"})
	case errors.Is(err, remote.ErrInvalidURL):
		return nil, problemErr(CodeValidationFailed, err.Error(), FieldError{Pointer: "/url", Detail: "not a usable sidecar URL"})
	case errors.Is(err, remote.ErrNameTaken):
		return nil, problemErr(CodeConflict, err.Error())
	case err != nil:
		return nil, err
	}
	return oapi.CreateSidecar201JSONResponse{Sidecar: sidecarBody(s), Secret: secret}, nil
}

func (o *owner) DeleteSidecar(ctx context.Context, req oapi.DeleteSidecarRequestObject) (oapi.DeleteSidecarResponseObject, error) {
	_, _, sidecars, err := o.setup()
	if err != nil {
		return nil, err
	}
	p := auth.PrincipalFrom(ctx)
	if err := sidecars.Remove(ctx, req.Name, ptrVal(req.Params.Confirm), p.UserID, p.Actor()); err != nil {
		return nil, setupErr(err, "no such sidecar", "its connections stop syncing")
	}
	return oapi.DeleteSidecar204Response{}, nil
}
