// Ported from bot/kodiak/tests/evaluation/test_merge_message.py.
//
// Tests for general merge.message configuration options.
//
// Some configuration options have their own specific test files.
package evaluation

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/appscodelabs/kodiak/internal/config"
)

// mmAssertMergeBody compares MergeBody values like the Python
// `assert expected == actual` on the pydantic MergeBody model.
func mmAssertMergeBody(t *testing.T, actual, expected MergeBody) {
	t.Helper()
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Errorf("get_merge_body mismatch (-expected +actual):\n%s", diff)
	}
}

func TestPrGetMergeBodyFull(t *testing.T) {
	pullRequest := createPullRequest()
	cfg := config.DefaultV1()
	cfg.Merge.Message.Title = config.MergeTitleStylePullRequestTitle
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePRNumber = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod:   config.MergeMethodSquash,
		CommitTitle:   strPtr(pullRequest.Title + fmt.Sprintf(" (#%d)", pullRequest.Number)),
		CommitMessage: strPtr(pullRequest.Body),
	}
	mmAssertMergeBody(t, actual, expected)
}

func TestPrGetMergeBodyEmpty(t *testing.T) {
	pullRequest := createPullRequest()
	cfg := config.DefaultV1()
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash}
	mmAssertMergeBody(t, actual, expected)
}

func TestGetMergeBodyStripHTMLComments(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.StripHTMLComments = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("hello world")}
	mmAssertMergeBody(t, actual, expected)
}

func TestGetMergeBodyEmpty(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStyleEmpty
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("")}
	mmAssertMergeBody(t, actual, expected)
}
