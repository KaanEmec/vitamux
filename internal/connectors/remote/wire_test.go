package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KaanEmec/vitamux/internal/connectors"
)

// The mapping tests enforce that every field of the connector contract has exactly one
// representation in vitamux-connector/1. A Go field added without a wire mapping fails here:
// the tables list every leaf field path that reflection finds, and each must survive a
// round trip through JSON while changing only its own wire key.

var (
	descriptorWire = map[string]string{
		"Provider": "provider", "Name": "name", "Version": "version", "Official": "official", "AuthKind": "auth_kind",
		"Streams[].Name": "streams[].name", "Streams[].Interval": "streams[].interval_s", "Streams[].Lookback": "streams[].lookback_s",
		"Streams[].CorrectionEvery": "streams[].correction_every_s", "Streams[].MaxBackfill": "streams[].max_backfill_s",
		"Streams[].UnitSize":    "streams[].unit_size_s",
		"RateLimits[].Requests": "rate_limits[].requests", "RateLimits[].Per": "rate_limits[].per_s",
		"Capabilities.Incremental": "capabilities.incremental", "Capabilities.Backfill": "capabilities.backfill",
		"Capabilities.Webhooks": "capabilities.webhooks", "Capabilities.ManualSync": "capabilities.manual_sync",
		"Upstream.Package": "upstream.package", "Upstream.Version": "upstream.version", "Upstream.SourceURL": "upstream.source_url",
		"Remote": "", // not on the wire: every described sidecar is remote
	}
	stepWire = map[string]string{
		"RedirectURL": "redirect_url", "Session": "session", "Prompt.Message": "prompt.message",
		"Prompt.Fields[].Name": "prompt.fields[].name", "Prompt.Fields[].Label": "prompt.fields[].label", "Prompt.Fields[].Kind": "prompt.fields[].kind",
	}
	authorizedWire = map[string]string{
		"AccountID": "authorized.account_id", "Credentials": "authorized.credentials",
		"Next": "step authorized", // a next step replaces the authorization
	}
	fetchResultWire = map[string]string{
		"NextCursor": "next_cursor", "HighWatermark": "high_watermark", "Done": "done", "RetryAfter": "retry_after_s", "Credentials": "credentials",
	}
	authInputWire = map[string]string{ // forward only; RedirectURL is in both requests
		"RedirectURL": "begin.redirect_url continue.redirect_url", "State": "begin.state",
		"Callback": "continue.callback", "Values": "continue.values", "Session": "continue.session",
	}
	workUnitWire = map[string]string{ // forward only
		"Stream": "stream", "Cursor": "cursor", "From": "from", "To": "to",
	}
	rateLimitedWire = map[string]string{"RetryAfter": "retry_after_s"}
	driftWire       = map[string]string{"Endpoint": "endpoint", "Fingerprint": "fingerprint"}
)

// Types the mapping treats as one value: opaque on the wire, or mapped by their own table.
var leafTypes = map[reflect.Type]bool{
	reflect.TypeFor[time.Time]():              true,
	reflect.TypeFor[connectors.Credentials](): true,
	reflect.TypeFor[connectors.AuthStep]():    true,
}

func TestDescriptorMapping(t *testing.T) {
	checkMapping(t, descriptorWire, func(d connectors.Descriptor) any { return DescribeOf(d) },
		func(t *testing.T, b []byte) connectors.Descriptor {
			d, err := DecodeDescribe(b)
			if err != nil {
				t.Fatal(err)
			}
			return d
		},
		func(d *connectors.Descriptor) { d.Remote = true })
}

func TestAuthStepMapping(t *testing.T) {
	checkMapping(t, stepWire, func(s connectors.AuthStep) any { return StepOf(s) },
		func(t *testing.T, b []byte) connectors.AuthStep { return decode[Step](t, b).AuthStep() }, nil)
}

func TestAuthorizedMapping(t *testing.T) {
	checkMapping(t, authorizedWire, func(a connectors.Authorized) any { return AuthResponseOf(a) },
		func(t *testing.T, b []byte) connectors.Authorized { return decode[AuthResponse](t, b).Result() }, nil)
}

func TestFetchResultMapping(t *testing.T) {
	checkMapping(t, fetchResultWire, func(r connectors.FetchResult) any { return ResultOf(r) },
		func(t *testing.T, b []byte) connectors.FetchResult {
			l, err := DecodeLine(b)
			if err != nil || l.Result == nil {
				t.Fatalf("DecodeLine: %v", err)
			}
			return *l.Result
		}, nil)
}

func TestAuthInputMapping(t *testing.T) {
	checkMapping(t, authInputWire, func(in connectors.AuthInput) any {
		return map[string]any{"begin": BeginRequest(in, nil), "continue": ContinueRequest(in, nil)}
	}, nil, nil)
}

func TestWorkUnitMapping(t *testing.T) {
	checkMapping(t, workUnitWire, func(u connectors.WorkUnit) any {
		return FetchRequestOf(u, "", connectors.Credentials{}, nil)
	}, nil, nil)
}

func TestTypedErrorFieldMapping(t *testing.T) {
	checkMapping(t, rateLimitedWire, func(e connectors.RateLimitedError) any { return ProblemOf(&e) },
		func(t *testing.T, b []byte) connectors.RateLimitedError {
			e, ok := errors.AsType[*connectors.RateLimitedError](DecodeProblem(b))
			if !ok {
				t.Fatal("not a RateLimitedError")
			}
			return *e
		}, nil)
	checkMapping(t, driftWire, func(e connectors.SchemaDriftError) any { return ProblemOf(&e) },
		func(t *testing.T, b []byte) connectors.SchemaDriftError {
			e, ok := errors.AsType[*connectors.SchemaDriftError](DecodeProblem(b))
			if !ok {
				t.Fatal("not a SchemaDriftError")
			}
			return *e
		}, nil)
}

// TestErrorClassMapping: every error class constant is one problem code, both ways.
func TestErrorClassMapping(t *testing.T) {
	samples := map[string]error{
		connectors.ClassReauthRequired: connectors.ErrReauthRequired,
		connectors.ClassRateLimited:    &connectors.RateLimitedError{RetryAfter: 90 * time.Second},
		connectors.ClassTransient:      connectors.ErrTransient,
		connectors.ClassSchemaDrift:    &connectors.SchemaDriftError{Endpoint: "/v1/x", Fingerprint: "f"},
		connectors.ClassPermanent:      connectors.ErrPermanent,
	}
	classes := goConsts(t, "../errors.go", func(name, _ string) bool { return strings.HasPrefix(name, "Class") })
	if got := slices.Sorted(maps.Keys(samples)); !slices.Equal(got, classes) {
		t.Fatalf("error classes %v, mapped %v", classes, got)
	}
	if enum := schemaEnum(t, "error_fields", "code"); !slices.Equal(enum, classes) {
		t.Fatalf("schema codes %v, Go classes %v", enum, classes)
	}
	for class, err := range samples {
		p := ProblemOf(fmt.Errorf("wrapped: %w", err))
		if p.Code != class {
			t.Errorf("%s: problem code %s", class, p.Code)
		}
		b, _ := json.Marshal(p)
		back := DecodeProblem(b)
		if c := classOf(back); c != class {
			t.Errorf("%s: decoded as %s", class, c)
		}
	}
	if c := classOf(Problem{Code: "brand_new"}.Err()); c != connectors.ClassTransient {
		t.Errorf("unknown code decoded as %s", c)
	}
}

// TestAuthKinds: every AuthKind constant is one describe auth_kind.
func TestAuthKinds(t *testing.T) {
	kinds := goConsts(t, "../connector.go", func(_, typ string) bool { return typ == "AuthKind" })
	if enum := schemaEnum(t, "describe", "auth_kind"); !slices.Equal(enum, kinds) {
		t.Fatalf("schema auth kinds %v, Go %v", enum, kinds)
	}
}

// TestWireMatchesSchema: every wire struct's JSON keys are exactly its schema's properties.
func TestWireMatchesSchema(t *testing.T) {
	for def, typ := range map[string]reflect.Type{
		"describe": reflect.TypeFor[Describe](), "stream": reflect.TypeFor[Stream](), "rate_limit": reflect.TypeFor[RateLimit](),
		"capabilities": reflect.TypeFor[Capabilities](), "upstream": reflect.TypeFor[Upstream](),
		"credentials":        reflect.TypeFor[connectors.Credentials](),
		"auth_begin_request": reflect.TypeFor[AuthBeginRequest](), "auth_continue_request": reflect.TypeFor[AuthContinueRequest](),
		"auth_response": reflect.TypeFor[AuthResponse](), "step": reflect.TypeFor[Step](), "prompt": reflect.TypeFor[Prompt](),
		"field": reflect.TypeFor[Field](), "authorized": reflect.TypeFor[Authorized](),
		"refresh_request": reflect.TypeFor[RefreshRequest](), "refresh_response": reflect.TypeFor[RefreshResponse](),
		"fetch_request": reflect.TypeFor[FetchRequest](), "raw_line": reflect.TypeFor[RawLine](),
		"raw_line.request": reflect.TypeFor[Request](), "result_line": reflect.TypeFor[ResultLine](), "problem": reflect.TypeFor[Problem](),
	} {
		var keys []string
		for f := range typ.Fields() {
			keys = append(keys, strings.Split(f.Tag.Get("json"), ",")[0])
		}
		slices.Sort(keys)
		if props := schemaProps(t, def); !slices.Equal(keys, props) {
			t.Errorf("%s: Go keys %v, schema properties %v", def, keys, props)
		}
	}
}

// checkMapping checks table against G's leaf fields: setting one field changes exactly its
// wire paths, and (with fromJSON) the field survives the round trip. fix normalizes
// what the decoder always sets.
func checkMapping[G any](t *testing.T, table map[string]string, toWire func(G) any, fromJSON func(*testing.T, []byte) G, fix func(*G)) {
	t.Helper()
	typ := reflect.TypeFor[G]()
	if got, want := leafPaths(typ, ""), slices.Sorted(maps.Keys(table)); !slices.Equal(got, want) {
		t.Fatalf("%s fields %v, mapped %v", typ, got, want)
	}
	for path, wire := range table {
		var base, set G
		at(t, reflect.ValueOf(&base).Elem(), path, false)
		at(t, reflect.ValueOf(&set).Elem(), path, true)
		bb, sb := marshal(t, toWire(base)), marshal(t, toWire(set))
		changed := diff(flatten(t, bb), flatten(t, sb))
		want := strings.Fields(wire)
		for _, c := range changed {
			if !slices.ContainsFunc(want, func(w string) bool { return c == w || strings.HasPrefix(c, w+".") || strings.HasPrefix(c, w+"[]") }) {
				t.Errorf("%s.%s changed wire %s, want only %q", typ, path, c, wire)
			}
		}
		for _, w := range want {
			if !slices.ContainsFunc(changed, func(c string) bool { return c == w || strings.HasPrefix(c, w+".") || strings.HasPrefix(c, w+"[]") }) {
				t.Errorf("%s.%s did not change wire %s", typ, path, w)
			}
		}
		if fromJSON == nil {
			continue
		}
		got := fromJSON(t, sb)
		if fix != nil {
			fix(&set)
		}
		if !reflect.DeepEqual(got, set) {
			t.Errorf("%s.%s lost in the round trip:\n got %#v\nwant %#v", typ, path, got, set)
		}
	}
}

// leafPaths lists the field paths of a struct type, descending into structs, pointers to
// structs and slices of structs; "[]" marks a slice element.
func leafPaths(typ reflect.Type, prefix string) []string {
	var out []string
	for f := range typ.Fields() {
		p, ft := prefix+f.Name, f.Type
		for ft.Kind() == reflect.Pointer || (ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct) {
			if ft.Kind() == reflect.Slice {
				p += "[]"
			}
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && !leafTypes[ft] {
			out = append(out, leafPaths(ft, p+".")...)
		} else {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// at walks path in v, allocating pointers and one-element slices on the way, and fills the
// leaf when fill.
func at(t *testing.T, v reflect.Value, path string, fill bool) {
	t.Helper()
	parts := strings.Split(path, ".")
	for i, part := range parts {
		name, _ := strings.CutSuffix(part, "[]")
		v = v.FieldByName(name)
		for i < len(parts)-1 && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice) {
			switch {
			case v.Kind() == reflect.Pointer && v.IsNil():
				v.Set(reflect.New(v.Type().Elem()))
			case v.Kind() == reflect.Slice && v.Len() == 0:
				v.Set(reflect.MakeSlice(v.Type(), 1, 1))
			}
			if v.Kind() == reflect.Pointer {
				v = v.Elem()
			} else {
				v = v.Index(0)
			}
		}
	}
	if fill {
		v.Set(filler(t, v.Type()))
	}
}

var when = time.Date(2026, 9, 14, 7, 30, 0, 0, time.UTC)

// filler returns a non-zero value of typ that survives the wire (whole seconds, UTC).
func filler(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	switch typ {
	case reflect.TypeFor[time.Duration]():
		return reflect.ValueOf(7 * time.Second)
	case reflect.TypeFor[time.Time]():
		return reflect.ValueOf(when)
	case reflect.TypeFor[json.RawMessage]():
		return reflect.ValueOf(json.RawMessage(`{"k":1}`))
	case reflect.TypeFor[connectors.Credentials]():
		return reflect.ValueOf(connectors.Credentials{AccessToken: "synthetic-token"})
	case reflect.TypeFor[url.Values]():
		return reflect.ValueOf(url.Values{"k": {"v"}})
	case reflect.TypeFor[connectors.AuthStep]():
		return reflect.ValueOf(connectors.AuthStep{RedirectURL: "https://provider.example.com/authorize"})
	}
	switch typ.Kind() {
	case reflect.Pointer:
		p := reflect.New(typ.Elem())
		p.Elem().Set(filler(t, typ.Elem()))
		return p
	case reflect.String:
		return reflect.ValueOf("x").Convert(typ)
	case reflect.Bool:
		return reflect.ValueOf(true)
	case reflect.Int, reflect.Int64:
		return reflect.ValueOf(7).Convert(typ)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return reflect.ValueOf([]byte("s")).Convert(typ)
		}
	case reflect.Map:
		if typ.Key().Kind() == reflect.String && typ.Elem().Kind() == reflect.String {
			return reflect.ValueOf(map[string]string{"k": "v"}).Convert(typ)
		}
	default:
	}
	t.Fatalf("no filler for %s; add one", typ)
	return reflect.Value{}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decode[W any](t *testing.T, b []byte) W {
	t.Helper()
	var w W
	if err := json.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// flatten maps each JSON leaf path ("streams[].name") to its encoded value.
func flatten(t *testing.T, b []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(p string, v any)
	walk = func(p string, v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, e := range v {
				walk(strings.TrimPrefix(p+"."+k, "."), e)
			}
		case []any:
			for i, e := range v {
				walk(p+"[]", e)
				if i > 0 {
					t.Fatalf("flatten: %s has more than one element", p)
				}
			}
		default:
			out[p] = string(marshal(t, v))
		}
	}
	walk("", decode[any](t, b))
	return out
}

func diff(a, b map[string]string) []string {
	var out []string
	for k := range a {
		if v, ok := b[k]; !ok || v != a[k] {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// goConsts returns the sorted string values of the constants in file that keep says.
func goConsts(t *testing.T, file string, keep func(name, typ string) bool) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST {
			continue
		}
		for _, s := range g.Specs {
			vs := s.(*ast.ValueSpec)
			typ := ""
			if id, ok := vs.Type.(*ast.Ident); ok {
				typ = id.Name
			}
			for i, n := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if ok && lit.Kind == token.STRING && keep(n.Name, typ) {
					v, _ := strconv.Unquote(lit.Value)
					out = append(out, v)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

func classOf(err error) string {
	if c, ok := errors.AsType[interface {
		error
		ErrorClass() string
	}](err); ok {
		return c.ErrorClass()
	}
	return ""
}

func TestDecodeRejects(t *testing.T) {
	big := base64.StdEncoding.EncodeToString(make([]byte, MaxBinaryBytes+1))
	sum := sha256.Sum256(make([]byte, MaxBinaryBytes+1))
	bigSession := base64.StdEncoding.EncodeToString(make([]byte, MaxSessionBytes+1))
	for name, c := range map[string]struct {
		decode func([]byte) error
		doc    string
	}{
		"line not json":       {line, `{"type":`},
		"line unknown type":   {line, `{"type":"chunk"}`},
		"raw both bodies":     {line, `{"type":"raw","external_key":"k","content_type":"application/json","body":{},"body_base64":"AA==","sha256":"00"}`},
		"raw no body":         {line, `{"type":"raw","external_key":"k","content_type":"application/json"}`},
		"raw scalar body":     {line, `{"type":"raw","external_key":"k","content_type":"application/json","body":1}`},
		"raw no key":          {line, `{"type":"raw","content_type":"application/json","body":{}}`},
		"raw bad base64":      {line, `{"type":"raw","external_key":"k","content_type":"application/zip","body_base64":"!!","sha256":"00"}`},
		"raw sha mismatch":    {line, `{"type":"raw","external_key":"k","content_type":"application/zip","body_base64":"AA==","sha256":"` + strings.Repeat("0", 64) + `"}`},
		"raw over 25 MiB":     {line, `{"type":"raw","external_key":"k","content_type":"application/zip","body_base64":"` + big + `","sha256":"` + hex.EncodeToString(sum[:]) + `"}`},
		"result negative":     {line, `{"type":"result","done":true,"retry_after_s":-1}`},
		"error without code":  {line, `{"type":"error"}`},
		"describe protocol":   {describe, `{"protocol":"vitamux-connector/2","provider":"p"}`},
		"auth neither":        {auth, `{}`},
		"auth both":           {auth, `{"step":{"redirect_url":"https://a.example"},"authorized":{"account_id":"a","credentials":{}}}`},
		"step both":           {auth, `{"step":{"redirect_url":"https://a.example","prompt":{"message":"m","fields":[]}}}`},
		"step neither":        {auth, `{"step":{"session":"AA=="}}`},
		"session over 64 KiB": {auth, `{"step":{"redirect_url":"https://a.example","session":"` + bigSession + `"}}`},
	} {
		err := c.decode([]byte(c.doc))
		if !errors.Is(err, errProtocol) || !errors.Is(err, connectors.ErrTransient) {
			t.Errorf("%s: got %v, want a transient protocol violation", name, err)
		}
	}
	if err := DecodeProblem([]byte(`{"title":"x"}`)); !errors.Is(err, errProtocol) {
		t.Errorf("problem without code: %v", err)
	}
}

func line(b []byte) error     { _, err := DecodeLine(b); return err }
func describe(b []byte) error { _, err := DecodeDescribe(b); return err }
func auth(b []byte) error     { _, err := DecodeAuth(b); return err }

func TestRawLineItem(t *testing.T) {
	bin := []byte("synthetic\x00binary")
	sum := sha256.Sum256(bin)
	l, err := DecodeLine([]byte(`{"type":"raw","external_key":"f.fit","content_type":"application/vnd.ant.fit","body_base64":"` +
		base64.StdEncoding.EncodeToString(bin) + `","sha256":"` + hex.EncodeToString(sum[:]) + `","quarantine":true,` +
		`"request":{"endpoint":"/v1/f","params":{"page":12345678901234567890}}}`))
	if err != nil {
		t.Fatal(err)
	}
	it := *l.Raw
	if !bytes.Equal(it.Body, bin) || !it.Quarantine || it.Stream != "" || !it.FetchedAt.IsZero() || it.Request.Endpoint != "/v1/f" {
		t.Errorf("item %+v", it)
	}
	if n, ok := it.Request.Params["page"].(json.Number); !ok || n.String() != "12345678901234567890" {
		t.Errorf("params lost number precision: %v", it.Request.Params["page"])
	}
	l, err = DecodeLine([]byte(`{"type":"raw","stream":"s.v1","external_key":"k","content_type":"application/json","body":{"a": 1.50}}`))
	if err != nil || string(l.Raw.Body) != `{"a": 1.50}` || l.Raw.Stream != "s.v1" {
		t.Errorf("JSON body not verbatim: %v %+v", err, l.Raw)
	}
}

// The encoders below are the sidecar side of the wire mapping; the host only decodes.

// seconds rounds up, so a positive duration never becomes 0.
func seconds(d time.Duration) int64 { return int64((d + time.Second - 1) / time.Second) }

// DescribeOf is the describe response for d.
func DescribeOf(d connectors.Descriptor) Describe {
	w := Describe{
		Protocol: Protocol, Provider: d.Provider, Name: d.Name, Version: d.Version, Official: d.Official,
		AuthKind: string(d.AuthKind), Capabilities: Capabilities(d.Capabilities),
	}
	for _, s := range d.Streams {
		w.Streams = append(w.Streams, Stream{
			Name: s.Name, IntervalS: seconds(s.Interval), LookbackS: seconds(s.Lookback),
			CorrectionEveryS: seconds(s.CorrectionEvery), MaxBackfillS: seconds(s.MaxBackfill), UnitSizeS: seconds(s.UnitSize),
		})
	}
	for _, r := range d.RateLimits {
		w.RateLimits = append(w.RateLimits, RateLimit{Requests: r.Requests, PerS: seconds(r.Per)})
	}
	if d.Upstream != nil {
		w.Upstream = &Upstream{Package: d.Upstream.Package, Version: d.Upstream.Version, SourceURL: d.Upstream.SourceURL}
	}
	return w
}

// StepOf is the wire form of s.
func StepOf(s connectors.AuthStep) Step {
	w := Step{RedirectURL: s.RedirectURL, Session: s.Session}
	if s.Prompt != nil {
		w.Prompt = &Prompt{Message: s.Prompt.Message, Fields: make([]Field, 0, len(s.Prompt.Fields))}
		for _, f := range s.Prompt.Fields {
			w.Prompt.Fields = append(w.Prompt.Fields, Field(f))
		}
	}
	return w
}

// AuthResponseOf is the continue response for a: its Next step, or the authorization.
func AuthResponseOf(a connectors.Authorized) AuthResponse {
	if a.Next != nil {
		s := StepOf(*a.Next)
		return AuthResponse{Step: &s}
	}
	return AuthResponse{Authorized: &Authorized{AccountID: a.AccountID, Credentials: a.Credentials}}
}

// ResultOf is the result line for r.
func ResultOf(r connectors.FetchResult) ResultLine {
	return ResultLine{
		Type: LineResult, NextCursor: r.NextCursor, HighWatermark: r.HighWatermark, Done: r.Done,
		RetryAfterS: seconds(r.RetryAfter), Credentials: r.Credentials,
	}
}

// ProblemOf is the typed error err as a problem; untyped errors are transient.
func ProblemOf(err error) Problem {
	p := Problem{Code: connectors.ClassTransient, Status: http.StatusServiceUnavailable}
	var rl *connectors.RateLimitedError
	var drift *connectors.SchemaDriftError
	switch {
	case errors.As(err, &rl):
		p.Code, p.Status, p.RetryAfterS = connectors.ClassRateLimited, http.StatusTooManyRequests, seconds(rl.RetryAfter)
	case errors.As(err, &drift):
		p.Code, p.Status, p.Endpoint, p.Fingerprint = connectors.ClassSchemaDrift, http.StatusBadGateway, drift.Endpoint, drift.Fingerprint
	case errors.Is(err, connectors.ErrReauthRequired):
		p.Code, p.Status = connectors.ClassReauthRequired, http.StatusUnauthorized
	case errors.Is(err, connectors.ErrPermanent):
		p.Code, p.Status = connectors.ClassPermanent, http.StatusUnprocessableEntity
	}
	p.Title = p.Code
	return p
}
