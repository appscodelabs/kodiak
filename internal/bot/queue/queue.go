// Package queue implements the per-installation webhook queue and per-repo
// merge queue, both backed by Redis sorted sets.
//
// Ported from bot/kodiak/queue.py.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/appscodelabs/kodiak/internal/bot/logging"
)

const (
	mergeQueueNames   = "kodiak_merge_queue_names"
	webhookQueueNames = "kodiak_webhook_queue_names"

	// retryRate matches RETRY_RATE_SECONDS in the Python source.
	retryRate = 2 * time.Second
)

// WebhookEvent is the payload kept on every Redis queue entry.
type WebhookEvent struct {
	RepoOwner         string `json:"repo_owner"`
	RepoName          string `json:"repo_name"`
	PullRequestNumber int    `json:"pull_request_number"`
	InstallationID    string `json:"installation_id"`
}

// MergeQueueName returns the per-repo merge queue Redis key.
func (e WebhookEvent) MergeQueueName() string {
	return fmt.Sprintf("merge_queue:%s.%s/%s", e.InstallationID, e.RepoOwner, e.RepoName)
}

// MergeTargetQueueName returns the active-merge marker key.
func (e WebhookEvent) MergeTargetQueueName() string {
	return e.MergeQueueName() + ":target"
}

// WebhookQueueName returns the per-installation webhook queue Redis key.
func (e WebhookEvent) WebhookQueueName() string {
	return fmt.Sprintf("webhook:%s", e.InstallationID)
}

// JSON returns the canonical encoded form used as the Redis member value.
func (e WebhookEvent) JSON() string {
	b, _ := json.Marshal(e)
	return string(b)
}

// Evaluator is the callback used by the queue to evaluate a pull request once
// it has been dequeued. The bot's pull_request package supplies the real
// implementation; the queue package stays decoupled from evaluation.
type Evaluator interface {
	EvaluatePR(ctx context.Context, in EvaluateInput) error
}

// EvaluateInput is the input to a single PR evaluation pass.
type EvaluateInput struct {
	Event             WebhookEvent
	Merging           bool
	IsActiveMerging   bool
	Dequeue           func(context.Context) error
	Requeue           func(context.Context) error
	QueueForMerge     func(context.Context) (int, error)
}

// Queue is the Redis-backed event queue.
type Queue struct {
	rdb       *redis.Client
	evaluator Evaluator
	log       zerolog.Logger

	mu      sync.Mutex
	workers map[string]context.CancelFunc
}

// New constructs a new Queue. The caller is responsible for calling Start
// before enqueueing events.
func New(rdb *redis.Client, evaluator Evaluator, log zerolog.Logger) *Queue {
	return &Queue{
		rdb:       rdb,
		evaluator: evaluator,
		log:       log,
		workers:   make(map[string]context.CancelFunc),
	}
}

// Start restores all webhook and merge workers that were running before the
// process restarted by reading the set of known queue names from Redis.
func (q *Queue) Start(ctx context.Context) error {
	mergeNames, err := q.rdb.SMembers(ctx, mergeQueueNames).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("smembers merge_queue_names: %w", err)
	}
	webhookNames, err := q.rdb.SMembers(ctx, webhookQueueNames).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("smembers webhook_queue_names: %w", err)
	}
	for _, name := range mergeNames {
		q.startRepoWorker(ctx, name)
	}
	for _, name := range webhookNames {
		q.startWebhookWorker(ctx, name)
	}
	return nil
}

// Enqueue adds an event to its installation's webhook queue and ensures the
// worker for that queue is running.
func (q *Queue) Enqueue(ctx context.Context, e WebhookEvent) error {
	name := e.WebhookQueueName()
	score := float64(time.Now().UnixNano()) / float64(time.Second)
	member := e.JSON()
	pipe := q.rdb.TxPipeline()
	pipe.SAdd(ctx, webhookQueueNames, name)
	pipe.ZAddNX(ctx, name, redis.Z{Score: score, Member: member})
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("enqueue webhook event: %w", err)
	}
	log := logging.FromContext(ctx)
	log.Info().
		Str("owner", e.RepoOwner).
		Str("repo", e.RepoName).
		Int("number", e.PullRequestNumber).
		Str("install", e.InstallationID).
		Msg("enqueue webhook event")
	q.startWebhookWorker(ctx, name)
	return nil
}

// EnqueueForRepo adds an event to its repo's merge queue, returning the
// 0-based position the event occupies.
func (q *Queue) EnqueueForRepo(ctx context.Context, e WebhookEvent) (int, error) {
	name := e.MergeQueueName()
	score := float64(time.Now().UnixNano()) / float64(time.Second)
	member := e.JSON()
	pipe := q.rdb.TxPipeline()
	pipe.SAdd(ctx, mergeQueueNames, name)
	pipe.ZAddNX(ctx, name, redis.Z{Score: score, Member: member})
	rangeCmd := pipe.ZRangeWithScores(ctx, name, 0, 1000)
	if _, err := pipe.Exec(ctx); err != nil {
		return -1, fmt.Errorf("enqueue repo event: %w", err)
	}
	log := logging.FromContext(ctx)
	log.Info().
		Str("owner", e.RepoOwner).
		Str("repo", e.RepoName).
		Int("number", e.PullRequestNumber).
		Str("install", e.InstallationID).
		Msg("enqueue repo event")
	q.startRepoWorker(ctx, name)

	entries := rangeCmd.Val()
	for i, z := range entries {
		if s, ok := z.Member.(string); ok && s == member {
			return i, nil
		}
	}
	return -1, nil
}

func (q *Queue) startWebhookWorker(ctx context.Context, name string) {
	q.startWorker(ctx, "webhook:"+name, func(workerCtx context.Context) {
		q.runWebhookConsumer(workerCtx, name)
	})
}

func (q *Queue) startRepoWorker(ctx context.Context, name string) {
	q.startWorker(ctx, "merge:"+name, func(workerCtx context.Context) {
		q.runRepoConsumer(workerCtx, name)
	})
}

func (q *Queue) startWorker(parent context.Context, key string, fn func(context.Context)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.workers[key]; ok {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	q.workers[key] = cancel
	go func() {
		defer func() {
			q.mu.Lock()
			delete(q.workers, key)
			q.mu.Unlock()
		}()
		fn(ctx)
	}()
}

func (q *Queue) runWebhookConsumer(ctx context.Context, queueName string) {
	log := q.log.With().Str("queue", queueName).Logger()
	log.Info().Msg("start webhook event consumer")
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := q.processWebhookEvent(ctx, queueName, log); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Err(err).Msg("process webhook event")
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryRate):
			}
		}
	}
}

func (q *Queue) runRepoConsumer(ctx context.Context, queueName string) {
	log := q.log.With().Str("queue", queueName).Logger()
	log.Info().Msg("start repo consumer")
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := q.processRepoEvent(ctx, queueName, log); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Err(err).Msg("process repo event")
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryRate):
			}
		}
	}
}

func (q *Queue) processWebhookEvent(ctx context.Context, queueName string, log zerolog.Logger) error {
	log.Info().Msg("block for new webhook event")
	res, err := q.rdb.BZPopMin(ctx, 0, queueName).Result()
	if err != nil {
		return fmt.Errorf("bzpopmin: %w", err)
	}
	memberStr, ok := res.Member.(string)
	if !ok {
		return fmt.Errorf("unexpected bzpopmin member type %T", res.Member)
	}
	var ev WebhookEvent
	if err := json.Unmarshal([]byte(memberStr), &ev); err != nil {
		return fmt.Errorf("parse webhook event: %w", err)
	}

	target, err := q.rdb.Get(ctx, ev.MergeTargetQueueName()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("get merge target: %w", err)
	}
	isActive := target == memberStr

	in := EvaluateInput{
		Event:           ev,
		Merging:         false,
		IsActiveMerging: isActive,
		Dequeue: func(c context.Context) error {
			return q.rdb.ZRem(c, ev.MergeQueueName(), memberStr).Err()
		},
		Requeue: func(c context.Context) error {
			return q.rdb.ZAddNX(c, ev.WebhookQueueName(), redis.Z{
				Score:  float64(time.Now().UnixNano()) / float64(time.Second),
				Member: memberStr,
			}).Err()
		},
		QueueForMerge: func(c context.Context) (int, error) {
			return q.EnqueueForRepo(c, ev)
		},
	}
	if q.evaluator != nil {
		log.Info().Msg("evaluate pr for webhook event")
		return q.evaluator.EvaluatePR(ctx, in)
	}
	return nil
}

func (q *Queue) processRepoEvent(ctx context.Context, queueName string, log zerolog.Logger) error {
	log.Info().Msg("block for new repo event")
	res, err := q.rdb.BZPopMin(ctx, 0, queueName).Result()
	if err != nil {
		return fmt.Errorf("bzpopmin: %w", err)
	}
	memberStr, ok := res.Member.(string)
	if !ok {
		return fmt.Errorf("unexpected bzpopmin member type %T", res.Member)
	}
	var ev WebhookEvent
	if err := json.Unmarshal([]byte(memberStr), &ev); err != nil {
		return fmt.Errorf("parse merge event: %w", err)
	}
	if err := q.rdb.Set(ctx, ev.MergeTargetQueueName(), memberStr, 0).Err(); err != nil {
		return fmt.Errorf("set merge target: %w", err)
	}

	in := EvaluateInput{
		Event:           ev,
		Merging:         true,
		IsActiveMerging: false,
		Dequeue: func(c context.Context) error {
			return q.rdb.ZRem(c, ev.MergeQueueName(), memberStr).Err()
		},
		Requeue: func(c context.Context) error {
			return q.rdb.ZAddNX(c, ev.WebhookQueueName(), redis.Z{
				Score:  float64(time.Now().UnixNano()) / float64(time.Second),
				Member: memberStr,
			}).Err()
		},
		QueueForMerge: func(c context.Context) (int, error) {
			return 0, errors.New("queue_for_merge not implemented from repo consumer")
		},
	}
	if q.evaluator != nil {
		log.Info().Msg("evaluate pr for merging")
		return q.evaluator.EvaluatePR(ctx, in)
	}
	return nil
}

// MustDB returns a non-nil DB index parsed from the Redis URL path, or zero.
func MustDB(path string) int {
	if path == "" || path == "/" {
		return 0
	}
	if path[0] == '/' {
		path = path[1:]
	}
	n, err := strconv.Atoi(path)
	if err != nil {
		return 0
	}
	return n
}
