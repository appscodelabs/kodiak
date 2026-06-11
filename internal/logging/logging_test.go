package logging

import (
	"log/slog"
	"testing"
)

// NOTE(port): the SentryProcessor tests and test_add_request_info_processor
// from bot/kodiak/test_logging.py are not ported. They exercise the vendored
// structlog-sentry processor and a structlog request-info processor, which the
// Go port intentionally replaces with plain log/slog.

// Ported from test_get_logging_level in bot/kodiak/test_logging.py.
func TestGetLoggingLevel(t *testing.T) {
	cases := []struct {
		level    string
		expected slog.Level
	}{
		{"info", slog.LevelInfo},
		{"Warn", slog.LevelWarn},
	}
	for _, tc := range cases {
		got, err := GetLoggingLevel(tc.level)
		if err != nil {
			t.Fatalf("GetLoggingLevel(%q) returned error: %v", tc.level, err)
		}
		if got != tc.expected {
			t.Errorf("GetLoggingLevel(%q) = %v, want %v", tc.level, got, tc.expected)
		}
	}
}
