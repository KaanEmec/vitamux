package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/KaanEmec/vitamux/internal/auth"
)

// The route × principal matrix (api/authz.yaml, J13.1) is the reviewed statement of who may
// call what. TestAuthzMatrix keeps it, the registered routes and the spec's `security` in
// step; TestAuthzMatrixEnforced (integration) proves the server enforces it.

const authzPath = "../../api/authz.yaml"

// authzEntry is one line of api/authz.yaml.
type authzEntry struct {
	Allow   string `yaml:"allow"`
	CSRF    bool   `yaml:"csrf"`
	Planned bool   `yaml:"planned"`
}

// scoped reports whether e admits the owner session plus API keys with its scope.
func (e authzEntry) scoped() bool { return slices.Contains(auth.Scopes, auth.Scope(e.Allow)) }

func loadAuthz(t *testing.T) map[string]authzEntry {
	t.Helper()
	raw, err := os.ReadFile(authzPath)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Routes map[string]authzEntry `yaml:"routes"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", authzPath, err)
	}
	return f.Routes
}

// allowOf names a route's declared access in the matrix's terms.
func allowOf(a access) string {
	switch {
	case a.public:
		return "public"
	case a.session:
		return "session"
	case a.ingest:
		return "client"
	default:
		return string(a.scope)
	}
}

// specAllow reads an operation's `security` in the matrix's terms; "" if it fits no
// supported shape.
func specAllow(security any) (allow string, csrf bool) {
	reqs, ok := security.([]any)
	if !ok {
		return "", false
	}
	if len(reqs) == 0 {
		return "public", false
	}
	var scopes []string
	session, client := false, false
	for _, r := range reqs {
		m, _ := r.(map[string]any)
		if _, ok := m["session"]; ok {
			session = true
			_, csrf = m["csrf"]
		}
		if _, ok := m["clientToken"]; ok {
			client = true
		}
		if s, ok := m["apiKey"].([]any); ok {
			for _, v := range s {
				scopes = append(scopes, v.(string))
			}
		}
	}
	switch {
	case client && !session && len(scopes) == 0 && len(reqs) == 1:
		return "client", false
	case session && len(scopes) == 0 && len(reqs) == 1:
		return "session", csrf
	case session && len(scopes) == 1 && len(reqs) == 2:
		return scopes[0], csrf
	}
	return "", false
}

// specOps returns every operation in the spec as "METHOD /path" with its `security`.
func specOps(t *testing.T) map[string]any {
	t.Helper()
	doc, _, _ := loadSpec(t)
	paths, _ := doc["paths"].(map[string]any)
	ops := map[string]any{}
	for p, item := range paths {
		for m, op := range item.(map[string]any) {
			if m == "parameters" {
				continue
			}
			ops[strings.ToUpper(m)+" "+p] = op.(map[string]any)["security"]
		}
	}
	return ops
}

func normalizedKey(pattern string) string {
	method, path, _ := strings.Cut(pattern, " ")
	return method + " " + pathParam.ReplaceAllString(path, "{}")
}

// authzDrift lists every disagreement between the matrix, the spec operations and the
// registered routes: every operation and route needs an entry that agrees with both, and
// every entry is an operation that is served unless marked planned.
func authzDrift(matrix map[string]authzEntry, ops map[string]any, routes []route) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	byNorm := map[string]string{}
	for key, e := range matrix {
		method, _, _ := strings.Cut(key, " ")
		if e.Allow != "public" && e.Allow != "session" && e.Allow != "client" && !e.scoped() {
			add("%s: unknown allow %q", key, e.Allow)
		}
		if want := !safeMethod(method) && (e.Allow == "session" || e.scoped()); e.CSRF != want {
			add("%s: csrf must be %v (unsafe method with a session principal)", key, want)
		}
		security, ok := ops[key]
		if !ok {
			add("%s: in %s but not an operation in api/openapi.yaml", key, authzPath)
			continue
		}
		if allow, csrf := specAllow(security); allow != e.Allow || csrf != e.CSRF {
			add("%s: spec security means {allow: %s, csrf: %v}, matrix says {allow: %s, csrf: %v}", key, allow, csrf, e.Allow, e.CSRF)
		}
		byNorm[normalizedKey(key)] = key
	}
	for key, security := range ops {
		if _, ok := matrix[key]; !ok {
			allow, csrf := specAllow(security)
			add("%s: no entry in %s; add `  %s: {allow: %s%s}`", key, authzPath, key, allow, map[bool]string{true: ", csrf: true"}[csrf])
		}
	}
	served := map[string]bool{}
	for _, r := range routes {
		key, ok := byNorm[normalizedKey(r.pattern)]
		if !ok {
			add("%s: registered but has no entry in %s (add the operation to the spec and a line to the matrix)", r.pattern, authzPath)
			continue
		}
		served[key] = true
		e := matrix[key]
		if e.Planned {
			add("%s: registered, so remove `planned: true` from its entry in %s", r.pattern, authzPath)
		}
		if got := allowOf(r.access); got != e.Allow {
			add("%s: registered with access %q, matrix says %q", r.pattern, got, e.Allow)
		}
	}
	for key, e := range matrix {
		if !e.Planned && !served[key] {
			add("%s: no route serves it; register it or mark the entry `planned: true`", key)
		}
	}
	slices.Sort(out)
	return out
}

// TestAuthzMatrix is the CI guard for api/authz.yaml.
func TestAuthzMatrix(t *testing.T) {
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range authzDrift(loadAuthz(t), specOps(t), rt.routes) {
		t.Error(d)
	}
}

// TestAuthzMatrixCatchesDrift: a route without an entry, a route whose access differs from
// its entry, and an operation without an entry each fail the guard.
func TestAuthzMatrixCatchesDrift(t *testing.T) {
	matrix, ops := loadAuthz(t), specOps(t)
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	routes := append(slices.Clone(rt.routes), route{"GET /api/v1/not-in-the-matrix", scope(auth.ReadConfig)})
	for i, r := range routes {
		if r.pattern == "GET /api/v1/settings" {
			routes[i].access = scope(auth.ReadHealth)
		}
	}
	delete(matrix, "GET /api/v1/jobs")
	got := strings.Join(authzDrift(matrix, ops, routes), "\n")
	for _, want := range []string{
		"GET /api/v1/not-in-the-matrix: registered but has no entry",
		`GET /api/v1/settings: registered with access "read:health", matrix says "read:config"`,
		"GET /api/v1/jobs: no entry in",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("guard missed %q; got:\n%s", want, got)
		}
	}
}
