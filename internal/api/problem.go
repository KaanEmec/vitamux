package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Code is a stable, machine-readable error code (docs/architecture/api.md#conventions).
type Code string

const (
	CodeValidationFailed          Code = "validation_failed"
	CodeUnauthenticated           Code = "unauthenticated"
	CodeTOTPRequired              Code = "totp_required"
	CodeForbidden                 Code = "forbidden"
	CodeNotFound                  Code = "not_found"
	CodeConflict                  Code = "conflict"
	CodeRateLimited               Code = "rate_limited"
	CodeReauthRequired            Code = "reauth_required"
	CodeConsentRequired           Code = "consent_required"
	CodeUnsupportedWindow         Code = "unsupported_window"
	CodeRuleWarningUnacknowledged Code = "rule_warning_unacknowledged"
	CodePayloadTooLarge           Code = "payload_too_large"
	CodeInternal                  Code = "internal_error"
	CodeUnavailable               Code = "unavailable"
)

// problemKinds is the registry: every code an API response may carry, with its HTTP status
// and title. Add new codes here and to api.md; writeProblem refuses unregistered ones.
var problemKinds = map[Code]struct {
	status int
	title  string
}{
	CodeValidationFailed:          {http.StatusUnprocessableEntity, "Validation failed"},
	CodeUnauthenticated:           {http.StatusUnauthorized, "Authentication required"},
	CodeTOTPRequired:              {http.StatusUnauthorized, "TOTP code required"},
	CodeForbidden:                 {http.StatusForbidden, "Forbidden"},
	CodeNotFound:                  {http.StatusNotFound, "Not found"},
	CodeConflict:                  {http.StatusConflict, "Conflict"},
	CodeRateLimited:               {http.StatusTooManyRequests, "Rate limited"},
	CodeReauthRequired:            {http.StatusConflict, "Re-authorization required"},
	CodeConsentRequired:           {http.StatusConflict, "Consent required"},
	CodeUnsupportedWindow:         {http.StatusUnprocessableEntity, "Unsupported window"},
	CodeRuleWarningUnacknowledged: {http.StatusConflict, "Rule warning not acknowledged"},
	CodePayloadTooLarge:           {http.StatusRequestEntityTooLarge, "Payload too large"},
	CodeInternal:                  {http.StatusInternalServerError, "Internal error"},
	CodeUnavailable:               {http.StatusServiceUnavailable, "Service unavailable"},
}

// FieldError points at one invalid input in a validation_failed problem.
type FieldError struct {
	Pointer string `json:"pointer"`
	Detail  string `json:"detail"`
}

// problem is an RFC 9457 body. Detail must be safe to show to the caller: never put
// secrets, provider payloads or internal error text in it.
type problem struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Status    int          `json:"status"`
	Detail    string       `json:"detail,omitempty"`
	Code      Code         `json:"code"`
	RequestID string       `json:"request_id,omitempty"`
	Errors    []FieldError `json:"errors,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, code Code, detail string, errs ...FieldError) {
	kind, ok := problemKinds[code]
	if !ok {
		code, kind = CodeInternal, problemKinds[CodeInternal]
	}
	h := w.Header()
	h.Set("Content-Type", "application/problem+json")
	h.Set("Cache-Control", "no-store")
	h.Del("Content-Length")
	w.WriteHeader(kind.status)
	_ = json.NewEncoder(w).Encode(problem{
		Type: "urn:vitamux:problem:" + string(code), Title: kind.title, Status: kind.status,
		Detail: detail, Code: code, RequestID: requestIDFrom(r.Context()), Errors: errs,
	})
}

// writeBodyError answers a failed body read: 413 when the route-class limit was hit,
// 400-class validation otherwise. Handlers call it when reading r.Body fails.
func writeBodyError(w http.ResponseWriter, r *http.Request, err error) {
	if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
		writeProblem(w, r, CodePayloadTooLarge, "request body exceeds the limit for this endpoint")
		return
	}
	writeProblem(w, r, CodeValidationFailed, "request body could not be read")
}
