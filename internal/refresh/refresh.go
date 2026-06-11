// Package refresh triggers reevaluation of pull requests for a given
// installation, ported from bot/kodiak/refresh_pull_requests.py.
//
// This is useful for triggering the bot to remove the paywall status check
// message after a user has started a trial or subscription.
//
// We listen for events on a Redis list. When we receive an event we look up
// all the private PRs associated with that installation. We queue webhook
// events like a GitHub webhook would for each pull request to trigger the bot
// to reevaluate the mergeability.
package refresh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/queries"
	"github.com/appscodelabs/kodiak/internal/queue"
	"github.com/appscodelabs/kodiak/internal/redisclient"
)

const refreshQueueName = "kodiak:refresh_pull_requests_for_installation"

// we query for both organization repositories and user repositories because we
// do not know of the installation is a user or an organization. We filter to
// private repositories only because we may not have access to all public
// repositories and we'll only be able to see private repositories we can access.
//
// The limits on repositories and pull requests are arbitrary.
const refreshQuery = `
query ($login: String!) {
  userdata: user(login: $login) {
    repositories(first: 100, orderBy: {field: UPDATED_AT, direction: DESC}, privacy: PRIVATE) {
      nodes {
        name
        pullRequests(first: 100, states: OPEN, orderBy: {field: UPDATED_AT, direction: DESC}) {
          nodes {
            number
            baseRef {
              name
            }
          }
        }
      }
    }
  }
  organizationdata: organization(login: $login) {
    repositories(first: 100, orderBy: {field: UPDATED_AT, direction: DESC}, privacy: PRIVATE) {
      nodes {
        name
        pullRequests(first: 100, states: OPEN, orderBy: {field: UPDATED_AT, direction: DESC}) {
          nodes {
            number
            baseRef {
              name
            }
          }
        }
      }
    }
  }
}
`

var httpClient = &http.Client{}

func getLoginForInstall(ctx context.Context, installationID string) (string, error) {
	appToken, err := queries.GenerateJWT(appconf.PrivateKey, appconf.GithubAppID)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		appconf.V3URL("/app/installations/"+installationID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.machine-man-preview+json")
	req.Header.Set("Authorization", "Bearer "+appToken)
	res, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("get login for install failed with HTTP status %d", res.StatusCode)
	}
	var parsed struct {
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	return parsed.Account.Login, nil
}

type refreshUserData struct {
	Repositories struct {
		Nodes []struct {
			Name         string `json:"name"`
			PullRequests struct {
				Nodes []struct {
					Number  int `json:"number"`
					BaseRef struct {
						Name string `json:"name"`
					} `json:"baseRef"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"nodes"`
	} `json:"repositories"`
}

func refreshPullRequestsForInstallation(ctx context.Context, rdb *redis.Client, installationID string) error {
	login, err := getLoginForInstall(ctx, installationID)
	if err != nil {
		return err
	}
	token, err := queries.GetTokenForInstall(ctx, httpClient, installationID)
	if err != nil {
		return err
	}
	requestBody, err := json.Marshal(map[string]any{
		"query":     refreshQuery,
		"variables": map[string]any{"login": login},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, appconf.GithubV4APIURL,
		bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close() //nolint:errcheck
	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("refresh pull requests query failed with HTTP status %d", res.StatusCode)
	}

	var parsed struct {
		Data struct {
			UserData         *refreshUserData `json:"userdata"`
			OrganizationData *refreshUserData `json:"organizationdata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return err
	}
	var userKind string
	var data *refreshUserData
	if parsed.Data.OrganizationData != nil {
		userKind = "organization"
		data = parsed.Data.OrganizationData
	} else if parsed.Data.UserData != nil {
		userKind = "user"
		data = parsed.Data.UserData
	} else {
		return errors.New("missing data for user/organization")
	}

	var events []queue.WebhookEvent
	for _, repository := range data.Repositories.Nodes {
		for _, pullRequest := range repository.PullRequests.Nodes {
			events = append(events, queue.WebhookEvent{
				RepoOwner:         login,
				RepoName:          repository.Name,
				TargetName:        pullRequest.BaseRef.Name,
				PullRequestNumber: pullRequest.Number,
				InstallationID:    installationID,
			})
		}
	}
	for _, event := range events {
		if err := rdb.ZAddNX(ctx, event.GetWebhookQueueName(),
			redis.Z{Score: float64(time.Now().UnixNano()) / 1e9, Member: event.JSON()}).Err(); err != nil {
			return err
		}
	}
	slog.Info("pull_requests_refreshed",
		"installation_id", installationID,
		"events_queued", len(events),
		"user_kind", userKind)
	return nil
}

// refreshPullRequestsMessage corresponds to RefreshPullRequestsMessage in
// Python.
type refreshPullRequestsMessage struct {
	InstallationID string `json:"installation_id"`
}

// Run listens on a Redis list for messages triggering pull request
// reevaluations. It only returns on a Redis failure or context cancellation.
func Run(ctx context.Context) error {
	rdb, err := redisclient.Bot()
	if err != nil {
		return err
	}
	for {
		res, err := rdb.BLPop(ctx, 5*time.Second, refreshQueueName).Result()
		if errors.Is(err, redis.Nil) {
			slog.Info("pull_request_refresh", "timeout_reached", true)
			continue
		}
		if err != nil {
			return err
		}
		var message refreshPullRequestsMessage
		if err := json.Unmarshal([]byte(res[1]), &message); err != nil {
			return err
		}
		installationID := message.InstallationID
		start := time.Now()
		// NOTE(port): the Python process crashes (and is restarted by the
		// process supervisor) when a refresh fails; we log and continue.
		if err := refreshPullRequestsForInstallation(ctx, rdb, installationID); err != nil {
			slog.Warn("pull_request_refresh failed",
				"installation_id", installationID, "error", err)
			continue
		}
		slog.Info("pull_request_refresh",
			"installation_id", installationID,
			"duration", time.Since(start).Seconds())
	}
}
