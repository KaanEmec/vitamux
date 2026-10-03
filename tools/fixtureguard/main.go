// Command fixtureguard keeps real data out of the repository: every file under a `fixtures/` or
// `testdata/` directory must carry the synthetic marker and must not contain emails, phone
// numbers or token-like strings. Marker convention: docs/architecture/project.md#synthetic-fixtures-policy.
//
//	go run ./tools/fixtureguard [root]    # default root "."; exit 1 on any violation
//
// Findings print as `path:line: rule` and never echo the matched text, so CI logs stay clean.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Rule names, used in output and in the self-test.
const (
	RuleMarker = "marker" // missing synthetic: true
	RuleEmail  = "email"
	RulePhone  = "phone"
	RuleToken  = "token"
)

// Violation is one finding. Line is 0 for whole-file findings.
type Violation struct {
	Path string
	Line int
	Rule string
}

func (v Violation) String() string {
	if v.Line == 0 {
		return fmt.Sprintf("%s: %s", v.Path, v.Rule)
	}
	return fmt.Sprintf("%s:%d: %s", v.Path, v.Line, v.Rule)
}

const (
	markerLines = 5 // generic text files: the marker must appear within this many lines
	allowTag    = "fixtureguard:allow"
	sniffBytes  = 8000 // a NUL byte in this prefix means binary
)

var (
	markerRe = regexp.MustCompile(`\bsynthetic:\s*true\b`)

	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,})`)
	// Reserved domains (RFC 2606 / 6761) are safe in fixtures.
	safeDomainRe = regexp.MustCompile(`(?i)(^|\.)(example(\.(com|org|net))?|test|invalid|localhost)$`)

	phonePlusRe = regexp.MustCompile(`\+\d[\d\s().-]{7,}\d`)
	phoneUSRe   = regexp.MustCompile(`(?:^|[^\w])\(?\d{3}\)?[\s.-]\d{3}[\s.-]\d{4}\b`)

	tokenPatterns = []*regexp.Regexp{
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`),
		regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`), // JWT
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`),
	}
	// A credential-named key with a long value. Values containing "synthetic" are the convention for fake ones.
	tokenKeyRe = regexp.MustCompile(`(?i)\b(?:access_token|refresh_token|id_token|api_key|apikey|client_secret|secret|password|passwd|token)["']?\s*[:=]\s*["']?([A-Za-z0-9_\-./+=~]{16,})`)
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	vs, scanned, err := Scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixtureguard:", err)
		os.Exit(2)
	}
	for _, v := range vs {
		fmt.Println(v)
	}
	if len(vs) > 0 {
		fmt.Printf("fixtureguard: %d violation(s) in %d file(s) scanned; see docs/architecture/project.md#synthetic-fixtures-policy\n", len(vs), scanned)
		os.Exit(1)
	}
	fmt.Printf("fixtureguard: ok, %d file(s) scanned\n", scanned)
}

// Scan checks every file under any `fixtures` or `testdata` directory below root.
func Scan(root string) (vs []Violation, scanned int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error { //nolint:gosec // root is a developer-supplied CLI argument
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".svelte-kit":
				return filepath.SkipDir
			}
			if rel == "tmp" || rel == "data" || rel == "bin" { // gitignored top-level dirs
				return filepath.SkipDir
			}
			if d.Name() == "fixtures" || d.Name() == "testdata" {
				fv, n, err := scanDir(path)
				vs, scanned = append(vs, fv...), scanned+n
				if err != nil {
					return err
				}
				return filepath.SkipDir
			}
		}
		return nil
	})
	return vs, scanned, err
}

func scanDir(dir string) (vs []Violation, scanned int, err error) {
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || d.Name() == ".gitkeep" {
			return nil
		}
		fv, err := scanFile(path)
		vs, scanned = append(vs, fv...), scanned+1
		return err
	})
	return vs, scanned, err
}

func scanFile(path string) ([]Violation, error) {
	f, err := os.Open(path) //nolint:gosec // paths come from walking the repository
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 1<<16)

	// Schema-constrained files (e.g. rule specs) cannot carry a field: a `.synthetic` file in
	// their directory marks every file in it. Content rules still apply.
	marked := sidecar(filepath.Join(filepath.Dir(path), ".synthetic"))

	head, _ := r.Peek(sniffBytes)
	if bytes.IndexByte(head, 0) >= 0 {
		// Binary (PDF, FIT, zip, ...): content is not scanned; a `<file>.synthetic` sidecar carries the marker.
		if !marked && !sidecar(path+".synthetic") {
			return []Violation{{path, 0, RuleMarker}}, nil
		}
		return nil, nil
	}

	var vs []Violation
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		body, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		if !marked && !jsonMarker(body) {
			vs = append(vs, Violation{path, 0, RuleMarker})
		}
		return append(vs, scanLines(path, bytes.NewReader(body), true)...), nil
	}
	return append(vs, scanLines(path, r, marked)...), nil
}

// sidecar reports whether path exists and carries the marker.
func sidecar(path string) bool {
	b, err := os.ReadFile(path) //nolint:gosec // paths come from walking the repository
	return err == nil && markerRe.Match(b)
}

// scanLines applies the marker rule (unless marked already) and the pattern rules to every line.
func scanLines(path string, src io.Reader, marked bool) []Violation {
	var vs []Violation
	ext := strings.ToLower(filepath.Ext(path))
	lineMarker := !marked
	found := marked
	r := bufio.NewReaderSize(src, 1<<16)
	for n := 1; ; n++ {
		b, err := r.ReadBytes('\n')
		if len(b) > 0 {
			line := string(b)
			switch {
			case lineMarker && (ext == ".ndjson" || ext == ".jsonl") && n == 1:
				found = jsonMarker(b)
			case lineMarker && n <= markerLines && markerRe.MatchString(line):
				found = true
			}
			if !strings.Contains(line, allowTag) {
				for _, rule := range lineRules(line) {
					vs = append(vs, Violation{path, n, rule})
				}
			}
		}
		if err != nil {
			break
		}
	}
	if !found {
		vs = append(vs, Violation{path, 0, RuleMarker})
	}
	return vs
}

func jsonMarker(b []byte) bool {
	var h struct {
		Synthetic *bool `json:"synthetic"`
	}
	return json.Unmarshal(bytes.TrimSpace(b), &h) == nil && h.Synthetic != nil && *h.Synthetic
}

func lineRules(line string) (rules []string) {
	for _, m := range emailRe.FindAllStringSubmatch(line, -1) {
		if !safeDomainRe.MatchString(m[1]) {
			rules = append(rules, RuleEmail)
			break
		}
	}
	if hasPhone(line) {
		rules = append(rules, RulePhone)
	}
	if hasToken(line) {
		rules = append(rules, RuleToken)
	}
	return rules
}

func hasPhone(line string) bool {
	for _, m := range phonePlusRe.FindAllString(line, -1) {
		digits := 0
		for _, c := range m {
			if c >= '0' && c <= '9' {
				digits++
			}
		}
		if digits >= 8 && digits <= 15 {
			return true
		}
	}
	return phoneUSRe.MatchString(line)
}

func hasToken(line string) bool {
	for _, re := range tokenPatterns {
		if re.MatchString(line) {
			return true
		}
	}
	for _, m := range tokenKeyRe.FindAllStringSubmatch(line, -1) {
		if !strings.Contains(strings.ToLower(m[1]), "synthetic") {
			return true
		}
	}
	return false
}
