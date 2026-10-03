// Package config loads process configuration from VITAMUX_* environment variables.
//
// Secrets are read from VITAMUX_<NAME>_FILE paths. A plain VITAMUX_<NAME> value is
// accepted for secrets only in development (see docs/architecture/security.md).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

const prefix = "VITAMUX_"

// Env is the deployment environment.
type Env string

const (
	Production  Env = "production"
	Development Env = "development"
)

// Config is the validated process configuration.
type Config struct {
	Env           Env
	HTTPAddr      string
	PublicURL     *url.URL
	LogLevel      slog.Level
	DataDir       string
	MasterKeyFile string
	// PreviousMasterKeyFiles lists retired master keys that can still open old sealed values
	// during rotation (comma-separated VITAMUX_PREVIOUS_MASTER_KEY_FILES).
	PreviousMasterKeyFiles []string
	// TrustedProxies lists the peers whose X-Forwarded-For/-Proto are believed. Empty trusts none.
	TrustedProxies []netip.Prefix
	DatabaseURL    Secret
	// MigrateDatabaseURL connects `vitamux migrate`; it falls back to DatabaseURL.
	MigrateDatabaseURL Secret
}

// Secret holds a sensitive value that never prints itself.
type Secret struct{ value string }

// Value returns the secret. Call sites must not log it.
func (s Secret) Value() string { return s.value }

// IsSet reports whether a value was provided.
func (s Secret) IsSet() bool { return s.value != "" }

func (s Secret) String() string {
	if s.value == "" {
		return "<unset>"
	}
	return "<redacted>"
}

// GoString keeps %#v from leaking the value.
func (s Secret) GoString() string { return s.String() }

// String renders the config with secrets redacted, suitable for startup logs.
func (c Config) String() string {
	return fmt.Sprintf("env=%s http_addr=%s public_url=%s log_level=%s data_dir=%s master_key_file=%s trusted_proxies=%v database_url=%s",
		c.Env, c.HTTPAddr, c.PublicURL, c.LogLevel, c.DataDir, c.MasterKeyFile, c.TrustedProxies, c.DatabaseURL)
}

// Lookup abstracts os.LookupEnv for tests.
type Lookup func(string) (string, bool)

// ReadFile abstracts os.ReadFile for tests.
type ReadFile func(string) ([]byte, error)

// Load reads configuration from the process environment.
func Load() (Config, error) { return load(os.LookupEnv, os.ReadFile) }

func load(env Lookup, readFile ReadFile) (Config, error) {
	var errs []error
	get := func(name, def string) string {
		if v, ok := env(prefix + name); ok && v != "" {
			return v
		}
		return def
	}

	c := Config{
		Env:           Env(get("ENV", string(Production))),
		HTTPAddr:      get("HTTP_ADDR", "127.0.0.1:8080"),
		DataDir:       get("DATA_DIR", "./data"),
		MasterKeyFile: get("MASTER_KEY_FILE", ""),
	}
	for _, p := range strings.Split(get("PREVIOUS_MASTER_KEY_FILES", ""), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.PreviousMasterKeyFiles = append(c.PreviousMasterKeyFiles, p)
		}
	}
	if c.Env != Production && c.Env != Development {
		errs = append(errs, fmt.Errorf("%sENV must be %q or %q, got %q", prefix, Production, Development, c.Env))
	}

	pub, err := url.Parse(get("PUBLIC_URL", "http://127.0.0.1:8080"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("%sPUBLIC_URL: %w", prefix, err))
	case pub.Scheme != "http" && pub.Scheme != "https", pub.Host == "":
		errs = append(errs, fmt.Errorf("%sPUBLIC_URL must be an absolute http(s) URL", prefix))
	case c.Env == Production && pub.Scheme != "https" && !isLoopback(pub.Hostname()):
		errs = append(errs, fmt.Errorf("%sPUBLIC_URL must use https in production unless it is a loopback address", prefix))
	}
	c.PublicURL = pub

	for _, part := range strings.Split(get("TRUSTED_PROXIES", ""), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		p, err := parseCIDR(part)
		if err != nil {
			errs = append(errs, fmt.Errorf("%sTRUSTED_PROXIES: %q is not a CIDR or IP address", prefix, part))
			continue
		}
		c.TrustedProxies = append(c.TrustedProxies, p)
	}

	if err := c.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("%sLOG_LEVEL: %w", prefix, err))
	}

	db, err := secret(env, readFile, "DATABASE_URL", c.Env)
	if err != nil {
		errs = append(errs, err)
	}
	c.DatabaseURL = db
	mig, err := secret(env, readFile, "MIGRATE_DATABASE_URL", c.Env)
	if err != nil {
		errs = append(errs, err)
	}
	if !mig.IsSet() {
		mig = db
	}
	c.MigrateDatabaseURL = mig

	return c, errors.Join(errs...)
}

// secret resolves VITAMUX_<name>_FILE (preferred) or VITAMUX_<name> (development only).
func secret(env Lookup, readFile ReadFile, name string, mode Env) (Secret, error) {
	path, hasFile := env(prefix + name + "_FILE")
	plain, hasPlain := env(prefix + name)
	hasFile, hasPlain = hasFile && path != "", hasPlain && plain != ""
	switch {
	case hasFile && hasPlain:
		return Secret{}, fmt.Errorf("set only one of %s%s_FILE and %s%s", prefix, name, prefix, name)
	case hasFile:
		b, err := readFile(path)
		if err != nil {
			// The path is not secret; the content is never included in errors.
			return Secret{}, fmt.Errorf("%s%s_FILE: cannot read %q: %w", prefix, name, path, errors.Unwrap(err))
		}
		return Secret{value: strings.TrimRight(string(b), "\r\n")}, nil
	case hasPlain && mode != Development:
		return Secret{}, fmt.Errorf("%s%s may only be set directly in development; use %s%s_FILE", prefix, name, prefix, name)
	case hasPlain:
		return Secret{value: plain}, nil
	}
	return Secret{}, nil
}

// parseCIDR accepts a CIDR or a single address (treated as /32 or /128).
func parseCIDR(s string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
