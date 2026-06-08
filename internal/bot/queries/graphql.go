// GraphQL request, response parsing, and GetEventInfo implementation.
//
// Ported from bot/kodiak/queries.py (GET_EVENT_INFO_QUERY block + Client.get_event_info).

package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/appscodelabs/kodiak/internal/bot/config"
)

const defaultBranchQuery = `query ($owner: String!, $repo: String!) {
  repository(owner: $owner, name: $repo) {
    defaultBranchRef { name }
  }
}`

const getEventInfoQuery = `query GetEventInfo($owner: String!, $repo: String!, $rootConfigFileExpression: String!, $githubConfigFileExpression: String!, $PRNumber: Int!) {
  repository(owner: $owner, name: $repo) {
    branchProtectionRules(first: 100) {
      nodes {
        matchingRefs(first: 100) { nodes { name } }
        requiresApprovingReviews
        requiredApprovingReviewCount
        requiresStatusChecks
        requiredStatusCheckContexts
        requiresStrictStatusChecks
        requiresCommitSignatures
        restrictsPushes
        pushAllowances(first: 100) { nodes { actor { ... on App { databaseId } } } }
      }
    }
    mergeCommitAllowed
    rebaseMergeAllowed
    squashMergeAllowed
    deleteBranchOnMerge
    isPrivate
    pullRequest(number: $PRNumber) {
      id
      author {
        login
        type: __typename
        ... on User { databaseId name }
        ... on Bot { databaseId }
      }
      mergeStateStatus
      state
      mergeable
      isCrossRepository
      reviewRequests(first: 100) {
        nodes {
          requestedReviewer {
            __typename
            ... on User { login }
            ... on Team { name }
            ... on Mannequin { login }
          }
        }
      }
      title
      body
      bodyText
      bodyHTML
      url
      reviews(first: 100) {
        nodes {
          createdAt
          state
          author { login type: __typename }
          authorAssociation
        }
        totalCount
      }
      baseRefName
      headRefName
      headRef { id }
      commitHistory: commits(last: 100) {
        nodes { commit { author { user { databaseId name login type: __typename } } } }
      }
      commits(last: 1) {
        nodes {
          commit {
            checkSuites(first: 100) {
              nodes { checkRuns(first: 100) { nodes { name conclusion } } }
            }
            oid
            signature { isValid }
            status { state contexts { context state } }
          }
        }
      }
      labels(first: 100) { nodes { name } totalCount }
    }
    rootConfigFile: object(expression: $rootConfigFileExpression) { ... on Blob { text } }
    githubConfigFile: object(expression: $githubConfigFileExpression) { ... on Blob { text } }
  }
}`

type graphqlError struct {
	Message   string         `json:"message"`
	Locations []json.RawMessage `json:"locations"`
	Type      *string        `json:"type"`
	Path      []string       `json:"path"`
}

type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors"`
}

func (c *client) sendQuery(ctx context.Context, query string, variables map[string]any) (*graphqlResponse, error) {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.antiope-preview+json,application/vnd.github.merge-info-preview+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graphql: status=%d body=%s", resp.StatusCode, string(raw))
	}
	var out graphqlResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}
	if len(out.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", out.Errors[0].Message)
	}
	return &out, nil
}

// GetDefaultBranchName looks up the default branch ref name.
func (c *client) GetDefaultBranchName(ctx context.Context) (string, error) {
	resp, err := c.sendQuery(ctx, defaultBranchQuery, map[string]any{
		"owner": c.owner, "repo": c.repo,
	})
	if err != nil {
		return "", err
	}
	var payload struct {
		Repository struct {
			DefaultBranchRef struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(resp.Data, &payload); err != nil {
		return "", err
	}
	return payload.Repository.DefaultBranchRef.Name, nil
}

// rawEventInfo mirrors the GraphQL response payload.
type rawEventInfo struct {
	Repository struct {
		BranchProtectionRules struct {
			Nodes []rawBranchProtection `json:"nodes"`
		} `json:"branchProtectionRules"`
		MergeCommitAllowed  bool `json:"mergeCommitAllowed"`
		RebaseMergeAllowed  bool `json:"rebaseMergeAllowed"`
		SquashMergeAllowed  bool `json:"squashMergeAllowed"`
		DeleteBranchOnMerge bool `json:"deleteBranchOnMerge"`
		IsPrivate           bool `json:"isPrivate"`
		PullRequest         *rawPullRequest `json:"pullRequest"`
		RootConfigFile      *rawBlob        `json:"rootConfigFile"`
		GitHubConfigFile    *rawBlob        `json:"githubConfigFile"`
	} `json:"repository"`
}

type rawBlob struct {
	Text string `json:"text"`
}

type rawBranchProtection struct {
	MatchingRefs struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"matchingRefs"`
	RequiresApprovingReviews     bool     `json:"requiresApprovingReviews"`
	RequiredApprovingReviewCount *int     `json:"requiredApprovingReviewCount"`
	RequiresStatusChecks         bool     `json:"requiresStatusChecks"`
	RequiredStatusCheckContexts  []string `json:"requiredStatusCheckContexts"`
	RequiresStrictStatusChecks   bool     `json:"requiresStrictStatusChecks"`
	RequiresCommitSignatures     bool     `json:"requiresCommitSignatures"`
	RestrictsPushes              bool     `json:"restrictsPushes"`
	PushAllowances               struct {
		Nodes []struct {
			Actor struct {
				DatabaseID *int64 `json:"databaseId"`
			} `json:"actor"`
		} `json:"nodes"`
	} `json:"pushAllowances"`
}

type rawAuthor struct {
	Login      string `json:"login"`
	Type       string `json:"type"`
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
}

type rawReviewRequest struct {
	RequestedReviewer struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
		Name     string `json:"name"`
	} `json:"requestedReviewer"`
}

type rawReview struct {
	State     PRReviewState `json:"state"`
	CreatedAt time.Time     `json:"createdAt"`
	Author    struct {
		Login string    `json:"login"`
		Type  ActorType `json:"type"`
	} `json:"author"`
	AuthorAssociation string `json:"authorAssociation"`
}

type rawCommit struct {
	Commit struct {
		Author struct {
			User *rawAuthor `json:"user"`
		} `json:"author"`
		OID         string `json:"oid"`
		CheckSuites struct {
			Nodes []struct {
				CheckRuns struct {
					Nodes []rawCheckRun `json:"nodes"`
				} `json:"checkRuns"`
			} `json:"nodes"`
		} `json:"checkSuites"`
		Signature *struct {
			IsValid bool `json:"isValid"`
		} `json:"signature"`
		Status *struct {
			State    StatusState `json:"state"`
			Contexts []struct {
				Context string      `json:"context"`
				State   StatusState `json:"state"`
			} `json:"contexts"`
		} `json:"status"`
	} `json:"commit"`
}

type rawCheckRun struct {
	Name       string               `json:"name"`
	Conclusion CheckConclusionState `json:"conclusion"`
}

type rawLabel struct {
	Name string `json:"name"`
}

type rawPullRequest struct {
	ID                string           `json:"id"`
	Author            rawAuthor        `json:"author"`
	MergeStateStatus  MergeStateStatus `json:"mergeStateStatus"`
	State             PullRequestState `json:"state"`
	Mergeable         MergeableState   `json:"mergeable"`
	IsCrossRepository bool             `json:"isCrossRepository"`
	ReviewRequests    struct {
		Nodes []rawReviewRequest `json:"nodes"`
	} `json:"reviewRequests"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	BodyText      string `json:"bodyText"`
	BodyHTML      string `json:"bodyHTML"`
	URL           string `json:"url"`
	Reviews       struct {
		Nodes []rawReview `json:"nodes"`
	} `json:"reviews"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	HeadRef     *struct {
		ID string `json:"id"`
	} `json:"headRef"`
	CommitHistory struct {
		Nodes []rawCommit `json:"nodes"`
	} `json:"commitHistory"`
	Commits struct {
		Nodes []rawCommit `json:"nodes"`
	} `json:"commits"`
	Labels struct {
		Nodes []rawLabel `json:"nodes"`
	} `json:"labels"`
}

// getEventInfoImpl runs the GetEventInfo GraphQL query and assembles the
// EventInfo struct used by evaluation.
func (c *client) getEventInfoImpl(ctx context.Context, number int) (*EventInfo, error) {
	branch, err := c.GetDefaultBranchName(ctx)
	if err != nil {
		return nil, fmt.Errorf("default branch: %w", err)
	}
	rootCfg := branch + ":.kodiak.toml"
	githubCfg := branch + ":.github/.kodiak.toml"

	resp, err := c.sendQuery(ctx, getEventInfoQuery, map[string]any{
		"owner":                       c.owner,
		"repo":                        c.repo,
		"rootConfigFileExpression":    rootCfg,
		"githubConfigFileExpression":  githubCfg,
		"PRNumber":                    number,
	})
	if err != nil {
		return nil, err
	}
	var raw rawEventInfo
	if err := json.Unmarshal(resp.Data, &raw); err != nil {
		return nil, fmt.Errorf("decode event info: %w", err)
	}
	r := raw.Repository
	if r.PullRequest == nil {
		return nil, fmt.Errorf("pull request not found")
	}

	var configStr, configExpr string
	switch {
	case r.RootConfigFile != nil && r.RootConfigFile.Text != "":
		configStr = r.RootConfigFile.Text
		configExpr = rootCfg
	case r.GitHubConfigFile != nil && r.GitHubConfigFile.Text != "":
		configStr = r.GitHubConfigFile.Text
		configExpr = githubCfg
	default:
		return nil, fmt.Errorf("no .kodiak.toml found")
	}

	cfg, cfgErr := config.ParseTOML(configStr)

	pr := r.PullRequest
	info := &EventInfo{
		Config:               cfg,
		ConfigParseErr:       cfgErr,
		ConfigStr:            configStr,
		ConfigFileExpression: configExpr,
		Repository: RepoInfo{
			MergeCommitAllowed:  r.MergeCommitAllowed,
			RebaseMergeAllowed:  r.RebaseMergeAllowed,
			SquashMergeAllowed:  r.SquashMergeAllowed,
			DeleteBranchOnMerge: r.DeleteBranchOnMerge,
			IsPrivate:           r.IsPrivate,
		},
		HeadExists:          pr.HeadRef != nil && pr.HeadRef.ID != "",
		ValidMergeMethods:   buildValidMergeMethods(r.MergeCommitAllowed, r.RebaseMergeAllowed, r.SquashMergeAllowed),
		ReviewRequests:      buildReviewRequests(pr.ReviewRequests.Nodes),
		BranchProtection:    matchBranchProtection(r.BranchProtectionRules.Nodes, pr.BaseRefName),
		CommitAuthors:       extractCommitAuthors(pr.CommitHistory.Nodes),
		StatusContexts:      extractStatusContexts(pr.Commits.Nodes),
		CheckRuns:           extractCheckRuns(pr.Commits.Nodes),
		ValidSignature:      extractSignature(pr.Commits.Nodes),
		PullRequest: PullRequest{
			ID:                pr.ID,
			Number:            number,
			Title:             pr.Title,
			Body:              pr.Body,
			BodyText:          pr.BodyText,
			BodyHTML:          pr.BodyHTML,
			Author: PullRequestAuthor{
				Login:      pr.Author.Login,
				DatabaseID: pr.Author.DatabaseID,
				Type:       pr.Author.Type,
				Name:       pr.Author.Name,
			},
			MergeStateStatus:  pr.MergeStateStatus,
			State:             pr.State,
			Mergeable:         pr.Mergeable,
			IsCrossRepository: pr.IsCrossRepository,
			Labels:            extractLabels(pr.Labels.Nodes),
			LatestSHA:         extractLatestSHA(pr.Commits.Nodes),
			BaseRefName:       pr.BaseRefName,
			HeadRefName:       pr.HeadRefName,
			URL:               pr.URL,
		},
	}

	reviews, err := c.resolveReviewerPermissions(ctx, pr.Reviews.Nodes)
	if err != nil {
		return nil, err
	}
	info.Reviews = reviews
	return info, nil
}

func buildValidMergeMethods(merge, rebase, squash bool) []config.MergeMethod {
	var out []config.MergeMethod
	if merge {
		out = append(out, config.MergeMethodMerge)
	}
	if rebase {
		out = append(out, config.MergeMethodRebase)
	}
	if squash {
		out = append(out, config.MergeMethodSquash)
	}
	return out
}

func buildReviewRequests(nodes []rawReviewRequest) []PRReviewRequest {
	out := make([]PRReviewRequest, 0, len(nodes))
	for _, n := range nodes {
		name := n.RequestedReviewer.Login
		if name == "" {
			name = n.RequestedReviewer.Name
		}
		if name != "" {
			out = append(out, PRReviewRequest{Name: name})
		}
	}
	return out
}

func matchBranchProtection(rules []rawBranchProtection, baseRef string) *BranchProtectionRule {
	for _, rule := range rules {
		for _, m := range rule.MatchingRefs.Nodes {
			if m.Name == baseRef {
				out := &BranchProtectionRule{
					RequiresApprovingReviews:     rule.RequiresApprovingReviews,
					RequiredApprovingReviewCount: rule.RequiredApprovingReviewCount,
					RequiresStatusChecks:         rule.RequiresStatusChecks,
					RequiredStatusCheckContexts:  rule.RequiredStatusCheckContexts,
					RequiresStrictStatusChecks:   rule.RequiresStrictStatusChecks,
					RequiresCommitSignatures:     rule.RequiresCommitSignatures,
					RestrictsPushes:              rule.RestrictsPushes,
				}
				for _, pa := range rule.PushAllowances.Nodes {
					out.PushAllowances = append(out.PushAllowances, PushAllowance{ActorDatabaseID: pa.Actor.DatabaseID})
				}
				return out
			}
		}
	}
	return nil
}

func extractCommitAuthors(nodes []rawCommit) []CommitAuthor {
	seen := make(map[string]struct{})
	out := make([]CommitAuthor, 0, len(nodes))
	for _, n := range nodes {
		u := n.Commit.Author.User
		if u == nil {
			continue
		}
		key := fmt.Sprintf("%d|%s|%s", u.DatabaseID, u.Login, u.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		id := u.DatabaseID
		out = append(out, CommitAuthor{
			DatabaseID: &id,
			Login:      u.Login,
			Name:       u.Name,
			Type:       u.Type,
		})
	}
	return out
}

func extractStatusContexts(nodes []rawCommit) []StatusContext {
	if len(nodes) == 0 || nodes[0].Commit.Status == nil {
		return nil
	}
	out := make([]StatusContext, 0, len(nodes[0].Commit.Status.Contexts))
	for _, c := range nodes[0].Commit.Status.Contexts {
		out = append(out, StatusContext{Context: c.Context, State: c.State})
	}
	return out
}

func extractCheckRuns(nodes []rawCommit) []CheckRun {
	var out []CheckRun
	for _, n := range nodes {
		for _, suite := range n.Commit.CheckSuites.Nodes {
			for _, cr := range suite.CheckRuns.Nodes {
				out = append(out, CheckRun{Name: cr.Name, Conclusion: cr.Conclusion})
			}
		}
	}
	return out
}

func extractSignature(nodes []rawCommit) bool {
	if len(nodes) == 0 || nodes[0].Commit.Signature == nil {
		return false
	}
	return nodes[0].Commit.Signature.IsValid
}

func extractLabels(nodes []rawLabel) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.Name != "" {
			out = append(out, n.Name)
		}
	}
	return out
}

func extractLatestSHA(nodes []rawCommit) string {
	if len(nodes) == 0 {
		return ""
	}
	return nodes[0].Commit.OID
}

// resolveReviewerPermissions fetches collaborator permission for each human
// reviewer and combines them with bot reviews (bots always count as write).
func (c *client) resolveReviewerPermissions(ctx context.Context, reviews []rawReview) ([]PRReview, error) {
	usernames := make(map[string]struct{})
	botReviews := make([]PRReview, 0)
	for _, r := range reviews {
		switch r.Author.Type {
		case ActorUser:
			usernames[r.Author.Login] = struct{}{}
		case ActorBot:
			botReviews = append(botReviews, PRReview{
				State:     r.State,
				CreatedAt: r.CreatedAt,
				Author:    PRReviewAuthor{Login: r.Author.Login, Permission: PermWrite},
			})
		}
	}

	permMap := make(map[string]Permission, len(usernames))
	for u := range usernames {
		perm, err := c.getPermissionForUser(ctx, u)
		if err != nil {
			perm = PermNone
		}
		permMap[u] = perm
	}

	out := make([]PRReview, 0, len(reviews))
	out = append(out, botReviews...)
	for _, r := range reviews {
		if r.Author.Type != ActorUser {
			continue
		}
		out = append(out, PRReview{
			State:     r.State,
			CreatedAt: r.CreatedAt,
			Author:    PRReviewAuthor{Login: r.Author.Login, Permission: permMap[r.Author.Login]},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (c *client) getPermissionForUser(ctx context.Context, username string) (Permission, error) {
	var payload struct {
		Permission Permission `json:"permission"`
	}
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", c.owner, c.repo, username)
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return PermNone, err
	}
	return payload.Permission, nil
}
