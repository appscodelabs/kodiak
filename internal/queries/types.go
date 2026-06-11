// Package queries implements the GitHub GraphQL/REST API client,
// ported from bot/kodiak/queries/__init__.py.
package queries

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/appscodelabs/kodiak/internal/config"
)

const (
	CheckRunName   = "kodiakhq: status"
	ApplicationID  = "kodiak"
	ConfigFileName = ".kodiak.toml"
)

type ErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type GraphQLError struct {
	Message   string          `json:"message"`
	Locations []ErrorLocation `json:"locations"`
	Type      string          `json:"type"`
	Path      []any           `json:"path"`
}

type GraphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []GraphQLError  `json:"errors"`
}

// unmarshalEnum implements pydantic-like enum validation for JSON decoding.
// JSON null leaves the value unset, matching how pydantic handles Optional
// enum fields.
func unmarshalEnum[T ~string](b []byte, name string, valid ...T) (T, error) {
	var zero T
	if string(b) == "null" {
		return zero, nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return zero, err
	}
	for _, v := range valid {
		if T(s) == v {
			return v, nil
		}
	}
	return zero, fmt.Errorf("invalid %s: %q", name, s)
}

type MergeStateStatus string

const (
	// MergeStateStatusBehind: the head ref is out of date.
	MergeStateStatusBehind MergeStateStatus = "BEHIND"
	// MergeStateStatusBlocked: the merge is blocked.
	MergeStateStatusBlocked MergeStateStatus = "BLOCKED"
	// MergeStateStatusClean: mergeable and passing commit status.
	MergeStateStatusClean MergeStateStatus = "CLEAN"
	// MergeStateStatusDirty: the merge commit cannot be cleanly created.
	MergeStateStatusDirty MergeStateStatus = "DIRTY"
	// MergeStateStatusDraft: the merge is blocked due to the pull request being a draft.
	//
	// this member is being removed 2021-01-01. PullRequest.isDraft should be used instead.
	// https://docs.github.com/en/free-pro-team@latest/graphql/overview/breaking-changes#changes-scheduled-for-2021-01-01
	MergeStateStatusDraft MergeStateStatus = "DRAFT"
	// MergeStateStatusHasHooks: mergeable with passing commit status and pre-receive hooks.
	MergeStateStatusHasHooks MergeStateStatus = "HAS_HOOKS"
	// MergeStateStatusUnknown: the state cannot currently be determined.
	MergeStateStatusUnknown MergeStateStatus = "UNKNOWN"
	// MergeStateStatusUnstable: mergeable with non-passing commit status.
	MergeStateStatusUnstable MergeStateStatus = "UNSTABLE"
)

func (m *MergeStateStatus) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "MergeStateStatus",
		MergeStateStatusBehind, MergeStateStatusBlocked, MergeStateStatusClean,
		MergeStateStatusDirty, MergeStateStatusDraft, MergeStateStatusHasHooks,
		MergeStateStatusUnknown, MergeStateStatusUnstable)
	if err != nil {
		return err
	}
	*m = v
	return nil
}

type MergeableState string

const (
	// MergeableStateConflicting: the pull request cannot be merged due to merge conflicts.
	MergeableStateConflicting MergeableState = "CONFLICTING"
	// MergeableStateMergeable: the pull request can be merged.
	MergeableStateMergeable MergeableState = "MERGEABLE"
	// MergeableStateUnknown: the mergeability of the pull request is still being calculated.
	MergeableStateUnknown MergeableState = "UNKNOWN"
)

func (m *MergeableState) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "MergeableState",
		MergeableStateConflicting, MergeableStateMergeable, MergeableStateUnknown)
	if err != nil {
		return err
	}
	*m = v
	return nil
}

type PullRequestState string

const (
	// PullRequestStateOpen: a pull request that is still open.
	PullRequestStateOpen PullRequestState = "OPEN"
	// PullRequestStateClosed: a pull request that has been closed without being merged.
	PullRequestStateClosed PullRequestState = "CLOSED"
	// PullRequestStateMerged: a pull request that has been closed by being merged.
	PullRequestStateMerged PullRequestState = "MERGED"
)

func (p *PullRequestState) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "PullRequestState",
		PullRequestStateOpen, PullRequestStateClosed, PullRequestStateMerged)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

type PullRequestAuthor struct {
	Login      string  `json:"login"`
	DatabaseID int64   `json:"databaseId"`
	Type       string  `json:"type"`
	Name       *string `json:"name"`
}

func (a *PullRequestAuthor) UnmarshalJSON(b []byte) error {
	var aux struct {
		Login      *string `json:"login"`
		DatabaseID *int64  `json:"databaseId"`
		Type       *string `json:"type"`
		Name       *string `json:"name"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.Login == nil || aux.DatabaseID == nil || aux.Type == nil {
		return fmt.Errorf("invalid PullRequestAuthor: missing required field")
	}
	*a = PullRequestAuthor{
		Login:      *aux.Login,
		DatabaseID: *aux.DatabaseID,
		Type:       *aux.Type,
		Name:       aux.Name,
	}
	return nil
}

type PullRequestReviewDecision string

const (
	// PullRequestReviewDecisionApproved: the pull request has received an approving review.
	PullRequestReviewDecisionApproved PullRequestReviewDecision = "APPROVED"
	// PullRequestReviewDecisionChangesRequested: changes have been requested on the pull request.
	PullRequestReviewDecisionChangesRequested PullRequestReviewDecision = "CHANGES_REQUESTED"
	// PullRequestReviewDecisionReviewRequired: a review is required before the pull request can be merged.
	PullRequestReviewDecisionReviewRequired PullRequestReviewDecision = "REVIEW_REQUIRED"
)

func (p *PullRequestReviewDecision) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "PullRequestReviewDecision",
		PullRequestReviewDecisionApproved, PullRequestReviewDecisionChangesRequested,
		PullRequestReviewDecisionReviewRequired)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

type ReviewThread struct {
	IsCollapsed bool `json:"isCollapsed"`
}

type ReviewThreadConnection struct {
	Nodes []ReviewThread `json:"nodes"`
}

type PullRequest struct {
	ID       string             `json:"id"`
	Number   int                `json:"-"`
	Title    string             `json:"title"`
	Body     string             `json:"body"`
	BodyText string             `json:"bodyText"`
	BodyHTML string             `json:"bodyHTML"`
	Author   *PullRequestAuthor `json:"author"`
	IsDraft  bool               `json:"isDraft"`

	MergeStateStatus MergeStateStatus `json:"mergeStateStatus"`
	// ReviewDecision is null if the pull request does not require a review (no
	// branch protection rule). Otherwise shows if the pull request meets the
	// branch protection requirements.
	ReviewDecision    *PullRequestReviewDecision `json:"reviewDecision"`
	ReviewThreads     ReviewThreadConnection     `json:"reviewThreads"`
	State             PullRequestState           `json:"state"`
	Mergeable         MergeableState             `json:"mergeable"`
	IsCrossRepository bool                       `json:"isCrossRepository"`
	Labels            []string                   `json:"-"`
	// LatestSha is the SHA of the most recent commit.
	LatestSha   string `json:"-"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	URL         string `json:"url"`
}

type RepoInfo struct {
	MergeCommitAllowed  bool
	RebaseMergeAllowed  bool
	SquashMergeAllowed  bool
	DeleteBranchOnMerge bool
	IsPrivate           bool
}

// EventInfoResponse contains all the information needed to evaluate a pull
// request.
//
// NOTE(port): the Python `config: Union[V1, ValidationError, TomlDecodeError]`
// field is split into Config (set on success) and ConfigError (set on parse or
// validation failure). Exactly one of the two is non-nil.
type EventInfoResponse struct {
	Config               *config.V1
	ConfigError          error
	ConfigStr            string
	ConfigFileExpression string
	PullRequest          PullRequest
	Repository           RepoInfo
	Subscription         *Subscription
	BranchProtection     *BranchProtectionRule
	RulesetRules         []RulesetRule
	ReviewRequests       []PRReviewRequest
	HeadExists           bool
	BotReviews           []PRReview
	StatusContexts       []StatusContext
	CheckRuns            []CheckRun
	ValidMergeMethods    []config.MergeMethod
	Commits              []Commit
}

// PushAllowanceActor.
//
// https://developer.github.com/v4/object/app/
type PushAllowanceActor struct {
	// DatabaseID will be nil for non github apps (users, organizations, teams).
	DatabaseID *int64 `json:"databaseId"`
}

// PushAllowance.
//
// https://developer.github.com/v4/object/pushallowance/
type PushAllowance struct {
	Actor PushAllowanceActor `json:"actor"`
}

type NodeListPushAllowance struct {
	Nodes []PushAllowance `json:"nodes"`
}

type MergeQueueMergeMethod string

const (
	MergeQueueMergeMethodMerge  MergeQueueMergeMethod = "MERGE"
	MergeQueueMergeMethodSquash MergeQueueMergeMethod = "SQUASH"
	MergeQueueMergeMethodRebase MergeQueueMergeMethod = "REBASE"
)

func (m *MergeQueueMergeMethod) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "MergeQueueMergeMethod",
		MergeQueueMergeMethodMerge, MergeQueueMergeMethodSquash, MergeQueueMergeMethodRebase)
	if err != nil {
		return err
	}
	*m = v
	return nil
}

type PullRequestAllowedMergeMethods string

const (
	PullRequestAllowedMergeMethodsMerge  PullRequestAllowedMergeMethods = "MERGE"
	PullRequestAllowedMergeMethodsSquash PullRequestAllowedMergeMethods = "SQUASH"
	PullRequestAllowedMergeMethodsRebase PullRequestAllowedMergeMethods = "REBASE"
)

func (m *PullRequestAllowedMergeMethods) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "PullRequestAllowedMergeMethods",
		PullRequestAllowedMergeMethodsMerge, PullRequestAllowedMergeMethodsSquash,
		PullRequestAllowedMergeMethodsRebase)
	if err != nil {
		return err
	}
	*m = v
	return nil
}

// RuleParameters is the union of repository ruleset rule parameter types
// (Union[MergeQueueParameters, PullRequestParameters,
// RequiredStatusChecksParameters, UpdateParameters, None] in Python).
type RuleParameters interface {
	isRuleParameters()
}

type PullRequestParameters struct {
	AllowedMergeMethods            []PullRequestAllowedMergeMethods `json:"allowedMergeMethods"`
	RequiredReviewThreadResolution bool                             `json:"requiredReviewThreadResolution"`
}

func (*PullRequestParameters) isRuleParameters() {}

type MergeQueueParameters struct {
	MergeMethod MergeQueueMergeMethod `json:"mergeMethod"`
}

func (*MergeQueueParameters) isRuleParameters() {}

// StatusCheckConfiguration is a status check configuration item.
type StatusCheckConfiguration struct {
	Context string `json:"context"`
}

type RequiredStatusChecksParameters struct {
	StrictRequiredStatusChecksPolicy bool                       `json:"strictRequiredStatusChecksPolicy"`
	RequiredStatusChecks             []StatusCheckConfiguration `json:"requiredStatusChecks"`
}

func (*RequiredStatusChecksParameters) isRuleParameters() {}

type UpdateParameters struct {
	UpdateAllowsFetchAndMerge bool `json:"updateAllowsFetchAndMerge"`
}

func (*UpdateParameters) isRuleParameters() {}

// BranchProtectionRule.
//
// https://developer.github.com/v4/object/branchprotectionrule/
type BranchProtectionRule struct {
	RequiresStatusChecks           bool                  `json:"requiresStatusChecks"`
	RequiredStatusCheckContexts    []string              `json:"requiredStatusCheckContexts"`
	RequiresStrictStatusChecks     bool                  `json:"requiresStrictStatusChecks"`
	RequiresCommitSignatures       bool                  `json:"requiresCommitSignatures"`
	RequiresConversationResolution *bool                 `json:"requiresConversationResolution"`
	RestrictsPushes                bool                  `json:"restrictsPushes"`
	PushAllowances                 NodeListPushAllowance `json:"pushAllowances"`
}

type BypassActor struct {
	DatabaseID *int64 `json:"databaseId"`
}

type RepositoryRulesetBypassActor struct {
	Actor *BypassActor `json:"actor"`
}

type RepositoryRulesetBypassActorConnection struct {
	Nodes []*RepositoryRulesetBypassActor `json:"nodes"`
}

type RepositoryRuleset struct {
	BypassActors *RepositoryRulesetBypassActorConnection `json:"bypassActors"`
}

type RulesetRule struct {
	Type              string
	Parameters        RuleParameters
	RepositoryRuleset *RepositoryRuleset
}

func (r *RulesetRule) UnmarshalJSON(b []byte) error {
	var aux struct {
		Type              *string            `json:"type"`
		Parameters        json.RawMessage    `json:"parameters"`
		RepositoryRuleset *RepositoryRuleset `json:"repositoryRuleset"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	if aux.Type == nil {
		return fmt.Errorf("invalid RulesetRule: missing required field 'type'")
	}
	params, err := parseRuleParameters(aux.Parameters)
	if err != nil {
		return err
	}
	*r = RulesetRule{
		Type:              *aux.Type,
		Parameters:        params,
		RepositoryRuleset: aux.RepositoryRuleset,
	}
	return nil
}

// parseRuleParameters replicates the pydantic union resolution for
// RulesetRule.parameters: members are tried in declaration order and an empty
// dict is coerced to None.
func parseRuleParameters(raw json.RawMessage) (RuleParameters, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	if _, ok := fields["mergeMethod"]; ok {
		var p MergeQueueParameters
		if err := json.Unmarshal(raw, &p); err == nil && p.MergeMethod != "" {
			return &p, nil
		}
	}
	if _, ok := fields["requiredReviewThreadResolution"]; ok {
		var p PullRequestParameters
		if err := json.Unmarshal(raw, &p); err == nil {
			return &p, nil
		}
	}
	_, hasStrict := fields["strictRequiredStatusChecksPolicy"]
	_, hasChecks := fields["requiredStatusChecks"]
	if hasStrict && hasChecks {
		var p RequiredStatusChecksParameters
		if err := json.Unmarshal(raw, &p); err == nil {
			return &p, nil
		}
	}
	if _, ok := fields["updateAllowsFetchAndMerge"]; ok {
		var p UpdateParameters
		if err := json.Unmarshal(raw, &p); err == nil {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("invalid RulesetRule parameters: %s", string(raw))
}

type PRReviewState string

const (
	PRReviewStateApproved         PRReviewState = "APPROVED"
	PRReviewStateChangesRequested PRReviewState = "CHANGES_REQUESTED"
	PRReviewStateCommented        PRReviewState = "COMMENTED"
	PRReviewStateDismissed        PRReviewState = "DISMISSED"
	PRReviewStatePending          PRReviewState = "PENDING"
)

func (p *PRReviewState) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "PRReviewState",
		PRReviewStateApproved, PRReviewStateChangesRequested, PRReviewStateCommented,
		PRReviewStateDismissed, PRReviewStatePending)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// Actor.
//
// https://developer.github.com/v4/interface/actor/
type Actor string

const (
	ActorBot                   Actor = "Bot"
	ActorEnterpriseUserAccount Actor = "EnterpriseUserAccount"
	ActorMannequin             Actor = "Mannequin"
	ActorOrganization          Actor = "Organization"
	ActorUser                  Actor = "User"
)

func (a *Actor) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "Actor",
		ActorBot, ActorEnterpriseUserAccount, ActorMannequin, ActorOrganization, ActorUser)
	if err != nil {
		return err
	}
	*a = v
	return nil
}

type PRReviewAuthorSchema struct {
	Login string `json:"login"`
	Type  Actor  `json:"type"`
}

type PRReviewAuthor struct {
	Login string
}

type PRReviewSchema struct {
	State     PRReviewState         `json:"state"`
	CreatedAt time.Time             `json:"createdAt"`
	Author    *PRReviewAuthorSchema `json:"author"`
}

type PRReview struct {
	State     PRReviewState
	CreatedAt time.Time
	Author    PRReviewAuthor
}

type PRReviewRequest struct {
	Name        string
	AsCodeOwner bool
}

type StatusState string

const (
	StatusStateError    StatusState = "ERROR"
	StatusStateExpected StatusState = "EXPECTED"
	StatusStateFailure  StatusState = "FAILURE"
	StatusStatePending  StatusState = "PENDING"
	StatusStateSuccess  StatusState = "SUCCESS"
)

func (s *StatusState) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "StatusState",
		StatusStateError, StatusStateExpected, StatusStateFailure, StatusStatePending,
		StatusStateSuccess)
	if err != nil {
		return err
	}
	*s = v
	return nil
}

type StatusContext struct {
	Context string      `json:"context"`
	State   StatusState `json:"state"`
}

type CheckConclusionState string

const (
	CheckConclusionStateActionRequired CheckConclusionState = "ACTION_REQUIRED"
	CheckConclusionStateCancelled      CheckConclusionState = "CANCELLED"
	CheckConclusionStateFailure        CheckConclusionState = "FAILURE"
	CheckConclusionStateNeutral        CheckConclusionState = "NEUTRAL"
	CheckConclusionStateSkipped        CheckConclusionState = "SKIPPED"
	CheckConclusionStateStale          CheckConclusionState = "STALE"
	CheckConclusionStateSuccess        CheckConclusionState = "SUCCESS"
	CheckConclusionStateTimedOut       CheckConclusionState = "TIMED_OUT"
)

func (c *CheckConclusionState) UnmarshalJSON(b []byte) error {
	v, err := unmarshalEnum(b, "CheckConclusionState",
		CheckConclusionStateActionRequired, CheckConclusionStateCancelled,
		CheckConclusionStateFailure, CheckConclusionStateNeutral,
		CheckConclusionStateSkipped, CheckConclusionStateStale,
		CheckConclusionStateSuccess, CheckConclusionStateTimedOut)
	if err != nil {
		return err
	}
	*c = v
	return nil
}

type CheckRun struct {
	Name       string                `json:"name"`
	Conclusion *CheckConclusionState `json:"conclusion"`
}

// MergeBody is the request body for the GitHub merge API.
type MergeBody struct {
	MergeMethod   string  `json:"merge_method"`
	CommitTitle   *string `json:"commit_title,omitempty"`
	CommitMessage *string `json:"commit_message,omitempty"`
}

type Ref struct {
	Ref string `json:"ref"`
}

// GetOpenPullRequestsResponse corresponds to
// GetOpenPullRequestsResponseSchema in the Python implementation.
type GetOpenPullRequestsResponse struct {
	Number int `json:"number"`
	Base   Ref `json:"base"`
}

// SubscriptionBlocker is the union of subscription blocker kinds
// (Union[SubscriptionExpired, TrialExpired, SeatsExceeded] in Python).
type SubscriptionBlocker interface {
	Kind() string
}

type SubscriptionExpired struct{}

func (SubscriptionExpired) Kind() string { return "subscription_expired" }

type TrialExpired struct{}

func (TrialExpired) Kind() string { return "trial_expired" }

type SeatsExceeded struct {
	// AllowedUserIDs is a list of github account user ids that occupy seats.
	// These users will be allowed to use Kodiak while any new users will be
	// blocked by the paywall.
	AllowedUserIDs []int64 `json:"allowed_user_ids"`
}

func (SeatsExceeded) Kind() string { return "seats_exceeded" }

type Subscription struct {
	AccountID           string
	SubscriptionBlocker SubscriptionBlocker
}

// CfgInfo holds the parsed repository configuration.
//
// NOTE(port): the Python `parsed: V1 | ValidationError | TomlDecodeError`
// field is split into Parsed and ParseError. Exactly one of the two is
// non-nil.
type CfgInfo struct {
	Parsed         *config.V1
	ParseError     error
	Text           string
	FileExpression string
}

type APIFeatures struct {
	RequiresConversationResolution bool
}

type ConfigKind string

const (
	ConfigKindRepoRoot   ConfigKind = "repo_root"
	ConfigKindRepoGithub ConfigKind = "repo_github"
	ConfigKindOrgRoot    ConfigKind = "org_root"
	ConfigKindOrgGithub  ConfigKind = "org_github"
)

type ParsedConfig struct {
	Text string
	Kind ConfigKind
}
