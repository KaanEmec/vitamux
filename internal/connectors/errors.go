package connectors

import (
	"fmt"
	"time"
)

// Error classes, recorded in job_runs.error_class and connections.last_error_class.
const (
	ClassReauthRequired = "reauth_required"
	ClassRateLimited    = "rate_limited"
	ClassTransient      = "transient"
	ClassSchemaDrift    = "schema_drift"
	ClassPermanent      = "permanent"
)

// Typed errors of docs/architecture/connectors.md#typed-errors. Wrap the sentinels with %w
// to add context; errors.Is finds them.
var (
	ErrReauthRequired error = &classError{ClassReauthRequired, "reauthorization required"}
	ErrTransient      error = &classError{ClassTransient, "transient failure"}
	ErrPermanent      error = &classError{ClassPermanent, "permanent failure"}
)

type classError struct{ class, msg string }

func (e *classError) Error() string      { return e.msg }
func (e *classError) ErrorClass() string { return e.class }

// RateLimitedError is a provider refusing calls for RetryAfter. HTTPClient returns it for 429.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited; retry after %s", e.RetryAfter.Round(time.Second))
}
func (e *RateLimitedError) ErrorClass() string { return ClassRateLimited }

// SchemaDriftError is a response whose shape no longer matches what the connector knows.
// Records put into the sink before it is returned are stored as quarantined.
type SchemaDriftError struct{ Endpoint, Fingerprint string }

func (e *SchemaDriftError) Error() string {
	return fmt.Sprintf("schema drift at %s (shape %s)", e.Endpoint, e.Fingerprint)
}
func (e *SchemaDriftError) ErrorClass() string { return ClassSchemaDrift }
