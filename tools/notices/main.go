// Command notices generates THIRD_PARTY_NOTICES.md from the Go build graph of ./cmd/vitamux
// (via the pinned `go-licenses` tool) and web/package-lock.json plus web/node_modules (run
// `npm ci` first), and enforces the license allowlist in allowed-licenses.txt.
//
//	go run ./tools/notices          # regenerate THIRD_PARTY_NOTICES.md (make notices)
//	go run ./tools/notices -check   # fail on a disallowed license or a stale file (make notices-check)
//
// Output is deterministic and platform independent: Go dependencies are resolved for linux/amd64,
// and npm entries come from the lockfile (platform-specific optional packages are listed but
// carry no license text). Findings print package and license, never file contents.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

//go:embed allowed-licenses.txt
var allowedFile string

// The Go toolchain's own LICENSE (not always shipped inside GOROOT, e.g. Homebrew), for the standard library.
//
//go:embed go-license.txt
var goLicense string

const (
	outFile    = "THIRD_PARTY_NOTICES.md"
	reportTmpl = "{{range .}}{{.LicensePath}}|{{.LicenseName}}\n{{end}}"
)

// dep is one third-party component (a Go module or directory with its own license file, or an npm package).
type dep struct {
	name, version string
	licenses      []string // SPDX ids, sorted
	text          string   // license file contents; empty when none was found
}

var copyrightRe = regexp.MustCompile(`(?im)^[ \t>*#/-]*(copyright (\(c\)|©|\d|(?-i:[A-Z]))|\(c\) \d|©).*$`)

func main() {
	check := flag.Bool("check", false, "verify the committed file is current instead of writing it")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	goDeps, err := goDeps()
	if err != nil {
		return err
	}
	npmAll, npmTexts, err := npmDeps()
	if err != nil {
		return err
	}
	if bad := disallowed(goDeps, npmAll); len(bad) > 0 {
		for _, b := range bad {
			fmt.Fprintln(os.Stderr, "license not allowed:", b)
		}
		return fmt.Errorf("%d dependencies outside %s; review and extend the allowlist deliberately", len(bad), "tools/notices/allowed-licenses.txt")
	}
	out := render(goDeps, npmAll, npmTexts)
	if !check {
		return os.WriteFile(outFile, out, 0o644) //nolint:gosec // committed, world-readable document
	}
	have, err := os.ReadFile(outFile)
	if err != nil || !bytes.Equal(have, out) {
		return fmt.Errorf("%s is stale: run `make notices` and commit the result", outFile)
	}
	return nil
}

// goDeps lists license files of everything linked into the binary, grouped per license file, plus the Go standard library.
func goDeps() ([]dep, error) {
	ctx := context.Background()
	tool, err := exec.CommandContext(ctx, "go", "tool", "-n", "go-licenses").Output()
	if err != nil {
		return nil, fmt.Errorf("locate go-licenses: %w", err)
	}
	tmpl, err := os.CreateTemp("", "notices-*.tmpl")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpl.Name())
	if _, err := tmpl.WriteString(reportTmpl); err != nil {
		return nil, err
	}
	if err := tmpl.Close(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, strings.TrimSpace(string(tool)), "report", "./cmd/vitamux", "--template", tmpl.Name()) //nolint:gosec // path comes from `go tool -n`
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=-tags=webui")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil { // stderr is swallowed: go-licenses warns about assembly files on every run
		return nil, fmt.Errorf("go-licenses report: %w", err)
	}
	modCache, err := exec.CommandContext(ctx, "go", "env", "GOMODCACHE").Output()
	if err != nil {
		return nil, err
	}
	cache := strings.TrimSpace(string(modCache)) + string(filepath.Separator)
	root, _ := os.Getwd()

	byPath := map[string]*dep{}
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		path, lic, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || strings.HasPrefix(path, root+string(filepath.Separator)) { // skip this repository
			continue
		}
		d := byPath[path]
		if d == nil {
			rel := strings.TrimPrefix(path, cache)
			dir := filepath.ToSlash(filepath.Dir(rel))
			mod, rest, _ := strings.Cut(dir, "@")
			ver, sub, _ := strings.Cut(rest, "/")
			if sub != "" {
				mod += "/" + sub
			}
			text, err := os.ReadFile(path) //nolint:gosec // license path reported by go-licenses
			if err != nil {
				return nil, err
			}
			d = &dep{name: mod, version: ver, text: string(text)}
			byPath[path] = d
		}
		d.licenses = appendUnique(d.licenses, lic)
	}
	var deps []dep
	for _, d := range byPath {
		sort.Strings(d.licenses)
		deps = append(deps, *d)
	}
	deps = append(deps, dep{name: "Go standard library", licenses: []string{"BSD-3-Clause"}, text: goLicense})
	sortDeps(deps)
	return deps, nil
}

// npmDeps reads the lockfile. The second result holds the packages that are installed on every
// platform (and so have a license file in node_modules); optional platform binaries have none.
func npmDeps() (all, withText []dep, err error) {
	raw, err := os.ReadFile("web/package-lock.json")
	if err != nil {
		return nil, nil, err
	}
	var lock struct {
		Packages map[string]struct {
			Version  string          `json:"version"`
			License  json.RawMessage `json:"license"`
			Optional bool            `json:"optional"`
			OS       []string        `json:"os"`
			CPU      []string        `json:"cpu"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &lock); err != nil {
		return nil, nil, fmt.Errorf("web/package-lock.json: %w", err)
	}
	for key, p := range lock.Packages {
		if key == "" {
			continue
		}
		_, name, _ := strings.CutLast(key, "node_modules/")
		d := dep{name: name, version: p.Version, licenses: []string{"UNKNOWN"}}
		var s string
		if json.Unmarshal(p.License, &s) == nil && s != "" {
			d.licenses = []string{s}
		}
		all = append(all, d)
		if p.Optional || len(p.OS) > 0 || len(p.CPU) > 0 {
			continue
		}
		text, err := licenseFile(filepath.Join("web", filepath.FromSlash(key)))
		if err != nil {
			return nil, nil, err
		}
		d.text = text
		withText = append(withText, d)
	}
	sortDeps(all)
	sortDeps(withText)
	return all, withText, nil
}

func licenseFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("%s missing: run `npm ci` in web/ first", dir)
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() && (strings.HasPrefix(n, "license") || strings.HasPrefix(n, "licence")) {
			b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // lockfile-derived package directory
			return string(b), err
		}
	}
	return "", nil // some packages declare the license only in package.json
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

func sortDeps(d []dep) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].name != d[j].name {
			return d[i].name < d[j].name
		}
		return d[i].version < d[j].version
	})
}

// disallowed returns "name version: license" for every dependency whose license expression is not
// covered by the allowlist. "A OR B" needs one allowed side, "A AND B" needs both; parentheses are
// flattened, which is exact for the common (A OR B) and A AND B shapes.
func disallowed(groups ...[]dep) []string {
	allowed := map[string]bool{}
	for l := range strings.SplitSeq(allowedFile, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			allowed[l] = true
		}
	}
	var bad []string
	for _, g := range groups {
		for _, d := range g {
			for _, expr := range d.licenses {
				if !exprAllowed(expr, allowed) {
					bad = append(bad, fmt.Sprintf("%s %s: %s", d.name, d.version, expr))
				}
			}
		}
	}
	return bad
}

func exprAllowed(expr string, allowed map[string]bool) bool {
	flat := strings.NewReplacer("(", " ", ")", " ").Replace(expr)
	for and := range strings.SplitSeq(flat, " AND ") {
		ok := false
		for or := range strings.SplitSeq(and, " OR ") {
			ok = ok || allowed[strings.TrimSpace(or)]
		}
		if !ok {
			return false
		}
	}
	return true
}

func render(goDeps, npmAll, npmTexts []dep) []byte {
	var b bytes.Buffer
	b.WriteString("# Third-party notices\n\n")
	b.WriteString("Generated by `make notices` (`tools/notices`). Do not edit. Vitamux itself is MIT-licensed, see [LICENSE](LICENSE) and [NOTICE](NOTICE).\n\n")
	b.WriteString("Go entries cover everything linked into the `vitamux` binary (linux/amd64). npm entries cover every package in `web/package-lock.json`; the SPA is built with them and only part of that set ends up in the shipped bundle.\n\n")
	section(&b, "Go modules", goDeps, goDeps)
	section(&b, "npm packages", npmAll, npmTexts)
	return b.Bytes()
}

func section(b *bytes.Buffer, title string, all, withText []dep) {
	fmt.Fprintf(b, "## %s\n\n| Package | Version | License |\n| --- | --- | --- |\n", title)
	for _, d := range all {
		v := d.version
		if v == "" {
			v = "-"
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", d.name, v, strings.Join(d.licenses, ", "))
	}
	// Group identical license texts (ignoring copyright lines) so MIT/ISC/BSD appear once, with
	// each package's own copyright holders listed beneath.
	type group struct {
		text    string
		members []dep
	}
	groups := map[[32]byte]*group{}
	var order [][32]byte
	for _, d := range withText {
		if strings.TrimSpace(d.text) == "" {
			continue
		}
		norm := strings.ToLower(strings.Join(strings.Fields(copyrightRe.ReplaceAllString(d.text, "")), " "))
		k := sha256.Sum256([]byte(norm))
		g := groups[k]
		if g == nil {
			g = &group{text: strings.TrimSpace(strings.ReplaceAll(d.text, "\r\n", "\n"))}
			groups[k] = g
			order = append(order, k)
		}
		g.members = append(g.members, d)
	}
	sort.Slice(order, func(i, j int) bool { return groups[order[i]].members[0].name < groups[order[j]].members[0].name })
	fmt.Fprintf(b, "\n### %s: license texts and copyright notices\n\n", title)
	for i, k := range order {
		g := groups[k]
		fmt.Fprintf(b, "#### Text %d (%s)\n\nUsed by:\n\n", i+1, strings.Join(g.members[0].licenses, ", "))
		for _, d := range g.members {
			line := "- " + d.name
			if d.version != "" {
				line += " " + d.version
			}
			var holders []string
			for _, m := range copyrightRe.FindAllString(d.text, -1) {
				holders = append(holders, strings.TrimSpace(m))
			}
			if len(holders) > 0 {
				line += ": " + strings.Join(holders, "; ")
			}
			b.WriteString(line + "\n")
		}
		fmt.Fprintf(b, "\n```text\n%s\n```\n\n", g.text)
	}
}
