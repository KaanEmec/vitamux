package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var tomlName = regexp.MustCompile(`(?m)^name = "([^"]+)"`)

// compareUVLock compares uv.lock per [[package]] block. Blocks of allowed packages may change,
// and the project's own block (a virtual or editable source) may change lines that name them.
func compareUVLock(before, after string, allowed []string) error {
	b, a := uvBlocks(before), uvBlocks(after)
	for name := range union(b, a) {
		if b[name] == a[name] {
			continue
		}
		if isAllowed(name, allowed) {
			continue
		}
		own := b[name] + a[name]
		if (strings.Contains(own, "virtual = ") || strings.Contains(own, "editable = ")) && changedLinesMention(b[name], a[name], allowed) == nil {
			continue
		}
		return fmt.Errorf("package %q changed; only the upstream packages may", name)
	}
	return nil
}

// uvBlocks maps package name to block text; the text before the first block is keyed "".
func uvBlocks(s string) map[string]string {
	out := map[string]string{}
	for i, blk := range strings.Split(s, "\n[[package]]\n") {
		key := ""
		if i > 0 {
			if m := tomlName.FindStringSubmatch(blk); m != nil {
				key = strings.ToLower(m[1])
			} else {
				key = fmt.Sprintf("?%d", i)
			}
		}
		out[key] += blk
	}
	return out
}

func isAllowed(name string, allowed []string) bool {
	norm := func(s string) string { return strings.NewReplacer("_", "-", ".", "-").Replace(strings.ToLower(s)) }
	for _, n := range allowed {
		if norm(n) == norm(name) {
			return true
		}
	}
	return false
}

// compareJSON flattens package.json or package-lock.json into leaf paths and requires every
// changed leaf to belong to an allowed package: a dependency entry of the manifest (also as
// listed in the lockfile's root entry) or an installed package of the lockfile.
func compareJSON(lock bool, before, after string, allowed []string) error {
	var b, a any
	if err := json.Unmarshal([]byte(before), &b); err != nil {
		return fmt.Errorf("base version is not JSON: %w", err)
	}
	if err := json.Unmarshal([]byte(after), &a); err != nil {
		return fmt.Errorf("head version is not JSON: %w", err)
	}
	fb, fa := map[string]string{}, map[string]string{}
	flatten(nil, b, fb)
	flatten(nil, a, fa)
	for path := range union(fb, fa) {
		if fb[path] == fa[path] {
			continue
		}
		if !jsonPathAllowed(lock, strings.Split(path, "\x00"), allowed) {
			return fmt.Errorf("%s changed; only the upstream packages may", strings.ReplaceAll(path, "\x00", "."))
		}
	}
	return nil
}

func jsonPathAllowed(lock bool, p []string, allowed []string) bool {
	if lock {
		if len(p) >= 2 && p[0] == "packages" && p[1] != "" {
			_, name, _ := strings.CutLast(p[1], "node_modules/")
			return isAllowed(name, allowed)
		}
		if len(p) >= 4 && p[0] == "packages" && p[1] == "" { // the lockfile's copy of the manifest's dependency lists
			p = p[2:]
		} else {
			return false
		}
	}
	return len(p) >= 2 && (p[0] == "dependencies" || p[0] == "devDependencies" || p[0] == "optionalDependencies") && isAllowed(p[1], allowed)
}

func flatten(path []string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, c := range t {
			flatten(append(slices.Clip(path), k), c, out)
		}
	default:
		j, _ := json.Marshal(t)
		out[strings.Join(path, "\x00")] = string(j)
	}
}

func union[V any](a, b map[string]V) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}
