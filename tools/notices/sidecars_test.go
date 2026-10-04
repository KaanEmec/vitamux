package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const upstreamOK = `# Upstream: demo
- Repository: https://example.com/demo
- Package: demo-lib
- Version: 1.2.3
- Release tag: v1.2.3
- License: %s
- Status: unofficial
%s`

func writeSidecar(t *testing.T, root, name, upstream string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if upstream != "" {
		if err := os.WriteFile(filepath.Join(dir, "UPSTREAM.md"), []byte(upstream), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckSidecars(t *testing.T) {
	allowed := allowedSet()
	tests := []struct {
		name, upstream, want string // want: substring of the single finding, "" for none
	}{
		{"permissive", upstream("MIT", ""), ""},
		{"permissive expression", upstream("(MIT OR GPL-3.0-only)", ""), ""},
		{"missing file", "", "UPSTREAM.md is missing"},
		{"copyleft unreviewed", upstream("GPL-3.0-only", ""), "outside the allowlist"},
		{"copyleft with a not-yet review", upstream("AGPL-3.0-only", "- License review: pending\n"), "outside the allowlist"},
		{"copyleft reviewed", upstream("GPL-3.0-only", "- License review: reviewed 2026-10-04 by the owner: separate container, no linking\n"), ""},
		{"unknown license", upstream("UNKNOWN", ""), "outside the allowlist"},
		{"placeholder", strings.Replace(upstream("MIT", ""), "demo-lib", "<package>", 1), `"Package" is empty`},
		{"bad status", strings.Replace(upstream("MIT", ""), "unofficial", "maybe", 1), "official or unofficial"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeSidecar(t, root, "demo", tc.upstream)
			got := checkSidecars(root, allowed)
			switch {
			case tc.want == "" && len(got) != 0:
				t.Fatalf("want no findings, got %v", got)
			case tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)):
				t.Fatalf("want one finding containing %q, got %v", tc.want, got)
			}
		})
	}
}

func TestCheckSidecarsSkipsTemplateAndMissingDir(t *testing.T) {
	root := t.TempDir()
	writeSidecar(t, root, "_template", "")
	if got := checkSidecars(root, allowedSet()); len(got) != 0 {
		t.Fatalf("template must be skipped, got %v", got)
	}
	if got := checkSidecars(filepath.Join(root, "absent"), allowedSet()); len(got) != 0 {
		t.Fatalf("absent directory must pass, got %v", got)
	}
}

func upstream(license, extra string) string { return fmt.Sprintf(upstreamOK, license, extra) }
