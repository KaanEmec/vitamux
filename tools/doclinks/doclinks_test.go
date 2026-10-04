// Package doclinks checks the repository's Markdown offline: every relative link in docs/ and
// the root *.md files must name an existing file, and every #anchor a heading of its target
// (GitHub's slug rules). External URLs are not fetched. Run: go test ./tools/doclinks
package doclinks

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
)

const root = "../.."

var (
	inlineLink = regexp.MustCompile(`!?\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	refLink    = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s*<?(\S+?)>?(?:\s|$)`)
	codeSpan   = regexp.MustCompile("`+[^`]*`+")
	heading    = regexp.MustCompile(`^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$`)
	htmlAnchor = regexp.MustCompile(`<a\s+(?:name|id)="([^"]+)"`)
	scheme     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// skipped are generated files with no relative links worth the time.
var skipped = []string{"THIRD_PARTY_NOTICES.md"}

func TestRelativeLinks(t *testing.T) {
	var files []string
	tops, _ := filepath.Glob(filepath.Join(root, "*.md"))
	for _, f := range tops {
		if !slices.Contains(skipped, filepath.Base(f)) {
			files = append(files, f)
		}
	}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]map[string]bool{}
	checked := 0
	for _, f := range files {
		for _, l := range links(t, f) {
			checked++
			if msg := check(f, l.target, anchors); msg != "" {
				rel, _ := filepath.Rel(root, f)
				t.Errorf("%s:%d: %s: %s", rel, l.line, l.target, msg)
			}
		}
	}
	if checked < 100 {
		t.Fatalf("only %d links checked; the scanner is broken", checked)
	}
}

type link struct {
	target string
	line   int
}

// links returns the link targets of a Markdown file outside code blocks and code spans.
func links(t *testing.T, path string) []link {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []link
	fence := ""
	for i, line := range strings.Split(string(b), "\n") {
		if f := fenceOf(line); f != "" && (fence == "" || strings.HasPrefix(f, fence)) {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		line = codeSpan.ReplaceAllString(line, "")
		for _, m := range inlineLink.FindAllStringSubmatch(line, -1) {
			out = append(out, link{m[1], i + 1})
		}
		if m := refLink.FindStringSubmatch(line); m != nil {
			out = append(out, link{m[1], i + 1})
		}
	}
	return out
}

func fenceOf(line string) string {
	s := strings.TrimLeft(line, " ")
	for _, c := range []string{"```", "~~~"} {
		if strings.HasPrefix(s, c) {
			return s[:len(s)-len(strings.TrimLeft(s, c[:1]))]
		}
	}
	return ""
}

// check returns why target, linked from file, does not resolve, or "".
func check(file, target string, cache map[string]map[string]bool) string {
	if scheme.MatchString(target) || strings.HasPrefix(target, "//") {
		return "" // external: not fetched
	}
	path, frag, _ := strings.Cut(target, "#")
	dest := file
	if path != "" {
		if strings.HasPrefix(path, "/") {
			dest = filepath.Join(root, path)
		} else {
			dest = filepath.Join(filepath.Dir(file), path)
		}
		info, err := os.Stat(dest)
		if err != nil {
			return "no such file"
		}
		if info.IsDir() {
			if frag != "" {
				return "anchor on a directory"
			}
			return ""
		}
	}
	if frag == "" || !strings.HasSuffix(dest, ".md") {
		return ""
	}
	a, ok := cache[dest]
	if !ok {
		a = anchorsOf(dest)
		cache[dest] = a
	}
	if !a[frag] {
		return fmt.Sprintf("no heading with anchor #%s", frag)
	}
	return ""
}

// anchorsOf returns the anchors GitHub generates for a file's headings, plus explicit HTML ones.
func anchorsOf(path string) map[string]bool {
	b, err := os.ReadFile(path)
	out := map[string]bool{}
	if err != nil {
		return out
	}
	count := map[string]int{}
	fence := ""
	for line := range strings.SplitSeq(string(b), "\n") {
		if f := fenceOf(line); f != "" && (fence == "" || strings.HasPrefix(f, fence)) {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		for _, m := range htmlAnchor.FindAllStringSubmatch(line, -1) {
			out[m[1]] = true
		}
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		s := slug(m[1])
		if n := count[s]; n > 0 {
			out[fmt.Sprintf("%s-%d", s, n)] = true
		} else {
			out[s] = true
		}
		count[s]++
	}
	return out
}

var linkText = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)

// slug is GitHub's heading anchor: link text only, lower case, letters, digits, '-' and '_'
// kept, spaces turned into '-', everything else dropped.
func slug(h string) string {
	h = linkText.ReplaceAllString(h, "$1")
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}
