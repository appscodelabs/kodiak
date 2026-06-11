// ported from bot/kodiak/test_pull_request.py
package pullrequest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/kerrors"
	"github.com/appscodelabs/kodiak/internal/queries"
)

func createEvent() *queries.EventInfoResponse {
	method := config.MergeMethodSquash
	cfg := config.DefaultV1()
	cfg.Merge.AutomergeLabel = config.Labels{"automerge"}
	cfg.Merge.Method = &method
	return &queries.EventInfoResponse{
		Config: &cfg,
		ConfigStr: `version = 1
[merge]
method = "squash"
`,
		ConfigFileExpression: "master:.kodiak.toml",
		HeadExists:           true,
		PullRequest: queries.PullRequest{
			ID:                "e14ff7599399478fb9dbc2dacb87da72",
			Number:            100,
			Author:            &queries.PullRequestAuthor{Login: "arnold", DatabaseID: 49118, Type: "Bot"},
			MergeStateStatus:  queries.MergeStateStatusBehind,
			State:             queries.PullRequestStateOpen,
			IsDraft:           false,
			Mergeable:         queries.MergeableStateMergeable,
			IsCrossRepository: false,
			Labels:            []string{"automerge"},
			LatestSha:         "8d728d017cac4f5ba37533debe65730abe65730a",
			BaseRefName:       "master",
			HeadRefName:       "df825f90-9825-424c-a97e-733522027e4c",
			Title:             "Update README.md",
			URL:               "https://github.com/delos-corp/hive-mind/pull/324",
		},
		Repository: queries.RepoInfo{
			MergeCommitAllowed:  false,
			RebaseMergeAllowed:  false,
			SquashMergeAllowed:  true,
			IsPrivate:           true,
			DeleteBranchOnMerge: false,
		},
		BranchProtection: &queries.BranchProtectionRule{
			RequiresStatusChecks: true,
			RequiredStatusCheckContexts: []string{
				"ci/circleci: frontend_lint",
				"ci/circleci: frontend_test",
			},
			RequiresStrictStatusChecks: true,
		},
		ValidMergeMethods: []config.MergeMethod{config.MergeMethodSquash},
	}
}

type fakeClient struct {
	mergePullRequestResponse *queries.Response
	mergePullRequestCalls    []map[string]any

	deleteLabelResponse *queries.Response
	deleteLabelCalls    []map[string]any

	addLabelResponse *queries.Response
	addLabelCalls    []map[string]any

	updateBranchResponse *queries.Response
	updateBranchCalls    []map[string]any

	updateRefResponse *queries.Response
	updateRefCalls    []map[string]any
}

var _ githubClient = (*fakeClient)(nil)

func (f *fakeClient) CreateNotification(ctx context.Context, headSHA string, message string, summary *string) (*queries.Response, error) {
	panic("not implemented")
}

func (f *fakeClient) GetOpenPullRequests(ctx context.Context, base string, head string) ([]queries.GetOpenPullRequestsResponse, error) {
	panic("not implemented")
}

func (f *fakeClient) DeleteBranch(ctx context.Context, branch string) (*queries.Response, error) {
	panic("not implemented")
}

func (f *fakeClient) UpdateBranch(ctx context.Context, pullNumber int) (*queries.Response, error) {
	f.updateBranchCalls = append(f.updateBranchCalls, map[string]any{"pull_number": pullNumber})
	return f.updateBranchResponse, nil
}

func (f *fakeClient) ApprovePullRequest(ctx context.Context, pullNumber int) (*queries.Response, error) {
	panic("not implemented")
}

func (f *fakeClient) GetPullRequest(ctx context.Context, number int) (*queries.Response, error) {
	panic("not implemented")
}

func (f *fakeClient) MergePullRequest(ctx context.Context, number int, mergeMethod string, commitTitle *string, commitMessage *string) (*queries.Response, error) {
	f.mergePullRequestCalls = append(f.mergePullRequestCalls, map[string]any{
		"number":         number,
		"merge_method":   mergeMethod,
		"commit_title":   commitTitle,
		"commit_message": commitMessage,
	})
	return f.mergePullRequestResponse, nil
}

func (f *fakeClient) UpdateRef(ctx context.Context, ref string, sha string) (*queries.Response, error) {
	f.updateRefCalls = append(f.updateRefCalls, map[string]any{"ref": ref, "sha": sha})
	return f.updateRefResponse, nil
}

func (f *fakeClient) AddLabel(ctx context.Context, label string, pullNumber int) (*queries.Response, error) {
	f.addLabelCalls = append(f.addLabelCalls, map[string]any{"label": label, "pull_number": pullNumber})
	return f.addLabelResponse, nil
}

func (f *fakeClient) DeleteLabel(ctx context.Context, label string, pullNumber int) (*queries.Response, error) {
	f.deleteLabelCalls = append(f.deleteLabelCalls, map[string]any{"label": label, "pull_number": pullNumber})
	return f.deleteLabelResponse, nil
}

func (f *fakeClient) CreateComment(ctx context.Context, body string, pullNumber int) (*queries.Response, error) {
	panic("not implemented")
}

func createResponse(content []byte, statusCode int) *queries.Response {
	return &queries.Response{StatusCode: statusCode, Body: content}
}

func noop(ctx context.Context) error { return nil }

func noopQueueForMerge(ctx context.Context, first bool) (*int, error) { return nil, nil }

func createPRV2(client githubClient) *PRV2 {
	pr := NewPRV2(createEvent(), "88443234", "delos", "incite", 8634, noop, noop, noopQueueForMerge)
	pr.client = client
	return pr
}

func strPtr(s string) *string { return &s }

// requireAPICallError asserts err is an APICallError with the given method
// and status code, and that the response contains substr.
func requireAPICallError(t *testing.T, err error, method string, statusCode int, substr string) {
	t.Helper()
	var apiErr *kerrors.APICallError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APICallError, got %v", err)
	}
	if apiErr.Method != method {
		t.Errorf("Method = %q, want %q", apiErr.Method, method)
	}
	if apiErr.StatusCode != statusCode {
		t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, statusCode)
	}
	if !bytes.Contains(apiErr.Response, []byte(substr)) {
		t.Errorf("Response %q does not contain %q", apiErr.Response, substr)
	}
}

// We should be able to merge successfully.
func TestPRV2Merge(t *testing.T) {
	client := &fakeClient{
		mergePullRequestResponse: createResponse([]byte(`{
      "sha": "6dcb09b5b57875f334f61aebed695e2e4193db5e",
      "merged": true,
      "message": "Pull Request successfully merged"
    }`), 200),
	}
	prV2 := createPRV2(client)
	if err := prV2.Merge(context.Background(), "squash", strPtr("my title"), strPtr("my message")); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(client.mergePullRequestCalls) != 1 {
		t.Errorf("merge call count = %d, want 1", len(client.mergePullRequestCalls))
	}
}

// We should raise ApiCallException when we get a bad API response.
func TestPRV2MergeRebaseError(t *testing.T) {
	client := &fakeClient{
		mergePullRequestResponse: createResponse([]byte(`{"message":"This branch can't be rebased","documentation_url":"https://developer.github.com/v3/pulls/#merge-a-pull-request-merge-button"}`), 405),
	}
	prV2 := createPRV2(client)
	err := prV2.Merge(context.Background(), "squash", strPtr("my title"), strPtr("my message"))
	if len(client.mergePullRequestCalls) != 1 {
		t.Errorf("merge call count = %d, want 1", len(client.mergePullRequestCalls))
	}
	requireAPICallError(t, err, "pull_request/merge", 405, "merge-a-pull-request-merge-button")
}

// We should raise ApiCallException when we get a bad API response.
func TestPRV2MergeServiceUnavailable(t *testing.T) {
	client := &fakeClient{
		mergePullRequestResponse: createResponse([]byte(`<html>Service Unavailable</html>`), 503),
	}
	prV2 := createPRV2(client)
	err := prV2.Merge(context.Background(), "squash", strPtr("my title"), strPtr("my message"))
	if len(client.mergePullRequestCalls) != 1 {
		t.Errorf("merge call count = %d, want 1", len(client.mergePullRequestCalls))
	}
	requireAPICallError(t, err, "pull_request/merge", 503, "Service Unavailable")
}

// We should be able to update a branch.
func TestPRV2UpdateBranchOK(t *testing.T) {
	client := &fakeClient{updateBranchResponse: createResponse(nil, 204)}
	prV2 := createPRV2(client)
	if err := prV2.UpdateBranch(context.Background()); err != nil {
		t.Fatalf("UpdateBranch: %v", err)
	}
	want := []map[string]any{{"pull_number": prV2.number}}
	if diff := cmp.Diff(want, client.updateBranchCalls); diff != "" {
		t.Errorf("update branch calls mismatch (-want +got):\n%s", diff)
	}
}

// We should raise ApiCallException when we get a bad API response.
func TestPRV2UpdateBranchServiceUnavailable(t *testing.T) {
	client := &fakeClient{updateBranchResponse: createResponse([]byte("<html>Service Unavailable</html>"), 503)}
	prV2 := createPRV2(client)
	err := prV2.UpdateBranch(context.Background())
	want := []map[string]any{{"pull_number": prV2.number}}
	if diff := cmp.Diff(want, client.updateBranchCalls); diff != "" {
		t.Errorf("update branch calls mismatch (-want +got):\n%s", diff)
	}
	requireAPICallError(t, err, "pull_request/update_branch", 503, "Service Unavailable")
}

// We should be able to add a label.
func TestPRV2AddLabelOK(t *testing.T) {
	client := &fakeClient{addLabelResponse: createResponse(nil, 204)}
	prV2 := createPRV2(client)
	if err := prV2.AddLabel(context.Background(), "some-label-to-delete"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if len(client.addLabelCalls) != 1 {
		t.Fatalf("add label call count = %d, want 1", len(client.addLabelCalls))
	}
	if got := client.addLabelCalls[0]["label"]; got != "some-label-to-delete" {
		t.Errorf("label = %v, want some-label-to-delete", got)
	}
}

// We should raise ApiCallException when we get a bad API response.
func TestPRV2AddLabelServiceUnavailable(t *testing.T) {
	client := &fakeClient{addLabelResponse: createResponse([]byte("<html>Service Unavailable</html>"), 503)}
	prV2 := createPRV2(client)
	err := prV2.AddLabel(context.Background(), "some-label-to-delete")
	if len(client.addLabelCalls) != 1 {
		t.Fatalf("add label call count = %d, want 1", len(client.addLabelCalls))
	}
	if got := client.addLabelCalls[0]["label"]; got != "some-label-to-delete" {
		t.Errorf("label = %v, want some-label-to-delete", got)
	}
	requireAPICallError(t, err, "pull_request/add_label", 503, "Service Unavailable")
}

// Check that remove_label works.
func TestPRV2RemoveLabelOK(t *testing.T) {
	client := &fakeClient{deleteLabelResponse: createResponse(nil, 204)}
	prV2 := createPRV2(client)
	if err := prV2.RemoveLabel(context.Background(), "some-label-to-delete"); err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if len(client.deleteLabelCalls) != 1 {
		t.Fatalf("delete label call count = %d, want 1", len(client.deleteLabelCalls))
	}
	if got := client.deleteLabelCalls[0]["label"]; got != "some-label-to-delete" {
		t.Errorf("label = %v, want some-label-to-delete", got)
	}
}

// We should raise ApiCallException when we get a bad API response.
func TestPRV2RemoveLabelServiceUnavailable(t *testing.T) {
	client := &fakeClient{deleteLabelResponse: createResponse([]byte("<html>Service Unavailable</html>"), 503)}
	prV2 := createPRV2(client)
	err := prV2.RemoveLabel(context.Background(), "some-label-to-delete")
	if len(client.deleteLabelCalls) != 1 {
		t.Fatalf("delete label call count = %d, want 1", len(client.deleteLabelCalls))
	}
	if got := client.deleteLabelCalls[0]["label"]; got != "some-label-to-delete" {
		t.Errorf("label = %v, want some-label-to-delete", got)
	}
	requireAPICallError(t, err, "pull_request/delete_label", 503, "Service Unavailable")
}

// Check that update_ref works.
func TestUpdateRefOK(t *testing.T) {
	client := &fakeClient{updateRefResponse: createResponse(nil, 200)}
	prV2 := createPRV2(client)
	if err := prV2.UpdateRef(context.Background(), "master", "aa218f56b14c9653891f9e74264a383fa43fefbd"); err != nil {
		t.Fatalf("UpdateRef: %v", err)
	}
	want := []map[string]any{{"ref": "master", "sha": "aa218f56b14c9653891f9e74264a383fa43fefbd"}}
	if diff := cmp.Diff(want, client.updateRefCalls); diff != "" {
		t.Errorf("update ref calls mismatch (-want +got):\n%s", diff)
	}
}

// We should raise ApiCallException when we get a bad API response.
func TestUpdateRefServiceUnavailable(t *testing.T) {
	client := &fakeClient{updateRefResponse: createResponse([]byte("<html>Service Unavailable</html>"), 503)}
	prV2 := createPRV2(client)
	err := prV2.UpdateRef(context.Background(), "master", "aa218f56b14c9653891f9e74264a383fa43fefbd")
	want := []map[string]any{{"ref": "master", "sha": "aa218f56b14c9653891f9e74264a383fa43fefbd"}}
	if diff := cmp.Diff(want, client.updateRefCalls); diff != "" {
		t.Errorf("update ref calls mismatch (-want +got):\n%s", diff)
	}
	requireAPICallError(t, err, "pull_request/update_ref", 503, "Service Unavailable")
}
