// Package eventhandlers maps raw GitHub webhook payloads onto Redis-queued
// WebhookEvents. Ported from bot/kodiak/event_handlers.py.
package eventhandlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/bot/appconfig"
	"github.com/appscodelabs/kodiak/internal/bot/events"
	"github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/bot/queries"
	"github.com/appscodelabs/kodiak/internal/bot/queue"
)

// CheckRunName matches queries.CHECK_RUN_NAME — needed so we don't loop on our
// own check_run updates.
const CheckRunName = queries.CheckRunName

// Dispatcher routes parsed webhook payloads into the Redis queue.
type Dispatcher struct {
	cfg   *appconfig.Config
	rdb   *redis.Client
	queue *queue.Queue
	api   queries.ClientFactory
}

// New constructs a Dispatcher.
func New(cfg *appconfig.Config, rdb *redis.Client, q *queue.Queue, api queries.ClientFactory) *Dispatcher {
	return &Dispatcher{cfg: cfg, rdb: rdb, queue: q, api: api}
}

// Handle dispatches a webhook event to its handler.
func (d *Dispatcher) Handle(ctx context.Context, eventName string, payload []byte) error {
	log := logging.FromContext(ctx).With().Str("event_name", eventName).Logger()
	ctx = logging.WithContext(ctx, log)

	if d.cfg.UsageReporting {
		if _, ok := d.cfg.UsageReportingEvents[eventName]; ok {
			if err := d.reportUsage(ctx, eventName, payload); err != nil {
				log.Warn().Err(err).Msg("usage reporting failed")
			}
		}
	}

	switch eventName {
	case "check_run":
		var ev events.CheckRunEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("decode check_run: %w", err)
		}
		return d.handleCheckRun(ctx, &ev)
	case "pull_request":
		var ev events.PullRequestEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("decode pull_request: %w", err)
		}
		return d.handlePullRequest(ctx, &ev)
	case "pull_request_review":
		var ev events.PullRequestReviewEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("decode pull_request_review: %w", err)
		}
		return d.handlePullRequestReview(ctx, &ev)
	case "push":
		var ev events.PushEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("decode push: %w", err)
		}
		return d.handlePush(ctx, &ev)
	case "status":
		var ev events.StatusEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return fmt.Errorf("decode status: %w", err)
		}
		return d.handleStatus(ctx, &ev)
	default:
		log.Info().Bool("event_parsed", false).Msg("webhook_event_handled")
		return nil
	}
}

func (d *Dispatcher) handlePullRequest(ctx context.Context, ev *events.PullRequestEvent) error {
	return d.queue.Enqueue(ctx, queue.WebhookEvent{
		RepoOwner:         ev.RepoOwner(),
		RepoName:          ev.RepoName(),
		PullRequestNumber: ev.Number,
		InstallationID:    strconv.FormatInt(ev.Installation.ID, 10),
	})
}

func (d *Dispatcher) handlePullRequestReview(ctx context.Context, ev *events.PullRequestReviewEvent) error {
	return d.queue.Enqueue(ctx, queue.WebhookEvent{
		RepoOwner:         ev.RepoOwner(),
		RepoName:          ev.RepoName(),
		PullRequestNumber: ev.PullRequest.Number,
		InstallationID:    strconv.FormatInt(ev.Installation.ID, 10),
	})
}

func (d *Dispatcher) handleCheckRun(ctx context.Context, ev *events.CheckRunEvent) error {
	// Prevent an infinite loop when we update our own check run.
	if ev.CheckRun.Name == CheckRunName {
		return nil
	}
	for _, num := range ev.PullRequestNumbers() {
		if err := d.queue.Enqueue(ctx, queue.WebhookEvent{
			RepoOwner:         ev.RepoOwner(),
			RepoName:          ev.RepoName(),
			PullRequestNumber: num,
			InstallationID:    strconv.FormatInt(ev.Installation.ID, 10),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) handleStatus(ctx context.Context, ev *events.StatusEvent) error {
	owner := ev.RepoOwner()
	repo := ev.RepoName()
	installID := strconv.FormatInt(ev.Installation.ID, 10)
	log := logging.FromContext(ctx).With().Str("owner", owner).Str("repo", repo).Str("install", installID).Logger()

	refs := branchNamesLatest(ev.SHA, ev.Branches)

	client, err := d.api.Client(ctx, owner, repo, installID)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}
	defer client.Close()

	seen := make(map[queue.WebhookEvent]struct{})
	if len(refs) == 0 {
		log.Warn().Msg("could not find refs for status_event")
		prs, err := client.GetOpenPullRequests(ctx, queries.OpenPullRequestsFilter{})
		if err != nil {
			return fmt.Errorf("list open prs: %w", err)
		}
		for _, pr := range prs {
			seen[queue.WebhookEvent{
				RepoOwner: owner, RepoName: repo,
				PullRequestNumber: pr.Number,
				InstallationID:    installID,
			}] = struct{}{}
		}
	} else {
		for _, ref := range refs {
			prs, err := client.GetOpenPullRequests(ctx, queries.OpenPullRequestsFilter{
				Head: owner + ":" + ref,
			})
			if err != nil {
				return fmt.Errorf("list open prs head=%s: %w", ref, err)
			}
			for _, pr := range prs {
				seen[queue.WebhookEvent{
					RepoOwner: owner, RepoName: repo,
					PullRequestNumber: pr.Number,
					InstallationID:    installID,
				}] = struct{}{}
			}
		}
	}

	for ev := range seen {
		if err := d.queue.Enqueue(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) handlePush(ctx context.Context, ev *events.PushEvent) error {
	owner := ev.RepoOwner()
	repo := ev.RepoName()
	installID := strconv.FormatInt(ev.Installation.ID, 10)
	branch := branchNameFromRef(ev.Ref)
	log := logging.FromContext(ctx).With().Str("ref", ev.Ref).Str("branch_name", branch).Logger()
	if branch == "" {
		log.Info().Msg("could not extract branch name from ref")
		return nil
	}
	client, err := d.api.Client(ctx, owner, repo, installID)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}
	defer client.Close()

	prs, err := client.GetOpenPullRequests(ctx, queries.OpenPullRequestsFilter{Base: branch})
	if err != nil {
		log.Info().Err(err).Msg("api call to find pull requests failed")
		return nil
	}
	for _, pr := range prs {
		if err := d.queue.Enqueue(ctx, queue.WebhookEvent{
			RepoOwner:         owner,
			RepoName:          repo,
			PullRequestNumber: pr.Number,
			InstallationID:    installID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) reportUsage(ctx context.Context, eventName string, payload []byte) error {
	envelope, err := json.Marshal(map[string]any{
		"event_name": eventName,
		"payload":    json.RawMessage(payload),
	})
	if err != nil {
		return err
	}
	pipe := d.rdb.TxPipeline()
	pipe.RPush(ctx, "kodiak:webhook_event", envelope)
	pipe.LTrim(ctx, "kodiak:webhook_event", 0, int64(d.cfg.UsageReportingQueueLength))
	_, err = pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

func branchNamesLatest(sha string, branches []events.Branch) []string {
	out := make([]string, 0, len(branches))
	for _, b := range branches {
		if b.Commit.SHA == sha {
			out = append(out, b.Name)
		}
	}
	return out
}

func branchNameFromRef(ref string) string {
	const prefix = "refs/heads/"
	if strings.HasPrefix(ref, prefix) {
		return strings.TrimPrefix(ref, prefix)
	}
	return ""
}
