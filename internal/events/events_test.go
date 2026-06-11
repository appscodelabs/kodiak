package events

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ported from bot/kodiak/test_events.py
//
// A mapping of all events to their corresponding fixtures. Any new event must
// register itself here for testing.
//
// NOTE(port): pydantic's parse_file validates required fields; Go's
// encoding/json only checks that the JSON is well formed and the field types
// match.
func TestEventParsing(t *testing.T) {
	mapping := []struct {
		eventName string
		parse     func([]byte) error
	}{
		{"check_run", func(b []byte) error { return decodeJSON(b, &CheckRunEvent{}) }},
		{"pull_request", func(b []byte) error { return decodeJSON(b, &PullRequestEvent{}) }},
		{"pull_request_review", func(b []byte) error { return decodeJSON(b, &PullRequestReviewEvent{}) }},
		{"pull_request_review_thread", func(b []byte) error { return decodeJSON(b, &PullRequestReviewThreadEvent{}) }},
		{"status", func(b []byte) error { return decodeJSON(b, &StatusEvent{}) }},
		{"push", func(b []byte) error { return decodeJSON(b, &PushEvent{}) }},
	}
	for _, m := range mapping {
		t.Run(m.eventName, func(t *testing.T) {
			fixtureDir := filepath.Join("..", "..", "bot", "kodiak", "test", "fixtures", "events", m.eventName)
			paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			// like Python's rglob, a missing fixture directory yields no
			// fixtures rather than an error.
			for _, path := range paths {
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := m.parse(content); err != nil {
					t.Errorf("%s: %v", path, err)
				}
			}
		})
	}
}

func decodeJSON(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	return dec.Decode(v)
}
