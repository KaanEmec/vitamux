package remote

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	schemaFile  = "../../../schemas/connector-sidecar.v1.json"
	examplesDir = "../../../schemas/examples/connector-sidecar"
)

func loadSchema(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// def returns $defs.<name>; "a.b" is property b of definition a.
func def(t *testing.T, name string) map[string]any {
	t.Helper()
	parts := strings.Split(name, ".")
	d, ok := loadSchema(t)["$defs"].(map[string]any)[parts[0]].(map[string]any)
	for _, p := range parts[1:] {
		d, ok = d["properties"].(map[string]any)[p].(map[string]any)
	}
	if !ok {
		t.Fatalf("schema has no %s", name)
	}
	return d
}

// schemaProps lists the properties of a definition, including those of its allOf references.
func schemaProps(t *testing.T, name string) []string {
	t.Helper()
	d := def(t, name)
	var out []string
	for k := range d["properties"].(map[string]any) {
		out = append(out, k)
	}
	all, _ := d["allOf"].([]any)
	for _, a := range all {
		ref := strings.TrimPrefix(a.(map[string]any)["$ref"].(string), "#/$defs/")
		out = append(out, schemaProps(t, ref)...)
	}
	slices.Sort(out)
	return out
}

func schemaEnum(t *testing.T, name, prop string) []string {
	t.Helper()
	var out []string
	for _, v := range def(t, name)["properties"].(map[string]any)[prop].(map[string]any)["enum"].([]any) {
		out = append(out, v.(string))
	}
	slices.Sort(out)
	return out
}

func compileDef(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.AssertContent()
	abs, err := filepath.Abs(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(abs + "#/$defs/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validate(t *testing.T, s *jsonschema.Schema, doc []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return s.Validate(inst)
}

// TestExamplesValidate: every example is valid against its schema definition (named by the
// file name up to the first dot; NDJSON lines by their type) and accepted by the Go decoders.
func TestExamplesValidate(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(examplesDir, "*.*json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(f)
		if strings.HasSuffix(name, ".ndjson") {
			checkPage(t, name, b)
			continue
		}
		d, _, _ := strings.Cut(name, ".")
		if err := validate(t, compileDef(t, d), b); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := decodeExample(d, b); err != nil {
			t.Errorf("%s: Go decoder: %v", name, err)
		}
	}
}

func decodeExample(d string, b []byte) error {
	strict := func(v any) error {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		return dec.Decode(v)
	}
	switch d {
	case "describe":
		_, err := DecodeDescribe(b)
		return err
	case "auth_response":
		_, err := DecodeAuth(b)
		return err
	case "problem":
		if e := DecodeProblem(b); classOf(e) == "" || strings.Contains(e.Error(), "violation") {
			return e
		}
		return nil
	case "auth_begin_request":
		return strict(&AuthBeginRequest{})
	case "auth_continue_request":
		return strict(&AuthContinueRequest{})
	case "refresh_request":
		return strict(&RefreshRequest{})
	case "refresh_response":
		return strict(&RefreshResponse{})
	case "fetch_request":
		return strict(&FetchRequest{})
	}
	return os.ErrNotExist
}

// checkPage validates a fetch response: raw lines, then exactly one result or error line.
func checkPage(t *testing.T, name string, b []byte) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(b))
	var final int
	for n := 1; sc.Scan(); n++ {
		var head struct{ Type string }
		if err := json.Unmarshal(sc.Bytes(), &head); err != nil {
			t.Fatalf("%s:%d: %v", name, n, err)
		}
		if final > 0 {
			t.Errorf("%s:%d: line after the final line", name, n)
		}
		if err := validate(t, compileDef(t, head.Type+"_line"), sc.Bytes()); err != nil {
			t.Errorf("%s:%d: %v", name, n, err)
		}
		l, err := DecodeLine(sc.Bytes())
		if err != nil {
			t.Errorf("%s:%d: DecodeLine: %v", name, n, err)
		}
		if l.Result != nil || l.Err != nil {
			final = n
		}
	}
	if final == 0 {
		t.Errorf("%s: no result or error line", name)
	}
}
