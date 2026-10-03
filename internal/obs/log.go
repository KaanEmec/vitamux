// Package obs holds observability plumbing: structured logging now, metrics later (J06.7).
package obs

import (
	"io"
	"log/slog"
)

// NewLogger returns a JSON slog logger at the given level.
// Secret redaction is added in J03.4; until then callers must not log secrets.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
