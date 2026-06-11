// Package queue contains the Redis-based queueing for webhook events and the
// merge queue, ported from bot/kodiak/queue.py.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/pullrequest"
	"github.com/appscodelabs/kodiak/internal/redisclient"
	"github.com/appscodelabs/kodiak/internal/schemas"
)

const (
	IngestQueueNames  = "kodiak_ingest_queue_names"
	MergeQueueNames   = "kodiak_merge_queue_names:v2"
	WebhookQueueNames = "kodiak_webhook_queue_names"
	QueuePubsubIngest = "kodiak:pubsub:ingest"
)

func GetIngestQueue(installationID int64) string {
	return fmt.Sprintf("kodiak:ingest:%d", installationID)
}

// PubsubIngestQueueMessage corresponds to PubsubIngestQueueSchema in
// entrypoints/worker.py.
type PubsubIngestQueueMessage struct {
	InstallationID int64 `json:"installation_id"`
}

// InstallationIDFromQueue extracts the installation id from the queue names.
//
// On restart we only have queue names, so we need to extract installation ids
// from the names.
//
//	webhook:848733 -> 848733
//	merge_queue:848733.chdsbd/kodiak/master -> 848733
func InstallationIDFromQueue(queueName string) string {
	_, after, _ := strings.Cut(queueName, ":")
	id, _, _ := strings.Cut(after, ".")
	return id
}

// pyTime returns the current time as a Python time.time() float.
func pyTime() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// processWebhookEvent pops one event from a webhook queue and evaluates the
// pull request (merging=false).
func processWebhookEvent(ctx context.Context, rdb *redis.Client, webhookQueue *RedisWebhookQueue, queueName string, log *slog.Logger) error {
	log.Info("block for new webhook event")
	res, err := rdb.BZPopMin(ctx, appconf.RedisBlockingPopTimeout, queueName).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	log.Info("parsing webhook event")
	var webhookEvent WebhookEvent
	member, _ := res.Member.(string)
	if err := json.Unmarshal([]byte(member), &webhookEvent); err != nil {
		return err
	}
	targetValue, err := rdb.Get(ctx, webhookEvent.GetMergeTargetQueueName()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	isActiveMerging := err == nil && targetValue == webhookEvent.JSON()

	dequeue := func(ctx context.Context) error {
		return rdb.ZRem(ctx, webhookEvent.GetMergeQueueName(), webhookEvent.JSON()).Err()
	}
	requeue := func(ctx context.Context) error {
		return rdb.ZAddNX(ctx, webhookEvent.GetWebhookQueueName(),
			redis.Z{Score: pyTime(), Member: webhookEvent.JSON()}).Err()
	}
	queueForMerge := func(ctx context.Context, first bool) (*int, error) {
		return webhookQueue.EnqueueForRepo(ctx, webhookEvent, first)
	}

	log.Info("evaluate pr for webhook event")
	return pullrequest.EvaluatePR(
		ctx,
		webhookEvent.InstallationID,
		webhookEvent.RepoOwner,
		webhookEvent.RepoName,
		webhookEvent.PullRequestNumber,
		false, // merging
		dequeue,
		requeue,
		queueForMerge,
		isActiveMerging,
		log,
	)
}

// processRepoQueue pops one event from a repo merge queue and evaluates the
// pull request for merging (merging=true).
func processRepoQueue(ctx context.Context, rdb *redis.Client, queueName string, log *slog.Logger) error {
	log.Info("block for new repo event")
	res, err := rdb.BZPopMin(ctx, appconf.RedisBlockingPopTimeout, queueName).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	var webhookEvent WebhookEvent
	member, _ := res.Member.(string)
	if err := json.Unmarshal([]byte(member), &webhookEvent); err != nil {
		return err
	}
	targetName := webhookEvent.GetMergeTargetQueueName()
	// mark this PR as being merged currently. we check this elsewhere to set proper status codes
	if err := rdb.Set(ctx, targetName, webhookEvent.JSON(), 0).Err(); err != nil {
		return err
	}
	if err := rdb.Set(ctx, targetName+":time", strconv.FormatFloat(res.Score, 'f', -1, 64), 0).Err(); err != nil {
		return err
	}

	dequeue := func(ctx context.Context) error {
		return rdb.ZRem(ctx, webhookEvent.GetMergeQueueName(), webhookEvent.JSON()).Err()
	}
	requeue := func(ctx context.Context) error {
		return rdb.ZAddNX(ctx, webhookEvent.GetWebhookQueueName(),
			redis.Z{Score: pyTime(), Member: webhookEvent.JSON()}).Err()
	}
	queueForMerge := func(ctx context.Context, first bool) (*int, error) {
		return nil, errors.New("queue_for_merge not implemented for repo queue")
	}

	log.Info("evaluate PR for merging")
	if err := pullrequest.EvaluatePR(
		ctx,
		webhookEvent.InstallationID,
		webhookEvent.RepoOwner,
		webhookEvent.RepoName,
		webhookEvent.PullRequestNumber,
		true, // merging
		dequeue,
		requeue,
		queueForMerge,
		false, // isActiveMerging
		log,
	); err != nil {
		return err
	}
	log.Info("merge completed, remove target marker", "target_name", targetName)
	if err := rdb.Del(ctx, targetName).Err(); err != nil {
		return err
	}
	return rdb.Del(ctx, targetName+":time").Err()
}

// processIngestQueue pops one raw GitHub event from an installation's ingest
// queue and dispatches it to the webhook event handlers. Ported from
// work_ingest_queue in entrypoints/worker.py.
func processIngestQueue(ctx context.Context, rdb *redis.Client, queue WebhookQueue, queueName string, log *slog.Logger) error {
	res, err := rdb.BLPop(ctx, appconf.RedisBlockingPopTimeout, queueName).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	var parsedEvent schemas.RawWebhookEvent
	if err := json.Unmarshal([]byte(res[1]), &parsedEvent); err != nil {
		return err
	}
	handleCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := HandleWebhookEvent(handleCtx, queue, parsedEvent.EventName, parsedEvent.Payload); err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			log.Warn("handle_webhook_event timed out")
		} else {
			return err
		}
	}
	log.Info("ingest_event_handled")
	return nil
}

const (
	workerKindRepo    = "repo"
	workerKindWebhook = "webhook"
	workerKindIngest  = "ingest"
)

// RedisWebhookQueue manages the per-installation webhook queues, the per-repo
// merge queues, and the per-installation ingest queues. Each queue gets a
// worker goroutine.
type RedisWebhookQueue struct {
	rdb *redis.Client

	mu      sync.Mutex
	workers map[string]string
}

func NewRedisWebhookQueue() (*RedisWebhookQueue, error) {
	rdb, err := redisclient.Bot()
	if err != nil {
		return nil, err
	}
	return &RedisWebhookQueue{
		rdb:     rdb,
		workers: map[string]string{},
	}, nil
}

// Create restarts the workers for the queues registered in Redis.
func (q *RedisWebhookQueue) Create(ctx context.Context) error {
	// restart repo workers
	mergeQueues, err := q.rdb.SMembers(ctx, MergeQueueNames).Result()
	if err != nil {
		return err
	}
	webhookQueues, err := q.rdb.SMembers(ctx, WebhookQueueNames).Result()
	if err != nil {
		return err
	}
	for _, queueName := range mergeQueues {
		q.StartRepoWorker(ctx, queueName)
	}
	for _, queueName := range webhookQueues {
		q.StartWebhookWorker(ctx, queueName)
	}
	return nil
}

func (q *RedisWebhookQueue) StartWebhookWorker(ctx context.Context, queueName string) {
	q.startWorker(ctx, queueName, workerKindWebhook, func(ctx context.Context, log *slog.Logger) error {
		return processWebhookEvent(ctx, q.rdb, q, queueName, log)
	})
}

func (q *RedisWebhookQueue) StartRepoWorker(ctx context.Context, queueName string) {
	q.startWorker(ctx, queueName, workerKindRepo, func(ctx context.Context, log *slog.Logger) error {
		return processRepoQueue(ctx, q.rdb, queueName, log)
	})
}

// StartIngestWorker starts a worker for an installation's ingest queue.
func (q *RedisWebhookQueue) StartIngestWorker(ctx context.Context, queueName string) {
	q.startWorker(ctx, queueName, workerKindIngest, func(ctx context.Context, log *slog.Logger) error {
		return processIngestQueue(ctx, q.rdb, q, queueName, log)
	})
}

// startWorker launches a goroutine processing queueName, if one isn't running
// already.
//
// NOTE(port): the Python implementation restarts failed asyncio tasks from a
// supervision loop in entrypoints/worker.py. Here each worker goroutine is
// self-supervising: it recovers from panics, logs errors, and keeps
// processing, so it never needs to be restarted.
func (q *RedisWebhookQueue) startWorker(ctx context.Context, queueName string, kind string, process func(ctx context.Context, log *slog.Logger) error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.workers[queueName]; ok {
		return
	}
	q.workers[queueName] = kind
	log := slog.With("queue", queueName, "install", InstallationIDFromQueue(queueName), "kind", kind)
	log.Info("creating worker for queue")
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if err := runSafely(ctx, log, process); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Warn("worker task failed", "error", err)
				time.Sleep(time.Second)
			}
		}
	}()
}

func runSafely(ctx context.Context, log *slog.Logger, process func(ctx context.Context, log *slog.Logger) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("worker panic: %v", r)
		}
	}()
	return process(ctx, log)
}

// Enqueue adds the event to its installation's webhook queue.
func (q *RedisWebhookQueue) Enqueue(ctx context.Context, event WebhookEvent) error {
	queueName := GetWebhookQueueName(event)
	pipe := q.rdb.TxPipeline()
	pipe.SAdd(ctx, WebhookQueueNames, queueName)
	pipe.ZAddNX(ctx, queueName, redis.Z{Score: pyTime(), Member: event.JSON()})
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	slog.Info("enqueue webhook event",
		"owner", event.RepoOwner,
		"repo", event.RepoName,
		"number", event.PullRequestNumber,
		"install", event.InstallationID)
	q.StartWebhookWorker(ctx, queueName)
	return nil
}

// EnqueueForRepo enqueues the event for merging:
//
//  1. get the corresponding repo queue for event
//  2. add key to MERGE_QUEUE_NAMES so on restart we can recreate the
//     worker for the queue.
//  3. add event
//  4. start worker (will create new worker if one does not exist)
//
// It returns the position of the event in the queue.
func (q *RedisWebhookQueue) EnqueueForRepo(ctx context.Context, event WebhookEvent, first bool) (*int, error) {
	queueName := GetMergeQueueName(event)
	pipe := q.rdb.TxPipeline()
	mergeQueuesByInstall := "merge_queue_by_install:" + event.InstallationID
	pipe.SAdd(ctx, mergeQueuesByInstall, queueName)
	pipe.Expire(ctx, mergeQueuesByInstall, 24*time.Hour)
	if first {
		// place at front of queue. To allow us to always place this PR at
		// the front, we should not pass only_if_not_exists.
		pipe.ZAdd(ctx, queueName, redis.Z{Score: 1.0, Member: event.JSON()})
	} else {
		// use only_if_not_exists to prevent changing queue positions on new
		// webhook events.
		pipe.ZAddNX(ctx, queueName, redis.Z{Score: pyTime(), Member: event.JSON()})
	}
	zrangeCmd := pipe.ZRangeWithScores(ctx, queueName, 0, 1000)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	slog.Info("enqueue repo event",
		"owner", event.RepoOwner,
		"repo", event.RepoName,
		"number", event.PullRequestNumber,
		"install", event.InstallationID)
	q.StartRepoWorker(ctx, queueName)

	zrangeResults, err := zrangeCmd.Result()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(zrangeResults, func(i, j int) bool {
		return zrangeResults[i].Score < zrangeResults[j].Score
	})
	eventJSON := event.JSON()
	for index, z := range zrangeResults {
		if member, ok := z.Member.(string); ok && member == eventJSON {
			return &index, nil
		}
	}
	return nil, nil
}
