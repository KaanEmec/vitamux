package obs

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
)

const masked = "[REDACTED]"

var (
	// Matched as a case-insensitive substring, so it also hits keys such as
	// status_code or X-Api-Key. Prefer other key names for non-secret values.
	sensitiveKey = regexp.MustCompile(`(?i)token|secret|passw(or)?d|authorization|cookie|code|refresh|api[_-]?key`)

	queryValue = regexp.MustCompile(`([?&#][^=&#\s"'<>]+=)[^&#\s"'<>]*`)
	userinfo   = regexp.MustCompile(`(://)[^/?#@\s"'<>]+@`)
	bearer     = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)
)

// IsSensitiveKey reports whether a log attribute, header or parameter name holds a secret.
func IsSensitiveKey(key string) bool { return sensitiveKey.MatchString(key) }

// RedactString masks query and fragment values, URL credentials and bearer tokens
// anywhere in s, so URLs inside error messages are safe to log.
func RedactString(s string) string {
	s = queryValue.ReplaceAllString(s, "${1}"+masked)
	s = userinfo.ReplaceAllString(s, "${1}"+masked+"@")
	return bearer.ReplaceAllString(s, "Bearer "+masked)
}

// NewRedactingHandler wraps next so that attributes with sensitive keys (at any group
// depth) are masked, and URL queries and credentials in strings, errors and messages
// are scrubbed before they reach next.
func NewRedactingHandler(next slog.Handler) slog.Handler { return &redactHandler{next: next} }

type redactHandler struct {
	next slog.Handler
	// masked is set once a sensitive group name is entered: everything below it is masked.
	masked bool
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, RedactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a, h.masked))
		return true
	})
	return h.next.Handle(ctx, nr)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a, h.masked)
	}
	return &redactHandler{next: h.next.WithAttrs(out), masked: h.masked}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(name), masked: h.masked || IsSensitiveKey(name)}
}

func redactAttr(a slog.Attr, forced bool) slog.Attr {
	v := a.Value.Resolve()
	forced = forced || IsSensitiveKey(a.Key)
	switch {
	case v.Kind() == slog.KindGroup:
		group := v.Group()
		out := make([]slog.Attr, len(group))
		for i, g := range group {
			out[i] = redactAttr(g, forced)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case forced:
		return slog.String(a.Key, masked)
	case v.Kind() == slog.KindString:
		return slog.String(a.Key, RedactString(v.String()))
	case v.Kind() == slog.KindAny:
		return slog.Any(a.Key, redactAny(v.Any()))
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// redactAny scrubs the value shapes that routinely carry secrets: errors (a *url.Error
// embeds the full request URL), URLs, headers, query values and string maps.
func redactAny(v any) any {
	switch x := v.(type) {
	case error:
		return RedactString(x.Error())
	case *url.URL:
		if x == nil {
			return v
		}
		return RedactString(x.String())
	case url.URL:
		return RedactString(x.String())
	case http.Header:
		return redactListMap(x)
	case url.Values: // query values are request data: mask them all
		out := make(url.Values, len(x))
		for k, vals := range x {
			out[k] = make([]string, len(vals))
			for i := range vals {
				out[k][i] = masked
			}
		}
		return out
	case map[string][]string:
		return redactListMap(x)
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, s := range x {
			out[k] = masked
			if !IsSensitiveKey(k) {
				out[k] = RedactString(s)
			}
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = masked
			if !IsSensitiveKey(k) {
				out[k] = redactAny(e)
			}
		}
		return out
	case string:
		return RedactString(x)
	}
	return v
}

func redactListMap(m map[string][]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, vals := range m {
		scrubbed := make([]string, len(vals))
		for i, s := range vals {
			scrubbed[i] = masked
			if !IsSensitiveKey(k) {
				scrubbed[i] = RedactString(s)
			}
		}
		out[k] = scrubbed
	}
	return out
}
