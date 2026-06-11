// Tests ported from bot/kodiak/test_evaluation.py lines 1076-1690
// (test_mergeable_block_on_reviews_requested through
// test_mergeable_prioritize_ready_to_merge).
package evaluation

import (
	"context"
	"errors"
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// e2StatusMsg returns the message of the i-th set_status call, failing the
// test fatally if that call doesn't exist (matching the IndexError a Python
// test would raise on api.set_status.calls[i]).
func e2StatusMsg(t *testing.T, api *fakePRAPI, i int) string {
	t.Helper()
	if len(api.SetStatusCalls) <= i {
		t.Fatalf("set_status call %d missing: only %d call(s) recorded", i, len(api.SetStatusCalls))
	}
	return api.SetStatusCalls[i].Msg
}

// block merge if reviews are requested and merge.block_on_reviews_requested is
// enabled.
func TestMergeableBlockOnReviewsRequested(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	reviewRequest := createReviewRequest()

	params.Config.Merge.BlockOnReviewsRequested = true

	params.ReviewRequests = []queries.PRReviewRequest{reviewRequest}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "reviews requested: ['ghost']", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR is already merged we shouldn't take anymore action on it besides
// deleting the branch if configured.
//
// Here we test with the delete_branch_on_merge config disabled.
func TestMergeablePullRequestMergedNoDeleteBranch(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateMerged
	params.Config.Merge.DeleteBranchOnMerge = false

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.DeleteBranchCalls), 0, "delete_branch call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR is already merged we shouldn't take anymore action on it besides
// deleting the branch if configured.
//
// Here we test with the delete_branch_on_merge config enabled.
func TestMergeablePullRequestMergedDeleteBranch(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateMerged
	params.Config.Merge.DeleteBranchOnMerge = true

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.DeleteBranchCalls), 1, "delete_branch call count")
	if len(api.DeleteBranchCalls) > 0 {
		assertEqual(t, api.DeleteBranchCalls[0], params.PullRequest.HeadRefName, "delete_branch branch_name")
	}

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR is already merged we shouldn't take anymore action on it besides
// deleting the branch if configured.
//
// Here we test with the delete_branch_on_merge config enabled, but with other
// PR dependencies on a branch. If there are open PRs that depend on a branch,
// we should _not_ delete the branch.
func TestMergeablePullRequestMergedDeleteBranchWithBranchDependencies(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateMerged
	params.Config.Merge.DeleteBranchOnMerge = true
	api.PullRequestsForRefReturnValue = intPtr(1)

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.DeleteBranchCalls), 0, "delete_branch call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR is already merged we shouldn't take anymore action on it besides
// deleting the branch if configured.
//
// Here we test with the delete_branch_on_merge config enabled, but we use a
// cross repository (fork) pull request, which we aren't able to delete. We
// shouldn't try to delete the branch.
func TestMergeablePullRequestMergedDeleteBranchCrossRepoPr(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateMerged
	params.PullRequest.IsCrossRepository = true
	params.Config.Merge.DeleteBranchOnMerge = true

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.DeleteBranchCalls), 0, "delete_branch call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If the repository has delete_branch_on_merge enabled we shouldn't bother
// trying to delete the branch.
func TestMergeablePullRequestMergedDeleteBranchRepoDeleteEnabled(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateMerged
	params.Repository.DeleteBranchOnMerge = true
	params.Config.Merge.DeleteBranchOnMerge = true

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertEqual(t, len(api.DeleteBranchCalls), 0, "delete_branch call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR is closed we don't want to act on it.
func TestMergeablePullRequestClosed(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.State = queries.PullRequestStateClosed

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR has a merge conflict we can't merge. If configured, we should leave
// a comment and remove the automerge label.
func TestMergeablePullRequestMergeConflict(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = false

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "merge conflict", "set_status msg")
	assertEqual(t, len(api.RemoveLabelCalls), 0, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 0, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR has a merge conflict we can't merge. If configured, we should leave
// a comment and remove the automerge label.
func TestMergeablePullRequestMergeConflictNotifyOnConflict(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = true
	params.Config.Merge.RequireAutomergeLabel = true

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "merge conflict", "set_status msg")
	assertEqual(t, len(api.RemoveLabelCalls), 1, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 1, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR has a merge conflict we can't merge. If the title matches the
// blacklist_title_regex we should still leave a comment and remove the
// automerge label.
func TestMergeablePullRequestMergeConflictNotifyOnConflictBlacklistTitleRegex(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = true
	params.Config.Merge.RequireAutomergeLabel = true
	params.Config.Merge.BlacklistTitleRegex = "WIP.*"
	params.PullRequest.Title = "WIP: add csv download to reports view"

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "merge conflict", "set_status msg")
	assertEqual(t, len(api.RemoveLabelCalls), 1, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 1, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR has a merge conflict we can't merge. If configured, we should leave
// a comment and remove the automerge label. If the automerge label is missing
// we shouldn't create a comment.
func TestMergeablePullRequestMergeConflictNotifyOnConflictMissingLabel(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = true
	params.Config.Merge.RequireAutomergeLabel = true
	params.PullRequest.Labels = []string{}

	runMergeable(t, api, params)
	assertEqual(t, len(api.CreateCommentCalls), 0, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// We should only notify on conflict when we have an automerge label.
//
// If we have an array of merge.automerge_label labels, we should remove each
// one like we do with merge.automerge_label.
func TestMergeablePullRequestMergeConflictNotifyOnConflictAutomergeLabels(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = true
	params.Config.Merge.RequireAutomergeLabel = true
	params.PullRequest.Labels = []string{"ship it!!!"}
	params.Config.Merge.AutomergeLabel = config.Labels{"ship it!!!"}

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "merge conflict", "set_status msg")
	assertEqual(t, len(api.RemoveLabelCalls), 1, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 1, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// if a PR has a merge conflict we can't merge. If require_automerge_label is
// set then we shouldn't notify even if notify_on_conflict is configured. This
// allows prevents infinite commenting.
func TestMergeablePullRequestMergeConflictNotifyOnConflictNoRequireAutomergeLabel(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.Config.Merge.NotifyOnConflict = true
	params.Config.Merge.RequireAutomergeLabel = false

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, e2StatusMsg(t, api, 0), "cannot merge", "set_status msg")
	assertContains(t, e2StatusMsg(t, api, 0), "merge conflict", "set_status msg")
	assertEqual(t, len(api.RemoveLabelCalls), 0, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 0, "create_comment call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If the pull request is merged we shouldn't error on merge conflict.
//
// On merge we will get the following values when we fetch pull request information:
//
//	"mergeStateStatus": "DIRTY"
//	"state": "MERGED"
//	"mergeable": "CONFLICTING"
//
// Although the PR is said to be dirty or conflicting, we don't want to leave a
// comment because the pull request is already merged.
func TestMergeablePullRequestMergeConflictNotifyOnConflictPullRequestMerged(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.MergeStateStatus = queries.MergeStateStatusDirty
	params.PullRequest.Mergeable = queries.MergeableStateConflicting
	params.PullRequest.State = queries.PullRequestStateMerged
	params.Config.Merge.NotifyOnConflict = true

	runMergeable(t, api, params)
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.RemoveLabelCalls), 0, "remove_label call count")
	assertEqual(t, len(api.CreateCommentCalls), 0, "create_comment call count")
	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// When you view a PR on GitHub, GitHub makes a test commit to see if a PR can
// be merged cleanly, but calling through the api doesn't trigger this test
// commit unless we explictly call the GET endpoint for a pull request.
func TestMergeablePullRequestNeedTestCommit(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.Mergeable = queries.MergeableStateUnknown

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.TriggerTestCommitCalls), 1, "trigger_test_commit call count")
	assertEqual(t, len(api.RequeueCalls), 1, "requeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If we're merging a PR we should raise the PollForever exception instead of
// returning. This way we stay in the merge loop.
func TestMergeablePullRequestNeedTestCommitMerging(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.PullRequest.Mergeable = queries.MergeableStateUnknown

	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.TriggerTestCommitCalls), 1, "trigger_test_commit call count")
	assertEqual(t, len(api.RequeueCalls), 1, "requeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If a pull request mergeable status is UNKNOWN we should trigger a test
// commit and queue it for evaluation.
//
// Regression test, merge.blocking_title_regex should not prevent us for
// triggering a test commit.
func TestMergeablePullRequestNeedTestCommitNeedUpdate(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.Config.Update.Always = true
	params.Config.Merge.BlockingTitleRegex = "^WIP:.*"
	params.PullRequest.Title = "WIP: add(api): endpoint for checking notifications"

	params.PullRequest.Mergeable = queries.MergeableStateUnknown
	params.PullRequest.State = queries.PullRequestStateOpen

	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 0, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.TriggerTestCommitCalls), 1, "trigger_test_commit call count")
	assertEqual(t, len(api.RequeueCalls), 1, "requeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If a pull request mergeable status is UNKNOWN _and_ it is OPEN we should
// trigger a test commit and queue it for evaluation.
//
// Regression test to prevent infinite loop calling trigger_test_commit on
// merged/closed pull requests.
func TestMergeablePullRequestNeedTestCommitNeedUpdatePrNotOpen(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.Config.Update.Always = true
	params.Config.Merge.BlockingTitleRegex = "^WIP:.*"
	params.PullRequest.Title = "WIP: add(api): endpoint for checking notifications"

	params.PullRequest.Mergeable = queries.MergeableStateUnknown

	// this test is nearly identical to
	// test_mergeable_pull_request_need_test_commit_need_update, except our pull
	// request state is MERGED or CLOSED instead of OPEN.
	for index, pullRequestState := range []queries.PullRequestState{
		queries.PullRequestStateMerged, queries.PullRequestStateClosed,
	} {
		params.PullRequest.State = pullRequestState
		runMergeable(t, api, params)
		assertEqual(t, len(api.SetStatusCalls), index+1, "set_status call count")
		assertContains(t, e2StatusMsg(t, api, 0),
			"cannot merge (title matches merge.blocking_title_regex", "set_status msg")
		assertEqual(t, len(api.DequeueCalls), index+1, "dequeue call count")
		assertEqual(t, len(api.TriggerTestCommitCalls), 0, "trigger_test_commit call count")
		assertEqual(t, len(api.RequeueCalls), 0, "requeue call count")

		// verify we haven't tried to update/merge the PR
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
		assertEqual(t, len(api.MergeCalls), 0, "merge not called")
		assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	}
}

// Test how kodiak behaves when we cannot figure out why a PR is blocked.
func TestMergeableUnknownMergeBlockage(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusBlocked

	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertContains(t, e2StatusMsg(t, api, 0), "Merging blocked by GitHub", "set_status msg")
	assertEqual(t, len(api.RequeueCalls), 1, "we want to retry when we hit this status")
	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

func TestMergeableUnknownMergeBlockageCodeOwner(t *testing.T) {
	// NOTE(port): marked @pytest.mark.xfail in Python
	// (bot/kodiak/test_evaluation.py:1627) — the asserted "Codeowner review
	// required" behavior is not implemented, so we skip instead of failing.
	t.Skip("expected failure (@pytest.mark.xfail in Python)")

	api := createAPI()
	params := createMergeableParams()
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	params.PullRequest.ReviewDecision = nil

	params.ReviewRequests = []queries.PRReviewRequest{{Name: "chdsbd", AsCodeOwner: true}}
	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, e2StatusMsg(t, api, 0), "Codeowner review required", "set_status msg")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.RequeueCalls), 0, "requeue not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

func TestMergeableUnknownMergeBlockageCodeOwnerApproval(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()
	params.PullRequest.MergeStateStatus = queries.MergeStateStatusClean
	params.PullRequest.ReviewDecision = nil

	params.ReviewRequests = []queries.PRReviewRequest{{Name: "sbdchd", AsCodeOwner: true}}
	runMergeable(t, api, params)

	assertEqual(t, len(api.QueueForMergeCalls) > 0, true, "queue_for_merge called")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// If we enabled merge.prioritize_ready_to_merge, then if a PR is ready to
// merge when it reaches Kodiak, we merge it immediately.
// merge.prioritize_ready_to_merge is basically the sibling of
// merge.update_branch_immediately.
func TestMergeablePrioritizeReadyToMerge(t *testing.T) {
	api := createAPI()
	params := createMergeableParams()

	params.Config.Merge.PrioritizeReadyToMerge = true

	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls), 2, "set_status call count")
	assertContains(t, e2StatusMsg(t, api, 0), "attempting to merge PR (merging)", "set_status msg")
	assertEqual(t, e2StatusMsg(t, api, 1), "merge complete 🎉", "set_status msg")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
	assertEqual(t, len(api.MergeCalls), 1, "merge call count")

	// verify we haven't tried to merge the PR
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}
