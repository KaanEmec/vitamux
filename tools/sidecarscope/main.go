// Command sidecarscope decides whether a Dependabot PR may be auto-merged by
// .github/workflows/sidecar-automerge.yml: it must change only the upstream package lines of one
// sidecar's manifest and lockfile (docs/sidecars.md#automatic-upstream-updates).
//
//	sidecarscope -sidecars sidecars -base DIR -head DIR < changed-paths.txt
//
// stdin lists the changed repository paths; DIR holds the base and head copy of each at the same
// relative path. UPSTREAM.md in -sidecars must come from the trusted base branch: it names the
// upstream package (Package) and the packages released with it (Lockstep packages, comma separated).
// On success it prints the sidecar name; otherwise it prints the reason to stderr and exits 1.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func main() {
	sidecars := flag.String("sidecars", "sidecars", "directory with the trusted sidecars/<name>/UPSTREAM.md files")
	base := flag.String("base", "", "directory with the base copy of the changed files")
	head := flag.String("head", "", "directory with the head copy of the changed files")
	flag.Parse()
	var files []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			files = append(files, l)
		}
	}
	name, err := check(*sidecars, *base, *head, files)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sidecarscope:", err)
		os.Exit(1)
	}
	fmt.Println(name)
}

var (
	pathRe  = regexp.MustCompile(`^sidecars/([a-z][a-z0-9_-]*)/(pyproject\.toml|uv\.lock|package\.json|package-lock\.json)$`)
	fieldRe = regexp.MustCompile(`(?m)^- ([A-Za-z ]+):[ \t]*(.*?)[ \t]*$`)
)

// check returns the sidecar name when every changed file is a manifest or lockfile of that one
// sidecar and every difference concerns an allowed (upstream) package.
func check(sidecarsDir, baseDir, headDir string, files []string) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("no changed files")
	}
	name := ""
	for _, f := range files {
		m := pathRe.FindStringSubmatch(f)
		if m == nil {
			return "", fmt.Errorf("%s is not a sidecar manifest or lockfile", f)
		}
		if name != "" && m[1] != name {
			return "", fmt.Errorf("changes span sidecars %s and %s", name, m[1])
		}
		name = m[1]
	}
	allowed, err := upstreamPackages(sidecarsDir + "/" + name + "/UPSTREAM.md")
	if err != nil {
		return "", err
	}
	for _, f := range files {
		before, err := os.ReadFile(baseDir + "/" + f) //nolint:gosec // CI-provided directories
		if err != nil {
			return "", fmt.Errorf("%s: no base version (added or removed files need review)", f)
		}
		after, err := os.ReadFile(headDir + "/" + f) //nolint:gosec // CI-provided directories
		if err != nil {
			return "", fmt.Errorf("%s: no head version (added or removed files need review)", f)
		}
		if err := compare(f, string(before), string(after), allowed); err != nil {
			return "", fmt.Errorf("%s: %w", f, err)
		}
	}
	return name, nil
}

// upstreamPackages reads the package names a bump may touch from a sidecar's UPSTREAM.md.
func upstreamPackages(path string) ([]string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // trusted base checkout
	if err != nil {
		return nil, fmt.Errorf("sidecar has no UPSTREAM.md on the base branch")
	}
	fields := map[string]string{}
	for _, m := range fieldRe.FindAllStringSubmatch(string(raw), -1) {
		fields[m[1]] = m[2]
	}
	var names []string
	for _, n := range append([]string{fields["Package"]}, strings.Split(fields["Lockstep packages"], ",")...) {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" && !strings.ContainsAny(n, "<>") {
			names = append(names, n)
		}
	}
	if fields["Package"] == "" || len(names) == 0 {
		return nil, fmt.Errorf("UPSTREAM.md names no package")
	}
	return names, nil
}

func compare(file, before, after string, allowed []string) error {
	if before == after {
		return nil
	}
	switch {
	case strings.HasSuffix(file, "/uv.lock"):
		return compareUVLock(before, after, allowed)
	case strings.HasSuffix(file, "/pyproject.toml"):
		return changedLinesMention(before, after, allowed)
	default:
		return compareJSON(strings.HasSuffix(file, "/package-lock.json"), before, after, allowed)
	}
}

// nameRe matches a quoted package name followed by a non-name character, treating - _ . alike
// (PyPI normalization), e.g. `"garminconnect>=0.2"` or `{ name = "Garmin_Connect", ...`.
func nameRe(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	q = strings.NewReplacer(`\-`, `[-_.]`, "_", `[-_.]`, `\.`, `[-_.]`).Replace(q)
	return regexp.MustCompile(`(?i)["']` + q + `(?:[^A-Za-z0-9_.-]|$)`)
}

func mentions(line string, allowed []string) bool {
	for _, n := range allowed {
		if nameRe(n).MatchString(line) {
			return true
		}
	}
	return false
}

// changedLinesMention requires every added or removed line to name an allowed package.
func changedLinesMention(before, after string, allowed []string) error {
	counts := map[string]int{}
	for l := range strings.SplitSeq(before, "\n") {
		counts[strings.TrimSpace(l)]++
	}
	for l := range strings.SplitSeq(after, "\n") {
		counts[strings.TrimSpace(l)]--
	}
	for l, c := range counts {
		if c != 0 && l != "" && !mentions(l, allowed) {
			return fmt.Errorf("a changed line is not about the upstream package: %q", truncate(l))
		}
	}
	return nil
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
