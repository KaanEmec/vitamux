package ingest

import (
	"slices"
	"strings"
	"testing"
)

func fp(t *testing.T, doc string) string {
	t.Helper()
	f, err := ShapeFingerprint([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestShapeFingerprint(t *testing.T) {
	base := fp(t, `{"a": 1, "b": {"c": "x"}, "list": [{"v": 1}, {"v": 2}]}`)
	if !strings.HasPrefix(base, "v1:") || len(base) != 3+64 {
		t.Fatalf("unexpected format %q", base)
	}
	same := map[string]string{
		"other values": `{"a": 9.5, "b": {"c": "y"}, "list": [{"v": 3}]}`,
		"key order":    `{"list": [{"v": 1}], "b": {"c": "x"}, "a": 1}`,
		"whitespace":   "{\"a\":1,\n\"b\":{\"c\":\"x\"},\"list\":[{\"v\":1}]}",
	}
	for name, doc := range same {
		if fp(t, doc) != base {
			t.Errorf("%s changed the fingerprint", name)
		}
	}
	different := map[string]string{
		"extra field":     `{"a": 1, "b": {"c": "x", "d": 1}, "list": [{"v": 1}]}`,
		"missing field":   `{"a": 1, "list": [{"v": 1}]}`,
		"retyped field":   `{"a": "1", "b": {"c": "x"}, "list": [{"v": 1}]}`,
		"object to array": `{"a": 1, "b": [], "list": [{"v": 1}]}`,
		"empty list":      `{"a": 1, "b": {"c": "x"}, "list": []}`,
		"dotted key":      `{"a": 1, "b.c": "x", "b": {}, "list": [{"v": 1}]}`,
	}
	for name, doc := range different {
		if fp(t, doc) == base {
			t.Errorf("%s kept the fingerprint", name)
		}
	}
}

func TestShapePaths(t *testing.T) {
	got, err := ShapePaths([]byte(`{"samples": [{"value": 61, "ok": true, "note": null}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`$."samples":array`,
		`$."samples"[]."note":null`,
		`$."samples"[]."ok":boolean`,
		`$."samples"[]."value":number`,
		`$."samples"[]:object`,
		`$:object`,
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, bad := range []string{`{"a":`, `{} {}`, strings.Repeat("[", 300) + strings.Repeat("]", 300)} {
		if _, err := ShapePaths([]byte(bad)); err == nil {
			t.Errorf("accepted %.20q", bad)
		}
	}
}
