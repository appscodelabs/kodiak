package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/queue"
	"github.com/appscodelabs/kodiak/internal/redisclient"
)

// runWorker processes webhook events from the Redis queues. Ported from
// bot/kodiak/entrypoints/worker.py.
func runWorker(ctx context.Context) error {
	rdb, err := redisclient.Bot()
	if err != nil {
		return err
	}
	q, err := queue.NewRedisWebhookQueue()
	if err != nil {
		return err
	}
	if err := q.Create(ctx); err != nil {
		return err
	}

	log := slog.With("task", "main_worker")
	ingestQueueNames, err := rdb.SMembers(ctx, queue.IngestQueueNames).Result()
	if err != nil {
		return err
	}
	for _, queueName := range ingestQueueNames {
		log.Info("start ingest_queue_worker", "queue_name", queueName)
		q.StartIngestWorker(ctx, queueName)
	}

	log.Info("start ingest_queue_watcher")
	// NOTE(port): worker goroutines are self-supervising (see
	// queue.RedisWebhookQueue), so the Python health-check/restart loop is
	// replaced by a restart loop around the pubsub watcher only.
	for {
		if err := ingestQueueStarter(ctx, q, rdb); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("worker task failed", "kind", "ingest_queue_watcher", "error", err)
			time.Sleep(time.Second)
			continue
		}
		return nil
	}
}

// ingestQueueStarter listens on Redis Pubsub and starts a queue worker if we
// don't have one already.
func ingestQueueStarter(ctx context.Context, q *queue.RedisWebhookQueue, rdb *redis.Client) error {
	pubsub := rdb.Subscribe(ctx, queue.QueuePubsubIngest)
	defer pubsub.Close() //nolint:errcheck
	log := slog.With("task", "ingest_queue_starter")
	log.Info("start watch for ingest_queues")
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reply, ok := <-ch:
			if !ok {
				return context.Canceled
			}
			var message queue.PubsubIngestQueueMessage
			if err := json.Unmarshal([]byte(reply.Payload), &message); err != nil {
				log.Warn("failed to parse pubsub message", "error", err)
				continue
			}
			q.StartIngestWorker(ctx, queue.GetIngestQueue(message.InstallationID))
		}
	}
}
