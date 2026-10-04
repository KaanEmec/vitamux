package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A sidecar image is a separate work from the MIT core, so its upstream's license is checked here
// instead of being listed in THIRD_PARTY_NOTICES.md (docs/sidecars.md#license-gate).
var (
	upstreamField  = regexp.MustCompile(`(?m)^- ([A-Za-z ]+):[ \t]*(.*?)[ \t]*$`)
	reviewedRe     = regexp.MustCompile(`^reviewed \d{4}-\d{2}-\d{2}\b`)
	requiredFields = []string{"Repository", "Package", "Version", "Release tag", "License", "Status"}
)

func runSidecars() error {
	bad := checkSidecars("sidecars", allowedSet())
	for _, b := range bad {
		fmt.Fprintln(os.Stderr, "sidecar:", b)
	}
	if len(bad) > 0 {
		return fmt.Errorf("%d sidecar licensing problems; see docs/sidecars.md#license-gate", len(bad))
	}
	return nil
}

// checkSidecars returns one finding per problem in dir/<name>/UPSTREAM.md. Directories starting
// with "_" (the template) are skipped. A license outside the allowlist, i.e. copyleft or unknown,
// passes only with a "License review: reviewed YYYY-MM-DD ..." line.
func checkSidecars(dir string, allowed map[string]bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no sidecars directory: nothing to check
	}
	var bad []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		for _, f := range checkUpstream(filepath.Join(dir, e.Name()), allowed) {
			bad = append(bad, fmt.Sprintf("%s: %s", e.Name(), f))
		}
	}
	return bad
}

func checkUpstream(sidecarDir string, allowed map[string]bool) []string {
	raw, err := os.ReadFile(filepath.Join(sidecarDir, "UPSTREAM.md")) //nolint:gosec // repository path
	if err != nil {
		return []string{"UPSTREAM.md is missing"}
	}
	fields := map[string]string{}
	for _, m := range upstreamField.FindAllStringSubmatch(string(raw), -1) {
		fields[m[1]] = m[2]
	}
	var bad []string
	for _, k := range requiredFields {
		if v := fields[k]; v == "" || strings.ContainsAny(v, "<>") {
			bad = append(bad, fmt.Sprintf("UPSTREAM.md: %q is empty or still a placeholder", k))
		}
	}
	if s := fields["Status"]; s != "" && !slices.Contains([]string{"official", "unofficial"}, s) {
		bad = append(bad, `UPSTREAM.md: "Status" must be official or unofficial`)
	}
	if lic := fields["License"]; lic != "" && !strings.ContainsAny(lic, "<>") &&
		!exprAllowed(lic, allowed) && !reviewedRe.MatchString(fields["License review"]) {
		bad = append(bad, fmt.Sprintf("UPSTREAM.md: license %q is outside the allowlist and has no \"License review: reviewed YYYY-MM-DD ...\" line", lic))
	}
	return bad
}
