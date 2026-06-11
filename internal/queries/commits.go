// Ported from bot/kodiak/queries/commits.py.
package queries

import (
	"encoding/json"
	"log/slog"
)

type CommitConnection struct {
	TotalCount int `json:"totalCount"`
}

// PullRequestCommitUser corresponds to kodiak.queries.commits.User in the
// Python implementation (exported there as PullRequestCommitUser).
type PullRequestCommitUser struct {
	DatabaseID *int64  `json:"databaseId"`
	Login      string  `json:"login"`
	Name       *string `json:"name"`
	Type       string  `json:"type"`
}

type GitActor struct {
	User *PullRequestCommitUser `json:"user"`
}

type Commit struct {
	Parents CommitConnection `json:"parents"`
	Author  *GitActor        `json:"author"`
}

// commitHistoryData mirrors the pydantic models used by get_commits. Pointer
// fields let us replicate pydantic's required-field validation: a single
// invalid node fails the whole parse, matching the Python behavior.
type commitHistoryData struct {
	CommitHistory *struct {
		Nodes []*struct {
			Commit *struct {
				Parents *struct {
					TotalCount *int `json:"totalCount"`
				} `json:"parents"`
				Author *struct {
					User *struct {
						DatabaseID *int64  `json:"databaseId"`
						Login      *string `json:"login"`
						Name       *string `json:"name"`
						Type       *string `json:"type"`
					} `json:"user"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commitHistory"`
}

// GetCommits extracts the commit authors from the pull request commits.
func GetCommits(pr json.RawMessage) []Commit {
	var parsed commitHistoryData
	if err := json.Unmarshal(pr, &parsed); err != nil || parsed.CommitHistory == nil {
		slog.Error("problem parsing commit authors", "error", err)
		return nil
	}
	nodes := parsed.CommitHistory.Nodes
	if len(nodes) == 0 {
		return nil
	}
	commits := make([]Commit, 0, len(nodes))
	for _, node := range nodes {
		if node == nil || node.Commit == nil || node.Commit.Parents == nil ||
			node.Commit.Parents.TotalCount == nil {
			slog.Error("problem parsing commit authors")
			return nil
		}
		commit := Commit{
			Parents: CommitConnection{TotalCount: *node.Commit.Parents.TotalCount},
		}
		if author := node.Commit.Author; author != nil {
			gitActor := GitActor{}
			if author.User != nil {
				if author.User.Login == nil || author.User.Type == nil {
					slog.Error("problem parsing commit authors")
					return nil
				}
				gitActor.User = &PullRequestCommitUser{
					DatabaseID: author.User.DatabaseID,
					Login:      *author.User.Login,
					Name:       author.User.Name,
					Type:       *author.User.Type,
				}
			}
			commit.Author = &gitActor
		}
		commits = append(commits, commit)
	}
	return commits
}
