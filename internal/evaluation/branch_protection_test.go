// Ported from bot/kodiak/tests/evaluation/test_branch_protection.py.
package evaluation

import (
	"context"
	"errors"
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// file-local helpers, prefixed bp to avoid collisions with other ported files.
func bpBoolPtr(b bool) *bool                                    { return &b }
func bpMergeMethodPtr(m config.MergeMethod) *config.MergeMethod { return &m }

// We should warn when user is missing a push allowance with restrictsPushes
// enabled. If Kodiak isn't given an allowance it won't be able to merge pull
// requests and will get a mysterious "merge blocked by GitHub requirements".
func TestMergeableMissingPushAllowance(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	branchProtection := createBranchProtection()
	branchProtection.RestrictsPushes = true
	branchProtection.PushAllowances = queries.NodeListPushAllowance{Nodes: []queries.PushAllowance{}}
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "config error", "set_status message")
	assertContains(t, api.SetStatusCalls[0].Msg, "missing push allowance for Kodiak", "set_status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// When restrictsPushes is enabled, but Kodiak is added as a push allowance, we
// should not raise a configuration error. We should let the merge continue
// unimpeded.
func TestMergeableMissingPushAllowanceCorrect(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	branchProtection := createBranchProtection()
	branchProtection.RestrictsPushes = true
	branchProtection.PushAllowances = queries.NodeListPushAllowance{
		Nodes: []queries.PushAllowance{
			{Actor: queries.PushAllowanceActor{DatabaseID: i64Ptr(534524)}},
		},
	}
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// Verify we can handle a null databaseId for the PushAllowanceActor
func TestMergeableMissingPushAllowanceCorrectNullDatabaseID(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	branchProtection := createBranchProtection()
	branchProtection.RestrictsPushes = true
	branchProtection.PushAllowances = queries.NodeListPushAllowance{
		Nodes: []queries.PushAllowance{
			{Actor: queries.PushAllowanceActor{DatabaseID: nil}},
			{Actor: queries.PushAllowanceActor{DatabaseID: i64Ptr(534524)}},
		},
	}
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// When merge.do_not_merge is enabled, we should ignore any issues with restrictPushes because Kodiak isn't pushing.
func TestMergeableMissingPushAllowanceMergeDoNotMerge(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	branchProtection := createBranchProtection()
	cfg := createConfig()

	branchProtection.RestrictsPushes = true
	cfg.Merge.DoNotMerge = true
	branchProtection.PushAllowances = queries.NodeListPushAllowance{Nodes: []queries.PushAllowance{}}
	params.Config = cfg
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, api.SetStatusCalls[0].Msg, "✅ okay to merge", "set_status message")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// requiresCommitSignatures doesn't work with Kodiak when rebase is configured
//
// https://github.com/chdsbd/kodiak/issues/89
func TestMergeableRequiresCommitSignaturesRebase(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	branchProtection := createBranchProtection()
	cfg := createConfig()

	branchProtection.RequiresCommitSignatures = true
	cfg.Merge.Method = bpMergeMethodPtr(config.MergeMethodRebase)
	params.Config = cfg
	params.BranchProtection = branchProtection
	params.ValidMergeMethods = []config.MergeMethod{config.MergeMethodRebase}
	runMergeable(t, api, params)
	assertContains(t, api.SetStatusCalls[0].Msg,
		`"Require signed commits" branch protection is only supported`,
		"set_status message")

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// requiresCommitSignatures works with merge commits and squash
//
// https://github.com/chdsbd/kodiak/issues/89
func TestMergeableRequiresCommitSignaturesSquashAndMerge(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	branchProtection := createBranchProtection()

	branchProtection.RequiresCommitSignatures = true
	for index, method := range []config.MergeMethod{config.MergeMethodSquash, config.MergeMethodMerge} {
		cfg.Merge.Method = bpMergeMethodPtr(method)
		params := createMergeableParams()
		params.Config = cfg
		params.BranchProtection = branchProtection
		params.ValidMergeMethods = []config.MergeMethod{method}
		runMergeable(t, api, params)
		assertEqual(t, len(api.SetStatusCalls), index+1, "set_status call count")
		assertContains(t, api.SetStatusCalls[index].Msg, "enqueued for merge", "set_status message")
		assertEqual(t, len(api.QueueForMergeCalls), index+1, "queue_for_merge call count")
		assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")

		// verify we haven't tried to update/merge the PR
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
		assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	}
}

// Don't merge when a review is required.
func TestMergeableMissingRequiredReview(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	pullRequest.ReviewDecision = reviewDecisionPtr(queries.PullRequestReviewDecisionReviewRequired)
	// previously with code owner blocked PRs Kodiak would update the pull
	// request, even if the pull request was blocked from merging.
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind

	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "missing required review", "set_status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// Don't merge when a changes requested.
func TestMergeableMissingChangesRequested(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	pullRequest.ReviewDecision = reviewDecisionPtr(queries.PullRequestReviewDecisionChangesRequested)

	params.PullRequest = pullRequest
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "changes requested", "set_status message")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// update branch immediately if configured
func TestMergeableUpdateBranchImmediately(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	cfg.Merge.UpdateBranchImmediately = true

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")
	if api.SetStatusCalls[0].MarkdownContent == nil {
		t.Fatal("set_status markdown_content is nil")
	}
	assertContains(t, *api.SetStatusCalls[0].MarkdownContent, "branch updated because", "set_status markdown_content")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// update branch immediately if configured. When we are merging we should raise the PollForever exception to keep the merge loop going instead of returning.
func TestMergeableUpdateBranchImmediatelyModeMerging(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	cfg.Merge.UpdateBranchImmediately = true

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")
	if api.SetStatusCalls[0].MarkdownContent == nil {
		t.Fatal("set_status markdown_content is nil")
	}
	assertContains(t, *api.SetStatusCalls[0].MarkdownContent, "branch updated because", "set_status markdown_content")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// prioritize branch update over waiting for checks when merging if merge.optimistic_updates enabled.
func TestMergeableOptimisticUpdateNeedBranchUpdate(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()

	cfg.Merge.OptimisticUpdates = true
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "ci/test-api"

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// prioritize waiting for checks over branch updates when merging if merge.optimistic_updates is disabled.
func TestMergeableNeedBranchUpdate(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()

	cfg.Merge.OptimisticUpdates = false
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "ci/test-api"

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg,
		"merging PR (waiting for status checks: {'ci/test-api'})",
		"set_status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
}

// If a PR has pending status checks or is behind, we still consider it eligible for merge and throw it in the merge queue.
func TestMergeableQueueInProgress(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()

	cfg.Merge.OptimisticUpdates = false
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "ci/test-api"

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge", "set_status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
}

// If a PR has pending status checks or is behind, we still consider it eligible for merge and throw it in the merge queue.
//
// regression test to verify that with config.merge.prioritize_ready_to_merge =
// true we don't attempt to merge a PR directly but called queue_for_merge
// instead. If the status checks haven't passed or the branch needs an update
// it's not good to be merged directly, but it can be queued for the merge
// queue.
func TestMergeableQueueInProgressWithReadyToMerge(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()

	cfg.Merge.OptimisticUpdates = false
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "ci/test-api"
	cfg.Merge.PrioritizeReadyToMerge = true

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge", "set_status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
}

// test merge.optimistic_updates when we don't need a branch update. Since merge.optimistic_updates is enabled we should wait_for_checks
func TestMergeableOptimisticUpdateWaitForChecks(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	statusContext := createContext()

	cfg.Merge.OptimisticUpdates = true
	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStrictStatusChecks = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "ci/test-api"

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	//
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertContains(t, api.SetStatusCalls[0].Msg,
		"merging PR (waiting for status checks: {'ci/test-api'})",
		"set_status message")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// test merge.optimistic_updates when we don't have checks to wait for. Since merge.optimistic_updates is disabled we should update the branch.
func TestMergeableWaitForChecks(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg.Merge.OptimisticUpdates = false
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")
	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// merge.do_not_merge is only useful with merge.update_branch_immediately,
// Test when PR doesn't need update but is waiting for checks to finish.
func TestMergeableDoNotMergeWithUpdateBranchImmediatelyWaitingForChecks(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	pullRequest := createPullRequest()
	cfg := createConfig()
	branchProtection := createBranchProtection()
	statusContext := createContext()
	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	cfg.Merge.DoNotMerge = true
	cfg.Merge.UpdateBranchImmediately = true
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.Context = "ci/test-api"
	statusContext.State = queries.StatusStatePending

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls) > 0, true, "set_status called")
	assertContains(t, api.SetStatusCalls[0].Msg,
		"waiting for required status checks: {'ci/test-api'}",
		"set_status message")

	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// Kodiak should not update PR if user is blacklisted.
func TestMergeableUpdateUsernameBlacklist(t *testing.T) {
	blacklistConfig := createConfig()
	blacklistConfig.Update.Always = true
	blacklistConfig.Update.BlacklistUsernames = []string{"mr-test"}
	blacklistConfig.Update.RequireAutomergeLabel = true

	ignoredConfig := createConfig()
	ignoredConfig.Update.Always = true
	ignoredConfig.Update.IgnoredUsernames = []string{"mr-test"}
	ignoredConfig.Update.RequireAutomergeLabel = true

	pullRequest := createPullRequest()
	if pullRequest.Author == nil {
		t.Fatal("pull_request.author is nil")
	}
	pullRequest.Author.Login = "mr-test"
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind

	branchProtection := createBranchProtection()
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}

	checkRun := createCheckRun()
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	for _, isMerging := range []bool{true, false} {
		for _, cfg := range []*config.V1{blacklistConfig, ignoredConfig} {
			api := createAPI()
			params := createMergeableParams()
			params.Config = cfg
			params.PullRequest = pullRequest
			params.BranchProtection = branchProtection
			params.CheckRuns = []queries.CheckRun{checkRun}
			params.Merging = isMerging
			runMergeable(t, api, params)
			assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
			assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
			assertContains(t, api.SetStatusCalls[0].Msg, "updates blocked by update.", "set_status message")
			assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

			assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
			assertEqual(t, len(api.MergeCalls), 0, "merge call count")
		}
	}
}

// When we're merging with update.always enabled we don't want to update the
// branch using our update.always logic. We want to update using our merging
// logic so we trigger the PollForever exception necessary to continue our
// merge loop. If we used the update.always logic we'd eject a PR if it became
// out of sync during merge.
func TestMergeableUpdateAlwaysEnabledMergingBehindPullRequest(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()

	cfg.Update.Always = true
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStrictStatusChecks = true

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// When requiresConversationResolution is enabled and we have unresolved review
// threads, we should block merge.
func TestMergeableRequiresConversationResolution(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	pullRequest.ReviewThreads.Nodes = []queries.ReviewThread{
		{IsCollapsed: true},
		{IsCollapsed: false},
	}
	branchProtection.RequiresConversationResolution = bpBoolPtr(true)

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "cannot merge (unresolved conversations)", "set_status message")
}

// We should only block merge for unresolved review threads if
// requiresConversationResolution is enabled.
func TestMergeableUncollapsedReviews(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	pullRequest.ReviewThreads.Nodes = []queries.ReviewThread{
		{IsCollapsed: true},
		{IsCollapsed: false},
	}

	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "Merging blocked by GitHub requirements", "set_status message")
}
