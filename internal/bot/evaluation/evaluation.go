// Package evaluation implements the merge-decision state machine.
//
// This is the Go skeleton for bot/kodiak/evaluation.py (~900 lines) plus
// bot/kodiak/pull_request.py (~350 lines). The Evaluator type satisfies the
// queue.Evaluator interface so the rest of the bot can wire it up; the
// individual decision branches are stubs and will be ported incrementally.
package evaluation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/bot/queries"
	"github.com/appscodelabs/kodiak/internal/bot/queue"
)

// Tunables mirroring bot/kodiak/pull_request.py.
const (
	retryRate                = 2 * time.Second
	pollRate                 = 3 * time.Second
	skippableCheckBudget     = 4
	apiCallRetryBudget       = 5
)

// Evaluator implements queue.Evaluator using a real GitHub client factory and
// the parsed per-repo Kodiak configuration loaded on demand.
type Evaluator struct {
	API                  queries.ClientFactory
	AppID                string
	KodiakLogin          string
	SubscriptionsEnabled bool
}

// New constructs an Evaluator.
func New(api queries.ClientFactory, appID, kodiakLogin string, subscriptionsEnabled bool) *Evaluator {
	return &Evaluator{
		API:                  api,
		AppID:                appID,
		KodiakLogin:          kodiakLogin,
		SubscriptionsEnabled: subscriptionsEnabled,
	}
}

// EvaluatePR runs the merge-decision loop over a pull request, retrying on
// skippable checks, transient API errors, and "poll forever" signals from
// Mergeable. Ported from bot/kodiak/pull_request.py:evaluate_pr.
func (e *Evaluator) EvaluatePR(ctx context.Context, in queue.EvaluateInput) error {
	log := logging.FromContext(ctx).With().
		Str("owner", in.Event.RepoOwner).
		Str("repo", in.Event.RepoName).
		Int("number", in.Event.PullRequestNumber).
		Str("install", in.Event.InstallationID).
		Bool("merging", in.Merging).
		Logger()

	client, err := e.API.Client(ctx, in.Event.RepoOwner, in.Event.RepoName, in.Event.InstallationID)
	if err != nil {
		return fmt.Errorf("github client: %w", err)
	}
	defer client.Close()

	skipBudget := skippableCheckBudget
	apiBudget := apiCallRetryBudget

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		log.Info().Msg("get_pr")
		info, err := client.GetEventInfo(ctx, in.Event.PullRequestNumber)
		if err != nil {
			log.Warn().Err(err).Msg("failed to get event info")
			if in.Dequeue != nil {
				_ = in.Dequeue(ctx)
			}
			return nil
		}

		adapter := newPRAdapter(client, info, in)
		err = Mergeable(ctx, log, adapter, MergeableInput{
			Config:               info.Config,
			ConfigParseErr:       info.ConfigParseErr,
			ConfigStr:            info.ConfigStr,
			ConfigPath:           info.ConfigFileExpression,
			PullRequest:          info.PullRequest,
			BranchProtection:     info.BranchProtection,
			ReviewRequests:       info.ReviewRequests,
			Reviews:              info.Reviews,
			Contexts:             info.StatusContexts,
			CheckRuns:            info.CheckRuns,
			CommitAuthors:        info.CommitAuthors,
			ValidSignature:       info.ValidSignature,
			ValidMergeMethods:    info.ValidMergeMethods,
			Repository:           info.Repository,
			Subscription:         info.Subscription,
			Merging:              in.Merging,
			IsActiveMerge:        in.IsActiveMerging,
			SkippableCheckBudget: skipBudget,
			APICallRetryBudget:   apiBudget,
			AppID:                e.AppID,
			KodiakLogin:          e.KodiakLogin,
			SubscriptionsEnabled: e.SubscriptionsEnabled,
		})
		switch {
		case err == nil:
			log.Info().Msg("evaluate_pr successful")
			return nil
		case errors.Is(err, ErrRetryForSkippableChecks):
			if skipBudget <= 0 {
				log.Info().Msg("skippable check budget exhausted")
				return nil
			}
			skipBudget--
			log.Info().Msg("waiting for skippable checks to pass")
			if sleepErr := sleepCtx(ctx, retryRate); sleepErr != nil {
				return sleepErr
			}
		case errors.Is(err, ErrPollForever):
			log.Info().Msg("polling")
			if sleepErr := sleepCtx(ctx, pollRate); sleepErr != nil {
				return sleepErr
			}
		default:
			var apiErr *ErrAPICall
			if errors.As(err, &apiErr) && apiBudget > 0 {
				apiBudget--
				log.Info().Str("method", apiErr.Method).Msg("problem contacting remote api. retrying")
				continue
			}
			log.Err(err).Msg("evaluate_pr terminal error")
			return nil
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

