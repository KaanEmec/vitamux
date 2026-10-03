// Package obs holds observability plumbing: structured logging now, metrics later (J06.7).
package obs

import (
	"io"
	"log/slog"
)

// NewLogger returns a JSON slog logger at the given level. Every record passes through
// the redacting handler (see redact.go).
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewRedactingHandler(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})))
}
