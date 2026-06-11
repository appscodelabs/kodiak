// Ported from bot/kodiak/test_evaluation.py lines 219-1075
// (test_mock_pr_api through test_mergeable_method_override_with_label).
package evaluation

import (
	"context"
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/queries"
)

func e1MergeMethodPtr(m config.MergeMethod) *config.MergeMethod { return &m }

// e1StatusMsg returns the message of the first set_status call, failing the
// test immediately if set_status was never called.
func e1StatusMsg(t *testing.T, api *fakePRAPI) string {
	t.Helper()
	if len(api.SetStatusCalls) == 0 {
		t.Fatal("expected at least one set_status call")
	}
	return api.SetStatusCalls[0].Msg
}

// test_mock_pr_api verifies the Python MockPrApi mock itself; here we exercise
// the fakePRAPI call recording and Called().
func TestMockPRAPI(t *testing.T) {
	api := createAPI()
	if err := api.Dequeue(context.Background()); err != nil {
		t.Fatalf("dequeue returned unexpected error: %v", err)
	}
	assertEqual(t, api.Called(), true, "api.called")
}

// If we set is_active_merge, that means that in the merge queue the current PR
// is being updated/merged, so in the frontend we don't want to act on the PR
// because the PR is being handled.
func TestMergeableAbortIsActiveMerge(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.IsActiveMerge = true
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")

	// we don't want to set a status message from the frontend when the PR is being merged
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// If we have a problem finding or parsing a configuration error we should set
// a status and remove our item from the merge queue.
func TestMergeableConfigErrorSetsWarning(t *testing.T) {
	api := createAPI()
	brokenConfigStr := "something[invalid["
	brokenConfig, err := config.ParseToml(brokenConfigStr)
	if err == nil {
		t.Fatalf("expected TomlDecodeError, got config %+v", brokenConfig)
	}

	params := createMergeableParams()
	params.Config = nil
	params.ConfigError = err
	params.ConfigStr = brokenConfigStr
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e1StatusMsg(t, api), "Invalid configuration", "status message")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If our app id doesn't match the one in the config, we shouldn't touch the repo.
func TestMergeableDifferentAppID(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	appID := config.AppIDStr("1234567")
	cfg.AppID = &appID
	ourFakeAppID := "909090"

	params := createMergeableParams()
	params.Config = cfg
	params.AppID = ourFakeAppID
	runMergeable(t, api, params)
	assertEqual(t, len(api.DequeueCalls) > 0, true, "dequeue called")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should warn when we cannot retrieve branch protection settings.
func TestMergeableMissingBranchProtection(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.BranchProtection = nil
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "config error", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We don't need branch protection settings if rulesets are in use.
func TestMergeableMissingBranchProtectionWithRulesets(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.BranchProtection = nil
	params.RulesetRules = []queries.RulesetRule{
		{
			Type: "PULL_REQUEST",
			Parameters: &queries.PullRequestParameters{
				AllowedMergeMethods: []queries.PullRequestAllowedMergeMethods{
					queries.PullRequestAllowedMergeMethodsSquash,
				},
				RequiredReviewThreadResolution: false,
			},
		},
	}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertNotContains(t, e1StatusMsg(t, api), "config error", "status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")
}

func TestMergeableMissingPushAllowanceWithRulesets(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.RulesetRules = []queries.RulesetRule{
		{
			Type:       "UPDATE",
			Parameters: nil,
			RepositoryRuleset: &queries.RepositoryRuleset{
				BypassActors: &queries.RepositoryRulesetBypassActorConnection{
					Nodes: []*queries.RepositoryRulesetBypassActor{},
				},
			},
		},
	}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e1StatusMsg(t, api), "config error", "status message")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

func TestMergeableWithPushAllowanceWithRulesets(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.RulesetRules = []queries.RulesetRule{
		{
			Type:       "UPDATE",
			Parameters: nil,
			RepositoryRuleset: &queries.RepositoryRuleset{
				BypassActors: &queries.RepositoryRulesetBypassActorConnection{
					Nodes: []*queries.RepositoryRulesetBypassActor{
						{Actor: &queries.BypassActor{DatabaseID: i64Ptr(534524)}},
					},
				},
			},
		},
	}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertNotContains(t, e1StatusMsg(t, api), "config error", "status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")
}

// GitHub returns `bypassActors.nodes: [null]` (or with `actor: null`) when
// the App installation lacks permission to read the bypass actor — including
// when the bypass entry is for our own App. We can't tell whether the
// redacted entry is us or some other App, so we should not block the merge.
func TestMergeableWithPushAllowanceWithRulesetsRedactedActor(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.RulesetRules = []queries.RulesetRule{
		{
			Type:       "UPDATE",
			Parameters: nil,
			RepositoryRuleset: &queries.RepositoryRuleset{
				BypassActors: &queries.RepositoryRulesetBypassActorConnection{
					Nodes: []*queries.RepositoryRulesetBypassActor{nil},
				},
			},
		},
	}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertNotContains(t, e1StatusMsg(t, api), "config error", "status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")
}

// If we're missing an automerge label we should not merge the PR.
func TestMergeableMissingAutomergeLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.RequireAutomergeLabel = true
	pullRequest.Labels = []string{}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "Ignored (no automerge label:", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// No status message if show_missing_automerge_label_message is disabled
func TestMergeableMissingAutomergeLabelNoMessage(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.RequireAutomergeLabel = true
	cfg.Merge.ShowMissingAutomergeLabelMessage = false
	pullRequest.Labels = []string{}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We can work on a PR if we're missing labels and we have require_automerge_label disabled.
func TestMergeableMissingAutomergeLabelRequireAutomergeLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.RequireAutomergeLabel = false
	pullRequest.Labels = []string{}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e1StatusMsg(t, api), "enqueued for merge (position=4th)", "status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// blacklist labels should prevent merge
func TestMergeableHasBlacklistLabels(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.RequireAutomergeLabel = false
	cfg.Merge.BlacklistLabels = []string{"dont merge!"}
	pullRequest.Labels = []string{"bug", "dont merge!", "needs review"}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "has blacklist_labels", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// blocking labels should prevent merge
func TestMergeableHasBlockingLabels(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.RequireAutomergeLabel = false
	cfg.Merge.BlockingLabels = []string{"dont merge!"}
	pullRequest.Labels = []string{"bug", "dont merge!", "needs review"}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "has merge.blocking_labels", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// block merge if blacklist_title_regex matches pull request
func TestMergeableBlacklistTitleRegex(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	pullRequest.Title = "WIP: add new feature"
	cfg.Merge.BlacklistTitleRegex = "^WIP.*"

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "matches merge.blacklist_title_regex", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// block merge if blocking_title_regex matches pull request
func TestMergeableBlockingTitleRegex(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	pullRequest.Title = "WIP: add new feature"
	cfg.Merge.BlockingTitleRegex = "^WIP.*"

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "matches merge.blocking_title_regex", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// raise config error if regex is invalid.
func TestMergeableBlockingTitleRegexInvalid(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.BlockingTitleRegex = "^((?!chore(deps).*)"

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e1StatusMsg(t, api), "Invalid blocking_title_regex", "status message")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should default to "^WIP.*" if unset.
func TestMergeableBlockingTitleRegexDefault(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	pullRequest.Title = "WIP: add new feature"
	assertEqual(t, cfg.Merge.BlockingTitleRegex,
		":::|||kodiak|||internal|||reserved|||:::", "blocking_title_regex unset sentinel")

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "matches merge.blocking_title_regex", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should be able to disable the title regex by setting it to empty string.
func TestMergeableBlockingTitleDisabled(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	pullRequest.Title = "WIP: add new feature"
	cfg.Merge.BlockingTitleRegex = "WIP.*"

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest

	// verify by default we block
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "matches merge.blocking_title_regex", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")

	// should disable blocking_title_regex by setting to empty string
	cfg.Merge.BlockingTitleRegex = ""
	api = createAPI()
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// Ensure Kodiak uses a linear time regex engine.
//
// When using an exponential engine this test will timeout.
func TestMergeableBlacklistTitleMatchWithExpRegex(t *testing.T) {
	// a ReDos regex and accompanying string
	// via: https://en.wikipedia.org/wiki/ReDoS#Vulnerable_regexes_in_online_repositories
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	// NOTE(port): the Python test uses mocker.spy on kodiak.evaluation's `re`
	// module to verify the regex search executed. Go cannot patch package
	// functions, so instead we assert that evaluation proceeded past the
	// title-regex check (the PR was queued for merge), which requires
	// evaluating the regex. Go's regexp package is RE2 and guarantees
	// linear-time matching, so a backtracking (exponential) engine would hang
	// this test just like in Python.
	cfg.Merge.BlacklistTitleRegex = "^(a+)+$"
	pullRequest.Title = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!"

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	// we don't really care about the result for this so long as this test
	// doesn't hang the entire suite.
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "we should hit our regex search")
}

// block merge if pull request is in draft state
//
// `mergeStateStatus.DRAFT` is being removed 2021-01-01.
// https://docs.github.com/en/free-pro-team@latest/graphql/overview/breaking-changes#changes-scheduled-for-2021-01-01
func TestMergeableDraftPullRequest(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()

	pullRequest.MergeStateStatus = queries.MergeStateStatusDraft

	params := createMergeableParams()
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "in draft state", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// block merge if pull request is in draft state.
//
// Test using the `isDraft` field. `mergeStateStatus.DRAFT` is being removed 2021-01-01.
// https://docs.github.com/en/free-pro-team@latest/graphql/overview/breaking-changes#changes-scheduled-for-2021-01-01
func TestMergeableDraftPullRequestIsDraftField(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()

	pullRequest.IsDraft = true

	params := createMergeableParams()
	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "cannot merge", "status message")
	assertContains(t, e1StatusMsg(t, api), "in draft state", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// block merge if configured merge method is not enabled
func TestMergeableInvalidMergeMethod(t *testing.T) {
	api := createAPI()
	cfg := createConfig()

	cfg.Merge.Method = e1MergeMethodPtr(config.MergeMethodSquash)

	params := createMergeableParams()
	params.Config = cfg
	params.ValidMergeMethods = []config.MergeMethod{config.MergeMethodMerge}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e1StatusMsg(t, api), "config error", "status message")
	assertContains(t, e1StatusMsg(t, api),
		"configured merge.method 'squash' is invalid", "status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// Should default to `merge` commits.
func TestMergeableDefaultMergeMethod(t *testing.T) {
	api := createAPI()
	cfg := createConfig()

	if cfg.Merge.Method != nil {
		t.Fatal("we shouldn't have specified a value for the merge method. We want to allow the default.")
	}

	params := createMergeableParams()
	params.Config = cfg
	params.Merging = true
	runMergeable(t, api, params)
	if len(api.MergeCalls) != 1 {
		t.Fatalf("merge call count: got %d, want 1", len(api.MergeCalls))
	}
	assertEqual(t, api.MergeCalls[0].MergeMethod, string(config.MergeMethodMerge), "merge_method")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
}

// If an account only has one merge method configured, use that if they haven't
// specified an option.
func TestMergeableSingleMergeMethod(t *testing.T) {
	api := createAPI()
	cfg := createConfig()

	if cfg.Merge.Method != nil {
		t.Fatal("we must not specify a default for this to work")
	}

	params := createMergeableParams()
	params.Config = cfg
	params.ValidMergeMethods = []config.MergeMethod{
		// Kodiak should select the only valid merge method if `merge.method`
		// is not configured.
		config.MergeMethodRebase,
	}
	params.Merging = true
	runMergeable(t, api, params)
	if len(api.MergeCalls) != 1 {
		t.Fatalf("merge call count: got %d, want 1", len(api.MergeCalls))
	}
	assertEqual(t, api.MergeCalls[0].MergeMethod, "rebase", "merge_method")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If we have two options available, choose the first one, based on our ordered
// list of "merge", "squash", "rebase".
func TestMergeableTwoMergeMethods(t *testing.T) {
	api := createAPI()
	cfg := createConfig()

	if cfg.Merge.Method != nil {
		t.Fatal("we must not specify a default for this to work")
	}

	params := createMergeableParams()
	params.Config = cfg
	params.ValidMergeMethods = []config.MergeMethod{
		// Kodiak should select the first valid merge method if `merge.method`
		// is not configured.
		config.MergeMethodSquash,
		config.MergeMethodRebase,
	}
	params.Merging = true
	runMergeable(t, api, params)
	if len(api.MergeCalls) != 1 {
		t.Fatalf("merge call count: got %d, want 1", len(api.MergeCalls))
	}
	assertEqual(t, api.MergeCalls[0].MergeMethod, "squash", "merge_method")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should always have at least one valid_merge_method in production, but
// this is just a test to make sure we handle this case anyway.
func TestMergeableNoValidMethods(t *testing.T) {
	api := createAPI()
	cfg := createConfig()

	if cfg.Merge.Method != nil {
		t.Fatal("we must not specify a default for this to work")
	}

	params := createMergeableParams()
	params.Config = cfg
	params.ValidMergeMethods = []config.MergeMethod{}
	runMergeable(t, api, params)
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e1StatusMsg(t, api),
		"configured merge.method 'merge' is invalid.", "status message")

	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should be able to override merge methods with a label.
func TestMergeableMethodOverrideWithLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()

	cfg.Merge.Method = e1MergeMethodPtr(config.MergeMethodSquash)
	overrideLabels := []string{
		// basic
		`kodiak:merge.method='rebase'`,
		// spacing
		`kodiak:merge.method= 'rebase'`,
		// more spacing
		`kodiak:merge.method = 'rebase'`,
		// full spacing
		`kodiak: merge.method = 'rebase'`,
		// try with double quotes
		`kodiak:merge.method="rebase"`,
	}
	for index, overrideLabel := range overrideLabels {
		pullRequest.Labels = []string{"automerge", overrideLabel}

		params := createMergeableParams()
		params.Config = cfg
		params.PullRequest = pullRequest
		params.Merging = true
		runMergeable(t, api, params)
		if len(api.MergeCalls) != index+1 {
			t.Fatalf("merge call count: got %d, want %d", len(api.MergeCalls), index+1)
		}
		assertEqual(t, api.MergeCalls[0].MergeMethod, string(config.MergeMethodRebase), "merge_method")

		assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
		assertEqual(t, len(api.DequeueCalls), 0, "dequeue not called")
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	}
}
