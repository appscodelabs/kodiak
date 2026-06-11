// Ported from the get_commits tests in bot/kodiak/test_queries.py.
package queries

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// createCommit corresponds to kodiak.tests.fixtures.create_commit.
func createCommit(databaseID *int64, name *string, login, commitType string, parents int) Commit {
	return Commit{
		Parents: CommitConnection{TotalCount: parents},
		Author: &GitActor{
			User: &PullRequestCommitUser{
				DatabaseID: databaseID,
				Name:       name,
				Login:      login,
				Type:       commitType,
			},
		},
	}
}

// Verify we parse commit authors correctly. We should handle the nullability
// of name and databaseId.
func TestGetCommits(t *testing.T) {
	pullRequestData := json.RawMessage(`{
  "commitHistory": {
    "nodes": [
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": "Christopher Dignam",
              "databaseId": 1929960,
              "login": "chdsbd",
              "type": "User"
            }
          }
        }
      },
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": "b-lowe",
              "databaseId": 5345234,
              "login": "b-lowe",
              "type": "User"
            }
          }
        }
      },
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": null,
              "databaseId": 435453,
              "login": "kodiakhq",
              "type": "Bot"
            }
          }
        }
      },
      {"commit": {"parents": {"totalCount": 1}, "author": {"user": null}}},
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": "Christopher Dignam",
              "databaseId": 1929960,
              "login": "chdsbd",
              "type": "User"
            }
          }
        }
      },
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": null,
              "databaseId": null,
              "login": "j-doe",
              "type": "SomeGitActor"
            }
          }
        }
      }
    ]
  }
}`)
	res := GetCommits(pullRequestData)
	expected := []Commit{
		createCommit(ptr(int64(1929960)), ptr("Christopher Dignam"), "chdsbd", "User", 1),
		createCommit(ptr(int64(5345234)), ptr("b-lowe"), "b-lowe", "User", 1),
		createCommit(ptr(int64(435453)), nil, "kodiakhq", "Bot", 1),
		{Parents: CommitConnection{TotalCount: 1}, Author: &GitActor{User: nil}},
		createCommit(ptr(int64(1929960)), ptr("Christopher Dignam"), "chdsbd", "User", 1),
		createCommit(nil, nil, "j-doe", "SomeGitActor", 1),
	}
	if diff := cmp.Diff(expected, res, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("commits mismatch (-expected +actual):\n%s", diff)
	}
}

// We should handle parsing errors without raising an exception.
func TestGetCommitsErrorHandling(t *testing.T) {
	pullRequestData := json.RawMessage(`{
  "commitHistory": {
    "nodes": [
      {"commit": {"parents": {"totalCount": 1}, "author": {"user": null}}},
      {"commit": {"parents": {"totalCount": 1}, "author": null}},
      {
        "commit": {
          "parents": {"totalCount": 3},
          "author": {
            "user": {
              "name": null,
              "databaseId": 435453,
              "login": "kodiakhq",
              "type": "Bot"
            }
          }
        }
      },
      {
        "commit": {
          "parents": {"totalCount": 1},
          "author": {
            "user": {
              "name": "Christopher Dignam",
              "databaseId": 1929960,
              "login": "chdsbd",
              "type": "User"
            }
          }
        }
      },
      {
        "commit": {
          "parents": {"totalCount": 2},
          "author": {
            "user": {
              "name": null,
              "databaseId": null,
              "login": "j-doe",
              "type": "SomeGitActor"
            }
          }
        }
      }
    ]
  }
}`)
	res := GetCommits(pullRequestData)
	expected := []Commit{
		{Parents: CommitConnection{TotalCount: 1}, Author: &GitActor{User: nil}},
		{Parents: CommitConnection{TotalCount: 1}, Author: nil},
		createCommit(ptr(int64(435453)), nil, "kodiakhq", "Bot", 3),
		createCommit(ptr(int64(1929960)), ptr("Christopher Dignam"), "chdsbd", "User", 1),
		createCommit(nil, nil, "j-doe", "SomeGitActor", 2),
	}
	if diff := cmp.Diff(expected, res, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("commits mismatch (-expected +actual):\n%s", diff)
	}
}

// We should handle parsing errors without raising an exception.
func TestGetCommitsErrorHandlingMissingResponse(t *testing.T) {
	res := GetCommits(json.RawMessage(`{"commitHistory": null}`))
	if len(res) != 0 {
		t.Errorf("expected no commits, got %+v", res)
	}
}
