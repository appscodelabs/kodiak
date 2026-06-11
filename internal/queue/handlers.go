package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/events"
	"github.com/appscodelabs/kodiak/internal/queries"
	"github.com/appscodelabs/kodiak/internal/redisclient"
)

// usageReportingQueueName is the Redis list consumed by the web API ingest
// job.
const usageReportingQueueName = "kodiak:webhook_event"

// WebhookQueue corresponds to WebhookQueueProtocol in Python.
type WebhookQueue interface {
	Enqueue(ctx context.Context, event WebhookEvent) error
	EnqueueForRepo(ctx context.Context, event WebhookEvent, first bool) (*int, error)
}

// prEvent triggers evaluation of a modified PR.
func prEvent(ctx context.Context, queue WebhookQueue, pr *events.PullRequestEvent) error {
	return queue.Enqueue(ctx, WebhookEvent{
		RepoOwner:         pr.Repository.Owner.Login,
		RepoName:          pr.Repository.Name,
		PullRequestNumber: pr.Number,
		TargetName:        pr.PullRequest.Base.Ref,
		InstallationID:    strconv.FormatInt(pr.Installation.ID, 10),
	})
}

// checkRun returns the events for all PRs included in a check run.
func checkRun(checkRunEvent *events.CheckRunEvent) []WebhookEvent {
	// Prevent an infinite loop when we update our check run
	if checkRunEvent.CheckRun.Name == queries.CheckRunName {
		return nil
	}
	var out []WebhookEvent
	for _, pr := range checkRunEvent.CheckRun.PullRequests {
		// filter out pull requests for other repositories
		if pr.Base.Repo == nil || pr.Base.Repo.ID != checkRunEvent.Repository.ID {
			continue
		}
		out = append(out, WebhookEvent{
			RepoOwner:         checkRunEvent.Repository.Owner.Login,
			RepoName:          checkRunEvent.Repository.Name,
			PullRequestNumber: pr.Number,
			TargetName:        pr.Base.Ref,
			InstallationID:    strconv.FormatInt(checkRunEvent.Installation.ID, 10),
		})
	}
	return out
}

// findBranchNamesLatest filters status event branches to those whose head is
// the given commit.
//
// from the docs:
//
//	The "branches" key is "an array of branch objects containing the status'
//	SHA. Each branch contains the given SHA, but the SHA may or may not be
//	the head of the branch. The array includes a maximum of 10 branches."
//
// https://developer.github.com/v3/activity/events/types/#statusevent
//
// NOTE(chdsbd): only take branches with commit at branch head to reduce
// potential number of api requests we need to make.
func findBranchNamesLatest(sha string, branches []events.Branch) []string {
	var names []string
	for _, branch := range branches {
		if branch.Commit.SHA != nil && *branch.Commit.SHA == sha {
			names = append(names, branch.Name)
		}
	}
	return names
}

// statusEvent triggers evaluation of all PRs associated with the status event
// commit SHA.
func statusEvent(ctx context.Context, queue WebhookQueue, ev *events.StatusEvent) error {
	owner := ev.Repository.Owner.Login
	repo := ev.Repository.Name
	installationID := strconv.FormatInt(ev.Installation.ID, 10)
	log := slog.With("owner", owner, "repo", repo, "install", installationID)

	refs := findBranchNamesLatest(ev.SHA, ev.Branches)

	apiClient := queries.NewClient(owner, repo, installationID)
	var prResults [][]queries.GetOpenPullRequestsResponse
	if len(refs) == 0 {
		// when a pull request is from a fork the status event will not have
		// any `branches`, so to be able to trigger evaluation of the PR, we
		// fetch all pull requests.
		//
		// I think we could optimize this by selecting only the fork PRs, but
		// I worry that we might miss some events where `branches` is empty,
		// but not because of a fork.
		prs, err := apiClient.GetOpenPullRequests(ctx, "", "")
		if err != nil {
			prs = nil
		}
		prResults = append(prResults, prs)
		log.Info("could not find refs for status_event")
	} else {
		for _, ref := range refs {
			prs, err := apiClient.GetOpenPullRequests(ctx, "", fmt.Sprintf("%s:%s", owner, ref))
			if err != nil {
				prs = nil
			}
			prResults = append(prResults, prs)
		}
	}

	allEvents := map[WebhookEvent]struct{}{}
	for _, prs := range prResults {
		for _, pr := range prs {
			allEvents[WebhookEvent{
				RepoOwner:         owner,
				RepoName:          repo,
				PullRequestNumber: pr.Number,
				TargetName:        pr.Base.Ref,
				InstallationID:    installationID,
			}] = struct{}{}
		}
	}
	for event := range allEvents {
		if err := queue.Enqueue(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// prReviewEvent is the shared shape of PullRequestReviewEvent and
// PullRequestReviewThreadEvent.
type prReviewEvent struct {
	repository   events.Repository
	pullRequest  events.PullRequest
	installation events.Installation
}

// prReview triggers evaluation of the modified PR.
func prReview(ctx context.Context, queue WebhookQueue, review prReviewEvent) error {
	return queue.Enqueue(ctx, WebhookEvent{
		RepoOwner:         review.repository.Owner.Login,
		RepoName:          review.repository.Name,
		PullRequestNumber: review.pullRequest.Number,
		TargetName:        review.pullRequest.Base.Ref,
		InstallationID:    strconv.FormatInt(review.installation.ID, 10),
	})
}

// getBranchName extracts the branch name from the ref.
func getBranchName(rawRef string) (string, bool) {
	return strings.CutPrefix(rawRef, "refs/heads/")
}

// push triggers evaluation of PRs that depend on the pushed branch.
func push(ctx context.Context, queue WebhookQueue, pushEvent *events.PushEvent) error {
	owner := pushEvent.Repository.Owner.Login
	repo := pushEvent.Repository.Name
	installationID := strconv.FormatInt(pushEvent.Installation.ID, 10)
	log := slog.With("ref", pushEvent.Ref)
	branchName, ok := getBranchName(pushEvent.Ref)
	if !ok {
		log.Info("could not extract branch name from ref")
		return nil
	}
	apiClient := queries.NewClient(owner, repo, installationID)
	// find all the PRs that depend on the branch affected by this push and
	// queue them for evaluation.
	// Any PR that has a base ref matching our event ref is dependent.
	prs, err := apiClient.GetOpenPullRequests(ctx, branchName, "")
	if err != nil {
		log.Info("api call to find pull requests failed", "error", err)
		return nil
	}
	for _, pr := range prs {
		if err := queue.Enqueue(ctx, WebhookEvent{
			RepoOwner:         owner,
			RepoName:          repo,
			PullRequestNumber: pr.Number,
			TargetName:        pr.Base.Ref,
			InstallationID:    installationID,
		}); err != nil {
			return err
		}
	}
	return nil
}

var (
	zstdEncoderOnce sync.Once
	zstdEncoder     *zstd.Encoder
	zstdEncoderErr  error
)

func compressPayload(data []byte) ([]byte, error) {
	zstdEncoderOnce.Do(func() {
		zstdEncoder, zstdEncoderErr = zstd.NewWriter(nil)
	})
	if zstdEncoderErr != nil {
		return nil, zstdEncoderErr
	}
	return zstdEncoder.EncodeAll(data, nil), nil
}

// HandleWebhookEvent dispatches a webhook event to the correct handler and
// reports usage if configured. Ported from kodiak.queue.handle_webhook_event.
func HandleWebhookEvent(ctx context.Context, queue WebhookQueue, eventName string, payload json.RawMessage) error {
	log := slog.With("event_name", eventName)

	if appconf.UsageReporting && appconf.UsageReportingEvents[eventName] {
		// store events in Redis for dequeue by web api job.
		//
		// We limit the queue length to ensure that if the dequeue job fails, we
		// won't overload Redis.
		redisWebAPI, err := redisclient.WebAPI()
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(map[string]json.RawMessage{
			"event_name": json.RawMessage(strconv.Quote(eventName)),
			"payload":    payload,
		})
		if err != nil {
			return err
		}
		compressed, err := compressPayload(encoded)
		if err != nil {
			return err
		}
		if err := redisWebAPI.RPush(ctx, usageReportingQueueName, compressed).Err(); err != nil {
			return err
		}
		if err := redisWebAPI.LTrim(ctx, usageReportingQueueName, 0, int64(appconf.UsageReportingQueueLength)).Err(); err != nil {
			return err
		}
		log = log.With("usage_reported", true)
	}

	switch eventName {
	case "check_run":
		var ev events.CheckRunEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		for _, event := range checkRun(&ev) {
			if err := queue.Enqueue(ctx, event); err != nil {
				return err
			}
		}
	case "pull_request":
		var ev events.PullRequestEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if err := prEvent(ctx, queue, &ev); err != nil {
			return err
		}
	case "pull_request_review":
		var ev events.PullRequestReviewEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if err := prReview(ctx, queue, prReviewEvent{ev.Repository, ev.PullRequest, ev.Installation}); err != nil {
			return err
		}
	case "pull_request_review_thread":
		var ev events.PullRequestReviewThreadEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if err := prReview(ctx, queue, prReviewEvent{ev.Repository, ev.PullRequest, ev.Installation}); err != nil {
			return err
		}
	case "push":
		var ev events.PushEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if err := push(ctx, queue, &ev); err != nil {
			return err
		}
	case "status":
		var ev events.StatusEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return err
		}
		if err := statusEvent(ctx, queue, &ev); err != nil {
			return err
		}
	default:
		log = log.With("event_parsed", false)
	}

	log.Info("webhook_event_handled")
	return nil
}
