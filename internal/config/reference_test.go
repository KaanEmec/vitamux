package config

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const referenceDoc = "../../docs/configuration.md"

// literalDefault matches get("NAME", "default") and get("NAME", string(Production)) in
// config.go; names built at run time (p.name+"_MODEL") have no literal and default to empty.
var literalDefault = regexp.MustCompile(`get\("([A-Z_]+)", (?:"([^"]*)"|string\((Production|Development)\))\)`)

// TestConfigurationReference keeps docs/configuration.md in step with load: every variable load
// reads has exactly one row, with its default and whether it is a secret (read from _FILE).
// Descriptions and the Since column are written by hand.
func TestConfigurationReference(t *testing.T) {
	looked := map[string]bool{}
	_, _ = load(func(k string) (string, bool) {
		looked[strings.Replace(k, "_SIDECAR_NAME_", "_SIDECAR_<NAME>_", 1)] = true // per-sidecar variables
		if k == prefix+"SIDECARS" {
			return "name=http://sidecar-name:8080", true
		}
		return "", false
	}, func(string) ([]byte, error) { return nil, os.ErrNotExist })
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatal(err)
	}
	defaults := map[string]string{}
	for _, m := range literalDefault.FindAllStringSubmatch(string(src), -1) {
		defaults[prefix+m[1]] = m[2] + map[string]string{"Production": string(Production), "Development": string(Development)}[m[3]]
	}
	type row struct {
		def    string
		secret bool
	}
	want := map[string]row{}
	for k := range looked {
		if base, ok := strings.CutSuffix(k, "_FILE"); ok && looked[base] {
			continue // the secret's _FILE form, documented on the base row
		}
		want[k] = row{def: defaults[k], secret: looked[k+"_FILE"]}
	}

	doc, err := os.ReadFile(referenceDoc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]row{}
	for line := range strings.SplitSeq(string(doc), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 7 || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`"+prefix) {
			continue // not a variable row: | Variable | Default | Secret | Since | Description |
		}
		name := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if _, dup := got[name]; dup {
			t.Errorf("%s: documented twice", name)
		}
		if strings.TrimSpace(cells[4]) == "" || strings.TrimSpace(cells[5]) == "" {
			t.Errorf("%s: Since and Description are required", name)
		}
		got[name] = row{def: strings.Trim(strings.TrimSpace(cells[2]), "`"), secret: strings.TrimSpace(cells[3]) != ""}
	}

	var errs []error
	for _, k := range sortedKeys(want) {
		g, ok := got[k]
		switch {
		case !ok:
			errs = append(errs, errors.New(k+": missing from docs/configuration.md"))
		case g.def != want[k].def:
			errs = append(errs, errors.New(k+": default is "+quote(want[k].def)+", the doc says "+quote(g.def)))
		case g.secret != want[k].secret:
			errs = append(errs, errors.New(k+": the Secret column must be filled exactly for secrets (read from _FILE)"))
		}
	}
	for _, k := range sortedKeys(got) {
		if _, ok := want[k]; !ok {
			errs = append(errs, errors.New(k+": documented but not read by internal/config"))
		}
	}
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func quote(s string) string {
	if s == "" {
		return "empty"
	}
	return "`" + s + "`"
}
