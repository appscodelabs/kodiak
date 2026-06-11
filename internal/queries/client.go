package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/throttle"
)

// defaultAccept matches the session-level Accept header set by the Python
// client.
const defaultAccept = "application/vnd.github.antiope-preview+json,application/vnd.github.merge-info-preview+json"

// sharedHTTPClient has no timeout to match the behavior of the Python client
// (timeout=None); deadlines are enforced via context.Context.
var sharedHTTPClient = &http.Client{}

// Response is a simplified HTTP response, the counterpart of the
// httpx.Response values the Python client returns.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// IsError reports whether the response has a 4xx or 5xx status code, the
// equivalent of httpx's Response.raise_for_status.
func (r *Response) IsError() bool {
	return r.StatusCode >= 400
}

// rawRequest performs an HTTP request applying the session-level headers
// (default Accept plus the optional extra GitHub API header), with
// per-request headers taking precedence.
func rawRequest(ctx context.Context, session *http.Client, method, requestURL string, headers map[string]string, jsonBody any) (*Response, error) {
	var bodyReader io.Reader
	if jsonBody != nil {
		encoded, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", defaultAccept)
	if appconf.GithubAPIHeaderName != "" && appconf.GithubAPIHeaderValue != "" {
		req.Header.Set(appconf.GithubAPIHeaderName, appconf.GithubAPIHeaderValue)
	}
	if jsonBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := session.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: res.StatusCode, Header: res.Header, Body: body}, nil
}

// quotePath escapes each path segment, like urllib.parse.quote with the
// default safe="/".
func quotePath(s string) string {
	parts := strings.Split(s, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

var (
	apiFeaturesCacheMu sync.Mutex
	apiFeaturesCache   *APIFeatures
)

type Client struct {
	Owner          string
	Repo           string
	InstallationID string

	session   *http.Client
	throttler *throttle.Throttler
	log       *slog.Logger
}

func NewClient(owner, repo, installationID string) *Client {
	return &Client{
		Owner:          owner,
		Repo:           repo,
		InstallationID: installationID,
		session:        sharedHTTPClient,
		throttler:      throttle.ForInstallation(installationID),
		log:            slog.With("owner", owner, "repo", repo, "install", installationID),
	}
}

// doRequest performs a throttled HTTP request.
func (c *Client) doRequest(ctx context.Context, method, requestURL string, headers map[string]string, jsonBody any) (*Response, error) {
	if err := c.throttler.Acquire(ctx); err != nil {
		return nil, err
	}
	return rawRequest(ctx, c.session, method, requestURL, headers, jsonBody)
}

func (c *Client) SendQuery(ctx context.Context, query string, variables map[string]any) (*GraphQLResponse, error) {
	log := c.log

	token, err := GetTokenForInstall(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	res, err := c.doRequest(ctx, http.MethodPost, appconf.GithubV4APIURL,
		map[string]string{"Authorization": "Bearer " + token},
		map[string]any{"query": query, "variables": variables})
	if err != nil {
		log.Warn("github api request error", "error", err)
		return nil, err
	}
	rateLimit := fmt.Sprintf("%s/%s",
		res.Header.Get("x-ratelimit-remaining"), res.Header.Get("x-ratelimit-limit"))
	log = log.With("rate_limit", rateLimit)
	if res.IsError() {
		log.Warn("github api request error", "status_code", res.StatusCode, "res", string(res.Body))
		return nil, fmt.Errorf("github graphql request failed with HTTP status %d", res.StatusCode)
	}
	var gqlRes GraphQLResponse
	if err := json.Unmarshal(res.Body, &gqlRes); err != nil {
		log.Warn("github api request error", "error", err)
		return nil, err
	}
	return &gqlRes, nil
}

// GetAPIFeatures checks if we can use recently added schema fields.
//
// For GitHub Enterprise installations, the latest GraphQL fields may not be
// available.
//
// To query the GraphQL API, we need an installation token, so for the first
// client to make an API request, we use their credentials to view schema
// metadata and cache the results.
func (c *Client) GetAPIFeatures(ctx context.Context) (*APIFeatures, error) {
	apiFeaturesCacheMu.Lock()
	cached := apiFeaturesCache
	apiFeaturesCacheMu.Unlock()
	if cached != nil {
		return cached, nil
	}
	res, err := c.SendQuery(ctx, apiFeaturesQuery, map[string]any{})
	if err != nil {
		c.log.Warn("failed to fetching api features", "error", err)
		return nil, err
	}
	if len(res.Errors) > 0 || len(res.Data) == 0 || string(res.Data) == "null" {
		c.log.Warn("errors fetching api features", "errors", res.Errors, "data", string(res.Data))
		return nil, errors.New("errors fetching api features")
	}

	var parsed struct {
		Type *struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"__type"`
	}
	if err := json.Unmarshal(res.Data, &parsed); err != nil || parsed.Type == nil {
		c.log.Warn("problem parsing api features", "error", err)
		return nil, errors.New("problem parsing api features")
	}
	requiresConversationResolution := false
	for _, field := range parsed.Type.Fields {
		if field.Name == "requiresConversationResolution" {
			requiresConversationResolution = true
			break
		}
	}
	features := &APIFeatures{RequiresConversationResolution: requiresConversationResolution}
	apiFeaturesCacheMu.Lock()
	apiFeaturesCache = features
	apiFeaturesCacheMu.Unlock()
	return features, nil
}

// GetConfigForRef fetches and parses the Kodiak configuration for a git ref.
// orgRepoDefaultBranch may be nil when the organization has no .github
// repository.
func (c *Client) GetConfigForRef(ctx context.Context, ref string, orgRepoDefaultBranch *string) (*CfgInfo, error) {
	repoRootConfigExpression := CreateRootConfigFileExpression(ref)
	repoGithubConfigExpression := CreateGithubConfigFileExpression(ref)
	var orgRootConfigExpression *string
	var orgGithubConfigFileExpression *string

	if orgRepoDefaultBranch != nil {
		root := CreateRootConfigFileExpression(*orgRepoDefaultBranch)
		orgRootConfigExpression = &root
		github := CreateGithubConfigFileExpression(*orgRepoDefaultBranch)
		orgGithubConfigFileExpression = &github
	}
	res, err := c.SendQuery(ctx, GetConfigQuery, map[string]any{
		"owner":                         c.Owner,
		"repo":                          c.Repo,
		"rootConfigFileExpression":      repoRootConfigExpression,
		"githubConfigFileExpression":    repoGithubConfigExpression,
		"orgRootConfigFileExpression":   orgRootConfigExpression,
		"orgGithubConfigFileExpression": orgGithubConfigFileExpression,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Data) == 0 || string(res.Data) == "null" {
		c.log.Error("could not fetch default branch name", "res", res)
		return nil, errors.New("could not fetch default branch name")
	}

	parsedConfig, err := ParseConfig(res.Data)
	if err != nil {
		return nil, err
	}
	if parsedConfig == nil {
		return nil, nil
	}

	var fileExpression string
	switch parsedConfig.Kind {
	case ConfigKindRepoRoot:
		fileExpression = repoRootConfigExpression
	case ConfigKindRepoGithub:
		fileExpression = repoGithubConfigExpression
	case ConfigKindOrgRoot:
		fileExpression = *orgRootConfigExpression
	case ConfigKindOrgGithub:
		fileExpression = *orgGithubConfigFileExpression
	default:
		return nil, fmt.Errorf("unknown config kind %q", parsedConfig.Kind)
	}

	parsed, parseErr := config.ParseToml(parsedConfig.Text)
	return &CfgInfo{
		Parsed:         parsed,
		ParseError:     parseErr,
		Text:           parsedConfig.Text,
		FileExpression: fileExpression,
	}, nil
}

// GetEventInfo retrieves all the information we need to evaluate a pull
// request.
//
// This is basically the "do-all-the-things" query.
func (c *Client) GetEventInfo(ctx context.Context, prNumber int) (*EventInfoResponse, error) {
	log := c.log.With("pr", prNumber)

	apiFeatures, err := c.GetAPIFeatures(ctx)
	if err != nil {
		apiFeatures = nil
	}
	requiresConversationResolution := true
	if apiFeatures != nil {
		requiresConversationResolution = apiFeatures.RequiresConversationResolution
	}

	variables := map[string]any{"owner": c.Owner, "repo": c.Repo, "PRNumber": prNumber}
	res, err := c.SendQuery(ctx, GetEventInfoQuery(requiresConversationResolution, true), variables)
	if err != nil {
		return nil, err
	}
	if len(res.Data) == 0 || string(res.Data) == "null" {
		log.Error("could not fetch event info", "res", res)
		return nil, errors.New("could not fetch event info")
	}

	// NOTE(chdsbd): The GitHub GraphQL API has a bug where querying for
	// the bodyHTML field sometimes breaks and returns errors. I don't know why and I've tried to talk with GitHub Support.
	//
	// In these cases, we retry without the bodyHTML edge. Instead we map the markdown body edge to bodyHTML. This let's us workaround the issue.
	//
	// I think it's better to revert to using the markdown body for bodyHTML instead of failing to merge the PR completely.
	if hasBodyHTMLError(res.Errors) {
		log.Info("has_body_html_error")
		res, err = c.SendQuery(ctx, GetEventInfoQuery(requiresConversationResolution, false), variables)
		if err != nil {
			return nil, err
		}
		if len(res.Data) == 0 || string(res.Data) == "null" {
			log.Error("could not fetch event info", "res", res)
			return nil, errors.New("could not fetch event info")
		}
	}

	repository := getRepo(res.Data)
	if repository == nil {
		log.Warn("could not find repository")
		return nil, errors.New("could not find repository")
	}

	orgRepoDefaultBranch := getOrgConfigDefaultBranch(res.Data)

	var subscription *Subscription
	if appconf.SubscriptionsEnabled {
		subscription, err = c.GetSubscription(ctx)
		if err != nil {
			return nil, err
		}
	}

	pullRequestRaw := getPullRequestRaw(repository)
	if pullRequestRaw == nil {
		log.Warn("Could not find PR")
		return nil, errors.New("could not find PR")
	}
	pullRequestData := decodePRData(pullRequestRaw)

	latestSha := getSHA(pullRequestData)
	if latestSha == "" {
		// PR didn't have a diff associated with it!
		log.Info("pull request missing sha")
		return nil, errors.New("pull request missing sha")
	}

	pr, err := parsePullRequest(pullRequestRaw, prNumber, getLabels(pullRequestData), latestSha)
	if err != nil {
		log.Warn("Could not parse pull request", "error", err)
		return nil, fmt.Errorf("could not parse pull request: %w", err)
	}

	cfg, err := c.GetConfigForRef(ctx, pr.BaseRefName, orgRepoDefaultBranch)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		log.Info("no config found")
		return nil, errors.New("no config found")
	}

	branchProtection := getBranchProtection(pullRequestData)
	rulesetRules := getRulesetRules(pullRequestData)

	allReviews := getReviews(pullRequestData)
	botReviews := GetBotReviews(allReviews)
	return &EventInfoResponse{
		Config:               cfg.Parsed,
		ConfigError:          cfg.ParseError,
		ConfigStr:            cfg.Text,
		ConfigFileExpression: cfg.FileExpression,
		PullRequest:          *pr,
		Repository: RepoInfo{
			MergeCommitAllowed:  repository.MergeCommitAllowed,
			RebaseMergeAllowed:  repository.RebaseMergeAllowed,
			SquashMergeAllowed:  repository.SquashMergeAllowed,
			DeleteBranchOnMerge: repository.DeleteBranchOnMerge,
			IsPrivate:           repository.IsPrivate,
		},
		Subscription:      subscription,
		BranchProtection:  branchProtection,
		RulesetRules:      rulesetRules,
		ReviewRequests:    getRequestedReviews(pullRequestData),
		BotReviews:        botReviews,
		StatusContexts:    getStatusContexts(pullRequestData),
		Commits:           GetCommits(pullRequestRaw),
		CheckRuns:         getCheckRuns(pullRequestData),
		HeadExists:        getHeadExists(pullRequestData),
		ValidMergeMethods: getValidMergeMethods(repository),
	}, nil
}

// GetOpenPullRequests lists open pull requests. base and head are optional
// filters; an empty string means unset.
//
// https://developer.github.com/v3/pulls/#list-pull-requests
func (c *Client) GetOpenPullRequests(ctx context.Context, base string, head string) ([]GetOpenPullRequestsResponse, error) {
	log := c.log.With("base", base, "head", head)
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("state", "open")
	params.Set("sort", "updated")
	params.Set("per_page", "100")
	if base != "" {
		params.Set("base", base)
	}
	if head != "" {
		params.Set("head", head)
	}

	openPRs := []GetOpenPullRequestsResponse{}

	for currentPage := 1; ; currentPage++ {
		if currentPage > 20 {
			log.Info("hit pagination limit")
			break
		}

		params.Set("page", fmt.Sprintf("%d", currentPage))
		res, err := c.doRequest(ctx, http.MethodGet,
			appconf.V3URL(fmt.Sprintf("/repos/%s/%s/pulls", c.Owner, c.Repo))+"?"+params.Encode(),
			headers, nil)
		if err != nil {
			log.Warn("problem finding prs", "error", err)
			return nil, err
		}
		if res.IsError() {
			log.Warn("problem finding prs", "status_code", res.StatusCode, "res", string(res.Body))
			return nil, fmt.Errorf("problem finding prs: HTTP status %d", res.StatusCode)
		}

		var page []GetOpenPullRequestsResponse
		if err := json.Unmarshal(res.Body, &page); err != nil {
			return nil, err
		}
		openPRs = append(openPRs, page...)
		if len(page) == 0 {
			break
		}
	}

	return openPRs, nil
}

// DeleteBranch deletes a branch by name.
func (c *Client) DeleteBranch(ctx context.Context, branch string) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	ref := quotePath("heads/" + branch)
	return c.doRequest(ctx, http.MethodDelete,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/git/refs/%s", c.Owner, c.Repo, ref)),
		headers, nil)
}

func (c *Client) UpdateBranch(ctx context.Context, pullNumber int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPut,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/pulls/%d/update-branch", c.Owner, c.Repo, pullNumber)),
		headers, nil)
}

// ApprovePullRequest approves a pull request.
//
// https://developer.github.com/v3/pulls/reviews/#create-a-pull-request-review
func (c *Client) ApprovePullRequest(ctx context.Context, pullNumber int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPost,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", c.Owner, c.Repo, pullNumber)),
		headers, map[string]any{"event": "APPROVE"})
}

func (c *Client) GetPullRequest(ctx context.Context, number int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodGet,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/pulls/%d", c.Owner, c.Repo, number)),
		headers, nil)
}

func (c *Client) MergePullRequest(ctx context.Context, number int, mergeMethod string, commitTitle *string, commitMessage *string) (*Response, error) {
	body := map[string]any{"merge_method": mergeMethod}
	// we must not pass the keys for commit_title or commit_message when they
	// are null because GitHub will error saying the title/message cannot be
	// null. When the keys are not passed, GitHub creates a title and
	// message.
	if commitTitle != nil {
		body["commit_title"] = *commitTitle
	}
	if commitMessage != nil {
		body["commit_message"] = *commitMessage
	}
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPut,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", c.Owner, c.Repo, number)),
		headers, body)
}

// UpdateRef updates a git reference to point at the given SHA.
//
// https://docs.github.com/en/rest/reference/git#update-a-reference
func (c *Client) UpdateRef(ctx context.Context, ref string, sha string) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPatch,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/git/refs/heads/%s", c.Owner, c.Repo, ref)),
		headers, map[string]any{"sha": sha})
}

// CreateNotification sets a "kodiakhq: status" check run on the given commit.
func (c *Client) CreateNotification(ctx context.Context, headSHA string, message string, summary *string) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	summaryText := ""
	if summary != nil {
		summaryText = *summary
	}
	body := map[string]any{
		"name":         CheckRunName,
		"head_sha":     headSHA,
		"status":       "completed",
		"completed_at": time.Now().UTC().Format(time.RFC3339),
		"conclusion":   "neutral",
		"output":       map[string]any{"title": message, "summary": summaryText},
	}
	return c.doRequest(ctx, http.MethodPost,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/check-runs", c.Owner, c.Repo)),
		headers, body)
}

func (c *Client) AddLabel(ctx context.Context, label string, pullNumber int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPost,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/issues/%d/labels", c.Owner, c.Repo, pullNumber)),
		headers, map[string]any{"labels": []string{label}})
}

func (c *Client) DeleteLabel(ctx context.Context, label string, pullNumber int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	escapedLabel := quotePath(label)
	return c.doRequest(ctx, http.MethodDelete,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", c.Owner, c.Repo, pullNumber, escapedLabel)),
		headers, nil)
}

func (c *Client) CreateComment(ctx context.Context, body string, pullNumber int) (*Response, error) {
	headers, err := getHeaders(ctx, c.session, c.InstallationID)
	if err != nil {
		return nil, err
	}
	return c.doRequest(ctx, http.MethodPost,
		appconf.V3URL(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", c.Owner, c.Repo, pullNumber)),
		headers, map[string]any{"body": body})
}
