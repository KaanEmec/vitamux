package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const upstreamMD = `# Upstream: demo
- Repository: https://example.com/demo
- Package: demo-lib
- Lockstep packages: demo-types
- License: MIT
`

const (
	pyBase  = "[project]\nname = \"vitamux-sidecar-demo\"\ndependencies = [\n  \"demo-lib>=1.0\",\n  \"requests>=2.0\",\n]\n"
	lockHdr = "version = 1\nrequires-python = \">=3.12\"\n"
	lockOwn = "\n[[package]]\nname = \"vitamux-sidecar-demo\"\nsource = { virtual = \".\" }\nrequires-dist = [\n    { name = \"demo-lib\", specifier = \">=%s\" },\n    { name = \"requests\", specifier = \">=2.0\" },\n]\n"
	lockPkg = "\n[[package]]\nname = \"%s\"\nversion = \"%s\"\nsdist = { hash = \"sha256:%s\" }\n"
	pkgBase = `{"name":"demo","dependencies":{"demo-lib":"^1.0.0","left-pad":"^1.0.0"}}`
	npmLock = `{"name":"demo","lockfileVersion":3,"packages":{"":{"name":"demo","dependencies":{"demo-lib":"^%s","left-pad":"^1.0.0"}},"node_modules/demo-lib":{"version":"%s"},"node_modules/left-pad":{"version":"%s"},"node_modules/demo-lib/node_modules/demo-types":{"version":"1.0.0"}}}`
)

func uvLock(spec, lib, libHash, req string) string {
	return lockHdr + strings.Replace(lockOwn, "%s", spec, 1) + pkg("demo-lib", lib, libHash) + pkg("requests", req, "aa")
}

func pkg(name, ver, hash string) string {
	s := strings.Replace(lockPkg, "%s", name, 1)
	s = strings.Replace(s, "%s", ver, 1)
	return strings.Replace(s, "%s", hash, 1)
}

func run(t *testing.T, files map[string][2]string, changed ...string) (string, error) {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("trusted/demo/UPSTREAM.md", upstreamMD)
	for p, v := range files {
		if v[0] != "" {
			write("base/"+p, v[0])
		}
		if v[1] != "" {
			write("head/"+p, v[1])
		}
	}
	return check(root+"/trusted", root+"/base", root+"/head", changed)
}

func TestCheck(t *testing.T) {
	const (
		py   = "sidecars/demo/pyproject.toml"
		lock = "sidecars/demo/uv.lock"
		pj   = "sidecars/demo/package.json"
		pl   = "sidecars/demo/package-lock.json"
	)
	pyBump := strings.Replace(pyBase, "demo-lib>=1.0", "demo-lib>=1.1", 1)
	tests := []struct {
		name    string
		files   map[string][2]string
		changed []string
		wantErr string // substring; "" for success
	}{
		{"python bump", map[string][2]string{
			py:   {pyBase, pyBump},
			lock: {uvLock("1.0", "1.0.0", "aa", "2.0"), uvLock("1.1", "1.1.0", "bb", "2.0")},
		}, []string{py, lock}, ""},
		{"python bump also bumps another package", map[string][2]string{
			lock: {uvLock("1.0", "1.0.0", "aa", "2.0"), uvLock("1.1", "1.1.0", "bb", "2.1")},
		}, []string{lock}, `"requests"`},
		{"python manifest changes another dependency", map[string][2]string{
			py: {pyBase, strings.Replace(pyBase, "requests>=2.0", "requests>=3.0", 1)},
		}, []string{py}, "not about the upstream"},
		{"lockstep package in lockfile", map[string][2]string{
			lock: {uvLock("1.0", "1.0.0", "aa", "2.0") + pkg("demo-types", "1.0", "x"), uvLock("1.0", "1.0.0", "aa", "2.0") + pkg("demo-types", "1.1", "y")},
		}, []string{lock}, ""},
		{"lockfile adds a package", map[string][2]string{
			lock: {uvLock("1.0", "1.0.0", "aa", "2.0"), uvLock("1.0", "1.0.0", "aa", "2.0") + pkg("evil", "1.0", "z")},
		}, []string{lock}, `"evil"`},
		{"npm bump", map[string][2]string{
			pj: {pkgBase, strings.Replace(pkgBase, "demo-lib\":\"^1.0.0", "demo-lib\":\"^1.1.0", 1)},
			pl: {sprintfNpm("1.0.0", "1.0.0", "1.0.0"), sprintfNpm("1.1.0", "1.1.0", "1.0.0")},
		}, []string{pj, pl}, ""},
		{"npm bump moves a transitive package", map[string][2]string{
			pl: {sprintfNpm("1.0.0", "1.0.0", "1.0.0"), sprintfNpm("1.0.0", "1.0.0", "1.0.1")},
		}, []string{pl}, "left-pad"},
		{"npm manifest adds a script", map[string][2]string{
			pj: {pkgBase, strings.Replace(pkgBase, `"dependencies"`, `"scripts":{"postinstall":"x"},"dependencies"`, 1)},
		}, []string{pj}, "scripts"},
		{"dockerfile changed", nil, []string{"sidecars/demo/Dockerfile"}, "not a sidecar manifest"},
		{"core file changed", nil, []string{"go.mod"}, "not a sidecar manifest"},
		{"two sidecars", nil, []string{"sidecars/demo/uv.lock", "sidecars/other/uv.lock"}, "span sidecars"},
		{"template", nil, []string{"sidecars/_template/uv.lock"}, "not a sidecar manifest"},
		{"nothing changed", nil, nil, "no changed files"},
		{"file added", map[string][2]string{lock: {"", uvLock("1.0", "1.0.0", "aa", "2.0")}}, []string{lock}, "no base version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := run(t, tc.files, tc.changed...)
			switch {
			case tc.wantErr == "" && (err != nil || got != "demo"):
				t.Fatalf("want demo, got %q, %v", got, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func sprintfNpm(spec, lib, other string) string {
	s := strings.Replace(npmLock, "%s", spec, 1)
	s = strings.Replace(s, "%s", lib, 1)
	return strings.Replace(s, "%s", other, 1)
}

func TestUnknownUpstream(t *testing.T) {
	root := t.TempDir()
	if _, err := check(root, root, root, []string{"sidecars/demo/uv.lock"}); err == nil || !strings.Contains(err.Error(), "UPSTREAM.md") {
		t.Fatalf("want UPSTREAM.md error, got %v", err)
	}
}
