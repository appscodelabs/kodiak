// Package ingest ports web_api/web_api/event_ingestion.py: remove GitHub
// webhook events from Redis and store them in Postgres for analysis.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// interestingEvents are the events we want to store in Postgres. Discard
// anything else.
var interestingEvents = map[string]bool{
	"pull_request":         true,
	"pull_request_review":  true,
	"pull_request_comment": true,
}

type webhookEvent struct {
	EventName string          `json:"event_name"`
	Payload   json.RawMessage `json:"payload"`
}

// Run pulls webhook events off the queue and inserts them into Postgres to
// calculate usage statistics. It loops until ctx is cancelled, finishing any
// in-flight event first so events are not lost on shutdown.
func Run(ctx context.Context, rdb *redis.Client, db *pgxpool.Pool) error {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return err
	}
	defer decoder.Close()

	for {
		select {
		case <-ctx.Done():
			slog.Info("received shutdown signal. Exiting gracefully.")
			return nil
		default:
		}
		// use a background context for the pop+insert so we never drop a
		// popped event during shutdown (mirrors GracefulTermination).
		res, err := rdb.BLPop(context.Background(), 5*time.Second, "kodiak:webhook_event").Result()
		if errors.Is(err, redis.Nil) {
			// we likely hit the timeout.
			continue
		}
		if err != nil {
			return err
		}
		decompressed, err := decoder.DecodeAll([]byte(res[1]), nil)
		if err != nil {
			return err
		}
		var event webhookEvent
		if err := json.Unmarshal(decompressed, &event); err != nil {
			return err
		}
		if !interestingEvents[event.EventName] {
			continue
		}
		if err := models.CreateGitHubEvent(context.Background(), db, event.EventName, event.Payload); err != nil {
			return err
		}
	}
}
