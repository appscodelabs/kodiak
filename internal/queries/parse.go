package queries

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/appscodelabs/kodiak/internal/config"
)

// NOTE(port): the Python helpers operate on untyped dicts and rely on
// pydantic for validation. Here the raw GraphQL JSON is decoded into shadow
// structs. Presence of scalar fields selected by the GraphQL query is not
// strictly enforced (GraphQL guarantees selected fields are returned), but
// enum values, datetimes, and fields the Python models mark as required for
// correctness are validated.

type configQueryText struct {
	Text *string `json:"text"`
}

type configQueryOptions struct {
	RootConfigFile   *configQueryText `json:"rootConfigFile"`
	GithubConfigFile *configQueryText `json:"githubConfigFile"`
}

// some replicates ConfigQueryOptions.__bool__.
func (o *configQueryOptions) some() bool {
	return o != nil && (o.RootConfigFile != nil || o.GithubConfigFile != nil)
}

// ParseConfig finds the configuration file in the GET_CONFIG_QUERY response.
func ParseConfig(data json.RawMessage) (*ParsedConfig, error) {
	var res struct {
		Repository    *configQueryOptions `json:"repository"`
		OrgConfigRepo *configQueryOptions `json:"orgConfigRepo"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		slog.Error("problem parsing api response for config", "error", err)
		return nil, nil
	}

	if res.Repository.some() {
		if res.Repository.RootConfigFile != nil && res.Repository.RootConfigFile.Text != nil {
			return &ParsedConfig{Text: *res.Repository.RootConfigFile.Text, Kind: ConfigKindRepoRoot}, nil
		}
		if res.Repository.GithubConfigFile != nil && res.Repository.GithubConfigFile.Text != nil {
			return &ParsedConfig{Text: *res.Repository.GithubConfigFile.Text, Kind: ConfigKindRepoGithub}, nil
		}
		return nil, errors.New("unexpected missing config file")
	}
	if res.OrgConfigRepo.some() {
		if res.OrgConfigRepo.RootConfigFile != nil && res.OrgConfigRepo.RootConfigFile.Text != nil {
			return &ParsedConfig{Text: *res.OrgConfigRepo.RootConfigFile.Text, Kind: ConfigKindOrgRoot}, nil
		}
		if res.OrgConfigRepo.GithubConfigFile != nil && res.OrgConfigRepo.GithubConfigFile.Text != nil {
			return &ParsedConfig{Text: *res.OrgConfigRepo.GithubConfigFile.Text, Kind: ConfigKindOrgGithub}, nil
		}
		return nil, errors.New("unexpected missing config file")
	}
	return nil, nil
}

func getOrgConfigDefaultBranch(data json.RawMessage) *string {
	var parsed struct {
		OrgConfigRepo *struct {
			DefaultBranchRef *struct {
				Name *string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"orgConfigRepo"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	if parsed.OrgConfigRepo == nil || parsed.OrgConfigRepo.DefaultBranchRef == nil {
		return nil
	}
	return parsed.OrgConfigRepo.DefaultBranchRef.Name
}

// repositoryData is the decoded `repository` object from the event info
// query.
type repositoryData struct {
	MergeCommitAllowed  bool            `json:"mergeCommitAllowed"`
	RebaseMergeAllowed  bool            `json:"rebaseMergeAllowed"`
	SquashMergeAllowed  bool            `json:"squashMergeAllowed"`
	DeleteBranchOnMerge bool            `json:"deleteBranchOnMerge"`
	IsPrivate           bool            `json:"isPrivate"`
	PullRequest         json.RawMessage `json:"pullRequest"`
}

func getRepo(data json.RawMessage) *repositoryData {
	var parsed struct {
		Repository *repositoryData `json:"repository"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	return parsed.Repository
}

func getPullRequestRaw(repo *repositoryData) json.RawMessage {
	if len(repo.PullRequest) == 0 || string(repo.PullRequest) == "null" {
		slog.Warn("Could not find PR")
		return nil
	}
	return repo.PullRequest
}

// prData decodes the pull request sub-objects that the Python helpers
// traverse via dict access.
type prData struct {
	HeadRef *struct {
		ID string `json:"id"`
	} `json:"headRef"`
	BaseRef *struct {
		BranchProtectionRule json.RawMessage `json:"branchProtectionRule"`
		Rules                *struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"rules"`
	} `json:"baseRef"`
	ReviewRequests *struct {
		Nodes []json.RawMessage `json:"nodes"`
	} `json:"reviewRequests"`
	Reviews *struct {
		Nodes []json.RawMessage `json:"nodes"`
	} `json:"reviews"`
	Commits *struct {
		Nodes []struct {
			Commit *struct {
				Oid         string `json:"oid"`
				CheckSuites *struct {
					Nodes []*struct {
						CheckRuns *struct {
							Nodes []json.RawMessage `json:"nodes"`
						} `json:"checkRuns"`
					} `json:"nodes"`
				} `json:"checkSuites"`
				Status *struct {
					Contexts []json.RawMessage `json:"contexts"`
				} `json:"status"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Labels *struct {
		Nodes []struct {
			Name *string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
}

func decodePRData(pr json.RawMessage) *prData {
	var parsed prData
	if err := json.Unmarshal(pr, &parsed); err != nil {
		slog.Warn("problem decoding pull request data", "error", err)
	}
	return &parsed
}

func getLabels(pr *prData) []string {
	if pr.Labels == nil {
		return nil
	}
	var labels []string
	for _, node := range pr.Labels.Nodes {
		if node.Name != nil {
			labels = append(labels, *node.Name)
		}
	}
	return labels
}

func getSHA(pr *prData) string {
	if pr.Commits == nil || len(pr.Commits.Nodes) == 0 || pr.Commits.Nodes[0].Commit == nil {
		return ""
	}
	return pr.Commits.Nodes[0].Commit.Oid
}

func getBranchProtection(pr *prData) *BranchProtectionRule {
	if pr.BaseRef == nil || len(pr.BaseRef.BranchProtectionRule) == 0 ||
		string(pr.BaseRef.BranchProtectionRule) == "null" {
		return nil
	}
	var rule BranchProtectionRule
	if err := json.Unmarshal(pr.BaseRef.BranchProtectionRule, &rule); err != nil {
		slog.Warn("Could not parse branch protection", "error", err)
		return nil
	}
	return &rule
}

func getRulesetRules(pr *prData) []RulesetRule {
	if pr.BaseRef == nil || pr.BaseRef.Rules == nil {
		return nil
	}
	var rules []RulesetRule
	for _, node := range pr.BaseRef.Rules.Nodes {
		var rule RulesetRule
		if err := json.Unmarshal(node, &rule); err != nil {
			slog.Warn("Could not parse RulesetRule", "error", err)
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

// getRequestedReviews parses https://developer.github.com/v4/union/requestedreviewer/
func getRequestedReviews(pr *prData) []PRReviewRequest {
	if pr.ReviewRequests == nil {
		return nil
	}
	var reviewRequests []PRReviewRequest
	for _, node := range pr.ReviewRequests.Nodes {
		var requestDict struct {
			AsCodeOwner       bool `json:"asCodeOwner"`
			RequestedReviewer *struct {
				Login *string `json:"login"`
				Name  *string `json:"name"`
			} `json:"requestedReviewer"`
		}
		if err := json.Unmarshal(node, &requestDict); err != nil {
			slog.Warn("Could not parse PRReviewRequest", "error", err)
			continue
		}
		request := requestDict.RequestedReviewer
		if request == nil {
			continue
		}
		var name string
		switch {
		case request.Login != nil:
			name = *request.Login
		case request.Name != nil:
			name = *request.Name
		default:
			continue
		}
		reviewRequests = append(reviewRequests, PRReviewRequest{
			Name:        name,
			AsCodeOwner: requestDict.AsCodeOwner,
		})
	}
	return reviewRequests
}

func getReviews(pr *prData) []PRReviewSchema {
	if pr.Reviews == nil {
		return nil
	}
	var reviews []PRReviewSchema
	for _, node := range pr.Reviews.Nodes {
		var review PRReviewSchema
		if err := json.Unmarshal(node, &review); err != nil {
			slog.Warn("Could not parse PRReviewSchema", "error", err)
			continue
		}
		if review.State == "" || review.CreatedAt.IsZero() {
			slog.Warn("Could not parse PRReviewSchema", "error", "missing required field")
			continue
		}
		reviews = append(reviews, review)
	}
	return reviews
}

func getStatusContexts(pr *prData) []StatusContext {
	if pr.Commits == nil || len(pr.Commits.Nodes) == 0 ||
		pr.Commits.Nodes[0].Commit == nil || pr.Commits.Nodes[0].Commit.Status == nil {
		return nil
	}
	var statusContexts []StatusContext
	for _, node := range pr.Commits.Nodes[0].Commit.Status.Contexts {
		var statusContext StatusContext
		if err := json.Unmarshal(node, &statusContext); err != nil {
			slog.Warn("Could not parse StatusContext", "error", err)
			continue
		}
		if statusContext.State == "" {
			slog.Warn("Could not parse StatusContext", "error", "missing required field")
			continue
		}
		statusContexts = append(statusContexts, statusContext)
	}
	return statusContexts
}

func getCheckRuns(pr *prData) []CheckRun {
	var checkRunDicts []json.RawMessage
	if pr.Commits != nil {
		for _, commitNode := range pr.Commits.Nodes {
			if commitNode.Commit == nil || commitNode.Commit.CheckSuites == nil {
				continue
			}
			for _, checkSuiteNode := range commitNode.Commit.CheckSuites.Nodes {
				if checkSuiteNode == nil || checkSuiteNode.CheckRuns == nil {
					continue
				}
				checkRunDicts = append(checkRunDicts, checkSuiteNode.CheckRuns.Nodes...)
			}
		}
	}

	var checkRuns []CheckRun
	for _, checkRunDict := range checkRunDicts {
		if len(checkRunDict) == 0 || string(checkRunDict) == "null" {
			continue
		}
		var checkRun CheckRun
		if err := json.Unmarshal(checkRunDict, &checkRun); err != nil {
			slog.Warn("Could not parse CheckRun", "error", err)
			continue
		}
		checkRuns = append(checkRuns, checkRun)
	}
	return checkRuns
}

func getHeadExists(pr *prData) bool {
	return pr.HeadRef != nil && pr.HeadRef.ID != ""
}

func getValidMergeMethods(repo *repositoryData) []config.MergeMethod {
	var validMergeMethods []config.MergeMethod
	if repo.MergeCommitAllowed {
		validMergeMethods = append(validMergeMethods, config.MergeMethodMerge)
	}
	if repo.RebaseMergeAllowed {
		validMergeMethods = append(validMergeMethods, config.MergeMethodRebase)
	}
	if repo.SquashMergeAllowed {
		validMergeMethods = append(validMergeMethods, config.MergeMethodSquash)
	}
	return validMergeMethods
}

// parsePullRequest replicates PullRequest.parse_obj.
func parsePullRequest(raw json.RawMessage, number int, labels []string, latestSha string) (*PullRequest, error) {
	var pr PullRequest
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	if pr.MergeStateStatus == "" || pr.State == "" || pr.Mergeable == "" {
		return nil, fmt.Errorf("invalid PullRequest: missing required field")
	}
	pr.Number = number
	pr.Labels = labels
	pr.LatestSha = latestSha
	return &pr, nil
}

func CreateRootConfigFileExpression(branch string) string {
	return fmt.Sprintf("%s:%s", branch, ConfigFileName)
}

func CreateGithubConfigFileExpression(branch string) string {
	return fmt.Sprintf("%s:.github/%s", branch, ConfigFileName)
}

func hasBodyHTMLError(errs []GraphQLError) bool {
	expectedPath := []string{"repository", "pullRequest", "headRef"}
	for _, err := range errs {
		if err.Type != "FORBIDDEN" || err.Message != "Resource not accessible by integration" {
			continue
		}
		if len(err.Path) != len(expectedPath) {
			continue
		}
		match := true
		for i, p := range err.Path {
			if s, ok := p.(string); !ok || s != expectedPath[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// GetBotReviews filters reviews to those authored by bots.
//
// Bots either have read or write permissions for a pull request, so if
// they've been able to write a review on a PR, their review counts as a user
// with write access.
func GetBotReviews(reviews []PRReviewSchema) []PRReview {
	var botReviews []PRReview
	for _, review := range reviews {
		if review.Author == nil {
			continue
		}
		if review.Author.Type == ActorBot {
			botReviews = append(botReviews, PRReview{
				State:     review.State,
				CreatedAt: review.CreatedAt,
				Author:    PRReviewAuthor{Login: review.Author.Login},
			})
		}
	}
	sort.SliceStable(botReviews, func(i, j int) bool {
		return botReviews[i].CreatedAt.Before(botReviews[j].CreatedAt)
	})
	return botReviews
}
