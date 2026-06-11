// Package mergequeue ports web_api/web_api/merge_queues.py: reading the
// bot's active merge queues from Redis.
package mergequeue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

// PullRequest JSON keys match the pydantic model field names consumed by the
// frontend.
type PullRequest struct {
	Number           string   `json:"number"`
	Merging          bool     `json:"merging"`
	AddedAtTimestamp *float64 `json:"added_at_timestamp"`
}

type Queue struct {
	Branch       string        `json:"branch"`
	PullRequests []PullRequest `json:"pull_requests"`
}

// Repository groups queues for an owner/repo pair.
type Repository struct {
	Owner  string
	Repo   string
	Queues []Queue
}

// kodiakQueueEntry must match the schema written by the bot's queue.
type kodiakQueueEntry struct {
	PullRequestNumber string `json:"pull_request_number"`
}

// UnmarshalJSON accepts pull_request_number as either a JSON string or a
// number. The bot serializes it as an integer
// (WebhookEvent.pull_request_number), which pydantic's `str` field coerces;
// mirror that coercion here.
func (e *kodiakQueueEntry) UnmarshalJSON(data []byte) error {
	var raw struct {
		PullRequestNumber json.RawMessage `json:"pull_request_number"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.PullRequestNumber) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw.PullRequestNumber, &s); err == nil {
		e.PullRequestNumber = s
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(raw.PullRequestNumber, &n); err != nil {
		return err
	}
	e.PullRequestNumber = n.String()
	return nil
}

// queueInfoFromName parses a queue name like
// "merge_queue:11256551.sbdchd/squawk/main".
func queueInfoFromName(name string) (org, repo, branch string, err error) {
	_, rest, found := strings.Cut(name, ".")
	if !found {
		return "", "", "", fmt.Errorf("invalid queue name: %q", name)
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid queue name: %q", name)
	}
	return parts[0], parts[1], parts[2], nil
}

func parseKodiakQueueEntry(data string) *kodiakQueueEntry {
	var entry kodiakQueueEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil || entry.PullRequestNumber == "" {
		slog.Warn("failed to parse pull request", "error", err)
		return nil
	}
	return &entry
}

// GetActiveMergeQueues returns the active merge queues for an installation,
// grouped by repository.
func GetActiveMergeQueues(ctx context.Context, rdb *redis.Client, installID string) ([]Repository, error) {
	queueNames, err := rdb.SMembers(ctx, "merge_queue_by_install:"+installID).Result()
	if err != nil {
		return nil, err
	}

	pipe := rdb.Pipeline()
	type queueCmds struct {
		target     *redis.StringCmd
		targetTime *redis.StringCmd
		waiting    *redis.ZSliceCmd
	}
	cmds := make([]queueCmds, 0, len(queueNames))
	for _, queue := range queueNames {
		cmds = append(cmds, queueCmds{
			target:     pipe.Get(ctx, queue+":target"),
			targetTime: pipe.Get(ctx, queue+":target:time"),
			waiting:    pipe.ZRangeWithScores(ctx, queue, 0, 1000),
		})
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	var repos []Repository
	repoIndex := map[string]int{}
	for i, queue := range queueNames {
		org, repo, branch, err := queueInfoFromName(queue)
		if err != nil {
			return nil, err
		}

		var pullRequests []PullRequest
		var mergingPR *kodiakQueueEntry
		if raw, err := cmds[i].target.Result(); err == nil && raw != "" {
			mergingPR = parseKodiakQueueEntry(raw)
		}
		if mergingPR != nil {
			var addedAt *float64
			if rawTime, err := cmds[i].targetTime.Result(); err == nil && rawTime != "" {
				if v, err := strconv.ParseFloat(rawTime, 64); err == nil {
					addedAt = &v
				}
			}
			pullRequests = append(pullRequests, PullRequest{
				Number:           mergingPR.PullRequestNumber,
				Merging:          true,
				AddedAtTimestamp: addedAt,
			})
		}
		waiting, err := cmds[i].waiting.Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		for _, z := range waiting {
			member, ok := z.Member.(string)
			if !ok {
				continue
			}
			pr := parseKodiakQueueEntry(member)
			if pr == nil {
				continue
			}
			// the merging PR can exist in the waiting set too. We only want
			// to show it once.
			if mergingPR != nil && pr.PullRequestNumber == mergingPR.PullRequestNumber {
				continue
			}
			score := z.Score
			pullRequests = append(pullRequests, PullRequest{
				Number:           pr.PullRequestNumber,
				AddedAtTimestamp: &score,
			})
		}

		sort.SliceStable(pullRequests, func(a, b int) bool {
			av, bv := 0.0, 0.0
			if pullRequests[a].AddedAtTimestamp != nil {
				av = *pullRequests[a].AddedAtTimestamp
			}
			if pullRequests[b].AddedAtTimestamp != nil {
				bv = *pullRequests[b].AddedAtTimestamp
			}
			return av < bv
		})

		// only report targets that have pull requests merging or in queue.
		if len(pullRequests) == 0 {
			continue
		}
		key := org + "/" + repo
		idx, ok := repoIndex[key]
		if !ok {
			repos = append(repos, Repository{Owner: org, Repo: repo})
			idx = len(repos) - 1
			repoIndex[key] = idx
		}
		repos[idx].Queues = append(repos[idx].Queues, Queue{Branch: branch, PullRequests: pullRequests})
	}
	return repos, nil
}
