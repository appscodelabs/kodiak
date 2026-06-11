// ported from bot/kodiak/test_main.py
//
// NOTE(port): the Python tests run against the starlette app with a mocked
// redis client; here we run the http.ServeMux against miniredis. The
// uvicorn/starlette specifics (sentry middleware, ujson responses) have no Go
// counterpart.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/appconf"
)

const testSecretKey = "some-random-key"

func newTestIngest(t *testing.T) (*http.ServeMux, *miniredis.Miniredis) {
	t.Helper()
	appconf.SecretKey = testSecretKey
	appconf.IngestQueueLength = 1000
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newIngestMux(rdb), mr
}

func signBody(body []byte) string {
	mac := hmac.New(sha1.New, []byte(testSecretKey))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// countIngestedEvents sums the lengths of all per-installation ingest queues.
func countIngestedEvents(t *testing.T, mr *miniredis.Miniredis) int {
	t.Helper()
	total := 0
	for _, key := range mr.Keys() {
		if strings.HasPrefix(key, "kodiak:ingest:") {
			events, err := mr.List(key)
			if err != nil {
				t.Fatal(err)
			}
			total += len(events)
		}
	}
	return total
}

func TestRoot(t *testing.T) {
	mux, _ := newTestIngest(t)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.Code)
	}
	if res.Body.String() != "OK" {
		t.Fatalf("body = %q, want OK", res.Body.String())
	}
}

// Test all of the events we have.
func TestWebhookEvent(t *testing.T) {
	eventNames := []string{
		"check_run",
		"pull_request",
		"pull_request_review",
		"pull_request_review_thread",
		"status",
		"push",
	}
	for _, eventName := range eventNames {
		t.Run(eventName, func(t *testing.T) {
			mux, mr := newTestIngest(t)
			fixtureDir := filepath.Join("..", "..", "bot", "kodiak", "test", "fixtures", "events", eventName)
			paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			for index, path := range paths {
				body, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := countIngestedEvents(t, mr); got != index {
					t.Fatalf("%s: ingested events = %d, want %d", path, got, index)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/github/hook", bytes.NewReader(body))
				req.Header.Set("X-Github-Event", eventName)
				req.Header.Set("X-Hub-Signature", signBody(body))
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("%s: status = %d, want 200 (body: %s)", path, res.Code, res.Body.String())
				}
				if got := countIngestedEvents(t, mr); got != index+1 {
					t.Fatalf("%s: ingested events = %d, want %d", path, got, index+1)
				}
			}
		})
	}
}

func TestWebhookEventMissingGithubEvent(t *testing.T) {
	mux, mr := newTestIngest(t)
	body := []byte(`{"hello": 123}`)
	req := httptest.NewRequest(http.MethodPost, "/api/github/hook", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", signBody(body))
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if got := countIngestedEvents(t, mr); got != 0 {
		t.Fatalf("ingested events = %d, want 0", got)
	}
}

func TestWebhookEventInvalidSignature(t *testing.T) {
	mux, mr := newTestIngest(t)
	// use a different body for the signature so we get a signature mismatch
	sha := signBody([]byte(`{}`))
	body := []byte(`{"hello": 123}`)
	req := httptest.NewRequest(http.MethodPost, "/api/github/hook", bytes.NewReader(body))
	req.Header.Set("X-Github-Event", "content_reference")
	req.Header.Set("X-Hub-Signature", sha)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if got := countIngestedEvents(t, mr); got != 0 {
		t.Fatalf("ingested events = %d, want 0", got)
	}
}
