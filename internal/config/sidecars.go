package config

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
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

// loadSidecars parses the sidecars and reads their secrets. A secret file that does not exist
// yet, or is empty, is not an error: the sidecar is reported as needing setup and its connector
// reads the file once `vitamux admin init-secrets` has created it (ADR-0021).
func (c *Config) loadSidecars(env Lookup, readFile ReadFile, get func(name, def string) string) []error {
	sidecars, errs := parseSidecars(env, get)
	for _, s := range sidecars {
		name := sidecarSecretVar(s.Name)
		var sec Secret
		var err error
		if s.SecretFile == "" { // set directly (development)
			sec, err = secret(env, readFile, name, c.Env)
		} else {
			var b []byte
			switch b, err = readFile(s.SecretFile); {
			case errors.Is(err, fs.ErrNotExist):
				err = nil
			case err != nil:
				err = fmt.Errorf("%s%s_FILE: cannot read %q: %w", prefix, name, s.SecretFile, cmp.Or(errors.Unwrap(err), err))
			}
			sec = Secret{value: strings.TrimSpace(string(b))}
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.Secret = sec
		c.Sidecars = append(c.Sidecars, s)
	}
	return errs
}
