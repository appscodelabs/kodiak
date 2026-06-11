// Tests ported from bot/kodiak/test_evaluation.py lines 1691-2450
// (test_mergeable_merge through test_mergeable_priority_merge_label).
package evaluation

import (
	"fmt"
	"testing"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/messages"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// If we're merging we should call api.merge
func TestMergeableMerge(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()

	params.Merging = true
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) != 2 {
		t.Fatalf("set_status call count: got %d, want 2", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "attempting to merge PR (merging)", "set_status message")
	assertEqual(t, api.SetStatusCalls[1].Msg, "merge complete 🎉", "set_status message")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertEqual(t, len(api.MergeCalls), 1, "merge call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If we're attempting to merge from the frontend we should place the PR on the queue.
//
// If permission information is unavailable (None) we should not set a status check.
func TestMergeableQueueForMergeNoPosition(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	api.QueueForMergeReturnValue = nil
	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
}

// This is the happy case where we want to enqueue the PR for merge.
func TestMergeablePassing(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// Test merge.automerge_label array allows a pull request to be merged.
func TestMergeableMergeAutomergeLabels(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.PullRequest.Labels = []string{"ship it!"}
	params.Config.Merge.AutomergeLabel = config.Labels{"ship it!"}
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// When a PR isn't in the queue but needs an update we should enqueue it for merge.
func TestMergeableNeedUpdate(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// merge.do_not_merge should disable merging a PR.
func TestMergeableDoNotMerge(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Config.Merge.DoNotMerge = true
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "okay to merge", "set_status message")

	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// merge.do_not_merge without merge.update_branch_immediately means that any PR
// behind target will never get updated. We should display a warning about
// this.
func TestMergeableDoNotMergeBehindNoUpdateImmediately(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Config.Merge.DoNotMerge = true
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusBehind

	runMergeable(t, api, params)
	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg,
		"need branch update (suggestion: use merge.update_branch_immediately",
		"set_status message")
	if api.SetStatusCalls[0].MarkdownContent == nil {
		t.Error("set_status markdown_content should be a string")
	}

	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// merge.do_not_merge is only useful with merge.update_branch_immediately,
// Test when PR doesn't need update.
func TestMergeableDoNotMergeWithUpdateBranchImmediatelyNoUpdate(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()

	params.Config.Merge.DoNotMerge = true
	params.Config.Merge.UpdateBranchImmediately = true
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "okay to merge", "set_status message")

	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// merge.do_not_merge is only useful with merge.update_branch_immediately,
// Test when PR needs update.
func TestMergeableDoNotMergeWithUpdateBranchImmediatelyNeedUpdate(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	params.Config.Merge.DoNotMerge = true
	params.Config.Merge.UpdateBranchImmediately = true
	runMergeable(t, api, params)

	assertEqual(t, len(api.UpdateBranchCalls) > 0, true, "update_branch called")
	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "updating branch", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// if we enounter too many errors calling GitHub api_call_retry_timeout will be zero. we should notify users via a status check.
func TestMergeableApiCallRetryTimeout(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	apiCallErr := apiCallError{
		apiName:    "pull_request/merge",
		httpStatus: "405",
		// NOTE(port): Python wraps the response body with str(b'...') which
		// produces the bytes repr; only the substring below is asserted.
		responseBody: "b'{\"message\":\"This branch can't be rebased\",\"documentation_url\":\"https://developer.github.com/v3/pulls/#merge-a-pull-request-merge-button\"}'",
	}
	params.APICallRetriesRemaining = 0
	params.APICallErrors = []messages.APICallRetry{apiCallErr}
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg,
		"problem contacting GitHub API with method 'pull_request/merge'",
		"set_status message")
	if api.SetStatusCalls[0].MarkdownContent == nil {
		t.Fatal("set_status markdown_content is nil")
	}
	assertContains(t, *api.SetStatusCalls[0].MarkdownContent,
		"merge-a-pull-request-merge-button",
		"set_status markdown content")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if we enounter too many errors calling GitHub api_call_retry_timeout will be zero. we should notify users via a status check.
//
// This shouldn't really be possible in reality, but this is a test where the method name is None but the timeout is zero.
func TestMergeableApiCallRetryTimeoutMissingMethod(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()

	params.APICallRetriesRemaining = 0
	params.APICallErrors = nil
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) == 0 {
		t.Fatal("set_status not called")
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "problem contacting GitHub API", "set_status message")
	if api.SetStatusCalls[0].MarkdownContent != nil {
		t.Errorf("set_status markdown_content: got %q, want nil", *api.SetStatusCalls[0].MarkdownContent)
	}
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// Test happy case with update.always enabled. We should shouldn't see any
// difference with update.always enabled.
func TestMergeablePassingUpdateAlwaysEnabled(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()

	params.Config.Update.Always = true
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// If a PR is opened by a user on the `approve.auto_approve_usernames` list Kodiak should approve the PR.
func TestMergeableAutoApproveUsername(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Config.Approve.AutoApproveUsernames = []string{"dependency-updater"}
	if params.PullRequest.Author == nil {
		t.Fatal("pull request author is nil")
	}
	params.PullRequest.Author.Login = "dependency-updater"
	params.BotReviews = nil
	runMergeable(t, api, params)
	assertEqual(t, len(api.ApprovePullRequestCalls), 1, "approve_pull_request call count")
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If a PR has a label which is in the `approve.auto_approve_labels` list Kodiak should approve the PR.
func TestMergeableAutoApproveLabel(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Config.Approve.AutoApproveLabels = []string{"autoapprove"}
	params.PullRequest.Labels = append(params.PullRequest.Labels, "autoapprove")
	params.BotReviews = nil
	runMergeable(t, api, params)
	assertEqual(t, len(api.ApprovePullRequestCalls), 1, "approve_pull_request call count")
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If a PR is opened by a user on the `approve.auto_approve_usernames` list Kodiak should approve the PR.
//
// If we have an existing, valid approval, we should not add another.
func TestMergeableAutoApproveExistingApproval(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	review := createReview()
	params.Config.Approve.AutoApproveUsernames = []string{"dependency-updater"}
	if params.PullRequest.Author == nil {
		t.Fatal("pull request author is nil")
	}
	params.PullRequest.Author.Login = "dependency-updater"
	review.Author.Login = "kodiak-test-app"
	review.State = queries.PRReviewStateApproved
	params.BotReviews = []queries.PRReview{review}
	runMergeable(t, api, params)
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If a PR is opened by a user on the `approve.auto_approve_usernames` list Kodiak should approve the PR.
//
// If we have a dismissed approval, we should add a fresh one.
func TestMergeableAutoApproveOldApproval(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	review := createReview()
	params.Config.Approve.AutoApproveUsernames = []string{"dependency-updater"}
	if params.PullRequest.Author == nil {
		t.Fatal("pull request author is nil")
	}
	params.PullRequest.Author.Login = "dependency-updater"
	review.Author.Login = "kodiak-test-app"
	review.State = queries.PRReviewStateDismissed
	params.BotReviews = []queries.PRReview{review}
	runMergeable(t, api, params)
	assertEqual(t, len(api.ApprovePullRequestCalls), 1, "approve_pull_request call count")
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If a PR is opened by a user on the `approve.auto_approve_usernames` list Kodiak should approve the PR.
//
// Kodiak should only approve open PRs (not merged or closed).
func TestMergeableAutoApproveIgnoreClosedMergedPrs(t *testing.T) {
	for _, pullRequestState := range []queries.PullRequestState{
		queries.PullRequestStateClosed,
		queries.PullRequestStateMerged,
	} {
		params := createMergeableParams()
		api := createAPI()
		params.Config.Approve.AutoApproveUsernames = []string{"dependency-updater"}
		if params.PullRequest.Author == nil {
			t.Fatal("pull request author is nil")
		}
		params.PullRequest.Author.Login = "dependency-updater"
		params.PullRequest.State = pullRequestState
		params.BotReviews = nil
		runMergeable(t, api, params)
		assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
		assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
		assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
		assertEqual(t, len(api.DequeueCalls), 1,
			"dequeue because the PR is closed. This isn't related to this test.")
		assertEqual(t, len(api.MergeCalls), 0, "merge call count")
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	}
}

// If a PR is opened by a user on the `approve.auto_approve_usernames` list Kodiak should approve the PR.
//
// Kodiak should not approve draft PRs.
func TestMergeableAutoApproveIgnoreDraftPr(t *testing.T) {
	params := createMergeableParams()
	pullRequestViaMergeStateStatus := createPullRequest()
	pullRequestViaIsDraft := createPullRequest()
	params.Config.Approve.AutoApproveUsernames = []string{"dependency-updater"}
	if pullRequestViaIsDraft.Author == nil {
		t.Fatal("pull request author is nil")
	}
	pullRequestViaIsDraft.Author.Login = "dependency-updater"
	if pullRequestViaMergeStateStatus.Author == nil {
		t.Fatal("pull request author is nil")
	}
	pullRequestViaMergeStateStatus.Author.Login = "dependency-updater"

	pullRequestViaIsDraft.IsDraft = true
	// configure mergeStateStatus.DRAFT instead of isDraft
	pullRequestViaMergeStateStatus.MergeStateStatus = queries.MergeStateStatusDraft

	for _, pullRequest := range []queries.PullRequest{
		pullRequestViaIsDraft,
		pullRequestViaMergeStateStatus,
	} {
		api := createAPI()
		params.PullRequest = pullRequest
		params.BotReviews = nil
		runMergeable(t, api, params)
		assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
		if len(api.SetStatusCalls) != 1 {
			t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
		}
		assertContains(t, api.SetStatusCalls[0].Msg,
			"cannot merge (pull request is in draft state)",
			"set_status message")
		assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
		assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
		assertEqual(t, len(api.MergeCalls), 0, "merge call count")
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	}
}

// If a subscription is missing we should not raise the paywall. The web_api
// system will set a subscription blocker if active users exceed the limit.
func TestMergeablePaywallMissingSubscription(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Repository = queries.RepoInfo{
		MergeCommitAllowed:  true,
		RebaseMergeAllowed:  true,
		SquashMergeAllowed:  true,
		DeleteBranchOnMerge: false,
		IsPrivate:           true,
	}
	params.Subscription = nil
	runMergeable(t, api, params)

	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued for merge (position=4th)", "set_status message")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If an account has a subscription_blocker we should display the paywall.
func TestMergeablePaywallSubscriptionBlocker(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	params.Repository = queries.RepoInfo{
		MergeCommitAllowed:  true,
		RebaseMergeAllowed:  true,
		SquashMergeAllowed:  true,
		DeleteBranchOnMerge: false,
		IsPrivate:           true,
	}
	params.Subscription = createSubscription(queries.SeatsExceeded{AllowedUserIDs: []int64{}})
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg,
		"💳 subscription: usage has exceeded licensed seats",
		"set_status message")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// Public repositories should never see a paywall.
func TestMergeablePaywallPublicRepository(t *testing.T) {
	for _, subscription := range []*queries.Subscription{
		nil,
		createSubscription(queries.SeatsExceeded{AllowedUserIDs: []int64{}}),
	} {
		api := createAPI()
		params := createMergeableParams()
		params.Repository = queries.RepoInfo{
			MergeCommitAllowed:  true,
			RebaseMergeAllowed:  true,
			SquashMergeAllowed:  true,
			DeleteBranchOnMerge: false,
			IsPrivate:           false,
		}
		params.Subscription = subscription
		runMergeable(t, api, params)
		assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")

		assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
		assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
		assertEqual(t, len(api.MergeCalls), 0, "merge call count")
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	}
}

// If the environment variable is disabled we should not throw up the paywall.
func TestMergeablePaywallMissingEnv(t *testing.T) {
	params := createMergeableParams()
	api := createAPI()
	oldSubscriptionsEnabled := appconf.SubscriptionsEnabled
	appconf.SubscriptionsEnabled = false
	defer func() { appconf.SubscriptionsEnabled = oldSubscriptionsEnabled }()
	params.Repository = queries.RepoInfo{
		MergeCommitAllowed:  true,
		RebaseMergeAllowed:  true,
		SquashMergeAllowed:  true,
		DeleteBranchOnMerge: false,
		IsPrivate:           true,
	}
	params.Subscription = createSubscription(queries.SeatsExceeded{AllowedUserIDs: []int64{}})
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")

	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// When a subscription is expired we should not merge a pull request.
//
// This only applies to private repositories because Kodiak is free on public
// repositories.
func TestMergeablePaywallSubscriptionExpired(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.Repository.IsPrivate = true
	params.Subscription = createSubscription(queries.SubscriptionExpired{})
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "subscription expired", "set_status message")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// When a trial has expired we should not act on a pull request.
func TestMergeablePaywallTrialExpired(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.Repository.IsPrivate = true
	params.Subscription = createSubscription(queries.TrialExpired{})
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "trial ended", "set_status message")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// When an account has exceeded their seat usage they should hit the paywall.
func TestMergeablePaywallSeatsExceeded(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.Repository.IsPrivate = true
	params.Subscription = createSubscription(queries.SeatsExceeded{AllowedUserIDs: []int64{}})
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "exceeded licensed seats", "set_status message")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// Users that have a seat should be allowed to continue using Kodiak even if
// the subscription has exceeded limits.
//
// When an account exceeds it's seat limit we raise the "seats_exceeded"
// paywall. However we also record the user ids that occupy a seat and should
// be allowed to continue using Kodiak. Any user on this list will be able to
// use Kodiak while any others will hit a paywall.
func TestMergeablePaywallSeatsExceededAllowedUser(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	if params.PullRequest.Author == nil {
		t.Fatal("pull request author is nil")
	}
	params.PullRequest.Author.DatabaseID = 234234234
	params.Repository.IsPrivate = true
	params.Subscription = createSubscription(queries.SeatsExceeded{
		AllowedUserIDs: []int64{params.PullRequest.Author.DatabaseID},
	})
	runMergeable(t, api, params)

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.ApprovePullRequestCalls), 0, "approve_pull_request call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// If we attempt to merge a pull request but get an internal server error from
// GitHub we should leave a comment and disable the bot via the
// `disable_bot_label` label. This will help prevent the bot from running out
// of control.
func TestMergeableMergePullRequestApiException(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	api.MergeRaises = kerrors.ErrGitHubAPIInternalServerError
	params.Config.Merge.RequireAutomergeLabel = false

	params.Merging = true
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 2 {
		t.Fatalf("set_status call count: got %d, want 2", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "attempting to merge PR", "set_status message")
	assertContains(t, api.SetStatusCalls[1].Msg, "Cannot merge due to GitHub API failure", "set_status message")
	assertEqual(t, len(api.MergeCalls), 1, "merge call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.RemoveLabelCalls), 0, "remove_label call count")
	if len(api.AddLabelCalls) != 1 {
		t.Fatalf("add_label call count: got %d, want 1", len(api.AddLabelCalls))
	}
	assertEqual(t, api.AddLabelCalls[0], params.Config.DisableBotLabel, "add_label label")
	if len(api.CreateCommentCalls) != 1 {
		t.Fatalf("create_comment call count: got %d, want 1", len(api.CreateCommentCalls))
	}
	assertContains(t, api.CreateCommentCalls[0],
		"This PR could not be merged because the GitHub API returned an internal server",
		"create_comment body")
	assertContains(t, api.CreateCommentCalls[0],
		fmt.Sprintf("remove the `%s` label", params.Config.DisableBotLabel),
		"create_comment body")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// Kodiak should take no action on a pull request when
// disable_bot_label is applied. This is similar to missing an
// automerge label when require_automerge_label is enabled.
func TestMergeableMergeFailureLabel(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.Config.Merge.RequireAutomergeLabel = false
	params.PullRequest.Labels = []string{params.Config.DisableBotLabel}

	params.Merging = true
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "kodiak disabled by disable_bot_label", "set_status message")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.RemoveLabelCalls), 0, "remove_label call count")
	assertEqual(t, len(api.AddLabelCalls), 0, "add_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 0, "create_comment call count")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
}

// When a PR has merge.priority_merge_label, we should place it at the front of
// the merge queue.
func TestMergeablePriorityMergeLabel(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.Config.Merge.PriorityMergeLabel = strPtr("merge this PR stat!")

	// check default case.
	runMergeable(t, api, params)
	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued", "set_status message")
	if len(api.QueueForMergeCalls) != 1 {
		t.Fatalf("queue_for_merge call count: got %d, want 1", len(api.QueueForMergeCalls))
	}
	assertEqual(t, api.QueueForMergeCalls[0], false,
		"by default we should place PR at end of queue (first=False)")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")

	// check merge.priority_merge_label.
	api = createAPI()
	pullRequest := createPullRequest()
	pullRequest.Labels = append(pullRequest.Labels, *params.Config.Merge.PriorityMergeLabel)
	params.PullRequest = pullRequest
	runMergeable(t, api, params)

	if len(api.SetStatusCalls) != 1 {
		t.Fatalf("set_status call count: got %d, want 1", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued", "set_status message")

	if len(api.QueueForMergeCalls) != 1 {
		t.Fatalf("queue_for_merge call count: got %d, want 1", len(api.QueueForMergeCalls))
	}
	assertEqual(t, api.QueueForMergeCalls[0], true,
		"when merge.priority_merge_label is configured we should place PR at front of queue")

	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
}
