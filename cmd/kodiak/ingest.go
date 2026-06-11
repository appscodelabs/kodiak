package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/queue"
	"github.com/appscodelabs/kodiak/internal/redisclient"
	"github.com/appscodelabs/kodiak/internal/schemas"
)

// administrative events do not have an installation id and are not queued for
// processing.
var administrativeEvents = map[string]bool{
	"github_app_authorization":  true,
	"installation":              true,
	"installation_repositories": true,
}

// runIngest accepts webhooks from GitHub and adds them to the Redis queues.
// Ported from bot/kodiak/entrypoints/ingest.py.
func runIngest(ctx context.Context) error {
	rdb, err := redisclient.Bot()
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", appconf.Port),
		Handler: newIngestMux(rdb),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("starting ingest server", "port", appconf.Port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newIngestMux builds the ingest HTTP routes. Extracted from runIngest as a
// test seam.
func newIngestMux(rdb *redis.Client) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("POST /api/github/hook", func(w http.ResponseWriter, r *http.Request) {
		githubWebhookEvent(w, r, rdb)
	})
	return mux
}

func writeJSONOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok": true}`))
}

func githubWebhookEvent(w http.ResponseWriter, r *http.Request, rdb *redis.Client) {
	ctx := r.Context()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not read request body", http.StatusBadRequest)
		return
	}

	mac := hmac.New(sha1.New, []byte(appconf.SecretKey))
	mac.Write(body)
	expectedSHA := hex.EncodeToString(mac.Sum(nil))

	githubEvent := r.Header.Get("X-Github-Event")
	if githubEvent == "" {
		http.Error(w, "missing required X-Github-Event header", http.StatusBadRequest)
		return
	}
	hubSignature := r.Header.Get("X-Hub-Signature")
	if hubSignature == "" {
		http.Error(w, "missing required X-Hub-Signature header", http.StatusBadRequest)
		return
	}
	sha := strings.ReplaceAll(hubSignature, "sha1=", "")
	if !hmac.Equal([]byte(sha), []byte(expectedSHA)) {
		http.Error(w, "Invalid signature: X-Hub-Signature", http.StatusBadRequest)
		return
	}

	var event struct {
		Installation *struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	log := slog.With("event_name", githubEvent)

	if administrativeEvents[githubEvent] {
		log.Info("administrative_event_received")
		writeJSONOK(w)
		return
	}

	if event.Installation == nil {
		log.Warn("unexpected_event_skipped")
		writeJSONOK(w)
		return
	}

	ingestQueue := queue.GetIngestQueue(event.Installation.ID)

	rawEvent, err := json.Marshal(schemas.RawWebhookEvent{
		EventName: githubEvent,
		Payload:   json.RawMessage(body),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := rdb.RPush(ctx, ingestQueue, rawEvent).Err(); err != nil {
		log.Warn("failed to enqueue webhook event", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := rdb.LTrim(ctx, ingestQueue, 0, int64(appconf.IngestQueueLength)).Err(); err != nil {
		log.Warn("failed to trim ingest queue", "error", err)
	}
	if err := rdb.SAdd(ctx, queue.IngestQueueNames, ingestQueue).Err(); err != nil {
		log.Warn("failed to record ingest queue name", "error", err)
	}
	pubsubMessage, err := json.Marshal(queue.PubsubIngestQueueMessage{InstallationID: event.Installation.ID})
	if err == nil {
		if err := rdb.Publish(ctx, queue.QueuePubsubIngest, pubsubMessage).Err(); err != nil {
			log.Warn("failed to publish ingest notification", "error", err)
		}
	}
	writeJSONOK(w)
}
