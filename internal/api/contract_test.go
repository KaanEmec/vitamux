package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// Contract tests validate real responses against api/openapi.yaml (ADR-0015). Owner endpoint
// tests call checkResponse on every response they assert on.

const specPath = "../../api/openapi.yaml"

var spec struct {
	once sync.Once
	url  string
	doc  map[string]any
	c    *jsonschema.Compiler
	err  error
}

// loadSpec parses the spec once and registers it with a compiler; relative $refs (the
// ingest JSON Schemas) resolve from the file's location.
func loadSpec(t *testing.T) (map[string]any, *jsonschema.Compiler, string) {
	t.Helper()
	spec.once.Do(func() {
		abs, err := filepath.Abs(specPath)
		if err != nil {
			spec.err = err
			return
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			spec.err = err
			return
		}
		var y any
		if spec.err = yaml.Unmarshal(raw, &y); spec.err != nil {
			return
		}
		j, err := json.Marshal(y)
		if err != nil {
			spec.err = err
			return
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
		if err != nil {
			spec.err = err
			return
		}
		spec.doc, _ = y.(map[string]any)
		spec.url = "file://" + filepath.ToSlash(abs)
		spec.c = jsonschema.NewCompiler()
		spec.c.AssertFormat()
		spec.err = spec.c.AddResource(spec.url, inst)
	})
	if spec.err != nil {
		t.Fatalf("load %s: %v", specPath, spec.err)
	}
	return spec.doc, spec.c, spec.url
}

// pointer escapes path segments into a JSON pointer.
func pointer(segs ...string) string {
	r := strings.NewReplacer("~", "~0", "/", "~1")
	var b strings.Builder
	for _, s := range segs {
		b.WriteString("/" + r.Replace(s))
	}
	return b.String()
}

// lookup walks the parsed spec; it does not follow $ref.
func lookup(doc map[string]any, segs ...string) (any, bool) {
	var cur any = doc
	for _, s := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[s]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// specSchema compiles the schema at a JSON pointer inside the spec.
func specSchema(t *testing.T, ptr string) *jsonschema.Schema {
	t.Helper()
	_, c, url := loadSpec(t)
	s, err := c.Compile(url + "#" + ptr)
	if err != nil {
		t.Fatalf("compile %s: %v", ptr, err)
	}
	return s
}

func validateJSON(t *testing.T, s *jsonschema.Schema, body []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return err
	}
	return s.Validate(inst)
}

// checkResponse fails t unless res is declared for the operation at pattern ("GET
// /api/v1/system/version", with the spec's path template) and its JSON body matches the
// declared schema. Error responses must be problem+json. It returns the body.
func checkResponse(t *testing.T, pattern string, res *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	checkBody(t, pattern, res.StatusCode, res.Header.Get("Content-Type"), body)
	return body
}

// checkBody is checkResponse for a body that was already read.
func checkBody(t *testing.T, pattern string, statusCode int, contentType string, body []byte) {
	t.Helper()
	doc, _, _ := loadSpec(t)
	method, path, _ := strings.Cut(pattern, " ")
	op := []string{"paths", path, strings.ToLower(method)}
	if _, ok := lookup(doc, op...); !ok {
		t.Fatalf("%s is not in the spec", pattern)
	}
	status := strconv.Itoa(statusCode)
	declared, ok := lookup(doc, append(op, "responses", status)...)
	if !ok {
		t.Fatalf("%s: status %s is not declared", pattern, status)
	}
	ct, _, _ := mime.ParseMediaType(contentType)
	if statusCode >= 400 {
		if ct != "application/problem+json" {
			t.Fatalf("%s: %s answered with %q, want problem+json", pattern, status, ct)
		}
		if err := validateJSON(t, specSchema(t, "/components/schemas/Problem"), body); err != nil {
			t.Fatalf("%s: problem does not match the spec: %v", pattern, err)
		}
		return
	}
	content, _ := declared.(map[string]any)["content"].(map[string]any)
	if len(content) == 0 {
		if len(body) != 0 {
			t.Fatalf("%s: %s declares no body but got %d bytes", pattern, status, len(body))
		}
		return
	}
	if _, ok := content[ct]; !ok {
		t.Fatalf("%s: %s content type %q is not declared", pattern, status, ct)
	}
	if strings.HasSuffix(ct, "json") {
		ptr := pointer(append(op, "responses", status, "content", ct, "schema")...)
		if err := validateJSON(t, specSchema(t, ptr), body); err != nil {
			t.Fatalf("%s: body does not match the spec: %v\n%s", pattern, err, body)
		}
	}
}

// TestRoutesMatchSpec: every registered API route is an operation in the spec. Owner routes
// must also use the spec's path parameter names, which the generated binding reads.
func TestRoutesMatchSpec(t *testing.T) {
	doc, _, _ := loadSpec(t)
	paths, _ := doc["paths"].(map[string]any)
	templates := map[string]string{} // normalized path -> spec path
	for p := range paths {
		templates[pathParam.ReplaceAllString(p, "{}")] = p
	}
	rt, err := newRouter(slog.New(slog.DiscardHandler), newUITestFS(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rt.routes {
		method, path, _ := strings.Cut(r.pattern, " ")
		tmpl, ok := templates[pathParam.ReplaceAllString(path, "{}")]
		if !ok {
			t.Errorf("%s: path is not in the spec", r.pattern)
			continue
		}
		if strings.HasPrefix(path, "/api/v1/") && tmpl != path {
			t.Errorf("%s: path parameters must be named as in the spec (%s)", r.pattern, tmpl)
		}
		if _, ok := lookup(doc, "paths", tmpl, strings.ToLower(method)); !ok {
			t.Errorf("%s: method is not in the spec", r.pattern)
		}
	}
}

// TestDocExamplesValidate keeps the synthetic examples in api.md and the spec in step.
func TestDocExamplesValidate(t *testing.T) {
	md, err := os.ReadFile("../../docs/architecture/api.md")
	if err != nil {
		t.Fatal(err)
	}
	for heading, schema := range map[string]string{
		"## Example: resolved day":          "ResolvedDaily",
		"## Example: all-sources drilldown": "SourcesDrilldown",
	} {
		_, rest, ok := strings.Cut(string(md), heading)
		if !ok {
			t.Fatalf("api.md: %q not found", heading)
		}
		_, rest, _ = strings.Cut(rest, "```json\n")
		example, _, ok := strings.Cut(rest, "```")
		if !ok {
			t.Fatalf("api.md: no JSON block under %q", heading)
		}
		if err := validateJSON(t, specSchema(t, pointer("components", "schemas", schema)), []byte(example)); err != nil {
			t.Errorf("%s does not match %s: %v", heading, schema, err)
		}
	}
}
