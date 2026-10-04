package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/KaanEmec/vitamux/internal/audit"
	"github.com/KaanEmec/vitamux/internal/connectors"
	"github.com/KaanEmec/vitamux/internal/crypto"
	"github.com/KaanEmec/vitamux/internal/db"
	"github.com/KaanEmec/vitamux/internal/db/dbq"
)

// startupDescribe bounds the describe of every sidecar at startup; startup never waits longer.
const startupDescribe = 3 * time.Second

// Sources of a sidecar registration.
const (
	SourceEnvironment = "environment" // VITAMUX_SIDECARS: read-only in the panel
	SourcePanel       = "panel"       // added in the panel: a sealed row in sidecars
)

// Bundled lists the sidecars the release Compose files ship, off until enabled (ADR-0021):
// the Compose profile and the Coolify replica variable that turn each on.
var Bundled = map[string]struct{ Profile, CoolifyVar string }{
	"garmin": {"garmin", "GARMIN_SIDECAR"},
	"whoop":  {"whoop", "WHOOP_SIDECAR"},
}

// Enable is how to turn on a bundled sidecar on one install target: the line to add, then
// what to run.
type Enable struct{ Install, Line, Apply string }

// EnableSteps returns how to turn on bundled sidecar name: for install ("compose" or
// "coolify"), or for both when install is empty. Nil for a sidecar that is not bundled.
func EnableSteps(name, install string) []Enable {
	b, ok := Bundled[name]
	if !ok {
		return nil
	}
	all := []Enable{
		{"compose", "COMPOSE_PROFILES=" + b.Profile, "docker compose up -d"},
		{"coolify", b.CoolifyVar + "=1", "Redeploy the resource in Coolify"},
	}
	if install == "" {
		return all
	}
	return slices.DeleteFunc(all, func(e Enable) bool { return e.Install != install })
}

// Sidecar is one registered sidecar connector.
type Sidecar struct {
	Name      string
	URL       *url.URL
	Source    string
	Bundled   bool
	CreatedAt *time.Time // panel sidecars
	Conn      *Connector
}

// EnvSidecar is a sidecar of VITAMUX_SIDECARS; Secret is empty while its file does not exist.
type EnvSidecar struct {
	Name, Secret, SecretFile string
	URL                      *url.URL
}

// Manager registers sidecar connectors: those of the environment and those added in the panel,
// which join and leave the registry while serving.
type Manager struct {
	db   *db.DB
	keys *crypto.Keyring // nil: panel sidecars are unavailable
	reg  *connectors.Registry
	log  *slog.Logger

	mu sync.Mutex
	m  map[string]*Sidecar
}

// NewManager returns a manager that registers into reg.
func NewManager(d *db.DB, keys *crypto.Keyring, reg *connectors.Registry, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Manager{db: d, keys: keys, reg: reg, log: log, m: map[string]*Sidecar{}}
}

func (m *Manager) connector(name, secret, secretFile string, u *url.URL) *Connector {
	_, bundled := Bundled[name]
	return New(Options{Name: name, URL: u, Secret: secret, SecretFile: secretFile, Optional: bundled, Log: m.log, OnDescribe: Recorder(m.db)})
}

// Start registers the environment's sidecars and the panel's stored ones, and describes them
// in parallel for at most a few seconds; an unreachable one stays registered as a placeholder.
func (m *Manager) Start(ctx context.Context, env []EnvSidecar) error {
	var all []*Sidecar
	for _, e := range env {
		_, bundled := Bundled[e.Name]
		all = append(all, &Sidecar{Name: e.Name, URL: e.URL, Source: SourceEnvironment, Bundled: bundled, Conn: m.connector(e.Name, e.Secret, e.SecretFile, e.URL)})
	}
	if m.keys != nil {
		rows, err := m.db.Q().ListSidecars(ctx)
		if err != nil {
			return db.MapErr(err)
		}
		for _, r := range rows {
			s, err := m.open(r)
			if err != nil {
				m.log.Warn("panel sidecar skipped", "provider", r.Name, "err", err)
				continue
			}
			all = append(all, s)
		}
	}
	m.mu.Lock()
	var added []*Sidecar
	for _, s := range all {
		if err := m.reg.Add(s.Conn); err != nil {
			m.log.Warn("sidecar not registered", "provider", s.Name, "err", err) // e.g. a panel name the environment took since
			continue
		}
		m.m[s.Name] = s
		added = append(added, s)
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, startupDescribe)
	defer cancel()
	var wg sync.WaitGroup
	for _, s := range added {
		wg.Go(func() { _ = s.Conn.Discover(ctx) }) // failures are logged by the connector
	}
	wg.Wait()
	return nil
}

func (m *Manager) open(r dbq.ListSidecarsRow) (*Sidecar, error) {
	u, err := ParseURL(r.Url)
	if err != nil {
		return nil, err
	}
	secret, err := m.keys.Open(crypto.Credentials, r.Ciphertext, crypto.SidecarAAD(r.Name))
	if err != nil {
		return nil, err
	}
	return &Sidecar{Name: r.Name, URL: u, Source: SourcePanel, CreatedAt: &r.CreatedAt, Conn: m.connector(r.Name, string(secret), "", u)}, nil
}

// List returns every registered sidecar by name.
func (m *Manager) List() []Sidecar {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sidecar, 0, len(m.m))
	for _, s := range m.m {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Sidecar) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Get returns the registered sidecar of provider.
func (m *Manager) Get(name string) (Sidecar, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.m[name]
	if !ok {
		return Sidecar{}, false
	}
	return *s, true
}

var sidecarName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Errors of Add, safe to show.
var (
	ErrInvalidName = errors.New("the name must be a provider code: lower case letters, digits and _, starting with a letter")
	ErrInvalidURL  = errors.New("the URL must be an absolute http(s) URL without credentials, query or fragment")
	ErrNameTaken   = errors.New("a connector with this name is already registered")
)

// ParseURL checks a sidecar base URL as VITAMUX_SIDECARS does; that it resolves to a private
// address is checked at dial time.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrInvalidURL
	}
	return u, nil
}

// Add registers a sidecar entered in the panel: Vitamux generates its shared secret, stores it
// sealed (AAD sidecar:<name>), audits sidecar.add and returns the secret, which is never shown
// again. The sidecar serves once it is configured with the secret and answers describe.
func (m *Manager) Add(ctx context.Context, name, rawURL string, userID uuid.UUID, actor string) (Sidecar, string, error) {
	u, err := ParseURL(rawURL)
	switch {
	case !sidecarName.MatchString(name):
		return Sidecar{}, "", ErrInvalidName
	case err != nil:
		return Sidecar{}, "", err
	case m.keys == nil:
		return Sidecar{}, "", errors.New("sidecars: the master key is not loaded")
	}
	if _, taken := m.reg.Get(name); taken {
		return Sidecar{}, "", ErrNameTaken
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	secret := hex.EncodeToString(b)
	sealed, err := m.keys.Seal(crypto.Credentials, []byte(secret), crypto.SidecarAAD(name))
	if err != nil {
		return Sidecar{}, "", err
	}
	now := time.Now()
	s := &Sidecar{Name: name, URL: u, Source: SourcePanel, CreatedAt: &now, Conn: m.connector(name, secret, "", u)}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.InsertSidecar(ctx, dbq.InsertSidecarParams{Name: name, Url: u.String(), Ciphertext: sealed, KeyID: m.keys.KeyID(), CreatedBy: &userID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNameTaken
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "sidecar.add",
			TargetType: "provider", TargetID: name, Detail: map[string]any{"url": u.String()}})
	})
	if err != nil {
		return Sidecar{}, "", err
	}
	if err := m.reg.Add(s.Conn); err != nil { // only the static connectors register outside m.mu
		_, _ = m.db.Q().DeleteSidecar(ctx, name)
		return Sidecar{}, "", ErrNameTaken
	}
	m.m[name] = s
	return *s, secret, nil
}

// Remove unregisters a panel sidecar and deletes its row, audited as sidecar.remove. Unless
// force, it refuses (*connectors.InUseError) while connections of its provider exist; they
// stop syncing without it. connectors.ErrManagedByEnvironment for an environment sidecar,
// db.ErrNotFound for an unknown one.
func (m *Manager) Remove(ctx context.Context, name string, force bool, userID uuid.UUID, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.m[name]
	switch {
	case !ok:
		return db.ErrNotFound
	case s.Source == SourceEnvironment:
		return connectors.ErrManagedByEnvironment
	}
	err := m.db.Tx(ctx, func(q *dbq.Queries) error {
		n, err := q.CountProviderConnections(ctx, name)
		if err != nil {
			return err
		}
		if n > 0 && !force {
			return &connectors.InUseError{Connections: int(n)}
		}
		if _, err := q.DeleteSidecar(ctx, name); err != nil {
			return err
		}
		return audit.Record(ctx, q, audit.Event{UserID: &userID, Actor: actor, Action: "sidecar.remove",
			TargetType: "provider", TargetID: name, Detail: map[string]any{"connections": n}})
	})
	if err != nil {
		return fmt.Errorf("remove sidecar: %w", err)
	}
	m.reg.Remove(name)
	delete(m.m, name)
	return nil
}
