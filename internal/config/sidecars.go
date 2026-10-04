package config

import (
	"cmp"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// sidecarName is a provider code (connectors registry).
var sidecarName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Sidecar is one remote connector: its provider code, base URL and shared bearer secret.
type Sidecar struct {
	Name string
	URL  *url.URL
	// SecretFile is where the secret is read from: VITAMUX_SIDECAR_<NAME>_SECRET_FILE, else
	// <data dir>/secrets/sidecar-<name>.secret. Empty when the secret is set directly (development).
	SecretFile string
	Secret     Secret
}

// Sidecars parses VITAMUX_SIDECARS without reading the secrets, for `admin init-secrets`.
func Sidecars(env Lookup) ([]Sidecar, error) {
	get := func(name, def string) string {
		if v, ok := env(prefix + name); ok && v != "" {
			return v
		}
		return def
	}
	s, errs := parseSidecars(env, get)
	return s, errors.Join(errs...)
}

// parseSidecars reads VITAMUX_SIDECARS=name=url[,name=url]. The URL must be http(s) without
// credentials, query or fragment; that it points at a private address is checked at dial time.
func parseSidecars(env Lookup, get func(name, def string) string) ([]Sidecar, []error) {
	var out []Sidecar
	var errs []error
	seen := map[string]bool{}
	for part := range strings.SplitSeq(get("SIDECARS", ""), ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		name, raw, _ := strings.Cut(part, "=")
		u, err := url.Parse(raw)
		switch {
		case !sidecarName.MatchString(name):
			errs = append(errs, fmt.Errorf("%sSIDECARS: name %q must match %s", prefix, name, sidecarName))
			continue
		case seen[name]:
			errs = append(errs, fmt.Errorf("%sSIDECARS: %s is listed twice", prefix, name))
			continue
		case err != nil, u.Host == "", u.User != nil, u.RawQuery != "", u.Fragment != "", u.Scheme != "https" && u.Scheme != "http":
			errs = append(errs, fmt.Errorf("%sSIDECARS: %s needs an absolute http(s) URL without credentials, query or fragment", prefix, name))
			continue
		}
		seen[name] = true
		s := Sidecar{Name: name, URL: u}
		if _, plain := env(prefix + sidecarSecretVar(name)); !plain {
			s.SecretFile = get(sidecarSecretVar(name)+"_FILE", filepath.Join(get("DATA_DIR", "./data"), "secrets", "sidecar-"+name+".secret"))
		}
		out = append(out, s)
	}
	return out, errs
}

func sidecarSecretVar(name string) string { return "SIDECAR_" + strings.ToUpper(name) + "_SECRET" }

// loadSidecars parses the sidecars and reads their secrets; a missing secret is an error that
// names `vitamux admin init-secrets`.
func (c *Config) loadSidecars(env Lookup, readFile ReadFile, get func(name, def string) string) []error {
	sidecars, errs := parseSidecars(env, get)
	for _, s := range sidecars {
		name := sidecarSecretVar(s.Name)
		sec, err := secret(env, readFile, name, c.Env)
		if err == nil && !sec.IsSet() {
			var b []byte
			if b, err = readFile(s.SecretFile); err != nil {
				err = fmt.Errorf("%s%s_FILE: cannot read %q (create it with `vitamux admin init-secrets`): %w", prefix, name, s.SecretFile, cmp.Or(errors.Unwrap(err), err))
			}
			sec = Secret{value: strings.TrimSpace(string(b))}
		}
		switch {
		case err != nil:
			errs = append(errs, err)
		case !sec.IsSet():
			errs = append(errs, fmt.Errorf("%s%s: the secret is empty", prefix, name))
		default:
			s.Secret = sec
			c.Sidecars = append(c.Sidecars, s)
		}
	}
	return errs
}
