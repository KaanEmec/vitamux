package withings

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
	"github.com/KaanEmec/vitamux/internal/jobs"
)

// SettingNotifications is the owner setting (settings.key, JSON boolean, default false) that
// turns Withings notifications on. Polling runs either way.
const SettingNotifications = "withings.notifications"

const (
	hookPath       = "webhooks/withings"
	maxNotifyRange = 31 * 24 * time.Hour // a notified window is a few seconds; longer ones are not trusted
)

var (
	// ErrUnknownHook is a notification for a token no connection has: answer 404, enqueue nothing.
	ErrUnknownHook = errors.New("withings: unknown notification hook")
	// ErrNoPublicURL means notifications cannot be turned on without VITAMUX_PUBLIC_URL.
	ErrNoPublicURL = errors.New("withings: notifications need a public URL")
)

// Notifications manages the optional notify subscriptions (docs/providers/withings.md#notifications)
// and turns incoming notifications into deduplicated window syncs.
type Notifications struct {
	db   *db.DB
	rt   *connectors.Runtime
	c    *Connector
	base string // ${VITAMUX_PUBLIC_URL}/webhooks/withings/; empty without a public URL
	log  *slog.Logger
}

// NewNotifications wires the service. Register its Authorized method with rt.OnAuthorized so
// new and reauthorized connections are subscribed while the setting is on.
func NewNotifications(d *db.DB, rt *connectors.Runtime, c *Connector, publicURL *url.URL, log *slog.Logger) *Notifications {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	n := &Notifications{db: d, rt: rt, c: c, log: log}
	if publicURL != nil {
		n.base = publicURL.JoinPath(hookPath).String() + "/"
	}
	return n
}

// Enabled reports the owner's setting.
func (n *Notifications) Enabled(ctx context.Context, userID uuid.UUID) (bool, error) {
	v, err := n.db.Q().GetUserSetting(ctx, dbq.GetUserSettingParams{UserID: userID, Key: SettingNotifications})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var on bool
	_ = json.Unmarshal(v, &on) // anything but true is off
	return on, nil
}

// SetEnabled stores the setting and applies it to every Withings connection of the user: on
// subscribes the active ones, off revokes every subscription and invalidates the hook tokens
// (later notifications get 404). The setting is stored even when a provider call fails; the
// joined errors are returned and applying again retries.
func (n *Notifications) SetEnabled(ctx context.Context, userID uuid.UUID, on bool) error {
	if on && n.base == "" {
		return ErrNoPublicURL
	}
	v, _ := json.Marshal(on)
	if err := n.db.Q().PutUserSetting(ctx, dbq.PutUserSettingParams{UserID: userID, Key: SettingNotifications, Value: v}); err != nil {
		return db.MapErr(err)
	}
	conns, err := n.db.Q().ListHookConnections(ctx, dbq.ListHookConnectionsParams{UserID: userID, Provider: Provider})
	if err != nil {
		return db.MapErr(err)
	}
	var errs []error
	for _, c := range conns {
		active := c.Status == "active" || c.Status == "degraded"
		switch {
		case on && active:
			errs = append(errs, n.subscribe(ctx, c.ID, c.HookTokenHash))
		case !on && c.HookTokenHash != nil:
			errs = append(errs, n.unsubscribe(ctx, c.ID, active))
		}
	}
	return errors.Join(errs...)
}

// Authorized is the connectors.Runtime OnAuthorized hook: it subscribes a connected or
// reauthorized Withings connection while the setting is on. Failures are logged; polling covers.
func (n *Notifications) Authorized(ctx context.Context, connectionID uuid.UUID, provider string) {
	if provider != Provider || n.base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	c, err := n.connection(ctx, connectionID)
	if err == nil && c != nil {
		err = n.subscribe(ctx, connectionID, c.HookTokenHash)
	}
	if err != nil {
		n.log.Warn("withings notifications: subscribe failed", "connection_id", connectionID, "err", err)
	}
}

// connection returns the connection's hook row when the owner has notifications on, else nil.
func (n *Notifications) connection(ctx context.Context, id uuid.UUID) (*dbq.ListHookConnectionsRow, error) {
	row, err := n.db.Q().GetSyncConnection(ctx, id)
	if err != nil {
		return nil, db.MapErr(err)
	}
	if on, err := n.Enabled(ctx, row.UserID); err != nil || !on {
		return nil, err
	}
	conns, err := n.db.Q().ListHookConnections(ctx, dbq.ListHookConnectionsParams{UserID: row.UserID, Provider: Provider})
	if err != nil {
		return nil, db.MapErr(err)
	}
	for _, c := range conns {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, db.ErrNotFound
}

// subscribe makes the connection's notify profiles exactly applis × one callback URL: it keeps
// a listed callback whose token matches the stored hash (or stores a new token's hash before
// subscribing, since a notification may follow at once), adds missing applis and revokes
// Vitamux callbacks with older tokens. Safe to repeat.
func (n *Notifications) subscribe(ctx context.Context, id uuid.UUID, stored []byte) error {
	return n.rt.WithCredentials(ctx, id, func(c connectors.Conn, cred connectors.Credentials) error {
		subs, err := n.c.subscriptions(ctx, c.HTTP, cred.AccessToken)
		if err != nil {
			return err
		}
		current := ""
		for _, s := range subs {
			if h := hookHash(s.CallbackURL); h != nil && bytes.Equal(h, stored) {
				current = s.CallbackURL
			}
		}
		if current == "" {
			var b [32]byte
			_, _ = rand.Read(b[:])
			token := base64.RawURLEncoding.EncodeToString(b[:])
			sum := sha256.Sum256([]byte(token))
			if err := n.db.Q().SetHookTokenHash(ctx, dbq.SetHookTokenHashParams{HookTokenHash: sum[:], ID: id}); err != nil {
				return db.MapErr(err)
			}
			current = n.base + token
		}
		for _, a := range applis {
			if !slices.Contains(subs, subscription{Appli: a, CallbackURL: current}) {
				if err := n.c.subscribe(ctx, c.HTTP, cred.AccessToken, current, a); err != nil {
					return err
				}
			}
		}
		for _, s := range subs {
			if s.CallbackURL != current && hookHash(s.CallbackURL) != nil {
				if err := n.c.unsubscribe(ctx, c.HTTP, cred.AccessToken, s.CallbackURL, s.Appli); err != nil {
					return err
				}
			}
		}
		n.log.Info("withings notifications subscribed", "connection_id", id)
		return nil
	})
}

// unsubscribe revokes every Vitamux notify profile of an active connection, then forgets the
// hook token whatever the provider said, so its notifications get 404 (Withings cancels a
// callback that keeps failing).
func (n *Notifications) unsubscribe(ctx context.Context, id uuid.UUID, active bool) error {
	var err error
	if active {
		err = n.rt.WithCredentials(ctx, id, func(c connectors.Conn, cred connectors.Credentials) error {
			subs, err := n.c.subscriptions(ctx, c.HTTP, cred.AccessToken)
			if err != nil {
				return err
			}
			for _, s := range subs {
				if hookHash(s.CallbackURL) != nil {
					if err := n.c.unsubscribe(ctx, c.HTTP, cred.AccessToken, s.CallbackURL, s.Appli); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	if cerr := n.db.Q().SetHookTokenHash(ctx, dbq.SetHookTokenHashParams{ID: id}); cerr != nil {
		return errors.Join(err, db.MapErr(cerr))
	}
	return err
}

// hookHash returns the SHA-256 of the token of a Vitamux callback URL (…/webhooks/withings/<token>),
// or nil for any other callback.
func hookHash(callback string) []byte {
	_, token, ok := strings.Cut(callback, "/"+hookPath+"/")
	if !ok || token == "" || strings.Contains(token, "/") {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// notification is what a callback form carries. The window is usable only when both dates
// parse, are not before the epoch and span at most maxNotifyRange (compared in seconds: a
// Duration product would overflow for absurd dates).
type notification struct {
	appli      int
	start, end int64
	user       [sha256.Size]byte // SHA-256 of userid, the form of connections.account_key
	windowOK   bool
}

func parseNotification(form url.Values) notification {
	var n notification
	n.appli, _ = strconv.Atoi(form.Get("appli"))
	start, serr := strconv.ParseInt(form.Get("startdate"), 10, 64)
	end, eerr := strconv.ParseInt(form.Get("enddate"), 10, 64)
	n.start, n.end = start, end
	n.user = sha256.Sum256([]byte(form.Get("userid")))
	n.windowOK = serr == nil && eerr == nil && start >= 0 && end >= start && end-start <= int64(maxNotifyRange/time.Second)
	return n
}

// Notify handles one notification POST to /webhooks/withings/{token} (form userid, appli,
// startdate, enddate). An unknown token is ErrUnknownHook. Otherwise the payload is only a hint:
// a measures notification of the connection's Withings user enqueues one correction sync of
// [startdate, enddate], deduplicated per connection, stream and window while it is queued or
// running; anything else is acknowledged and ignored. The caller answers 2xx on nil.
func (n *Notifications) Notify(ctx context.Context, token string, form url.Values) error {
	if token == "" {
		return ErrUnknownHook
	}
	sum := sha256.Sum256([]byte(token))
	c, err := n.db.Q().GetHookConnection(ctx, dbq.GetHookConnectionParams{HookTokenHash: sum[:], Provider: Provider})
	if err = db.MapErr(err); errors.Is(err, db.ErrNotFound) {
		return ErrUnknownHook
	} else if err != nil {
		return err
	}
	h := parseNotification(form)
	appli, start, end := h.appli, h.start, h.end
	ignore := ""
	switch {
	case !slices.Contains(applis, appli):
		ignore = "appli"
	case c.AccountKey != nil && !bytes.Equal(c.AccountKey, h.user[:]):
		ignore = "userid"
	case !h.windowOK:
		ignore = "window"
	case c.Status != "active" && c.Status != "degraded":
		ignore = "connection_inactive"
	}
	if ignore != "" {
		n.log.Info("withings notification ignored", "connection_id", c.ID, "reason", ignore)
		return nil
	}
	from, to := time.Unix(start, 0).UTC(), time.Unix(end+1, 0).UTC() // enddate is inclusive
	_, created, err := jobs.Enqueue(ctx, n.db.Q(), jobs.NewJob{
		Kind: jobs.KindSync, ConnectionID: &c.ID, Exclusive: true,
		DedupeKey: "notify:" + c.ID.String() + ":" + StreamMeasures + ":" + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10),
		Payload:   jobs.SyncPayload{Stream: StreamMeasures, Mode: connectors.ModeCorrection, Slot: to, From: &from},
	})
	if err != nil {
		return err
	}
	n.log.Info("withings notification", "connection_id", c.ID, "appli", appli, "enqueued", created)
	return nil
}
