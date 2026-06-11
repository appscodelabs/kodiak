// Test fixtures ported from bot/kodiak/test_evaluation.py. These helpers are
// shared by ports of bot/kodiak/tests/evaluation/*.py.
package evaluation

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/queries"
)

func TestMain(m *testing.M) {
	// mirror the pytest-env settings from bot/pyproject.toml.
	appconf.GithubAppID = "534524"
	appconf.GithubAppName = "kodiak-test-app"
	appconf.SubscriptionsEnabled = true
	os.Exit(m.Run())
}

func intPtr(n int) *int     { return &n }
func i64Ptr(n int64) *int64 { return &n }
func conclusionPtr(c queries.CheckConclusionState) *queries.CheckConclusionState {
	return &c
}
func reviewDecisionPtr(d queries.PullRequestReviewDecision) *queries.PullRequestReviewDecision {
	return &d
}

// apiCallError is the test stand-in for pull_request.APICallError, satisfying
// messages.APICallRetry.
type apiCallError struct {
	apiName      string
	httpStatus   string
	responseBody string
}

func (e apiCallError) APIName() string      { return e.apiName }
func (e apiCallError) HTTPStatus() string   { return e.httpStatus }
func (e apiCallError) ResponseBody() string { return e.responseBody }

type setStatusCall struct {
	Msg             string
	MarkdownContent *string
}

type mergeCall struct {
	MergeMethod   string
	CommitTitle   *string
	CommitMessage *string
}

type updateRefCall struct {
	Ref string
	Sha string
}

// fakePRAPI implements PRAPI and records every call, like MockPrApi in the
// Python tests. Stub return values via the *ReturnValue / MergeRaises fields.
type fakePRAPI struct {
	DequeueCalls            []struct{}
	RequeueCalls            []struct{}
	SetStatusCalls          []setStatusCall
	PullRequestsForRefCalls []string
	DeleteBranchCalls       []string
	RemoveLabelCalls        []string
	AddLabelCalls           []string
	CreateCommentCalls      []string
	TriggerTestCommitCalls  []struct{}
	MergeCalls              []mergeCall
	UpdateRefCalls          []updateRefCall
	QueueForMergeCalls      []bool // the `first` argument of each call
	UpdateBranchCalls       []struct{}
	ApprovePullRequestCalls []struct{}

	// stubbed return values
	PullRequestsForRefReturnValue *int
	QueueForMergeReturnValue      *int
	MergeRaises                   error
}

var _ PRAPI = (*fakePRAPI)(nil)

func (f *fakePRAPI) Dequeue(ctx context.Context) error {
	f.DequeueCalls = append(f.DequeueCalls, struct{}{})
	return nil
}

func (f *fakePRAPI) Requeue(ctx context.Context) error {
	f.RequeueCalls = append(f.RequeueCalls, struct{}{})
	return nil
}

func (f *fakePRAPI) SetStatus(ctx context.Context, msg string, markdownContent *string) error {
	f.SetStatusCalls = append(f.SetStatusCalls, setStatusCall{Msg: msg, MarkdownContent: markdownContent})
	return nil
}

func (f *fakePRAPI) PullRequestsForRef(ctx context.Context, ref string) (*int, error) {
	f.PullRequestsForRefCalls = append(f.PullRequestsForRefCalls, ref)
	return f.PullRequestsForRefReturnValue, nil
}

func (f *fakePRAPI) DeleteBranch(ctx context.Context, branchName string) error {
	f.DeleteBranchCalls = append(f.DeleteBranchCalls, branchName)
	return nil
}

func (f *fakePRAPI) RemoveLabel(ctx context.Context, label string) error {
	f.RemoveLabelCalls = append(f.RemoveLabelCalls, label)
	return nil
}

func (f *fakePRAPI) AddLabel(ctx context.Context, label string) error {
	f.AddLabelCalls = append(f.AddLabelCalls, label)
	return nil
}

func (f *fakePRAPI) CreateComment(ctx context.Context, body string) error {
	f.CreateCommentCalls = append(f.CreateCommentCalls, body)
	return nil
}

func (f *fakePRAPI) TriggerTestCommit(ctx context.Context) error {
	f.TriggerTestCommitCalls = append(f.TriggerTestCommitCalls, struct{}{})
	return nil
}

func (f *fakePRAPI) Merge(ctx context.Context, mergeMethod string, commitTitle *string, commitMessage *string) error {
	f.MergeCalls = append(f.MergeCalls, mergeCall{
		MergeMethod:   mergeMethod,
		CommitTitle:   commitTitle,
		CommitMessage: commitMessage,
	})
	return f.MergeRaises
}

func (f *fakePRAPI) UpdateRef(ctx context.Context, ref string, sha string) error {
	f.UpdateRefCalls = append(f.UpdateRefCalls, updateRefCall{Ref: ref, Sha: sha})
	return nil
}

func (f *fakePRAPI) QueueForMerge(ctx context.Context, first bool) (*int, error) {
	f.QueueForMergeCalls = append(f.QueueForMergeCalls, first)
	return f.QueueForMergeReturnValue, nil
}

func (f *fakePRAPI) UpdateBranch(ctx context.Context) error {
	f.UpdateBranchCalls = append(f.UpdateBranchCalls, struct{}{})
	return nil
}

func (f *fakePRAPI) ApprovePullRequest(ctx context.Context) error {
	f.ApprovePullRequestCalls = append(f.ApprovePullRequestCalls, struct{}{})
	return nil
}

// Called reports whether any api method has been called.
func (f *fakePRAPI) Called() bool {
	return len(f.DequeueCalls) > 0 ||
		len(f.RequeueCalls) > 0 ||
		len(f.SetStatusCalls) > 0 ||
		len(f.PullRequestsForRefCalls) > 0 ||
		len(f.DeleteBranchCalls) > 0 ||
		len(f.RemoveLabelCalls) > 0 ||
		len(f.AddLabelCalls) > 0 ||
		len(f.CreateCommentCalls) > 0 ||
		len(f.TriggerTestCommitCalls) > 0 ||
		len(f.MergeCalls) > 0 ||
		len(f.UpdateRefCalls) > 0 ||
		len(f.QueueForMergeCalls) > 0 ||
		len(f.UpdateBranchCalls) > 0 ||
		len(f.ApprovePullRequestCalls) > 0
}

func createAPI() *fakePRAPI {
	return &fakePRAPI{
		// in production we'll frequently have position information. `3` is an
		// arbitrary position.
		QueueForMergeReturnValue:      intPtr(3),
		PullRequestsForRefReturnValue: intPtr(0),
	}
}

func createConfigStr() string {
	return `version = 1

[merge]
automerge_label = "automerge"
blacklist_labels = []
method = "squash"
`
}

func createConfigPath() string {
	return "master:.kodiak.toml"
}

func createPullRequest() queries.PullRequest {
	return queries.PullRequest{
		ID:     "FDExOlB1bGxSZXX1ZXN0MjgxODQ0Nzg7",
		Number: 142,
		Author: &queries.PullRequestAuthor{
			Login:      "barry",
			Name:       strPtr("Barry Berkman"),
			DatabaseID: 828352,
			Type:       "User",
		},
		MergeStateStatus:  queries.MergeStateStatusClean,
		State:             queries.PullRequestStateOpen,
		IsDraft:           false,
		Mergeable:         queries.MergeableStateMergeable,
		IsCrossRepository: false,
		Labels:            []string{"bugfix", "automerge"},
		LatestSha:         "f89be6c",
		BaseRefName:       "master",
		HeadRefName:       "feature/hello-world",
		Title:             "new feature",
		Body:              "# some description",
		BodyText:          "some description",
		BodyHTML:          "<h1>some description</h1>",
		URL:               "https://github.com/example_org/example_repo/pull/65",
		ReviewThreads:     queries.ReviewThreadConnection{Nodes: []queries.ReviewThread{}},
	}
}

func createBranchProtection() *queries.BranchProtectionRule {
	requiresConversationResolution := false
	return &queries.BranchProtectionRule{
		RequiresStatusChecks:           true,
		RequiredStatusCheckContexts:    []string{"ci/api"},
		RequiresStrictStatusChecks:     true,
		RequiresCommitSignatures:       false,
		RequiresConversationResolution: &requiresConversationResolution,
		RestrictsPushes:                false,
		PushAllowances:                 queries.NodeListPushAllowance{Nodes: []queries.PushAllowance{}},
	}
}

func createReview() queries.PRReview {
	return queries.PRReview{
		State:     queries.PRReviewStateApproved,
		CreatedAt: time.Date(2015, 5, 25, 0, 0, 0, 0, time.UTC),
		Author:    queries.PRReviewAuthor{Login: "ghost"},
	}
}

func createContext() queries.StatusContext {
	return queries.StatusContext{Context: "ci/api", State: queries.StatusStateSuccess}
}

func createCheckRun() queries.CheckRun {
	return queries.CheckRun{
		Name:       "WIP (beta)",
		Conclusion: conclusionPtr(queries.CheckConclusionStateSuccess),
	}
}

func createReviewRequest() queries.PRReviewRequest {
	return queries.PRReviewRequest{Name: "ghost"}
}

func createRepoInfo() queries.RepoInfo {
	return queries.RepoInfo{
		MergeCommitAllowed:  true,
		RebaseMergeAllowed:  true,
		SquashMergeAllowed:  true,
		DeleteBranchOnMerge: false,
		IsPrivate:           false,
	}
}

func createConfig() *config.V1 {
	cfg := config.DefaultV1()
	cfg.Merge.AutomergeLabel = config.Labels{"automerge"}
	cfg.Merge.BlacklistLabels = []string{}
	return &cfg
}

func createSubscription(blocker queries.SubscriptionBlocker) *queries.Subscription {
	return &queries.Subscription{
		AccountID:           "cc5674b3-b53c-4c4e-855d-7b3c52b8325f",
		SubscriptionBlocker: blocker,
	}
}

// createCommit ports kodiak.tests.fixtures.create_commit.
func createCommit(databaseID *int64, name *string, login string, userType string, parents int) queries.Commit {
	return queries.Commit{
		Parents: queries.CommitConnection{TotalCount: parents},
		Author: &queries.GitActor{
			User: &queries.PullRequestCommitUser{
				DatabaseID: databaseID,
				Name:       name,
				Login:      login,
				Type:       userType,
			},
		},
	}
}

// createMergeableParams provides the default arguments of the Python
// create_mergeable() wrapper. Tests override individual fields and call
// runMergeable / Mergeable directly.
func createMergeableParams() MergeableParams {
	return MergeableParams{
		Config:           createConfig(),
		ConfigStr:        createConfigStr(),
		ConfigPath:       createConfigPath(),
		PullRequest:      createPullRequest(),
		BranchProtection: createBranchProtection(),
		RulesetRules:     nil,
		ReviewRequests:   nil,
		BotReviews:       []queries.PRReview{createReview()},
		Contexts:         []queries.StatusContext{createContext()},
		CheckRuns:        []queries.CheckRun{createCheckRun()},
		Commits:          nil,
		ValidMergeMethods: []config.MergeMethod{
			config.MergeMethodMerge,
			config.MergeMethodSquash,
			config.MergeMethodRebase,
		},
		Repository:              createRepoInfo(),
		Merging:                 false,
		IsActiveMerge:           false,
		SkippableCheckTimeout:   5,
		APICallRetriesRemaining: 5,
		APICallErrors:           nil,
		Subscription:            nil,
		AppID:                   "",
	}
}

// runMergeable calls Mergeable and fails the test on an unexpected error.
func runMergeable(t *testing.T, api PRAPI, params MergeableParams) {
	t.Helper()
	if err := Mergeable(context.Background(), api, params); err != nil {
		t.Fatalf("mergeable returned unexpected error: %v", err)
	}
}

func assertEqual[T comparable](t *testing.T, got, want T, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

func assertContains(t *testing.T, s, substr, msg string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("%s: %q does not contain %q", msg, s, substr)
	}
}

func assertNotContains(t *testing.T, s, substr, msg string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("%s: %q unexpectedly contains %q", msg, s, substr)
	}
}
