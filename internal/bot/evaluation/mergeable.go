// The merge-decision state machine.
//
// Ported from bot/kodiak/evaluation.py: mergeable().

package evaluation

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/rs/zerolog"

	"github.com/appscodelabs/kodiak/internal/bot/config"
	"github.com/appscodelabs/kodiak/internal/bot/messages"
	"github.com/appscodelabs/kodiak/internal/bot/queries"
)

// MergeableInput bundles everything mergeable() needs.
type MergeableInput struct {
	Config              *config.V1
	ConfigParseErr      error
	ConfigStr           string
	ConfigPath          string
	PullRequest         queries.PullRequest
	BranchProtection    *queries.BranchProtectionRule
	ReviewRequests      []queries.PRReviewRequest
	Reviews             []queries.PRReview
	Contexts            []queries.StatusContext
	CheckRuns           []queries.CheckRun
	CommitAuthors       []queries.CommitAuthor
	ValidSignature      bool
	ValidMergeMethods   []config.MergeMethod
	Repository          queries.RepoInfo
	Subscription        *queries.Subscription
	Merging             bool
	IsActiveMerge       bool
	SkippableCheckBudget int
	APICallRetryBudget   int
	APICallRetryMethod   string
	AppID               string
	KodiakLogin         string
	SubscriptionsEnabled bool
}

// Mergeable runs one pass of the merge state machine. It returns ErrPoll,
// ErrSkippable, or an ErrAPICall to instruct the caller to retry.
func Mergeable(ctx context.Context, log zerolog.Logger, api PRAPI, in MergeableInput) error {
	setStatus := func(msg, markdown string) error {
		if in.IsActiveMerge {
			return nil
		}
		return api.SetStatus(ctx, msg, in.PullRequest.LatestSHA, markdown)
	}

	if in.Config == nil || in.ConfigParseErr != nil {
		log.Warn().Err(in.ConfigParseErr).Msg("problem fetching config")
		_ = setStatus(
			"⚠️ Invalid configuration (Click \"Details\" for more info.)",
			messages.ForConfigError(in.ConfigParseErr, in.ConfigStr, in.ConfigPath),
		)
		return api.Dequeue(ctx)
	}
	cfg := in.Config

	if in.APICallRetryBudget == 0 {
		log.Warn().Msg("timeout reached for api calls to GitHub")
		if in.APICallRetryMethod != "" {
			return setStatus(fmt.Sprintf("⚠️ problem contacting GitHub API with method %q", in.APICallRetryMethod), "")
		}
		return setStatus("⚠️ problem contacting GitHub API", "")
	}

	if cfg.AppID != "" && cfg.AppID != in.AppID {
		log.Info().Msg("missing required app_id")
		return api.Dequeue(ctx)
	}

	if in.BranchProtection == nil {
		return cfgErr(ctx, api, in.PullRequest,
			fmt.Sprintf("missing branch protection for baseRef: %q", in.PullRequest.BaseRefName), "")
	}
	bp := in.BranchProtection

	if bp.RequiresCommitSignatures && cfg.Merge.Method == config.MergeMethodRebase {
		return cfgErr(ctx, api, in.PullRequest,
			`"Require signed commits" branch protection is only supported with "squash" or "merge" commits. Rebase is not supported by GitHub.`, "")
	}

	if !containsMergeMethod(in.ValidMergeMethods, cfg.Merge.Method) {
		valid := make([]string, len(in.ValidMergeMethods))
		for i, m := range in.ValidMergeMethods {
			valid[i] = string(m)
		}
		return cfgErr(ctx, api, in.PullRequest,
			fmt.Sprintf("configured merge.method %q is invalid. Valid methods for repo are %v", cfg.Merge.Method, valid), "")
	}

	if !cfg.Merge.DoNotMerge && bp.RestrictsPushes && missingPushAllowance(bp.PushAllowances, in.AppID) {
		return cfgErr(ctx, api, in.PullRequest,
			"push restriction branch protection setting is missing push allowance for Kodiak",
			messages.ForPushAllowanceError(in.PullRequest.BaseRefName))
	}

	if containsString(in.PullRequest.Labels, cfg.DisableBotLabel) {
		if err := api.Dequeue(ctx); err != nil {
			return err
		}
		return api.SetStatus(ctx,
			fmt.Sprintf("🚨 kodiak disabled by disable_bot_label (%s). Remove label to re-enable Kodiak.", cfg.DisableBotLabel),
			in.PullRequest.LatestSHA, "")
	}

	if in.SubscriptionsEnabled && in.Repository.IsPrivate && in.Subscription != nil && in.Subscription.Blocker != nil {
		if msg := paywallStatus(in.PullRequest, in.Subscription.Blocker, log); msg != "" {
			return setStatus("💳 subscription: "+msg, messages.ForPaywall())
		}
	}

	if containsString(cfg.Approve.AutoApproveUsernames, in.PullRequest.Author.Login) &&
		in.PullRequest.State == queries.PRStateOpen &&
		in.PullRequest.MergeStateStatus != queries.MergeStateDraft {
		kodiakReviews := filterReviewsByAuthor(in.Reviews, in.KodiakLogin)
		if reviewStatus(kodiakReviews) != queries.ReviewApproved {
			if err := api.ApprovePullRequest(ctx); err != nil {
				return err
			}
		} else {
			log.Info().Msg("approval already exists, not adding another")
		}
	}

	needBranchUpdate := bp.RequiresStrictStatusChecks && in.PullRequest.MergeStateStatus == queries.MergeStateBehind
	meetsLabelReq := containsString(in.PullRequest.Labels, cfg.Merge.AutomergeLabel) || !cfg.Update.RequireAutomergeLabel

	if needBranchUpdate && !in.Merging && cfg.Update.Always && meetsLabelReq {
		if containsString(cfg.Update.BlacklistUsernames, in.PullRequest.Author.Login) {
			return setStatus(fmt.Sprintf("🛑 not auto updating for update.blacklist_usernames: %v", cfg.Update.BlacklistUsernames), "")
		}
		if err := setStatus("🔄 updating branch", "branch updated because `update.always = true` is configured."); err != nil {
			return err
		}
		return api.UpdateBranch(ctx)
	}

	if cfg.Merge.RequireAutomergeLabel && !containsString(in.PullRequest.Labels, cfg.Merge.AutomergeLabel) {
		return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("missing automerge_label: %q", cfg.Merge.AutomergeLabel))
	}

	if (in.PullRequest.MergeStateStatus == queries.MergeStateDirty || in.PullRequest.Mergeable == queries.MergeableConflicting) &&
		in.PullRequest.State == queries.PRStateOpen {
		if err := blockMerge(ctx, api, in.PullRequest, "merge conflict"); err != nil {
			return err
		}
		if cfg.Merge.NotifyOnConflict && cfg.Merge.RequireAutomergeLabel {
			label := cfg.Merge.AutomergeLabel
			if err := api.RemoveLabel(ctx, label); err != nil {
				return err
			}
			body := fmt.Sprintf("\nThis PR currently has a merge conflict. Please resolve this and then re-add the `%s` label.\n", label)
			return api.CreateComment(ctx, body)
		}
		return nil
	}

	if hit := intersect(cfg.Merge.BlacklistLabels, in.PullRequest.Labels); len(hit) > 0 {
		return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("has blacklist_labels: %v", hit))
	}

	if cfg.Merge.BlacklistTitleRegex != "" {
		if matched, _ := regexp.MatchString(cfg.Merge.BlacklistTitleRegex, in.PullRequest.Title); matched {
			return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("title matches blacklist_title_regex: %q", cfg.Merge.BlacklistTitleRegex))
		}
	}

	if in.PullRequest.MergeStateStatus == queries.MergeStateDraft {
		return blockMerge(ctx, api, in.PullRequest, "pull request is in draft state")
	}

	if cfg.Merge.BlockOnReviewsRequested && len(in.ReviewRequests) > 0 {
		names := make([]string, len(in.ReviewRequests))
		for i, r := range in.ReviewRequests {
			names[i] = r.Name
		}
		return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("reviews requested: %v", names))
	}

	if in.PullRequest.State == queries.PRStateMerged {
		if err := api.Dequeue(ctx); err != nil {
			return err
		}
		if !cfg.Merge.DeleteBranchOnMerge || in.PullRequest.IsCrossRepository || in.Repository.DeleteBranchOnMerge {
			return nil
		}
		count, ok, err := api.PullRequestsForRef(ctx, in.PullRequest.HeadRefName)
		if err != nil || !ok || count > 0 {
			log.Info().Int("pr_count", count).Bool("ok", ok).Msg("skipping branch deletion because of dependent PRs")
			return nil
		}
		return api.DeleteBranch(ctx, in.PullRequest.HeadRefName)
	}

	if in.PullRequest.State == queries.PRStateClosed {
		return api.Dequeue(ctx)
	}

	if in.PullRequest.Mergeable == queries.MergeableUnknown {
		if err := api.TriggerTestCommit(ctx); err != nil {
			return err
		}
		if err := api.Requeue(ctx); err != nil {
			return err
		}
		if in.Merging {
			return ErrPollForever
		}
		return nil
	}

	waitForChecks := false
	var missingRequired []string

	if in.PullRequest.MergeStateStatus == queries.MergeStateBlocked || in.PullRequest.MergeStateStatus == queries.MergeStateBehind {
		if bp.RequiresApprovingReviews && bp.RequiredApprovingReviewCount != nil {
			reviewsByAuthor := make(map[string][]queries.PRReview)
			sorted := append([]queries.PRReview(nil), in.Reviews...)
			sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
			for _, r := range sorted {
				if r.Author.Permission != queries.PermAdmin && r.Author.Permission != queries.PermWrite {
					continue
				}
				reviewsByAuthor[r.Author.Login] = append(reviewsByAuthor[r.Author.Login], r)
			}
			successful := 0
			for author, list := range reviewsByAuthor {
				switch reviewStatus(list) {
				case queries.ReviewChangesRequested:
					return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("changes requested by %q", author))
				case queries.ReviewApproved:
					successful++
				}
			}
			if successful < *bp.RequiredApprovingReviewCount {
				return blockMerge(ctx, api, in.PullRequest,
					fmt.Sprintf("missing required reviews, have %d/%d", successful, *bp.RequiredApprovingReviewCount))
			}
		}

		required := make(map[string]struct{})
		passing := make(map[string]struct{})

		if bp.RequiresStatusChecks {
			var skippable, failing, pending []string
			for _, ctxName := range bp.RequiredStatusCheckContexts {
				required[ctxName] = struct{}{}
			}
			for _, sc := range in.Contexts {
				if containsString(cfg.Merge.DontWaitOnStatusChecks, sc.Context) &&
					(sc.State == queries.StatusExpected || sc.State == queries.StatusPending) {
					skippable = append(skippable, sc.Context)
					continue
				}
				switch sc.State {
				case queries.StatusError, queries.StatusFailure:
					failing = append(failing, sc.Context)
				case queries.StatusExpected, queries.StatusPending:
					pending = append(pending, sc.Context)
				case queries.StatusSuccess:
					passing[sc.Context] = struct{}{}
				}
			}
			_ = pending
			for _, cr := range in.CheckRuns {
				if containsString(cfg.Merge.DontWaitOnStatusChecks, cr.Name) &&
					(cr.Conclusion == "" || cr.Conclusion == queries.CheckNeutral) {
					skippable = append(skippable, cr.Name)
					continue
				}
				if cr.Conclusion == "" {
					continue
				}
				switch cr.Conclusion {
				case queries.CheckSuccess:
					passing[cr.Name] = struct{}{}
				case queries.CheckActionRequired,
					queries.CheckFailure,
					queries.CheckTimedOut,
					queries.CheckCancelled,
					queries.CheckSkipped,
					queries.CheckStale:
					failing = append(failing, cr.Name)
				}
			}

			failingRequired := make(map[string]struct{})
			for _, f := range failing {
				if _, ok := required[f]; ok {
					failingRequired[f] = struct{}{}
				}
			}

			// Travis-CI special case: "continuous-integration/travis-ci"
			// is satisfied by either /pr or /push, but either failing also
			// blocks merge.
			if _, ok := required["continuous-integration/travis-ci"]; ok {
				for _, n := range []string{"continuous-integration/travis-ci/pr", "continuous-integration/travis-ci/push"} {
					if containsString(failing, n) {
						failingRequired[n] = struct{}{}
					}
				}
				_, pPR := passing["continuous-integration/travis-ci/pr"]
				_, pPush := passing["continuous-integration/travis-ci/push"]
				if pPR || pPush {
					delete(required, "continuous-integration/travis-ci")
				}
			}

			if len(failingRequired) > 0 {
				names := keysOf(failingRequired)
				return blockMerge(ctx, api, in.PullRequest, fmt.Sprintf("failing required status checks: %v", names))
			}
			if len(skippable) > 0 {
				if in.Merging {
					if in.SkippableCheckBudget > 0 {
						if err := setStatus(fmt.Sprintf("⛴ merging PR (waiting a bit for dont_wait_on_status_checks: %v)", skippable), ""); err != nil {
							return err
						}
						return ErrRetryForSkippableChecks
					}
					log.Warn().Strs("skippable", skippable).Msg("timeout reached waiting for dont_wait_on_status_checks")
					_ = setStatus(fmt.Sprintf("⚠️ timeout reached for dont_wait_on_status_checks: %v", skippable), "")
				}
				return setStatus(fmt.Sprintf("🛑 not waiting for dont_wait_on_status_checks: %v", skippable), "")
			}
		}

		for r := range required {
			if _, ok := passing[r]; !ok {
				missingRequired = append(missingRequired, r)
			}
		}
		waitForChecks = bp.RequiresStatusChecks && len(missingRequired) > 0

		if cfg.Merge.UpdateBranchImmediately && needBranchUpdate {
			if err := setStatus("🔄 updating branch", "branch updated because `merge.update_branch_immediately = true` is configured."); err != nil {
				return err
			}
			if err := api.UpdateBranch(ctx); err != nil {
				return err
			}
			if in.Merging {
				return ErrPollForever
			}
			return nil
		}

		if in.Merging {
			if cfg.Merge.OptimisticUpdates {
				if needBranchUpdate {
					if err := setStatus("⛴ merging PR (updating branch)", ""); err != nil {
						return err
					}
					if err := api.UpdateBranch(ctx); err != nil {
						return err
					}
					return ErrPollForever
				}
				if waitForChecks {
					_ = setStatus(fmt.Sprintf("⛴ merging PR (waiting for status checks: %v)", missingRequired), "")
					return ErrPollForever
				}
			} else {
				if waitForChecks {
					_ = setStatus(fmt.Sprintf("⛴ merging PR (waiting for status checks: %v)", missingRequired), "")
					return ErrPollForever
				}
				if needBranchUpdate {
					if err := setStatus("⛴ merging PR (updating branch)", ""); err != nil {
						return err
					}
					if err := api.UpdateBranch(ctx); err != nil {
						return err
					}
					return ErrPollForever
				}
			}
		}

		if !(waitForChecks || needBranchUpdate) {
			log.Warn().Msg("merge blocked for unknown reason")
			return blockMerge(ctx, api, in.PullRequest, "Merging blocked by GitHub requirements")
		}
	}
	readyToMerge := !(waitForChecks || needBranchUpdate)

	if cfg.Merge.DoNotMerge {
		switch {
		case waitForChecks:
			return setStatus(fmt.Sprintf("⌛️ waiting for required status checks: %v", missingRequired), "")
		case needBranchUpdate:
			return setStatus("⚠️ need branch update (suggestion: use merge.update_branch_immediately with merge.do_not_merge)",
				"When `merge.do_not_merge = true` is configured `merge.update_branch_immediately = true` is recommended so Kodiak can automatically update branches.")
		default:
			return setStatus("✅ okay to merge", "")
		}
	}

	if (cfg.Merge.PrioritizeReadyToMerge && readyToMerge) || in.Merging {
		mb := BuildMergeBody(cfg, in.PullRequest, in.CommitAuthors)
		if err := setStatus("⛴ attempting to merge PR (merging)", ""); err != nil {
			return err
		}
		return api.Merge(ctx, mb.MergeMethod, mb.CommitTitle, mb.CommitMessage)
	}

	pos, ok, err := api.QueueForMerge(ctx)
	if err != nil {
		return err
	}
	if !ok {
		log.Warn().Msg("couldn't find position for enqueued PR")
		return nil
	}
	if !in.IsActiveMerge {
		return setStatus(fmt.Sprintf("📦 enqueued for merge (position=%s)", ordinalize(pos+1)), "")
	}
	log.Info().Msg("not setting status message for enqueued job because is_active_merge=True")
	return nil
}

// reviewStatus picks the most recent actionable review state.
func reviewStatus(reviews []queries.PRReview) queries.PRReviewState {
	s := queries.ReviewCommented
	for _, r := range reviews {
		switch r.State {
		case queries.ReviewChangesRequested, queries.ReviewApproved, queries.ReviewDismissed:
			s = r.State
		}
	}
	return s
}

func filterReviewsByAuthor(reviews []queries.PRReview, login string) []queries.PRReview {
	out := make([]queries.PRReview, 0)
	sorted := append([]queries.PRReview(nil), reviews...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })
	for _, r := range sorted {
		if r.Author.Login == login {
			out = append(out, r)
		}
	}
	return out
}

func missingPushAllowance(allowances []queries.PushAllowance, appID string) bool {
	for _, a := range allowances {
		if a.ActorDatabaseID == nil {
			continue
		}
		if fmt.Sprintf("%d", *a.ActorDatabaseID) == appID {
			return false
		}
	}
	return true
}

func paywallStatus(pr queries.PullRequest, blocker *queries.SubscriptionBlocker, log zerolog.Logger) string {
	switch blocker.Kind {
	case queries.BlockerSeatsExceeded:
		for _, id := range blocker.AllowedUserIDs {
			if id == pr.Author.DatabaseID {
				return ""
			}
		}
		return "usage has exceeded licensed seats"
	case queries.BlockerTrialExpired:
		return "trial ended"
	case queries.BlockerSubscriptionExpired:
		return "subscription expired"
	default:
		log.Warn().Str("blocker_kind", string(blocker.Kind)).Msg("unexpected subscription_blocker")
		return ""
	}
}

func cfgErr(ctx context.Context, api PRAPI, pr queries.PullRequest, msg, markdown string) error {
	if err := api.Dequeue(ctx); err != nil {
		return err
	}
	return api.SetStatus(ctx, "⚠️ config error ("+msg+")", pr.LatestSHA, markdown)
}

func blockMerge(ctx context.Context, api PRAPI, pr queries.PullRequest, msg string) error {
	if err := api.Dequeue(ctx); err != nil {
		return err
	}
	return api.SetStatus(ctx, "🛑 cannot merge ("+msg+")", pr.LatestSHA, "")
}

func containsMergeMethod(haystack []config.MergeMethod, needle config.MergeMethod) bool {
	for _, m := range haystack {
		if m == needle {
			return true
		}
	}
	return false
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func intersect(a, b []string) []string {
	bset := make(map[string]struct{}, len(b))
	for _, s := range b {
		bset[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := bset[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ordinalize renders 1→"1st", 2→"2nd", 3→"3rd", 4→"4th", matching the
// inflection.ordinalize() used in the Python source.
func ordinalize(n int) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}
	suffix := "th"
	switch abs % 100 {
	case 11, 12, 13:
	default:
		switch abs % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
