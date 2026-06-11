// Package evaluation implements the merge-decision core of the bot,
// ported from bot/kodiak/evaluation.py.
package evaluation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/dependencies"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/messages"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// PRAPI mirrors the PRAPI protocol from the Python implementation. It is
// implemented by pull_request.PRV2.
type PRAPI interface {
	Dequeue(ctx context.Context) error
	Requeue(ctx context.Context) error
	SetStatus(ctx context.Context, msg string, markdownContent *string) error
	PullRequestsForRef(ctx context.Context, ref string) (*int, error)
	DeleteBranch(ctx context.Context, branchName string) error
	RemoveLabel(ctx context.Context, label string) error
	AddLabel(ctx context.Context, label string) error
	CreateComment(ctx context.Context, body string) error
	TriggerTestCommit(ctx context.Context) error
	Merge(ctx context.Context, mergeMethod string, commitTitle *string, commitMessage *string) error
	UpdateRef(ctx context.Context, ref string, sha string) error
	QueueForMerge(ctx context.Context, first bool) (*int, error)
	UpdateBranch(ctx context.Context) error
	ApprovePullRequest(ctx context.Context) error
}

func strPtr(s string) *string { return &s }

// reviewStatus finds the most recent actionable review state for a user.
func reviewStatus(reviews []queries.PRReview) queries.PRReviewState {
	status := queries.PRReviewStateCommented
	for _, review := range reviews {
		// only these events are meaningful to us
		switch review.State {
		case queries.PRReviewStateChangesRequested,
			queries.PRReviewStateApproved,
			queries.PRReviewStateDismissed:
			status = review.State
		}
	}
	return status
}

// deduplicateCheckRuns keeps a single check run per name. Like the Python
// implementation (a dict keyed by name), the first-seen position is kept while
// the last-seen check run wins.
func deduplicateCheckRuns(checkRuns []queries.CheckRun) []queries.CheckRun {
	index := map[string]int{}
	out := make([]queries.CheckRun, 0, len(checkRuns))
	for _, checkRun := range checkRuns {
		if i, ok := index[checkRun.Name]; ok {
			out[i] = checkRun
			continue
		}
		index[checkRun.Name] = len(out)
		out = append(out, checkRun)
	}
	return out
}

func cfgErr(ctx context.Context, api PRAPI, msg string, markdownContent *string) error {
	if err := api.Dequeue(ctx); err != nil {
		return err
	}
	return api.SetStatus(ctx, fmt.Sprintf("⚠️ config error (%s)", msg), markdownContent)
}

func blockMerge(ctx context.Context, api PRAPI, msg string) error {
	if err := api.Dequeue(ctx); err != nil {
		return err
	}
	return api.SetStatus(ctx, fmt.Sprintf("🛑 cannot merge (%s)", msg), nil)
}

func missingBranchProtectionPushAllowance(branchProtection *queries.BranchProtectionRule) bool {
	for _, pushAllowance := range branchProtection.PushAllowances.Nodes {
		// a null databaseId indicates this is not a GitHub App.
		if pushAllowance.Actor.DatabaseID == nil {
			continue
		}
		if strconv.FormatInt(*pushAllowance.Actor.DatabaseID, 10) == appconf.GithubAppID {
			return false
		}
	}
	return true
}

func isUpdateRuleMissingAllowance(rulesetRule queries.RulesetRule) bool {
	if rulesetRule.RepositoryRuleset == nil ||
		rulesetRule.RepositoryRuleset.BypassActors == nil ||
		rulesetRule.RepositoryRuleset.BypassActors.Nodes == nil {
		// if we have some error, assume we have an allowance.
		return false
	}
	for _, pushAllowance := range rulesetRule.RepositoryRuleset.BypassActors.Nodes {
		// GitHub redacts the actor field when our App installation lacks the
		// permission to read it — even when the bypass entry is for our own
		// App. We can't distinguish "redacted self" from "redacted other
		// App", so assume allowance to avoid blocking valid configurations.
		if pushAllowance == nil || pushAllowance.Actor == nil {
			return false
		}
		if pushAllowance.Actor.DatabaseID == nil {
			continue
		}
		if strconv.FormatInt(*pushAllowance.Actor.DatabaseID, 10) == appconf.GithubAppID {
			return false
		}
	}
	return true
}

func hasRulesetRulesWithoutPushAllowances(rulesetRules []queries.RulesetRule) bool {
	for _, rulesetRule := range rulesetRules {
		if rulesetRule.Type == "UPDATE" && isUpdateRuleMissingAllowance(rulesetRule) {
			return true
		}
	}
	return false
}

func missingPushAllowance(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) bool {
	if branchProtection != nil &&
		branchProtection.RestrictsPushes &&
		missingBranchProtectionPushAllowance(branchProtection) {
		return true
	}
	return hasRulesetRulesWithoutPushAllowances(rulesetRules)
}

func paywallStatusForSeatsExceeded(
	pullRequest queries.PullRequest,
	seatsExceeded *queries.SeatsExceeded,
) (string, bool) {
	if pullRequest.Author != nil &&
		slices.Contains(seatsExceeded.AllowedUserIDs, pullRequest.Author.DatabaseID) {
		return "", false
	}
	return "usage has exceeded licensed seats", true
}

func getPaywallStatusForBlocker(
	pullRequest queries.PullRequest,
	subscriptionBlocker queries.SubscriptionBlocker,
	log *slog.Logger,
) (string, bool) {
	switch blocker := subscriptionBlocker.(type) {
	case *queries.SeatsExceeded:
		return paywallStatusForSeatsExceeded(pullRequest, blocker)
	case queries.SeatsExceeded:
		return paywallStatusForSeatsExceeded(pullRequest, &blocker)
	case queries.TrialExpired, *queries.TrialExpired:
		return "trial ended", true
	case queries.SubscriptionExpired, *queries.SubscriptionExpired:
		return "subscription expired", true
	}
	log.Warn("unexpected subscription_blocker", "subscription_blocker", subscriptionBlocker)
	return "", false
}

type blockingTitleRegex struct {
	Pattern   string
	ConfigKey string
}

// getBlockingTitleRegex returns the user-configured blocking title regex.
//
// merge.blocking_title_regex takes priority over merge.blacklist_title_regex.
//
// If neither options are set we return the default regex.
func getBlockingTitleRegex(cfg *config.V1) blockingTitleRegex {
	// config.merge.blocking_title_regex takes priority.
	if cfg.Merge.BlockingTitleRegex != config.UnsetTitleRegex {
		return blockingTitleRegex{
			Pattern:   cfg.Merge.BlockingTitleRegex,
			ConfigKey: "merge.blocking_title_regex",
		}
	}
	if cfg.Merge.BlacklistTitleRegex != config.UnsetTitleRegex {
		return blockingTitleRegex{
			Pattern:   cfg.Merge.BlacklistTitleRegex,
			ConfigKey: "merge.blacklist_title_regex",
		}
	}
	// user hasn't set either options so we return the default.
	return blockingTitleRegex{
		Pattern:   config.DefaultTitleRegex,
		ConfigKey: "merge.blocking_title_regex",
	}
}

// mergeMethods are ordered by preference for selection.
//
// rebase_fast_forward is not included here since it is not configurable via
// the GitHub UI. Kodiak users can manually specify rebase_fast_forward in
// their configuration file.
var mergeMethods = []config.MergeMethod{
	config.MergeMethodMerge,
	config.MergeMethodSquash,
	config.MergeMethodRebase,
}

// mergeMethodOverride parses the decoded toml value from a merge method
// override label.
//
// A well formed object to parse will look like the following:
//
//	{"merge": {"method": "rebase"}}
type mergeMethodOverride struct {
	Merge *struct {
		Method *config.MergeMethod `toml:"method"`
	} `toml:"merge"`
}

func getMergeMethod(
	cfgMergeMethod *config.MergeMethod,
	validMergeMethods []config.MergeMethod,
	labels []string,
	log *slog.Logger,
) config.MergeMethod {
	// parse merge.method override label
	// example: `kodiak: merge.method = "rebase"`
	for _, label := range labels {
		if !strings.HasPrefix(label, "kodiak:") {
			continue
		}
		// we have an existing label "kodiak:disabled". This label will not parse
		// here and will be ignored.
		maybeConfig := strings.TrimPrefix(label, "kodiak:")
		var override mergeMethodOverride
		if _, err := toml.Decode(maybeConfig, &override); err != nil {
			continue
		}
		if override.Merge == nil || override.Merge.Method == nil {
			continue
		}
		return *override.Merge.Method
	}

	if cfgMergeMethod != nil {
		return *cfgMergeMethod
	}

	// take the first valid merge method.
	for _, mergeMethod := range mergeMethods {
		if slices.Contains(validMergeMethods, mergeMethod) {
			return mergeMethod
		}
	}

	// NOTE(chdsbd): I don't think the following code should be reachable in
	// production, but I don't want to blow things up with an assert.
	log.Warn("no merge methods selected.",
		"cfg_merge_method", cfgMergeMethod,
		"valid_merge_methods", validMergeMethods)
	return config.MergeMethodMerge
}

// hasEquivalentBranchProtectionRulesets reports whether a ruleset requires
// code review or status checks.
//
// When we originally made Kodiak, we only enabled Kodiak if branch protection
// was configured. This was to prevent merging pull requests that didn't
// require any status checks or code review.
func hasEquivalentBranchProtectionRulesets(rulesetRules []queries.RulesetRule) bool {
	for _, rule := range rulesetRules {
		switch rule.Parameters.(type) {
		case *queries.PullRequestParameters, *queries.RequiredStatusChecksParameters:
			return true
		}
	}
	return false
}

func requiresSignedCommits(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) bool {
	if branchProtection != nil && branchProtection.RequiresCommitSignatures {
		return true
	}
	for _, rulesetRule := range rulesetRules {
		if rulesetRule.Type == "REQUIRED_SIGNATURES" {
			return true
		}
	}
	return false
}

func hasRulesetRulesRequiringStrictStatusChecks(rulesetRules []queries.RulesetRule) bool {
	for _, rulesetRule := range rulesetRules {
		if params, ok := rulesetRule.Parameters.(*queries.RequiredStatusChecksParameters); ok &&
			params.StrictRequiredStatusChecksPolicy {
			return true
		}
	}
	return false
}

func requiresStrictStatusChecks(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) bool {
	return (branchProtection != nil && branchProtection.RequiresStrictStatusChecks) ||
		hasRulesetRulesRequiringStrictStatusChecks(rulesetRules)
}

func getRequiredStatusChecks(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) map[string]bool {
	checks := map[string]bool{}
	if branchProtection != nil {
		for _, check := range branchProtection.RequiredStatusCheckContexts {
			checks[check] = true
		}
	}
	for _, rulesetRule := range rulesetRules {
		if params, ok := rulesetRule.Parameters.(*queries.RequiredStatusChecksParameters); ok {
			for _, check := range params.RequiredStatusChecks {
				checks[check.Context] = true
			}
		}
	}
	return checks
}

func hasRulesetRulesRequiringConversationResolution(rulesetRules []queries.RulesetRule) bool {
	for _, rulesetRule := range rulesetRules {
		if params, ok := rulesetRule.Parameters.(*queries.PullRequestParameters); ok &&
			params.RequiredReviewThreadResolution {
			return true
		}
	}
	return false
}

func requiresConversationResolution(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) bool {
	return (branchProtection != nil &&
		branchProtection.RequiresConversationResolution != nil &&
		*branchProtection.RequiresConversationResolution) ||
		hasRulesetRulesRequiringConversationResolution(rulesetRules)
}

func requiresStatusChecks(
	branchProtection *queries.BranchProtectionRule,
	rulesetRules []queries.RulesetRule,
) bool {
	if branchProtection != nil && branchProtection.RequiresStatusChecks {
		return true
	}
	for _, rulesetRule := range rulesetRules {
		if _, ok := rulesetRule.Parameters.(*queries.RequiredStatusChecksParameters); ok {
			return true
		}
	}
	return false
}

// automergeLabelStr formats merge.automerge_label like Python's str() of the
// Union[str, List[str]] config value.
//
// NOTE(port): the Go config always stores a list. A single-element list is
// rendered as the bare string, matching the common Python case where the
// config value is a plain string.
func automergeLabelStr(labels config.Labels) string {
	if len(labels) == 1 {
		return labels[0]
	}
	return pyListRepr(labels)
}

// automergeLabelRepr formats merge.automerge_label like Python's repr() of
// the Union[str, List[str]] config value. See NOTE(port) on automergeLabelStr.
func automergeLabelRepr(labels config.Labels) string {
	if len(labels) == 1 {
		return pyStrRepr(labels[0])
	}
	return pyListRepr(labels)
}

// MergeableParams holds the arguments to Mergeable, mirroring the keyword
// arguments of the Python mergeable() function.
type MergeableParams struct {
	// Config is the parsed configuration. ConfigError is set instead when the
	// configuration failed to parse or validate (Union[V1, ValidationError,
	// TomlDecodeError] in Python).
	Config                  *config.V1
	ConfigError             error
	ConfigStr               string
	ConfigPath              string
	PullRequest             queries.PullRequest
	BranchProtection        *queries.BranchProtectionRule
	RulesetRules            []queries.RulesetRule
	ReviewRequests          []queries.PRReviewRequest
	BotReviews              []queries.PRReview
	Contexts                []queries.StatusContext
	CheckRuns               []queries.CheckRun
	Commits                 []queries.Commit
	ValidMergeMethods       []config.MergeMethod
	Repository              queries.RepoInfo
	Merging                 bool
	IsActiveMerge           bool
	SkippableCheckTimeout   int
	APICallRetriesRemaining int
	APICallErrors           []messages.APICallRetry
	Subscription            *queries.Subscription
	AppID                   string
}

const doNotMergeBranchUpdateMarkdown = "When `merge.do_not_merge = true` is configured " +
	"`merge.update_branch_immediately = true` is recommended so Kodiak can automatically " +
	"update branches.\n\nBy default, Kodiak is efficient and only update branches when " +
	"merging a PR, but when `merge.do_not_merge` is enabled, Kodiak never has that " +
	"opportunity to update a branch during merge. `merge.update_branch_immediately = true` " +
	"will trigger Kodiak to update branches whenever a PR is outdated and not failing any " +
	"branch protection requirements.\n"

// Mergeable evaluates a pull request and performs the appropriate actions via
// the PRAPI. Ported from kodiak.evaluation.mergeable.
//
// The sentinel errors kerrors.ErrPollForever and
// kerrors.ErrRetryForSkippableChecks and the *kerrors.APICallError type
// replace the equivalent Python exceptions used for control flow by the
// caller.
func Mergeable(ctx context.Context, api PRAPI, m MergeableParams) error {
	pullRequest := m.PullRequest
	log := slog.With("number", pullRequest.Number, "url", pullRequest.URL)
	// we set is_active_merge when the PR is being merged from the merge queue.
	// We don't want to clobber any statuses set by that system, so we take no
	// action. If the PR becomes ineligible for merging that logic will handle
	// it.

	// rebase_fast_forward isn't determined via the GitHub UI and is always
	// available.
	validMergeMethods := append(slices.Clone(m.ValidMergeMethods), config.MergeMethodRebaseFastForward)

	setStatus := func(msg string, markdownContent *string) error {
		// don't clobber statuses set via merge loop.
		if m.IsActiveMerge {
			return nil
		}
		return api.SetStatus(ctx, msg, markdownContent)
	}

	if m.Config == nil {
		log.Info("invalid config")
		configError := m.ConfigError
		if configError == nil {
			// NOTE(port): the Python union type guarantees an error object;
			// guard against a missing one.
			configError = errors.New("invalid configuration")
		}
		if err := setStatus(
			`⚠️ Invalid configuration (Click "Details" for more info.)`,
			strPtr(messages.GetMarkdownForConfig(configError, m.ConfigStr, m.ConfigPath)),
		); err != nil {
			return err
		}
		return api.Dequeue(ctx)
	}
	cfg := m.Config

	if m.APICallRetriesRemaining == 0 {
		var firstError messages.APICallRetry
		if len(m.APICallErrors) > 0 {
			firstError = m.APICallErrors[0]
		}
		if firstError != nil {
			if err := setStatus(
				fmt.Sprintf("⚠️ problem contacting GitHub API with method %s", pyStrRepr(firstError.APIName())),
				strPtr(messages.GetMarkdownForAPICallErrors(m.APICallErrors)),
			); err != nil {
				return err
			}
		} else {
			if err := setStatus("⚠️ problem contacting GitHub API", nil); err != nil {
				return err
			}
		}
		mergedBlockedUnknownReason := false
		for _, apiCallError := range m.APICallErrors {
			if apiCallError.APIName() == "kodiak/merged_blocked_unknown_reason" {
				mergedBlockedUnknownReason = true
			}
		}
		log.Warn("timeout reached for api calls to GitHub",
			"merged_blocked_unknown_reason", mergedBlockedUnknownReason,
			"api_call_errors", m.APICallErrors)
		return nil
	}

	// if we have an app_id in the config then we only want to work on this repo
	// if our app_id from the environment matches the configuration.
	if cfg.AppID != nil && string(*cfg.AppID) != m.AppID {
		log.Info("missing required app_id")
		return api.Dequeue(ctx)
	}

	if m.BranchProtection == nil && !hasEquivalentBranchProtectionRulesets(m.RulesetRules) {
		return cfgErr(ctx, api,
			fmt.Sprintf("missing branch protection for baseRef: %s", pyStrRepr(pullRequest.BaseRefName)),
			nil)
	}

	mergeMethod := getMergeMethod(cfg.Merge.Method, validMergeMethods, pullRequest.Labels, log)

	if requiresSignedCommits(m.BranchProtection, m.RulesetRules) &&
		mergeMethod == config.MergeMethodRebase {
		return cfgErr(ctx, api,
			`"Require signed commits" branch protection is only supported with "squash" or "merge" commits. Rebase is not supported by GitHub.`,
			nil)
	}

	if !slices.Contains(validMergeMethods, mergeMethod) {
		validMergeMethodsStr := make([]string, 0, len(validMergeMethods))
		for _, method := range validMergeMethods {
			validMergeMethodsStr = append(validMergeMethodsStr, string(method))
		}
		return cfgErr(ctx, api,
			fmt.Sprintf("configured merge.method %s is invalid. Valid methods for repo are %s",
				pyStrRepr(string(mergeMethod)), pyListRepr(validMergeMethodsStr)),
			nil)
	}

	if !cfg.Merge.DoNotMerge && missingPushAllowance(m.BranchProtection, m.RulesetRules) {
		return cfgErr(ctx, api,
			"push restriction branch protection setting is missing push allowance for Kodiak",
			strPtr(messages.GetMarkdownForPushAllowanceError(pullRequest.BaseRefName)))
	}

	// we keep the configuration errors before the rest of the application logic
	// so configuration issues are surfaced as early as possible.

	if slices.Contains(pullRequest.Labels, cfg.DisableBotLabel) {
		if err := api.Dequeue(ctx); err != nil {
			return err
		}
		return api.SetStatus(ctx,
			fmt.Sprintf("🚨 kodiak disabled by disable_bot_label (%s). Remove label to re-enable Kodiak.",
				cfg.DisableBotLabel),
			nil)
	}

	if appconf.SubscriptionsEnabled &&
		m.Repository.IsPrivate &&
		m.Subscription != nil &&
		m.Subscription.SubscriptionBlocker != nil {
		// We only count private repositories in our usage calculations. A user
		// has an active subscription if a subscription exists in Redis and has
		// an empty subscription_blocker.
		//
		// We also ignore missing subscriptions. The web api will set
		// subscription blockers if usage exceeds limits.
		statusMessage, ok := getPaywallStatusForBlocker(pullRequest, m.Subscription.SubscriptionBlocker, log)
		if ok {
			return setStatus(
				fmt.Sprintf("💳 subscription: %s", statusMessage),
				strPtr(messages.GetMarkdownForPaywall()))
		}
	}

	pullRequestLabels := newStringSet(pullRequest.Labels)
	configAutomergeLabels := newStringSet(cfg.Merge.AutomergeLabel)
	pullRequestAutomergeLabels := setIntersection(configAutomergeLabels, pullRequestLabels)
	hasAutomergeLabel := len(pullRequestAutomergeLabels) > 0

	shouldDependencyAutomerge := pullRequest.Author != nil &&
		slices.Contains(cfg.Merge.AutomergeDependencies.Usernames, pullRequest.Author.Login) &&
		slices.Contains(cfg.Merge.AutomergeDependencies.Versions,
			config.UpgradeVersion(dependencies.DepVersionsFromPR(pullRequest.Title, pullRequest.Body)))

	// we should trigger mergeability checks whenever we encounter UNKNOWN.
	//
	// I don't foresee conflicts with checking configuration errors,
	// `config.disable_bot_label`, and the paywall before this code.
	//
	// Previously we had an issue where this code wasn't being entered because
	// `merge.blocking_title_regex` was checked first. Which caused
	// `update.always` to not operate.
	if pullRequest.Mergeable == queries.MergeableStateUnknown &&
		pullRequest.State == queries.PullRequestStateOpen {
		// we need to trigger a test commit to fix this. We do that by calling
		// GET on the pull request endpoint.
		if err := api.TriggerTestCommit(ctx); err != nil {
			return err
		}

		// queue the PR for evaluation again in case GitHub doesn't send another
		// webhook for the commit test.
		if err := api.Requeue(ctx); err != nil {
			return err
		}

		// we don't want to abort the merge if we encounter this status check.
		// Just keep polling!
		if m.Merging {
			return kerrors.ErrPollForever
		}

		return nil
	}

	isDraftPullRequest := pullRequest.IsDraft ||
		pullRequest.MergeStateStatus == queries.MergeStateStatusDraft
	pullRequestAutoApproveLabels := setIntersection(
		newStringSet(cfg.Approve.AutoApproveLabels), pullRequestLabels)
	hasAutoApproveLabel := len(pullRequestAutoApproveLabels) > 0
	if ((pullRequest.Author != nil &&
		slices.Contains(cfg.Approve.AutoApproveUsernames, pullRequest.Author.Login)) ||
		hasAutoApproveLabel) &&
		pullRequest.State == queries.PullRequestStateOpen &&
		!isDraftPullRequest {
		// if the PR was created by an approve author or has an approve label
		// and we have not previously given an approval, approve the PR.
		sortedReviews := slices.Clone(m.BotReviews)
		slices.SortStableFunc(sortedReviews, func(a, b queries.PRReview) int {
			return a.CreatedAt.Compare(b.CreatedAt)
		})
		var kodiakReviews []queries.PRReview
		for _, review := range sortedReviews {
			// TODO(chdsbd): We could make an API request to `/app` on start to
			// get this information, but this is pretty simple.
			if review.Author.Login == appconf.GithubAppName {
				kodiakReviews = append(kodiakReviews, review)
			}
		}
		status := reviewStatus(kodiakReviews)
		if status != queries.PRReviewStateApproved {
			if err := api.ApprovePullRequest(ctx); err != nil {
				return err
			}
		} else {
			log.Info("approval already exists, not adding another")
		}
	}

	needBranchUpdate := requiresStrictStatusChecks(m.BranchProtection, m.RulesetRules) &&
		pullRequest.MergeStateStatus == queries.MergeStateStatusBehind
	updateAlways := cfg.Update.Always &&
		(hasAutomergeLabel || !cfg.Update.RequireAutomergeLabel)
	hasAutoupdateLabel := cfg.Update.AutoupdateLabel != nil &&
		pullRequestLabels[*cfg.Update.AutoupdateLabel]
	autoUpdateEnabled := updateAlways || hasAutoupdateLabel

	// Dequeue pull request if out-of-date and author in
	// `update.ignored_usernames`. We cannot update or merge it.
	//
	// If `update.autoupdate_label` is applied to the pull request, bypass
	// `update.ignored_usernames` and let the pull request update.
	if needBranchUpdate && !hasAutoupdateLabel {
		if pullRequest.Author != nil &&
			slices.Contains(cfg.Update.BlacklistUsernames, pullRequest.Author.Login) {
			if err := setStatus(
				fmt.Sprintf("🛑 updates blocked by update.blacklist_usernames: %s",
					pyListRepr(cfg.Update.BlacklistUsernames)),
				strPtr("Apply the `update.autoupdate_label` label to enable updates for this pull request."),
			); err != nil {
				return err
			}
			return api.Dequeue(ctx)
		}
		if pullRequest.Author != nil &&
			slices.Contains(cfg.Update.IgnoredUsernames, pullRequest.Author.Login) {
			if err := setStatus(
				fmt.Sprintf("🛑 updates blocked by update.ignored_usernames: %s",
					pyListRepr(cfg.Update.IgnoredUsernames)),
				strPtr("Apply the `update.autoupdate_label` label to enable updates for this pull request."),
			); err != nil {
				return err
			}
			return api.Dequeue(ctx)
		}
	}

	if needBranchUpdate && !m.Merging && autoUpdateEnabled {
		if err := setStatus("🔄 updating branch",
			strPtr("branch updated because `update.always = true` is configured.")); err != nil {
			return err
		}
		return api.UpdateBranch(ctx)
	}

	if cfg.Merge.RequireAutomergeLabel &&
		!hasAutomergeLabel &&
		!shouldDependencyAutomerge {
		if err := api.Dequeue(ctx); err != nil {
			return err
		}
		// Update status when "show_missing_automerge_label_message" is enabled or label has been removed while already in merging state
		if cfg.Merge.ShowMissingAutomergeLabelMessage || m.Merging {
			return api.SetStatus(ctx,
				fmt.Sprintf("Ignored (no automerge label: %s)", automergeLabelRepr(cfg.Merge.AutomergeLabel)),
				nil)
		}
		return nil
	}

	// We want users to get notified a merge conflict even if the PR matches a
	// WIP title via merge.blacklist_title_regex.
	if (pullRequest.MergeStateStatus == queries.MergeStateStatusDirty ||
		pullRequest.Mergeable == queries.MergeableStateConflicting) &&
		pullRequest.State == queries.PullRequestStateOpen {
		if err := blockMerge(ctx, api, "merge conflict"); err != nil {
			return err
		}
		// remove label if configured and send message
		if cfg.Merge.NotifyOnConflict &&
			cfg.Merge.RequireAutomergeLabel &&
			hasAutomergeLabel {
			for _, label := range sortedKeys(pullRequestAutomergeLabels) {
				if err := api.RemoveLabel(ctx, label); err != nil {
					return err
				}
			}
			body := fmt.Sprintf(
				"\nThis PR currently has a merge conflict. Please resolve this and then re-add the `%s` label.\n",
				automergeLabelStr(cfg.Merge.AutomergeLabel))
			if err := api.CreateComment(ctx, body); err != nil {
				return err
			}
		}
		return nil
	}

	blacklistLabels := setIntersection(
		newStringSet(cfg.Merge.BlacklistLabels), newStringSet(pullRequest.Labels))
	blockingLabels := setIntersection(
		newStringSet(cfg.Merge.BlockingLabels), newStringSet(pullRequest.Labels))
	if len(blacklistLabels) > 0 {
		return blockMerge(ctx, api,
			fmt.Sprintf("has blacklist_labels: %s", pySetRepr(blacklistLabels)))
	}
	if len(blockingLabels) > 0 {
		return blockMerge(ctx, api,
			fmt.Sprintf("has merge.blocking_labels: %s", pySetRepr(blockingLabels)))
	}

	titleBlocker := getBlockingTitleRegex(cfg)

	if titleBlocker.Pattern != "" {
		// NOTE(port): Python uses the rure (Rust) regex engine for
		// user-supplied patterns; Go's RE2 is the closest equivalent. An
		// invalid pattern dequeues the PR with a status message, matching the
		// Python RegexError handling.
		titleRegex, err := regexp.Compile(titleBlocker.Pattern)
		if err != nil {
			if err := setStatus(
				fmt.Sprintf("⚠️ Invalid blocking_title_regex: %s", pyStrRepr(titleBlocker.Pattern)),
				nil); err != nil {
				return err
			}
			return api.Dequeue(ctx)
		}
		if titleRegex.MatchString(pullRequest.Title) {
			return blockMerge(ctx, api,
				fmt.Sprintf("title matches %s: %s", titleBlocker.ConfigKey, pyStrRepr(titleBlocker.Pattern)))
		}
	}

	if isDraftPullRequest {
		return blockMerge(ctx, api, "pull request is in draft state")
	}

	if cfg.Merge.BlockOnReviewsRequested && len(m.ReviewRequests) > 0 {
		names := make([]string, 0, len(m.ReviewRequests))
		for _, reviewRequest := range m.ReviewRequests {
			names = append(names, reviewRequest.Name)
		}
		return blockMerge(ctx, api, fmt.Sprintf("reviews requested: %s", pyListRepr(names)))
	}

	if pullRequest.State == queries.PullRequestStateMerged {
		log.Info("pull request merged",
			"config.merge.delete_branch_on_merge", cfg.Merge.DeleteBranchOnMerge)
		if err := api.Dequeue(ctx); err != nil {
			return err
		}
		if !cfg.Merge.DeleteBranchOnMerge ||
			pullRequest.IsCrossRepository ||
			m.Repository.DeleteBranchOnMerge {
			return nil
		}
		prCount, err := api.PullRequestsForRef(ctx, pullRequest.HeadRefName)
		if err != nil {
			return err
		}
		// if we couldn't access the dependent PR count or we have dependent PRs
		// we will abort deleting this branch.
		if prCount == nil || *prCount > 0 {
			log.Info("skipping branch deletion because of dependent PRs", "pr_count", prCount)
			return nil
		}
		return api.DeleteBranch(ctx, pullRequest.HeadRefName)
	}

	if pullRequest.State == queries.PullRequestStateClosed {
		return api.Dequeue(ctx)
	}

	// MergeStateStatus.UNSTABLE means that the pr is mergeable but has failing
	// status checks. we may want to handle this via config. The Python
	// implementation takes no action either.

	requiredStatusChecks := getRequiredStatusChecks(m.BranchProtection, m.RulesetRules)
	if cfg.Merge.BlockOnNeutralRequiredCheckRuns {
		neutralCheckRuns := map[string]bool{}
		for _, checkRun := range deduplicateCheckRuns(m.CheckRuns) {
			if checkRun.Conclusion != nil &&
				*checkRun.Conclusion == queries.CheckConclusionStateNeutral {
				neutralCheckRuns[checkRun.Name] = true
			}
		}

		failingRequiredStatusChecks := setIntersection(neutralCheckRuns, requiredStatusChecks)
		if len(failingRequiredStatusChecks) > 0 {
			return blockMerge(ctx, api,
				fmt.Sprintf("neutral required check runs: %s", pySetRepr(failingRequiredStatusChecks)))
		}
	}

	waitForChecks := false
	var missingRequiredStatusChecks map[string]bool
	if pullRequest.MergeStateStatus == queries.MergeStateStatusBlocked ||
		pullRequest.MergeStateStatus == queries.MergeStateStatusBehind {
		// figure out why we can't merge. There isn't a way to get this simply from the Github API. We need to find out ourselves.
		//
		// I think it's possible to find out blockers from branch protection issues
		// https://developer.github.com/v4/object/branchprotectionrule/?#fields
		//
		// - missing reviews
		// - blocking reviews
		// - missing required status checks
		// - failing required status checks
		// - branch not up to date (should be handled before this)
		// - missing required signature

		if pullRequest.ReviewDecision != nil &&
			*pullRequest.ReviewDecision == queries.PullRequestReviewDecisionReviewRequired {
			return blockMerge(ctx, api, "missing required reviews")
		}

		if pullRequest.ReviewDecision != nil &&
			*pullRequest.ReviewDecision == queries.PullRequestReviewDecisionChangesRequested {
			return blockMerge(ctx, api, "changes requested")
		}

		if requiresConversationResolution(m.BranchProtection, m.RulesetRules) &&
			pullRequest.ReviewThreads.Nodes != nil {
			for _, reviewThread := range pullRequest.ReviewThreads.Nodes {
				if !reviewThread.IsCollapsed {
					return blockMerge(ctx, api, "unresolved conversations")
				}
			}
		}

		passing := map[string]bool{}

		if requiresStatusChecks(m.BranchProtection, m.RulesetRules) {
			var skippableContexts []string
			var failingContexts []string
			var pendingContexts []string
			var passingContexts []string
			for _, statusContext := range m.Contexts {
				// handle dont_wait_on_status_checks. We want to consider a
				// status_check failed if it is incomplete and in the
				// configuration.
				if slices.Contains(cfg.Merge.DontWaitOnStatusChecks, statusContext.Context) &&
					(statusContext.State == queries.StatusStateExpected ||
						statusContext.State == queries.StatusStatePending) {
					skippableContexts = append(skippableContexts, statusContext.Context)
					continue
				}
				switch statusContext.State {
				case queries.StatusStateError, queries.StatusStateFailure:
					failingContexts = append(failingContexts, statusContext.Context)
				case queries.StatusStateExpected, queries.StatusStatePending:
					pendingContexts = append(pendingContexts, statusContext.Context)
				default:
					// assert status_context.state == StatusState.SUCCESS
					passingContexts = append(passingContexts, statusContext.Context)
				}
			}
			_ = pendingContexts

			for _, checkRun := range deduplicateCheckRuns(m.CheckRuns) {
				if slices.Contains(cfg.Merge.DontWaitOnStatusChecks, checkRun.Name) &&
					(checkRun.Conclusion == nil ||
						*checkRun.Conclusion == queries.CheckConclusionStateNeutral) {
					skippableContexts = append(skippableContexts, checkRun.Name)
					continue
				}
				if checkRun.Conclusion == nil {
					continue
				}
				switch *checkRun.Conclusion {
				case queries.CheckConclusionStateSkipped, queries.CheckConclusionStateSuccess:
					passingContexts = append(passingContexts, checkRun.Name)
				}
				switch *checkRun.Conclusion {
				case queries.CheckConclusionStateActionRequired,
					queries.CheckConclusionStateFailure,
					queries.CheckConclusionStateTimedOut,
					queries.CheckConclusionStateCancelled,
					queries.CheckConclusionStateStale:
					failingContexts = append(failingContexts, checkRun.Name)
				}
			}
			passing = newStringSet(passingContexts)
			failing := newStringSet(failingContexts)
			// we have failing statuses that are required
			failingRequiredStatusChecks := setIntersection(failing, requiredStatusChecks)
			// GitHub has undocumented logic for travis-ci checks in GitHub
			// branch protection rules. GitHub compresses
			// "continuous-integration/travis-ci/{pr,push}" to
			// "continuous-integration/travis-ci". There is only special handling
			// for these specific checks.
			if requiredStatusChecks["continuous-integration/travis-ci"] {
				if failing["continuous-integration/travis-ci/pr"] {
					failingRequiredStatusChecks["continuous-integration/travis-ci/pr"] = true
				}
				if failing["continuous-integration/travis-ci/push"] {
					failingRequiredStatusChecks["continuous-integration/travis-ci/push"] = true
				}
				// either check can satisfy continuous-integration/travis-ci, but
				// if either fails they'll also block the merge.
				if passing["continuous-integration/travis-ci/pr"] ||
					passing["continuous-integration/travis-ci/push"] {
					delete(requiredStatusChecks, "continuous-integration/travis-ci")
				}
			}
			if len(failingRequiredStatusChecks) > 0 {
				// NOTE(chdsbd): We need to skip this PR because it would block
				// the merge queue. We may be able to bump it to the back of the
				// queue, but it's easier just to remove it all together. There
				// is a similar question for the review counting.
				return blockMerge(ctx, api,
					fmt.Sprintf("failing required status checks: %s", pySetRepr(failingRequiredStatusChecks)))
			}
			if len(skippableContexts) > 0 {
				if m.Merging {
					if m.SkippableCheckTimeout > 0 {
						if err := setStatus(
							fmt.Sprintf("⛴ merging PR (waiting a bit for dont_wait_on_status_checks: %s)",
								pyListRepr(skippableContexts)),
							nil); err != nil {
							return err
						}
						return kerrors.ErrRetryForSkippableChecks
					}
					log.Warn("timeout reached waiting for dont_wait_on_status_checks",
						"skippable_contexts", skippableContexts)
					if err := setStatus(
						fmt.Sprintf("⚠️ timeout reached for dont_wait_on_status_checks: %s",
							pyListRepr(skippableContexts)),
						nil); err != nil {
						return err
					}
				}
				return setStatus(
					fmt.Sprintf("🛑 not waiting for dont_wait_on_status_checks: %s",
						pyListRepr(skippableContexts)),
					nil)
			}
		}

		missingRequiredStatusChecks = setDifference(requiredStatusChecks, passing)
		waitForChecks = requiresStatusChecks(m.BranchProtection, m.RulesetRules) &&
			len(missingRequiredStatusChecks) > 0

		if cfg.Merge.UpdateBranchImmediately && needBranchUpdate {
			if err := setStatus("🔄 updating branch",
				strPtr("branch updated because `merge.update_branch_immediately = true` is configured.")); err != nil {
				return err
			}
			if err := api.UpdateBranch(ctx); err != nil {
				return err
			}
			if m.Merging {
				return kerrors.ErrPollForever
			}
			return nil
		}

		if m.Merging {
			// prioritize branch updates over waiting for status checks to complete
			if cfg.Merge.OptimisticUpdates {
				if needBranchUpdate {
					if err := setStatus("⛴ merging PR (updating branch)", nil); err != nil {
						return err
					}
					if err := api.UpdateBranch(ctx); err != nil {
						return err
					}
					return kerrors.ErrPollForever
				}
				if waitForChecks {
					if err := setStatus(
						fmt.Sprintf("⛴ merging PR (waiting for status checks: %s)",
							pySetRepr(missingRequiredStatusChecks)),
						nil); err != nil {
						return err
					}
					return kerrors.ErrPollForever
				}
			} else {
				// almost the same as the pervious case, but we prioritize status checks
				// over branch updates.
				if waitForChecks {
					if err := setStatus(
						fmt.Sprintf("⛴ merging PR (waiting for status checks: %s)",
							pySetRepr(missingRequiredStatusChecks)),
						nil); err != nil {
						return err
					}
					return kerrors.ErrPollForever
				}
				if needBranchUpdate {
					if err := setStatus("⛴ merging PR (updating branch)", nil); err != nil {
						return err
					}
					if err := api.UpdateBranch(ctx); err != nil {
						return err
					}
					return kerrors.ErrPollForever
				}
			}
		}

		// if we reach this point and we don't need to wait for checks or update a branch
		// we've failed to calculate why the PR is blocked. This should _not_ happen
		// normally.
		//
		// We trigger an ApiCallException to trigger the retry handling. Usually
		if !(waitForChecks || needBranchUpdate) {
			codeownerReviewRequired := false
			if pullRequest.MergeStateStatus == queries.MergeStateStatusBlocked &&
				pullRequest.ReviewDecision == nil {
				for _, reviewRequest := range m.ReviewRequests {
					if reviewRequest.AsCodeOwner {
						codeownerReviewRequired = true
						break
					}
				}
			}
			log.Info("merge blocked for unknown reason",
				"merging", m.Merging,
				"wait_for_checks", waitForChecks,
				"need_branch_update", needBranchUpdate,
				"codeowner_review_required", codeownerReviewRequired)

			if m.Merging {
				// maybe we want to convert this to be a PollForever exception, but I'm not
				// confident enough that we'll always want to poll in this scenario.
				return &kerrors.APICallError{
					Method:     "kodiak/merged_blocked_unknown_reason",
					StatusCode: 0,
					Response:   []byte{},
				}
			}
			if !m.IsActiveMerge {
				if err := api.SetStatus(ctx,
					"Retrying (Merging blocked by GitHub requirements)", nil); err != nil {
					return err
				}
			}
			return api.Requeue(ctx)
		}
	}

	readyToMerge := !(waitForChecks || needBranchUpdate)

	if cfg.Merge.DoNotMerge {
		if waitForChecks {
			if err := setStatus(
				fmt.Sprintf("⌛️ waiting for required status checks: %s",
					pySetRepr(missingRequiredStatusChecks)),
				nil); err != nil {
				return err
			}
		} else if needBranchUpdate {
			if err := setStatus(
				"⚠️ need branch update (suggestion: use merge.update_branch_immediately with merge.do_not_merge)",
				strPtr(doNotMergeBranchUpdateMarkdown)); err != nil {
				return err
			}
		} else {
			if err := setStatus("✅ okay to merge", nil); err != nil {
				return err
			}
		}
		log.Info("eligible to merge, stopping because config.merge.do_not_merge is enabled.")
		return nil
	}

	// okay to merge if we reach this point.

	if (cfg.Merge.PrioritizeReadyToMerge && readyToMerge) || m.Merging {
		mergeArgs := GetMergeBody(cfg, mergeMethod, pullRequest, m.Commits)
		if err := setStatus("⛴ attempting to merge PR (merging)", nil); err != nil {
			return err
		}
		var mergeErr error
		// Use the Git Refs API to rebase merge.
		//
		// This preserves the rebased commits and their hashes. Using the
		// GitHub Pull Request API to rebase merge rewrites the rebased
		// commits, so the commit hashes change.
		//
		// For build systems that depend on commit hashes instead of tree
		// hashes, it's desirable to not rewrite commits.
		if mergeArgs.MergeMethod == config.MergeMethodRebaseFastForward {
			mergeErr = api.UpdateRef(ctx, pullRequest.BaseRefName, pullRequest.LatestSha)
		} else {
			mergeErr = api.Merge(ctx, string(mergeArgs.MergeMethod),
				mergeArgs.CommitTitle, mergeArgs.CommitMessage)
		}
		if mergeErr != nil {
			// if we encounter an internal server error (status code 500), it is
			// _not_ safe to retry. Instead we mark the pull request as unmergable
			// and require a user to re-enable Kodiak on the pull request.
			if !errors.Is(mergeErr, kerrors.ErrGitHubAPIInternalServerError) {
				return mergeErr
			}
			log.Warn("kodiak encountered GitHub API error merging pull request",
				"error", mergeErr)
			// We add the disable_bot_label to disable Kodiak from taking any
			// action to update, approve, comment, label, or merge.
			disableBotLabel := cfg.DisableBotLabel
			if err := api.AddLabel(ctx, disableBotLabel); err != nil {
				return err
			}

			if err := blockMerge(ctx, api, "Cannot merge due to GitHub API failure."); err != nil {
				return err
			}
			body := messages.Format(fmt.Sprintf(
				"\nThis PR could not be merged because the GitHub API returned an internal server error. To enable Kodiak on this pull request please remove the `%s` label.\n\nWhen the GitHub API returns an internal server error (HTTP status code 500), it is not safe for Kodiak to retry merging.\n\nFor more information please see https://kodiakhq.com/docs/troubleshooting#merge-errors\n",
				disableBotLabel))
			if err := api.CreateComment(ctx, body); err != nil {
				return err
			}
		} else {
			if err := setStatus("merge complete 🎉", nil); err != nil {
				return err
			}
		}
	} else {
		priorityMerge := cfg.Merge.PriorityMergeLabel != nil &&
			slices.Contains(pullRequest.Labels, *cfg.Merge.PriorityMergeLabel)
		positionInQueue, err := api.QueueForMerge(ctx, priorityMerge)
		if err != nil {
			return err
		}
		if positionInQueue == nil {
			// this case should be rare/impossible.
			log.Warn("couldn't find position for enqueued PR")
			return nil
		}
		ordinalPosition := ordinalize(*positionInQueue + 1)
		if !m.IsActiveMerge {
			if err := setStatus(
				fmt.Sprintf("📦 enqueued for merge (position=%s)", ordinalPosition),
				nil); err != nil {
				return err
			}
		} else {
			log.Info("not setting status message for enqueued job because is_active_merge=True")
		}
	}
	return nil
}
