// Package logging configures structured logging for the kodiak bot,
// ported from bot/kodiak/logging.py (structlog replaced with log/slog).
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// GetLoggingLevel converts a Python-style logging level name to a slog.Level.
func GetLoggingLevel(name string) (slog.Level, error) {
	switch strings.ToUpper(name) {
	case "CRITICAL", "FATAL":
		return slog.LevelError, nil
	case "ERROR":
		return slog.LevelError, nil
	case "WARNING", "WARN":
		return slog.LevelWarn, nil
	case "INFO":
		return slog.LevelInfo, nil
	case "DEBUG":
		return slog.LevelDebug, nil
	case "NOTSET":
		return slog.LevelDebug, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown logging level: %q", name)
	}
}

// Configure sets the process-wide default logger writing key-value logs to
// stdout, mirroring configure_logging in the Python implementation.
func Configure(level slog.Level) {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}
