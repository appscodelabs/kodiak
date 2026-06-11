// Ported from bot/kodiak/tests/evaluation/test_merge_message_trailers.py.
//
// Tests for commit message "trailers".
//
// Tested configuration options:
// - merge.message.include_pull_request_url
// - merge.message.include_coauthors
// - merge.message.include_pull_request_author
package evaluation

import (
	"fmt"
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/queries"
)

// Ensure that when the appropriate config option is set, we include the
// pull request url in the commit message.
func TestGetMergeBodyIncludesPullRequestURL(t *testing.T) {
	pullRequest := createPullRequest()
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePullRequestURL = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod: config.MergeMethodSquash,
		CommitMessage: strPtr("# some description\n\n" +
			"PR-URL: https://github.com/example_org/example_repo/pull/65"),
	}
	mmAssertMergeBody(t, actual, expected)
}

// We should not set a commit message when merge.body = "github_default".
func TestGetMergeBodyIncludesPullRequestURLGithubDefault(t *testing.T) {
	pullRequest := createPullRequest()
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStyleGithubDefault
	cfg.Merge.Message.IncludePullRequestURL = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodSquash, CommitMessage: nil}
	mmAssertMergeBody(t, actual, expected)
}

// Coauthor should appear after the pull request url
func TestGetMergeBodyIncludesPullRequestURLWithCoauthor(t *testing.T) {
	pullRequest := createPullRequest()
	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePullRequestURL = true
	cfg.Merge.Message.IncludePullRequestAuthor = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod: config.MergeMethodSquash,
		CommitMessage: strPtr("# some description\n\n" +
			"PR-URL: https://github.com/example_org/example_repo/pull/65\n" +
			"Co-authored-by: Barry Berkman <828352+barry@users.noreply.github.com>"),
	}
	mmAssertMergeBody(t, actual, expected)
}

func TestGetMergeBodyIncludePullRequestAuthorUser(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"

	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePullRequestAuthor = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod:   config.MergeMethodSquash,
		CommitMessage: strPtr("hello world\n\nCo-authored-by: Barry Berkman <828352+barry@users.noreply.github.com>"),
	}
	mmAssertMergeBody(t, actual, expected)
}

func TestGetMergeBodyIncludePullRequestAuthorBot(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	if pullRequest.Author == nil {
		t.Fatal("expected pull request author")
	}
	pullRequest.Author.Name = nil
	pullRequest.Author.Type = "Bot"

	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePullRequestAuthor = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod:   config.MergeMethodSquash,
		CommitMessage: strPtr("hello world\n\nCo-authored-by: barry[bot] <828352+barry[bot]@users.noreply.github.com>"),
	}
	mmAssertMergeBody(t, actual, expected)
}

// Test case where actor is not a User and Bot to see how we handle weird cases.
func TestGetMergeBodyIncludePullRequestAuthorMannequin(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	if pullRequest.Author == nil {
		t.Fatal("expected pull request author")
	}
	pullRequest.Author.Name = nil
	pullRequest.Author.Type = "Mannequin"

	cfg := config.DefaultV1()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludePullRequestAuthor = true
	actual := GetMergeBody(&cfg, config.MergeMethodSquash, pullRequest, nil)
	expected := MergeBody{
		MergeMethod:   config.MergeMethodSquash,
		CommitMessage: strPtr("hello world\n\nCo-authored-by: barry <828352+barry@users.noreply.github.com>"),
	}
	mmAssertMergeBody(t, actual, expected)
}

// We only include trailers MergeBodyStyle.pull_request_body and
// MergeBodyStyle.empty. Verify we don't include trailers for
// MergeBodyStyle.github_default.
func TestGetMergeBodyIncludePullRequestAuthorInvalidBodyStyle(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	cfg := createConfig()
	cfg.Merge.Message.IncludePullRequestAuthor = true

	cfg.Merge.Message.Body = config.MergeBodyStyleGithubDefault
	actual := GetMergeBody(cfg, config.MergeMethodMerge, pullRequest, nil)
	expected := MergeBody{MergeMethod: config.MergeMethodMerge, CommitMessage: nil}
	mmAssertMergeBody(t, actual, expected)
}

// Verify we include coauthor trailers for MergeBodyStyle.pull_request_body.
func TestGetMergeBodyIncludeCoauthors(t *testing.T) {
	pullRequest := createPullRequest()
	if pullRequest.Author == nil {
		t.Fatal("expected pull request author")
	}
	pullRequest.Body = "hello world"
	cfg := createConfig()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludeCoauthors = true
	cfg.Merge.Message.IncludePullRequestAuthor = false

	actual := GetMergeBody(cfg, config.MergeMethodMerge, pullRequest, []queries.Commit{
		createCommit(i64Ptr(9023904), strPtr("Bernard Lowe"), "b-lowe", "User", 1),
		createCommit(i64Ptr(590434), strPtr("Maeve Millay"), "maeve-m", "Bot", 1),
		// we default to the login when name is None.
		createCommit(i64Ptr(771233), nil, "d-abernathy", "Bot", 1),
		// without a databaseID the commit author will be ignored.
		createCommit(nil, nil, "william", "User", 1),
		// duplicate should be ignored.
		createCommit(i64Ptr(9023904), strPtr("Bernard Lowe"), "b-lowe", "User", 1),
		// merge commits should be ignored. merge commits will have more than
		// one parent.
		createCommit(i64Ptr(1), strPtr("Arnold Weber"), "arnold", "User", 2),
		// pull request author should be ignored when
		// include_pull_request_author is not enabled
		createCommit(i64Ptr(pullRequest.Author.DatabaseID), strPtr("Joe PR Author"), "j-author", "User", 1),
	})
	expected := MergeBody{
		MergeMethod: config.MergeMethodMerge,
		CommitMessage: strPtr("hello world\n\n" +
			"Co-authored-by: Bernard Lowe <9023904+b-lowe@users.noreply.github.com>\n" +
			"Co-authored-by: Maeve Millay <590434+maeve-m[bot]@users.noreply.github.com>\n" +
			"Co-authored-by: d-abernathy[bot] <771233+d-abernathy[bot]@users.noreply.github.com>"),
	}
	mmAssertMergeBody(t, actual, expected)
}

// We should include the pull request author when configured.
func TestGetMergeBodyIncludeCoauthorsIncludePrAuthor(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	cfg := createConfig()
	cfg.Merge.Message.Body = config.MergeBodyStylePullRequestBody
	cfg.Merge.Message.IncludeCoauthors = true
	cfg.Merge.Message.IncludePullRequestAuthor = true
	if pullRequest.Author == nil {
		t.Fatal("expected pull request author")
	}

	actual := GetMergeBody(cfg, config.MergeMethodMerge, pullRequest, []queries.Commit{
		createCommit(i64Ptr(9023904), strPtr("Bernard Lowe"), "b-lowe", "User", 1),
		// we should ignore a duplicate entry for the PR author when
		// include_pull_request_author is enabled.
		createCommit(
			i64Ptr(pullRequest.Author.DatabaseID),
			pullRequest.Author.Name,
			pullRequest.Author.Login,
			pullRequest.Author.Type,
			1,
		),
	})
	expected := MergeBody{
		MergeMethod: config.MergeMethodMerge,
		CommitMessage: strPtr(fmt.Sprintf(
			"hello world\n\nCo-authored-by: %s <%d+%s@users.noreply.github.com>\nCo-authored-by: Bernard Lowe <9023904+b-lowe@users.noreply.github.com>",
			*pullRequest.Author.Name, pullRequest.Author.DatabaseID, pullRequest.Author.Login)),
	}
	mmAssertMergeBody(t, actual, expected)
}

// We only include trailers for MergeBodyStyle.pull_request_body and MergeBodyStyle.empty. Verify we don't add coauthor trailers for MergeBodyStyle.github_default.
func TestGetMergeBodyIncludeCoauthorsInvalidBodyStyle(t *testing.T) {
	pullRequest := createPullRequest()
	pullRequest.Body = "hello world"
	cfg := createConfig()
	cfg.Merge.Message.IncludeCoauthors = true
	cfg.Merge.Message.Body = config.MergeBodyStyleGithubDefault
	actual := GetMergeBody(cfg, config.MergeMethodMerge, pullRequest, []queries.Commit{
		createCommit(i64Ptr(9023904), strPtr(""), "b-lowe", "User", 1),
		createCommit(i64Ptr(590434), strPtr("Maeve Millay"), "maeve-m", "Bot", 1),
	})
	expected := MergeBody{MergeMethod: config.MergeMethodMerge, CommitMessage: nil}
	mmAssertMergeBody(t, actual, expected)
}

// Include coauthors should attach coauthor when `merge.message.body = "pull_request_body"`
func TestMergeableIncludeCoauthors(t *testing.T) {
	cfg := createConfig()
	cfg.Merge.Message.IncludeCoauthors = true

	for _, tt := range []struct {
		bodyStyle     config.MergeBodyStyle
		commitMessage string
	}{
		{
			config.MergeBodyStylePullRequestBody,
			"# some description\n\nCo-authored-by: Barry Block <73213123+b-block@users.noreply.github.com>",
		},
		{
			config.MergeBodyStyleEmpty,
			"Co-authored-by: Barry Block <73213123+b-block@users.noreply.github.com>",
		},
	} {
		cfg.Merge.Message.Body = tt.bodyStyle
		api := createAPI()
		params := createMergeableParams()
		params.Config = cfg
		params.Commits = []queries.Commit{
			createCommit(i64Ptr(73213123), strPtr("Barry Block"), "b-block", "User", 1),
		}
		params.Merging = true
		runMergeable(t, api, params)

		if len(api.SetStatusCalls) != 2 {
			t.Fatalf("set_status call count: got %d, want 2", len(api.SetStatusCalls))
		}
		assertContains(t, api.SetStatusCalls[0].Msg, "attempting to merge PR", "first set_status message")
		assertEqual(t, api.SetStatusCalls[1].Msg, "merge complete 🎉", "second set_status message")

		if len(api.MergeCalls) != 1 {
			t.Fatalf("merge call count: got %d, want 1", len(api.MergeCalls))
		}
		if api.MergeCalls[0].CommitMessage == nil {
			t.Errorf("commit_message: got nil, want %q", tt.commitMessage)
		} else {
			assertEqual(t, *api.MergeCalls[0].CommitMessage, tt.commitMessage, "commit_message")
		}
		assertEqual(t, len(api.UpdateBranchCalls), 0, "update_branch call count")
		assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
		assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
	}
}
