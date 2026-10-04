// Package config loads process configuration from VITAMUX_* environment variables.
//
// Secrets are read from VITAMUX_<NAME>_FILE paths. A plain VITAMUX_<NAME> value is
// accepted for secrets only in development (see docs/architecture/security.md).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
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
	Env      Env
	HTTPAddr string
	// MetricsAddr is the private listener of /metrics (VITAMUX_METRICS_ADDR); empty disables it.
	MetricsAddr   string
	PublicURL     *url.URL
	LogLevel      slog.Level
	DataDir       string
	MasterKeyFile string
	// BackupDir enables the daily backup job (VITAMUX_BACKUP_DIR); BackupKeep is how many
	// backups it keeps (VITAMUX_BACKUP_KEEP, default 3). docs/operations/backup.md.
	BackupDir  string
	BackupKeep int
	// PreviousMasterKeyFiles lists retired master keys that can still open old sealed values
	// during rotation (comma-separated VITAMUX_PREVIOUS_MASTER_KEY_FILES).
	PreviousMasterKeyFiles []string
	// TrustedProxies lists the peers whose X-Forwarded-For/-Proto are believed. Empty trusts none.
	TrustedProxies []netip.Prefix
	DatabaseURL    Secret
	// MigrateDatabaseURL connects `vitamux migrate`; it falls back to DatabaseURL.
	MigrateDatabaseURL Secret
	// WithingsClientID and WithingsClientSecret are the owner's Withings application
	// (docs/providers/withings.md#app-registration-and-callback); both or neither.
	WithingsClientID     string
	WithingsClientSecret Secret
	// Extraction providers (docs/architecture/lab-documents.md#privacy-controls). A provider is
	// configured when its key (base URL for openai_compatible) and model are set; the owner
	// still has to enable it and consent per request.
	GeminiAPIKey                 Secret
	GeminiModel                  string
	OpenAIAPIKey                 Secret
	OpenAIModel                  string
	OpenAICompatibleBaseURL      *url.URL
	OpenAICompatibleAPIKey       Secret // optional
	OpenAICompatibleModel        string
	OpenAICompatibleAllowPrivate bool // allow http and private or loopback hosts in the base URL
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
	return fmt.Sprintf("env=%s http_addr=%s metrics_addr=%s public_url=%s log_level=%s data_dir=%s master_key_file=%s trusted_proxies=%v database_url=%s",
		c.Env, c.HTTPAddr, c.MetricsAddr, c.PublicURL, c.LogLevel, c.DataDir, c.MasterKeyFile, c.TrustedProxies, c.DatabaseURL)
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
		MetricsAddr:   get("METRICS_ADDR", ""),
		DataDir:       get("DATA_DIR", "./data"),
		MasterKeyFile: get("MASTER_KEY_FILE", ""),
	}
	c.BackupDir = get("BACKUP_DIR", "")
	if n, err := strconv.Atoi(get("BACKUP_KEEP", "3")); err != nil || n < 1 {
		errs = append(errs, fmt.Errorf("%sBACKUP_KEEP must be a positive integer", prefix))
	} else {
		c.BackupKeep = n
	}
	for p := range strings.SplitSeq(get("PREVIOUS_MASTER_KEY_FILES", ""), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.PreviousMasterKeyFiles = append(c.PreviousMasterKeyFiles, p)
		}
	}
	if c.Env != Production && c.Env != Development {
		errs = append(errs, fmt.Errorf("%sENV must be %q or %q, got %q", prefix, Production, Development, c.Env))
	}

	if _, _, err := net.SplitHostPort(c.MetricsAddr); c.MetricsAddr != "" && err != nil {
		errs = append(errs, fmt.Errorf("%sMETRICS_ADDR must be host:port, got %q", prefix, c.MetricsAddr))
	} else if c.MetricsAddr == c.HTTPAddr && c.MetricsAddr != "" {
		errs = append(errs, fmt.Errorf("%sMETRICS_ADDR must differ from %sHTTP_ADDR: /metrics is never served publicly", prefix, prefix))
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

	for part := range strings.SplitSeq(get("TRUSTED_PROXIES", ""), ",") {
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

	c.WithingsClientID = get("WITHINGS_CLIENT_ID", "")
	if c.WithingsClientSecret, err = secret(env, readFile, "WITHINGS_CLIENT_SECRET", c.Env); err != nil {
		errs = append(errs, err)
	} else if (c.WithingsClientID == "") != !c.WithingsClientSecret.IsSet() {
		errs = append(errs, fmt.Errorf("set both %sWITHINGS_CLIENT_ID and %sWITHINGS_CLIENT_SECRET_FILE, or neither", prefix, prefix))
	}

	errs = append(errs, c.loadExtractors(env, readFile, get)...)
	return c, errors.Join(errs...)
}

// loadExtractors reads the extraction provider settings. The openai_compatible base URL must
// be https on a public host unless VITAMUX_OPENAI_COMPATIBLE_ALLOW_PRIVATE=true (SSRF guard).
func (c *Config) loadExtractors(env Lookup, readFile ReadFile, get func(name, def string) string) []error {
	var errs []error
	var err error
	for _, p := range []struct {
		name  string
		key   *Secret
		model *string
	}{{"GEMINI", &c.GeminiAPIKey, &c.GeminiModel}, {"OPENAI", &c.OpenAIAPIKey, &c.OpenAIModel}} {
		*p.model = get(p.name+"_MODEL", "")
		if *p.key, err = secret(env, readFile, p.name+"_API_KEY", c.Env); err != nil {
			errs = append(errs, err)
		} else if p.key.IsSet() != (*p.model != "") {
			errs = append(errs, fmt.Errorf("set both %s%s_API_KEY_FILE and %s%s_MODEL, or neither", prefix, p.name, prefix, p.name))
		}
	}
	c.OpenAICompatibleModel = get("OPENAI_COMPATIBLE_MODEL", "")
	c.OpenAICompatibleAllowPrivate = get("OPENAI_COMPATIBLE_ALLOW_PRIVATE", "") == "true"
	if c.OpenAICompatibleAPIKey, err = secret(env, readFile, "OPENAI_COMPATIBLE_API_KEY", c.Env); err != nil {
		errs = append(errs, err)
	}
	raw := get("OPENAI_COMPATIBLE_BASE_URL", "")
	if (raw == "") != (c.OpenAICompatibleModel == "") {
		errs = append(errs, fmt.Errorf("set both %sOPENAI_COMPATIBLE_BASE_URL and %sOPENAI_COMPATIBLE_MODEL, or neither", prefix, prefix))
	}
	if raw == "" {
		return errs
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil, u.Host == "", u.User != nil, u.RawQuery != "", u.Fragment != "", u.Scheme != "https" && u.Scheme != "http":
		errs = append(errs, fmt.Errorf("%sOPENAI_COMPATIBLE_BASE_URL must be an absolute http(s) URL without credentials, query or fragment", prefix))
	case !c.OpenAICompatibleAllowPrivate && (u.Scheme != "https" || isPrivateHost(u.Hostname())):
		errs = append(errs, fmt.Errorf("%sOPENAI_COMPATIBLE_BASE_URL must use https on a public host; set %sOPENAI_COMPATIBLE_ALLOW_PRIVATE=true for local inference", prefix, prefix))
	default:
		c.OpenAICompatibleBaseURL = u
	}
	return errs
}

// isPrivateHost reports a loopback, private, link-local or unspecified address, or a name
// that is local by convention. Names are not resolved: the admin sets the URL.
func isPrivateHost(host string) bool {
	if a, err := netip.ParseAddr(host); err == nil {
		a = a.Unmap()
		return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified()
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || !strings.Contains(host, ".")
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
