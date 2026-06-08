// Merge-commit message construction.
//
// Ported from bot/kodiak/evaluation.py: get_body_content, get_commit_author_info,
// get_coauthor_trailer, get_merge_body.

package evaluation

import (
	"fmt"
	"strings"

	"github.com/appscodelabs/kodiak/internal/bot/config"
	"github.com/appscodelabs/kodiak/internal/bot/queries"
	"github.com/appscodelabs/kodiak/internal/bot/text"
)

// MergeBody is the title/method/body triple we send to GitHub's merge endpoint.
type MergeBody struct {
	MergeMethod   string
	CommitTitle   string
	CommitMessage string
	// HasTitle / HasMessage track whether the caller actually set these
	// (mirroring the Python None/string distinction), since GitHub rejects
	// nulls and uses its own default when the keys are omitted.
	HasTitle   bool
	HasMessage bool
}

func bodyContent(bodyType config.BodyText, stripHTMLComments bool, pr queries.PullRequest) string {
	switch bodyType {
	case config.BodyTextMarkdown:
		body := pr.Body
		if stripHTMLComments {
			body = text.StripHTMLComments(body)
		}
		return body
	case config.BodyTextPlain:
		return pr.BodyText
	case config.BodyTextHTML:
		return pr.BodyHTML
	default:
		return pr.Body
	}
}

type commitAuthorName struct {
	Name  string
	Login string
}

func commitAuthorInfo(login string, name string, kind string) commitAuthorName {
	an := login
	al := login
	if kind == "Bot" {
		an += "[bot]"
		al += "[bot]"
	}
	if name != "" {
		an = name
	}
	return commitAuthorName{Name: an, Login: al}
}

func coauthorTrailer(userID int64, login, name string) string {
	return fmt.Sprintf("Co-authored-by: %s <%d+%s@users.noreply.github.com>", name, userID, login)
}

// BuildMergeBody assembles the title/message/method for the merge call.
func BuildMergeBody(cfg *config.V1, pr queries.PullRequest, commitAuthors []queries.CommitAuthor) MergeBody {
	mb := MergeBody{MergeMethod: string(cfg.Merge.Method)}

	switch cfg.Merge.Message.Body {
	case config.MergeBodyPullRequestBody:
		mb.CommitMessage = bodyContent(cfg.Merge.Message.BodyType, cfg.Merge.Message.StripHTMLComments, pr)
		mb.HasMessage = true
	case config.MergeBodyEmpty:
		mb.CommitMessage = ""
		mb.HasMessage = true
	}

	if cfg.Merge.Message.Title == config.MergeTitlePullRequestTitle {
		mb.CommitTitle = pr.Title
		mb.HasTitle = true
	}

	if cfg.Merge.Message.IncludePRNumber && mb.HasTitle {
		mb.CommitTitle = fmt.Sprintf("%s (#%d)", mb.CommitTitle, pr.Number)
	}

	if cfg.Merge.Message.IncludePullRequestURL {
		if !mb.HasMessage {
			mb.CommitMessage = pr.URL
			mb.HasMessage = true
		} else {
			mb.CommitMessage = mb.CommitMessage + "\n\n" + pr.URL
		}
	}

	var trailers []string
	if cfg.Merge.Message.IncludePullRequestAuthor {
		a := commitAuthorInfo(pr.Author.Login, pr.Author.Name, pr.Author.Type)
		trailers = append(trailers, coauthorTrailer(pr.Author.DatabaseID, a.Login, a.Name))
	}
	if cfg.Merge.Message.IncludeCoauthors {
		for _, ca := range commitAuthors {
			if ca.DatabaseID == nil || *ca.DatabaseID == pr.Author.DatabaseID {
				continue
			}
			a := commitAuthorInfo(ca.Login, ca.Name, ca.Type)
			trailers = append(trailers, coauthorTrailer(*ca.DatabaseID, a.Login, a.Name))
		}
	}
	if len(trailers) > 0 && cfg.Merge.Message.Body == config.MergeBodyPullRequestBody {
		mb.CommitMessage = mb.CommitMessage + "\n\n" + strings.Join(trailers, "\n")
		mb.HasMessage = true
	}
	return mb
}
