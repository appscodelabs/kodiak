package evaluation

import (
	"fmt"
	"strings"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/queries"
	"github.com/appscodelabs/kodiak/internal/text"
)

func getBodyContent(
	bodyType config.BodyText,
	stripHTMLComments bool,
	cutBodyBefore string,
	cutBodyAfter string,
	cutBodyAndText bool,
	pullRequest queries.PullRequest,
) string {
	if bodyType == config.BodyTextMarkdown {
		body := pullRequest.Body
		if cutBodyBefore != "" {
			startIndex := strings.Index(body, cutBodyBefore)
			if startIndex != -1 {
				body = body[startIndex:]
				if cutBodyAndText {
					body = strings.Replace(body, cutBodyBefore, "", 1)
				}
			}
		}
		if cutBodyAfter != "" {
			endIndex := strings.Index(body, cutBodyAfter)
			if endIndex != -1 {
				body = body[:endIndex+len(cutBodyAfter)]
				if cutBodyAndText {
					body = strings.Replace(body, cutBodyAfter, "", 1)
				}
			}
		}
		if stripHTMLComments {
			return text.StripHTMLCommentsFromMarkdown(body)
		}
		return body
	}
	if bodyType == config.BodyTextPlainText {
		return pullRequest.BodyText
	}
	return pullRequest.BodyHTML
}

// getCoauthorTrailer builds a coauthor trailer given user information.
func getCoauthorTrailer(userID int64, login string, name *string, userType string) string {
	displayName := login
	displayLogin := login
	if userType == "Bot" {
		// the GitHub GraphQL API returns bot names without the `[bot]` suffix.
		displayName += "[bot]"
		displayLogin += "[bot]"
	}
	if name != nil && *name != "" {
		displayName = *name
	}

	// GitHub does not allow our GitHub App to view the email addresses of
	// pull request authors, so we generate a noreply GitHub email address
	// instead which works the same for the GitHub UI.
	authorEmail := fmt.Sprintf("%d+%s@users.noreply.github.com", userID, displayLogin)
	return fmt.Sprintf("Co-authored-by: %s <%s>", displayName, authorEmail)
}

// coauthorKey is a comparable representation of PullRequestCommitUser, used
// for deduplication (the Python implementation deduplicates via a dict keyed
// by the pydantic model).
type coauthorKey struct {
	hasDatabaseID bool
	databaseID    int64
	login         string
	hasName       bool
	name          string
	userType      string
}

func coauthorKeyFor(u queries.PullRequestCommitUser) coauthorKey {
	k := coauthorKey{login: u.Login, userType: u.Type}
	if u.DatabaseID != nil {
		k.hasDatabaseID = true
		k.databaseID = *u.DatabaseID
	}
	if u.Name != nil {
		k.hasName = true
		k.name = *u.Name
	}
	return k
}

// getCoauthorTrailers deduplicates coauthors and converts them to strings.
func getCoauthorTrailers(
	coauthors []queries.PullRequestCommitUser,
	includePullRequestAuthor bool,
	pullRequestAuthorID *int64,
) []string {
	var coauthorTrailers []string
	seen := map[coauthorKey]bool{}
	var dedupedCoauthors []queries.PullRequestCommitUser
	for _, dupeCoauthor := range coauthors {
		key := coauthorKeyFor(dupeCoauthor)
		if seen[key] {
			continue
		}
		seen[key] = true
		dedupedCoauthors = append(dedupedCoauthors, dupeCoauthor)
	}

	for _, coauthor := range dedupedCoauthors {
		if coauthor.DatabaseID == nil {
			continue
		}
		if !includePullRequestAuthor && pullRequestAuthorID != nil &&
			*coauthor.DatabaseID == *pullRequestAuthorID {
			continue
		}

		coauthorTrailers = append(
			coauthorTrailers,
			getCoauthorTrailer(*coauthor.DatabaseID, coauthor.Login, coauthor.Name, coauthor.Type),
		)
	}
	return coauthorTrailers
}

// MergeBody holds merge options for the GitHub merge API call.
type MergeBody struct {
	MergeMethod   config.MergeMethod
	CommitTitle   *string
	CommitMessage *string
}

// GetMergeBody gets merge options for a pull request to call GitHub API.
func GetMergeBody(
	cfg *config.V1,
	mergeMethod config.MergeMethod,
	pullRequest queries.PullRequest,
	commits []queries.Commit,
) MergeBody {
	mergeBody := MergeBody{MergeMethod: mergeMethod}
	if cfg.Merge.Message.Body == config.MergeBodyStylePullRequestBody {
		body := getBodyContent(
			cfg.Merge.Message.BodyType,
			cfg.Merge.Message.StripHTMLComments,
			cfg.Merge.Message.CutBodyBefore,
			cfg.Merge.Message.CutBodyAfter,
			cfg.Merge.Message.CutBodyAndText,
			pullRequest,
		)
		mergeBody.CommitMessage = &body
	}
	if cfg.Merge.Message.Body == config.MergeBodyStyleEmpty {
		empty := ""
		mergeBody.CommitMessage = &empty
	}
	if cfg.Merge.Message.Title == config.MergeTitleStylePullRequestTitle {
		title := pullRequest.Title
		mergeBody.CommitTitle = &title
	}
	if cfg.Merge.Message.IncludePRNumber && mergeBody.CommitTitle != nil {
		*mergeBody.CommitTitle += fmt.Sprintf(" (#%d)", pullRequest.Number)
	}

	// MergeBodyStyle.github_default overrides these settings so we can ensure that GitHub generates the commit body.
	if cfg.Merge.Message.Body == config.MergeBodyStylePullRequestBody ||
		cfg.Merge.Message.Body == config.MergeBodyStyleEmpty {
		// we share coauthor logic between include_pull_request_author and
		// include_coauthors.
		var coauthors []queries.PullRequestCommitUser
		if cfg.Merge.Message.IncludePullRequestAuthor && pullRequest.Author != nil {
			databaseID := pullRequest.Author.DatabaseID
			coauthors = append(coauthors, queries.PullRequestCommitUser{
				Login:      pullRequest.Author.Login,
				DatabaseID: &databaseID,
				Name:       pullRequest.Author.Name,
				Type:       pullRequest.Author.Type,
			})
		}
		if cfg.Merge.Message.IncludeCoauthors {
			for _, commit := range commits {
				if
				// only use commits that have identified authors.
				commit.Author == nil ||
					commit.Author.User == nil ||
					// ignore merge commits. They will have more than one parent.
					commit.Parents.TotalCount > 1 {
					continue
				}
				coauthors = append(coauthors, *commit.Author.User)
			}
		}

		var pullRequestAuthorID *int64
		if pullRequest.Author != nil {
			authorID := pullRequest.Author.DatabaseID
			pullRequestAuthorID = &authorID
		}
		coauthorTrailers := getCoauthorTrailers(
			coauthors,
			cfg.Merge.Message.IncludePullRequestAuthor,
			pullRequestAuthorID,
		)

		trailerBlock := ""
		if cfg.Merge.Message.IncludePullRequestURL {
			trailerBlock += "PR-URL: " + pullRequest.URL
		}

		if len(coauthorTrailers) > 0 {
			if trailerBlock != "" {
				trailerBlock += "\n"
			}
			trailerBlock += strings.Join(coauthorTrailers, "\n")
		}

		if mergeBody.CommitMessage != nil && *mergeBody.CommitMessage != "" {
			if trailerBlock != "" {
				*mergeBody.CommitMessage += "\n\n" + trailerBlock
			}
		} else {
			tb := trailerBlock
			mergeBody.CommitMessage = &tb
		}
	}

	return mergeBody
}
