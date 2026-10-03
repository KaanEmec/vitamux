package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// write creates root/rel with content, making parent directories.
func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func rules(t *testing.T, root string) []string {
	t.Helper()
	vs, _, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range vs {
		out = append(out, v.Rule)
	}
	slices.Sort(out)
	return out
}

// Offending values are assembled at runtime so this file itself never looks like a leak to scanners.
var (
	realEmail = "jane.doe" + "@" + "gmail.com"
	realPhone = "+49 151 2345 6789"
	usPhone   = "(415) 555-" + "2671"
	ghToken   = "gh" + "p_" + strings.Repeat("a1B2", 9)
	keyedTok  = `"refresh_token": "` + strings.Repeat("Zx9", 8) + `"`
)

func TestCleanTreePasses(t *testing.T) {
	root := t.TempDir()
	write(t, root, "fixtures/a.json", `{"synthetic": true, "email": "owner@example.com", "access_token": "synthetic-access-token-0001", "sha256": "9f2c`+strings.Repeat("0", 60)+`", "t": "2026-09-14T09:02:11Z"}`)
	write(t, root, "internal/x/testdata/b.ndjson", "{\"synthetic\":true,\"seed\":1}\n{\"v\":1,\"lat\":\"+37.7749\"}\n")
	write(t, root, "internal/x/testdata/c.yaml", "synthetic: true\nuser: someone@example.org\n")
	write(t, root, "fixtures/d.csv", "# synthetic: true\nts,value\n2026-09-14,120\n")
	write(t, root, "fixtures/e.pdf", "%PDF-1.4\x00\x01binary")
	write(t, root, "fixtures/e.pdf.synthetic", "synthetic: true\n")
	write(t, root, "fixtures/.gitkeep", "")
	write(t, root, "outside/real@gmail.com.txt", "not scanned: not under fixtures or testdata")
	if got := rules(t, root); len(got) != 0 {
		t.Fatalf("clean tree reported %v", got)
	}
}

func TestSeededViolations(t *testing.T) {
	cases := []struct {
		name, file, body, rule string
	}{
		{"json marker missing", "fixtures/a.json", `{"x": 1}`, RuleMarker},
		{"json marker false", "fixtures/a.json", `{"synthetic": false}`, RuleMarker},
		{"json array", "fixtures/a.json", `[{"synthetic": true}]`, RuleMarker},
		{"ndjson header missing", "testdata/a.ndjson", "{\"x\":1}\n{\"synthetic\":true}\n", RuleMarker},
		{"yaml marker missing", "testdata/a.yaml", "a: 1\n", RuleMarker},
		{"text marker too late", "testdata/a.csv", "a\nb\nc\nd\ne\n# synthetic: true\n", RuleMarker},
		{"empty file", "testdata/a.txt", "", RuleMarker},
		{"binary without sidecar", "fixtures/a.pdf", "%PDF\x00", RuleMarker},
		{"email", "fixtures/a.json", `{"synthetic": true, "e": "` + realEmail + `"}`, RuleEmail},
		{"phone plus", "fixtures/a.json", `{"synthetic": true, "p": "` + realPhone + `"}`, RulePhone},
		{"phone us", "testdata/a.yaml", "synthetic: true\np: " + usPhone + "\n", RulePhone},
		{"token prefix", "fixtures/a.json", `{"synthetic": true, "k": "` + ghToken + `"}`, RuleToken},
		{"token keyed", "fixtures/a.json", `{"synthetic": true, ` + keyedTok + `}`, RuleToken},
		{"jwt", "testdata/a.txt", "# synthetic: true\neyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig\n", RuleToken},
		{"private key", "testdata/a.txt", "# synthetic: true\n-----BEGIN RSA PRIVATE KEY-----\n", RuleToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, tc.file, tc.body)
			if got := rules(t, root); !slices.Equal(got, []string{tc.rule}) {
				t.Fatalf("got %v, want [%s]", got, tc.rule)
			}
		})
	}
}

func TestAllowTagAndOutputRedaction(t *testing.T) {
	root := t.TempDir()
	write(t, root, "fixtures/a.csv", "# synthetic: true\n"+realEmail+" "+allowTag+"\n")
	if got := rules(t, root); len(got) != 0 {
		t.Fatalf("allow tag ignored: %v", got)
	}
	write(t, root, "fixtures/b.csv", "# synthetic: true\n"+realEmail+"\n")
	vs, _, _ := Scan(root)
	if len(vs) != 1 || strings.Contains(vs[0].String(), "gmail") {
		t.Fatalf("violation output must not echo the match: %v", vs)
	}
}
