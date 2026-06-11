// Ported from bot/kodiak/tests/evaluation/test_merge_message_cut_body.py.
//
// Tests for the merge.message.{cut_body_before,cut_body_after} configuration options.
package evaluation

import (
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
)

// Basic check of cut_body_after removing content.
func TestGetMergeBodyCutBodyAfter(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyAfter = "<!-- testing -->"
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("hello <!-- testing -->")}
	mmAssertMergeBody(t, actual, expected)
}

// Verify that the separator is also gone after removing content.
func TestGetMergeBodyCutBodyAndTextAfter(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyAfter = "<!-- testing -->"
	cfg.Merge.Message.CutBodyAndText = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("hello ")}
	mmAssertMergeBody(t, actual, expected)
}

// Verify that the separator is also gone after removing content.
func TestGetMergeBodyCutBodyAndTextBefore(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyBefore = "<!-- testing -->"
	cfg.Merge.Message.CutBodyAndText = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("world")}
	mmAssertMergeBody(t, actual, expected)
}

// We should be able to use strip_html_comments with cut_body_after.
func TestGetMergeBodyCutBodyAfterStripHTML(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyAfter = "<!-- testing -->"
	cfg.Merge.Message.StripHTMLComments = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("hello ")}
	mmAssertMergeBody(t, actual, expected)
}

// We should choose the first substring matching cut_body_after.
func TestGetMergeBodyCutBodyAfterMultipleMarkers(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world<!-- testing --> 123"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyAfter = "<!-- testing -->"
	cfg.Merge.Message.StripHTMLComments = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("hello ")}
	mmAssertMergeBody(t, actual, expected)
}

// Ensure we don't edit the message if there isn't any match found with
// cut_body_after.
func TestGetMergeBodyCutBodyAfterNoMatchFound(t *testing.T) {
	pullRequest := createPullRequest()
	prBody := "hello <!-- foo -->world<!-- bar --> 123"
	pullRequest.Body = prBody
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyAfter = "<!-- buzz -->"
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr(prBody)}
	mmAssertMergeBody(t, actual, expected)
}

// Ensure we don't edit the message if there isn't any match found with
// cut_body_before.
func TestGetMergeBodyCutBodyBeforeNoMatchFound(t *testing.T) {
	pullRequest := createPullRequest()
	prBody := "hello <!-- foo -->world<!-- bar --> 123"
	pullRequest.Body = prBody
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyBefore = "<!-- buzz -->"
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr(prBody)}
	mmAssertMergeBody(t, actual, expected)
}

func TestGetMergeBodyCutBodyBefore(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyBefore = "<!-- testing -->"
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("<!-- testing -->world")}
	mmAssertMergeBody(t, actual, expected)
}

// We should be able to use strip_html_comments with cut_body_before.
func TestGetMergeBodyCutBodyBeforeStripHTML(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyBefore = "<!-- testing -->"
	cfg.Merge.Message.StripHTMLComments = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("world")}
	mmAssertMergeBody(t, actual, expected)
}

// We should choose the first substring matching cut_body_before.
func TestGetMergeBodyCutBodyBeforeMultipleMarkers(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello <!-- testing -->world<!-- testing --> 123"
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.CutBodyBefore = "<!-- testing -->"
	cfg.Merge.Message.StripHTMLComments = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: strPtr("world 123")}
	mmAssertMergeBody(t, actual, expected)
}
