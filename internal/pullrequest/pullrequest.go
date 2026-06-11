// Package pullrequest contains the PRV2 implementation of the evaluation
// PRAPI and the evaluate_pr retry loop, ported from bot/kodiak/pull_request.py.
package pullrequest

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/evaluation"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/messages"
	"github.com/appscodelabs/kodiak/internal/queries"
)

const (
	retryRate          = 2 * time.Second
	pollRate           = 3 * time.Second
	evaluationTimeout  = 60 * time.Second
	queryClientTimeout = 60 * time.Second
)

// Callback corresponds to the dequeue/requeue callbacks passed by the queue
// worker.
type Callback func(ctx context.Context) error

// QueueForMergeCallback corresponds to QueueForMergeCallback in Python.
type QueueForMergeCallback func(ctx context.Context, first bool) (*int, error)

// apiCallError corresponds to the APICallError dataclass and implements
// messages.APICallRetry.
type apiCallError struct {
	apiName      string
	httpStatus   string
	responseBody string
}

func (e apiCallError) APIName() string      { return e.apiName }
func (e apiCallError) HTTPStatus() string   { return e.httpStatus }
func (e apiCallError) ResponseBody() string { return e.responseBody }

// getPR fetches the event info necessary to evaluate a pull request.
func getPR(
	ctx context.Context,
	install string,
	owner string,
	repo string,
	number int,
	dequeueCallback Callback,
	requeueCallback Callback,
	queueForMergeCallback QueueForMergeCallback,
) (*PRV2, error) {
	log := slog.With("install", install, "owner", owner, "repo", repo, "number", number)
	client := queries.NewClient(owner, repo, install)
	event, err := client.GetEventInfo(ctx, number)
	if err != nil {
		// NOTE(port): the Python client returns None for "expected" failures
		// and raises for transport errors; the Go client returns an error for
		// both. Timeouts are surfaced to the caller, everything else is
		// treated like a missing event.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		log.Info("failed to find event", "error", err)
		return nil, nil
	}
	if event == nil {
		log.Info("failed to find event")
		return nil, nil
	}
	return NewPRV2(event, install, owner, repo, number, dequeueCallback, requeueCallback, queueForMergeCallback), nil
}

// EvaluatePR evaluates a pull request, retrying for skippable checks,
// poll-forever requests, and temporary API errors. Ported from
// kodiak.pull_request.evaluate_pr.
func EvaluatePR(
	ctx context.Context,
	install string,
	owner string,
	repo string,
	number int,
	merging bool,
	dequeueCallback Callback,
	requeueCallback Callback,
	queueForMergeCallback QueueForMergeCallback,
	isActiveMerging bool,
	log *slog.Logger,
) error {
	skippableCheckTimeout := 4
	apiCallRetriesRemaining := 5
	var apiCallErrors []messages.APICallRetry
	log = log.With("owner", owner, "repo", repo, "number", number, "merging", merging)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Info("get_pr")
		pr, err := func() (*PRV2, error) {
			getCtx, cancel := context.WithTimeout(ctx, queryClientTimeout)
			defer cancel()
			return getPR(getCtx, install, owner, repo, number, dequeueCallback, requeueCallback, queueForMergeCallback)
		}()
		if err != nil {
			if isTimeout(ctx, err) {
				// On timeout we add the PR to the back of the queue to try again.
				log.Warn("mergeable_timeout", "error", err)
				if requeueErr := requeueCallback(ctx); requeueErr != nil {
					return requeueErr
				}
				continue
			}
			return err
		}
		var evalErr error
		if pr == nil {
			log.Info("failed to get_pr")
			if !merging {
				return nil
			}
			evalErr = &kerrors.APICallError{Method: "kodiak/get_pr", StatusCode: 0, Response: nil}
		} else {
			evalErr = func() error {
				evalCtx, cancel := context.WithTimeout(ctx, evaluationTimeout)
				defer cancel()
				return evaluation.Mergeable(evalCtx, pr, evaluation.MergeableParams{
					Subscription:            pr.Event.Subscription,
					Config:                  pr.Event.Config,
					ConfigError:             pr.Event.ConfigError,
					ConfigStr:               pr.Event.ConfigStr,
					ConfigPath:              pr.Event.ConfigFileExpression,
					AppID:                   appconf.GithubAppID,
					Repository:              pr.Event.Repository,
					PullRequest:             pr.Event.PullRequest,
					BranchProtection:        pr.Event.BranchProtection,
					RulesetRules:            pr.Event.RulesetRules,
					ReviewRequests:          pr.Event.ReviewRequests,
					BotReviews:              pr.Event.BotReviews,
					Contexts:                pr.Event.StatusContexts,
					CheckRuns:               pr.Event.CheckRuns,
					Commits:                 pr.Event.Commits,
					ValidMergeMethods:       pr.Event.ValidMergeMethods,
					Merging:                 merging,
					IsActiveMerge:           isActiveMerging,
					SkippableCheckTimeout:   skippableCheckTimeout,
					APICallErrors:           apiCallErrors,
					APICallRetriesRemaining: apiCallRetriesRemaining,
				})
			}()
		}
		if evalErr == nil {
			log.Info("evaluate_pr successful")
			return nil
		}
		if errors.Is(evalErr, kerrors.ErrRetryForSkippableChecks) {
			if skippableCheckTimeout > 0 {
				skippableCheckTimeout--
				log.Info("waiting for skippable checks to pass")
				if err := sleep(ctx, retryRate); err != nil {
					return err
				}
				continue
			}
			return nil
		}
		if errors.Is(evalErr, kerrors.ErrPollForever) {
			log.Info("polling")
			if err := sleep(ctx, pollRate); err != nil {
				return err
			}
			continue
		}
		var apiErr *kerrors.APICallError
		if errors.As(evalErr, &apiErr) {
			// if we have some api exception, it's likely a temporary error that
			// can be resolved by calling GitHub again.
			if apiCallRetriesRemaining > 0 {
				apiCallErrors = append(apiCallErrors, apiCallError{
					apiName:      apiErr.Method,
					httpStatus:   strconv.Itoa(apiErr.StatusCode),
					responseBody: string(apiErr.Response),
				})
				apiCallRetriesRemaining--
				log.Info("problem contacting remote api. retrying")
				continue
			}
			log.Warn("api_call_retries_remaining", "error", evalErr)
			return nil
		}
		if isTimeout(ctx, evalErr) {
			// On timeout we add the PR to the back of the queue to try again.
			log.Warn("mergeable_timeout", "error", evalErr)
			if requeueErr := requeueCallback(ctx); requeueErr != nil {
				return requeueErr
			}
			continue
		}
		return evalErr
	}
}

// isTimeout reports whether err is the 60 second evaluation timeout rather
// than cancellation of the parent context.
func isTimeout(parent context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) && parent.Err() == nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// githubClient is the subset of *queries.Client used by PRV2.
//
// NOTE(port): introduced as a test seam; the Python tests inject a fake
// kodiak.queries.Client class into PRV2.
type githubClient interface {
	CreateNotification(ctx context.Context, headSHA string, message string, summary *string) (*queries.Response, error)
	GetOpenPullRequests(ctx context.Context, base string, head string) ([]queries.GetOpenPullRequestsResponse, error)
	DeleteBranch(ctx context.Context, branch string) (*queries.Response, error)
	UpdateBranch(ctx context.Context, pullNumber int) (*queries.Response, error)
	ApprovePullRequest(ctx context.Context, pullNumber int) (*queries.Response, error)
	GetPullRequest(ctx context.Context, number int) (*queries.Response, error)
	MergePullRequest(ctx context.Context, number int, mergeMethod string, commitTitle *string, commitMessage *string) (*queries.Response, error)
	UpdateRef(ctx context.Context, ref string, sha string) (*queries.Response, error)
	AddLabel(ctx context.Context, label string, pullNumber int) (*queries.Response, error)
	DeleteLabel(ctx context.Context, label string, pullNumber int) (*queries.Response, error)
	CreateComment(ctx context.Context, body string, pullNumber int) (*queries.Response, error)
}

// PRV2 is the representation of a PR for Kodiak.
//
// This struct implements the PRAPI interface found in internal/evaluation.
type PRV2 struct {
	Event *queries.EventInfoResponse

	install string
	owner   string
	repo    string
	number  int

	dequeueCallback       Callback
	requeueCallback       Callback
	queueForMergeCallback QueueForMergeCallback

	client githubClient
	log    *slog.Logger
}

var _ evaluation.PRAPI = (*PRV2)(nil)

func NewPRV2(
	event *queries.EventInfoResponse,
	install string,
	owner string,
	repo string,
	number int,
	dequeueCallback Callback,
	requeueCallback Callback,
	queueForMergeCallback QueueForMergeCallback,
) *PRV2 {
	return &PRV2{
		Event:                 event,
		install:               install,
		owner:                 owner,
		repo:                  repo,
		number:                number,
		dequeueCallback:       dequeueCallback,
		requeueCallback:       requeueCallback,
		queueForMergeCallback: queueForMergeCallback,
		client:                queries.NewClient(owner, repo, install),
		log:                   slog.With("install", install, "owner", owner, "repo", repo, "number", number),
	}
}

func (p *PRV2) Dequeue(ctx context.Context) error {
	p.log.Info("dequeue")
	return p.dequeueCallback(ctx)
}

func (p *PRV2) Requeue(ctx context.Context) error {
	p.log.Info("requeue")
	return p.requeueCallback(ctx)
}

// SetStatus displays a message to a user through a github check.
//
// markdownContent is the message displayed on the detail view for a status
// check. This detail view is accessible via the "Details" link alongside the
// summary/detail content.
func (p *PRV2) SetStatus(ctx context.Context, msg string, markdownContent *string) error {
	p.log.Info("set_status", "message", msg)
	res, err := p.client.CreateNotification(ctx, p.Event.PullRequest.LatestSha, msg, markdownContent)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to create notification", "status_code", res.StatusCode, "res", string(res.Body))
	}
	return nil
}

func (p *PRV2) PullRequestsForRef(ctx context.Context, ref string) (*int, error) {
	log := p.log.With("ref", ref)
	log.Info("pull_requests_for_ref")
	prs, err := p.client.GetOpenPullRequests(ctx, ref, "")
	if err != nil {
		// our api request failed.
		log.Info("failed to get pull request info for ref", "error", err)
		return nil, nil
	}
	count := len(prs)
	return &count, nil
}

func (p *PRV2) DeleteBranch(ctx context.Context, branchName string) error {
	p.log.Info("delete_branch", "branch_name", branchName)
	res, err := p.client.DeleteBranch(ctx, branchName)
	if err != nil {
		return err
	}
	if res.IsError() {
		if res.StatusCode == 422 {
			p.log.Info("branch already deleted, nothing to do", "res", string(res.Body))
		} else {
			p.log.Warn("failed to delete branch", "status_code", res.StatusCode, "res", string(res.Body))
		}
	}
	return nil
}

func (p *PRV2) UpdateBranch(ctx context.Context) error {
	p.log.Info("update_branch")
	res, err := p.client.UpdateBranch(ctx, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to update branch", "status_code", res.StatusCode, "res", string(res.Body))
		// we raise an exception to retry this request.
		return &kerrors.APICallError{
			Method:     "pull_request/update_branch",
			StatusCode: res.StatusCode,
			Response:   res.Body,
		}
	}
	return nil
}

func (p *PRV2) ApprovePullRequest(ctx context.Context) error {
	p.log.Info("approve_pull_request")
	res, err := p.client.ApprovePullRequest(ctx, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to approve pull request", "status_code", res.StatusCode, "res", string(res.Body))
	}
	return nil
}

func (p *PRV2) TriggerTestCommit(ctx context.Context) error {
	p.log.Info("trigger_test_commit")
	res, err := p.client.GetPullRequest(ctx, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to get pull request for test commit trigger",
			"status_code", res.StatusCode, "res", string(res.Body))
	}
	return nil
}

func (p *PRV2) Merge(ctx context.Context, mergeMethod string, commitTitle *string, commitMessage *string) error {
	p.log.Info("merge", "method", mergeMethod)
	res, err := p.client.MergePullRequest(ctx, p.number, mergeMethod, commitTitle, commitMessage)
	if err != nil {
		return err
	}
	if res.IsError() {
		if res.StatusCode == 405 {
			p.log.Info("branch is not mergeable. PR likely already merged.", "res", string(res.Body))
		} else {
			p.log.Warn("failed to merge pull request", "status_code", res.StatusCode, "res", string(res.Body))
		}
		if res.StatusCode == 500 {
			return kerrors.ErrGitHubAPIInternalServerError
		}
		// we raise an exception to retry this request.
		return &kerrors.APICallError{
			Method:     "pull_request/merge",
			StatusCode: res.StatusCode,
			Response:   res.Body,
		}
	}
	return nil
}

func (p *PRV2) UpdateRef(ctx context.Context, ref string, sha string) error {
	p.log.Info("update_ref", "ref", ref, "sha", sha)
	res, err := p.client.UpdateRef(ctx, ref, sha)
	if err != nil {
		return err
	}
	if res.IsError() {
		if res.StatusCode == 422 {
			p.log.Info("fast forward update not possible.", "res", string(res.Body))
		} else {
			p.log.Warn("failed to update ref", "status_code", res.StatusCode, "res", string(res.Body))
		}
		// we raise an exception to retry this request.
		return &kerrors.APICallError{
			Method:     "pull_request/update_ref",
			StatusCode: res.StatusCode,
			Response:   res.Body,
		}
	}
	return nil
}

func (p *PRV2) QueueForMerge(ctx context.Context, first bool) (*int, error) {
	p.log.Info("queue_for_merge")
	return p.queueForMergeCallback(ctx, first)
}

// AddLabel adds a label to the pull request.
func (p *PRV2) AddLabel(ctx context.Context, label string) error {
	p.log.Info("add_label", "label", label)
	res, err := p.client.AddLabel(ctx, label, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to add label", "label", label, "status_code", res.StatusCode, "res", string(res.Body))
		return &kerrors.APICallError{
			Method:     "pull_request/add_label",
			StatusCode: res.StatusCode,
			Response:   res.Body,
		}
	}
	return nil
}

// RemoveLabel removes a label from the pull request.
func (p *PRV2) RemoveLabel(ctx context.Context, label string) error {
	p.log.Info("remove_label", "label", label)
	res, err := p.client.DeleteLabel(ctx, label, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to delete label", "label", label, "status_code", res.StatusCode, "res", string(res.Body))
		// we raise an exception to retry this request.
		return &kerrors.APICallError{
			Method:     "pull_request/delete_label",
			StatusCode: res.StatusCode,
			Response:   res.Body,
		}
	}
	return nil
}

// CreateComment creates a comment on the pull request with the given body as
// text.
func (p *PRV2) CreateComment(ctx context.Context, body string) error {
	p.log.Info("create_comment", "body", body)
	res, err := p.client.CreateComment(ctx, body, p.number)
	if err != nil {
		return err
	}
	if res.IsError() {
		p.log.Warn("failed to create comment", "status_code", res.StatusCode, "res", string(res.Body))
	}
	return nil
}
