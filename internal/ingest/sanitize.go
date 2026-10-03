package ingest

import (
	"bytes"
	"encoding/json"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"

	"github.com/KaanEmec/vitamux/internal/obs"
)

// Request describes how a raw payload was fetched. SanitizeRequest turns it into the stored
// raw_payloads.request_meta.
type Request struct {
	Endpoint string
	Params   map[string]any
	Headers  map[string]string
}

// Parameter names that carry or bind credentials, beyond obs.IsSensitiveKey (which covers
// token, secret, password, authorization, cookie, code, refresh and api key).
var authParam = regexp.MustCompile(`(?i)auth|signature|^sig$|nonce|session|credential|private|otp|mfa|^key$|^state$`)

// Only these headers are kept; everything else (Authorization, Cookie, vendor key headers)
// is dropped.
var keptHeaders = map[string]bool{
	"Accept": true, "Accept-Encoding": true, "Accept-Language": true, "Content-Type": true,
	"If-Modified-Since": true, "If-None-Match": true, "User-Agent": true,
}

func dropParam(key string) bool { return obs.IsSensitiveKey(key) || authParam.MatchString(key) }

// SanitizeRequest returns request_meta for r: the endpoint without credentials, query or
// fragment (non-secret query parameters move to params), params without credential-like keys
// at any depth, and only allow-listed headers. String values are scrubbed of URL queries and
// bearer tokens.
func SanitizeRequest(r Request) json.RawMessage {
	meta := map[string]any{}
	params := map[string]any{}
	if r.Endpoint != "" {
		endpoint, query := splitEndpoint(r.Endpoint)
		meta["endpoint"] = endpoint
		for k, vs := range query {
			if len(vs) > 0 && !dropParam(k) {
				params[k] = obs.RedactString(vs[0])
			}
		}
	}
	for k, v := range sanitizeMap(generic(r.Params)) {
		params[k] = v
	}
	if len(params) > 0 {
		meta["params"] = params
	}
	headers := map[string]string{}
	for k, v := range r.Headers {
		if k = textproto.CanonicalMIMEHeaderKey(k); keptHeaders[k] {
			headers[k] = obs.RedactString(v)
		}
	}
	if len(headers) > 0 {
		meta["headers"] = headers
	}
	out, _ := json.Marshal(meta) // only strings, json.Number, bools and nils remain
	return out
}

// generic turns arbitrary Go values (nested structs, typed maps and slices) into the
// map[string]any / []any shapes sanitizeValue walks. Unencodable params are dropped.
func generic(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var out map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&out) != nil {
		return nil
	}
	return out
}

// splitEndpoint drops userinfo, query and fragment. Unparseable input keeps only the part
// before any '?' or '#', scrubbed.
func splitEndpoint(s string) (string, url.Values) {
	u, err := url.Parse(s)
	if err != nil {
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		return obs.RedactString(s), nil
	}
	query := u.Query()
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	return u.String(), query
}

func sanitizeMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if !dropParam(k) {
			out[k] = sanitizeValue(v)
		}
	}
	return out
}

func sanitizeValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return sanitizeMap(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sanitizeValue(e)
		}
		return out
	case string:
		return obs.RedactString(x)
	}
	return v
}
