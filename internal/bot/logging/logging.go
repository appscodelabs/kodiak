// Package logging configures the bot's structured logger.
//
// Ported from bot/kodiak/logging.py. Uses zerolog as the structlog analogue.
package logging

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/rs/zerolog"
)

// Level parses a Python-style log level name into a zerolog level.
func Level(name string) zerolog.Level {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "CRITICAL", "FATAL":
		return zerolog.FatalLevel
	case "ERROR":
		return zerolog.ErrorLevel
	case "WARNING", "WARN":
		return zerolog.WarnLevel
	case "DEBUG":
		return zerolog.DebugLevel
	case "NOTSET", "TRACE":
		return zerolog.TraceLevel
	default:
		return zerolog.InfoLevel
	}
}

// New constructs the root logger. The output writer defaults to os.Stdout.
func New(level zerolog.Level, out io.Writer) zerolog.Logger {
	if out == nil {
		out = os.Stdout
	}
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	return zerolog.New(out).Level(level).With().Timestamp().Logger()
}

type ctxKey struct{}

// WithContext stores a logger on the context.
func WithContext(ctx context.Context, log zerolog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, log)
}

// FromContext retrieves a logger from the context, falling back to a disabled
// logger so callers never have to nil-check.
func FromContext(ctx context.Context) zerolog.Logger {
	if v, ok := ctx.Value(ctxKey{}).(zerolog.Logger); ok {
		return v
	}
	return zerolog.Nop()
}
