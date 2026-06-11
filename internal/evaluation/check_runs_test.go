// Tests for flows that depend on status checks or check_runs.
//
// Ported from bot/kodiak/tests/evaluation/test_check_runs.py.
package evaluation

import (
	"context"
	"errors"
	"testing"

	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// crStatusMsg returns the msg of set_status call i, failing the test if the
// call does not exist.
func crStatusMsg(t *testing.T, api *fakePRAPI, i int) string {
	t.Helper()
	if len(api.SetStatusCalls) <= i {
		t.Fatalf("set_status call %d not found (call count %d)", i, len(api.SetStatusCalls))
	}
	return api.SetStatusCalls[i].Msg
}

// crMarkdownContent returns the markdown_content of set_status call i, failing
// the test if the call does not exist or markdown_content is nil.
func crMarkdownContent(t *testing.T, api *fakePRAPI, i int) string {
	t.Helper()
	if len(api.SetStatusCalls) <= i {
		t.Fatalf("set_status call %d not found (call count %d)", i, len(api.SetStatusCalls))
	}
	if api.SetStatusCalls[i].MarkdownContent == nil {
		t.Fatalf("set_status call %d markdown_content is nil", i)
	}
	return *api.SetStatusCalls[i].MarkdownContent
}

// If branch protection is enabled with requiresStatusChecks but a required check is failing we should not merge.
func TestMergeableMissingRequiresStatusChecksFailingStatusContext(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	statusContext := createContext()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	statusContext.Context = "ci/test-api"
	statusContext.State = queries.StatusStateFailure

	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.CheckRuns = nil
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"failing required status checks: {'ci/test-api'}", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If branch protection is enabled with requiresStatusChecks but a required check is failing we should not merge.
func TestMergeableMissingRequiresStatusChecksFailingCheckRun(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	for index, checkRunConclusion := range []queries.CheckConclusionState{
		queries.CheckConclusionStateActionRequired,
		queries.CheckConclusionStateFailure,
		queries.CheckConclusionStateTimedOut,
		queries.CheckConclusionStateCancelled,
		queries.CheckConclusionStateStale,
	} {
		checkRun.Conclusion = conclusionPtr(checkRunConclusion)
		params := createMergeableParams()
		params.PullRequest = pullRequest
		params.BranchProtection = branchProtection
		params.CheckRuns = []queries.CheckRun{checkRun}
		params.Contexts = nil
		runMergeable(t, api, params)
		assertEqual(t, len(api.SetStatusCalls), 1+index, "set_status call count")
		assertEqual(t, len(api.DequeueCalls), 1+index, "dequeue call count")
		assertContains(t, crStatusMsg(t, api, index),
			"failing required status checks: {'ci/test-api'}", "set_status msg")
	}

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// GitHub has some weird, _undocumented_ logic for continuous-integration/travis-ci where "continuous-integration/travis-ci/{pr,push}" become "continuous-integration/travis-ci" in requiredStatusChecks.
func TestMergeableTravisCiChecks(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	statusContext := createContext()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"continuous-integration/travis-ci"}
	statusContext.State = queries.StatusStateFailure
	statusContext.Context = "continuous-integration/travis-ci/pr"

	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.CheckRuns = nil
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"failing required status checks: {'continuous-integration/travis-ci/pr'}", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If continuous-integration/travis-ci/pr passes we shouldn't say we're waiting for continuous-integration/travis-ci.
func TestMergeableTravisCiChecksSuccess(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	statusContext := createContext()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{
		"continuous-integration/travis-ci",
		"ci/test-api",
	}
	statusContext.State = queries.StatusStateSuccess
	statusContext.Context = "continuous-integration/travis-ci/pr"

	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.Merging = true
	params.CheckRuns = nil
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrPollForever) {
		t.Fatalf("expected PollForever error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"merging PR (waiting for status checks: {'ci/test-api'})", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If a skippable check hasn't finished, we shouldn't do anything.
func TestMergeableSkippableContextsWithStatusCheck(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"WIP", "ci/test-api"}
	cfg.Merge.DontWaitOnStatusChecks = []string{"WIP"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "WIP"
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateSuccess)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"not waiting for dont_wait_on_status_checks: ['WIP']", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If a skippable check hasn't finished, we shouldn't do anything.
func TestMergeableSkippableContextsWithCheckRun(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"WIP", "ci/test-api"}
	cfg.Merge.DontWaitOnStatusChecks = []string{"WIP"}
	statusContext.State = queries.StatusStateSuccess
	statusContext.Context = "ci/test-api"
	checkRun.Name = "WIP"
	checkRun.Conclusion = nil

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"not waiting for dont_wait_on_status_checks: ['WIP']", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// If a skippable check is passing we should queue the PR for merging
func TestMergeableSkippableContextsPassing(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"WIP", "ci/test-api"}
	cfg.Merge.DontWaitOnStatusChecks = []string{"WIP"}
	statusContext.State = queries.StatusStateSuccess
	statusContext.Context = "WIP"
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateSuccess)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	params.Contexts = []queries.StatusContext{statusContext}
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"enqueued for merge (position=4th)", "set_status msg")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
}

// If a skippable check hasn't finished but we're merging, we need to raise an exception to retry for a short period of time to allow the check to finish. We won't retry forever because skippable checks will likely never finish.
func TestMergeableSkippableContextsMergingPullRequest(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"WIP", "ci/test-api"}
	cfg.Merge.DontWaitOnStatusChecks = []string{"WIP"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "WIP"
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateSuccess)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	params.Contexts = []queries.StatusContext{statusContext}
	params.Merging = true
	err := Mergeable(context.Background(), api, params)
	if !errors.Is(err, kerrors.ErrRetryForSkippableChecks) {
		t.Fatalf("expected RetryForSkippableChecks error, got %v", err)
	}
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, crStatusMsg(t, api, 0),
		"merging PR (waiting a bit for dont_wait_on_status_checks: ['WIP'])", "set_status msg")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")

	// verify we haven't tried to update/merge the PR
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// we wait for skippable checks when merging because it takes time for check statuses to be sent and acknowledged by GitHub. We time out after some time because skippable checks are likely to never complete. In this case we want to notify the user of this via status check.
func TestMergeableSkippableCheckTimeout(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	cfg := createConfig()
	statusContext := createContext()
	checkRun := createCheckRun()

	pullRequest.MergeStateStatus = queries.MergeStateStatusBlocked
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"WIP", "ci/test-api"}
	cfg.Merge.DontWaitOnStatusChecks = []string{"WIP"}
	statusContext.State = queries.StatusStatePending
	statusContext.Context = "WIP"
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateSuccess)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.Contexts = []queries.StatusContext{statusContext}
	params.CheckRuns = []queries.CheckRun{checkRun}
	params.Merging = true
	params.SkippableCheckTimeout = 0
	runMergeable(t, api, params)

	assertEqual(t, len(api.SetStatusCalls) > 0, true, "set_status called")
	assertContains(t, crStatusMsg(t, api, 0),
		"timeout reached for dont_wait_on_status_checks: ['WIP']", "set_status msg")
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
	assertEqual(t, len(api.MergeCalls), 0, "merge not called")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge not called")
}

// Kodiak should update PR even when failing requirements for merge
func TestMergeableUpdateAlways(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	checkRun := createCheckRun()

	cfg.Update.Always = true
	cfg.Update.RequireAutomergeLabel = true

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	runMergeable(t, api, params)
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, crStatusMsg(t, api, 0), "updating branch", "set_status msg")
	assertContains(t, crMarkdownContent(t, api, 0), "branch updated because", "set_status markdown_content")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// Kodiak should update the PR when the autoupdate_label is set on the PR.
func TestMergeableUpdateAutoupdateLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	checkRun := createCheckRun()

	cfg.Update.AutoupdateLabel = strPtr("update me please!")

	// create a pull requests that's behind and failing checks. We should still
	// update on failing checks.
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	runMergeable(t, api, params)
	assertEqual(t, len(api.UpdateBranchCalls), 0,
		"we shouldn't update when update.autoupdate_label isn't available on the PR")

	// PR should be updated when set on the PR
	pullRequest.Labels = []string{*cfg.Update.AutoupdateLabel}
	api = createAPI()
	params = createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	runMergeable(t, api, params)

	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, crStatusMsg(t, api, 0), "updating branch", "set_status msg")
	assertContains(t, crMarkdownContent(t, api, 0), "branch updated because", "set_status markdown_content")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// Kodiak should not update branch if update.require_automerge_label is True and we're missing the automerge label.
func TestMergeableUpdateAlwaysRequireAutomergeLabelMissingLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	checkRun := createCheckRun()

	cfg.Update.Always = true
	cfg.Update.RequireAutomergeLabel = true

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	pullRequest.Labels = []string{}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	runMergeable(t, api, params)
	assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")

	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, crStatusMsg(t, api, 0), "Ignored (no automerge label:", "set_status msg")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
}

// Kodiak should update branch if update.require_automerge_label is True and we're missing the automerge label.
func TestMergeableUpdateAlwaysNoRequireAutomergeLabelMissingLabel(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	pullRequest := createPullRequest()
	branchProtection := createBranchProtection()
	checkRun := createCheckRun()

	cfg.Update.Always = true
	cfg.Update.RequireAutomergeLabel = false

	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"ci/test-api"}
	checkRun.Name = "ci/test-api"
	checkRun.Conclusion = conclusionPtr(queries.CheckConclusionStateFailure)

	pullRequest.Labels = []string{}

	params := createMergeableParams()
	params.Config = cfg
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{checkRun}
	runMergeable(t, api, params)
	assertEqual(t, len(api.UpdateBranchCalls), 1, "update_branch call count")
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, crStatusMsg(t, api, 0), "updating branch", "set_status msg")
	assertContains(t, crMarkdownContent(t, api, 0), "branch updated because", "set_status markdown_content")

	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// Kodiak should only consider the most recent check run when evaluating a PR
// for merging.
//
// Regression test.
func TestDuplicateCheckSuites(t *testing.T) {
	api := createAPI()
	pullRequest := createPullRequest()
	pullRequest.MergeStateStatus = queries.MergeStateStatusBehind
	branchProtection := createBranchProtection()
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"Pre-merge checks"}
	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.BranchProtection = branchProtection
	params.CheckRuns = []queries.CheckRun{
		{Name: "Pre-merge checks", Conclusion: conclusionPtr(queries.CheckConclusionStateNeutral)},
		{Name: "Pre-merge checks", Conclusion: conclusionPtr(queries.CheckConclusionStateFailure)},
		{Name: "Pre-merge checks", Conclusion: conclusionPtr(queries.CheckConclusionStateSuccess)},
	}
	runMergeable(t, api, params)
	assertContains(t, crStatusMsg(t, api, 0), "enqueued for merge", "set_status msg")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
}

// When merge.block_on_neutral_required_check_runs is enabled, we should block
// merge if a required check run has a neutral conclusion.
func TestNeutralRequiredCheckRuns(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	cfg.Merge.BlockOnNeutralRequiredCheckRuns = true
	branchProtection := createBranchProtection()
	branchProtection.RequiresStatusChecks = true
	branchProtection.RequiredStatusCheckContexts = []string{"Pre-merge checks"}

	params := createMergeableParams()
	params.BranchProtection = branchProtection
	params.Config = cfg
	params.CheckRuns = []queries.CheckRun{
		{Name: "Pre-merge checks", Conclusion: conclusionPtr(queries.CheckConclusionStateNeutral)},
	}
	runMergeable(t, api, params)
	assertContains(t, crStatusMsg(t, api, 0), "neutral required check runs", "set_status msg")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
}
