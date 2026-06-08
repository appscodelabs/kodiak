// GraphQL response types for the bot's GetEventInfo query.
//
// Ported from the data classes scattered through bot/kodiak/queries.py.

package queries

import (
	"time"

	"github.com/appscodelabs/kodiak/internal/bot/config"
)

// MergeStateStatus mirrors GitHub's mergeStateStatus enum.
type MergeStateStatus string

const (
	MergeStateBehind   MergeStateStatus = "BEHIND"
	MergeStateBlocked  MergeStateStatus = "BLOCKED"
	MergeStateClean    MergeStateStatus = "CLEAN"
	MergeStateDirty    MergeStateStatus = "DIRTY"
	MergeStateDraft    MergeStateStatus = "DRAFT"
	MergeStateHasHooks MergeStateStatus = "HAS_HOOKS"
	MergeStateUnknown  MergeStateStatus = "UNKNOWN"
	MergeStateUnstable MergeStateStatus = "UNSTABLE"
)

// MergeableState is GitHub's mergeable enum.
type MergeableState string

const (
	MergeableConflicting MergeableState = "CONFLICTING"
	MergeableMergeable   MergeableState = "MERGEABLE"
	MergeableUnknown     MergeableState = "UNKNOWN"
)

// PullRequestState is the open/closed/merged state.
type PullRequestState string

const (
	PRStateOpen   PullRequestState = "OPEN"
	PRStateClosed PullRequestState = "CLOSED"
	PRStateMerged PullRequestState = "MERGED"
)

// Permission mirrors GitHub's collaborator permission level.
type Permission string

const (
	PermAdmin Permission = "admin"
	PermWrite Permission = "write"
	PermRead  Permission = "read"
	PermNone  Permission = "none"
)

// ActorType is the GitHub Actor __typename.
type ActorType string

const (
	ActorBot                   ActorType = "Bot"
	ActorEnterpriseUserAccount ActorType = "EnterpriseUserAccount"
	ActorMannequin             ActorType = "Mannequin"
	ActorOrganization          ActorType = "Organization"
	ActorUser                  ActorType = "User"
)

// PRReviewState matches GitHub's review state.
type PRReviewState string

const (
	ReviewApproved         PRReviewState = "APPROVED"
	ReviewChangesRequested PRReviewState = "CHANGES_REQUESTED"
	ReviewCommented        PRReviewState = "COMMENTED"
	ReviewDismissed        PRReviewState = "DISMISSED"
	ReviewPending          PRReviewState = "PENDING"
)

// StatusState is the GitHub commit-status state.
type StatusState string

const (
	StatusError    StatusState = "ERROR"
	StatusExpected StatusState = "EXPECTED"
	StatusFailure  StatusState = "FAILURE"
	StatusPending  StatusState = "PENDING"
	StatusSuccess  StatusState = "SUCCESS"
)

// CheckConclusionState is the conclusion of a single check_run.
type CheckConclusionState string

const (
	CheckActionRequired CheckConclusionState = "ACTION_REQUIRED"
	CheckCancelled      CheckConclusionState = "CANCELLED"
	CheckFailure        CheckConclusionState = "FAILURE"
	CheckNeutral        CheckConclusionState = "NEUTRAL"
	CheckSkipped        CheckConclusionState = "SKIPPED"
	CheckStale          CheckConclusionState = "STALE"
	CheckSuccess        CheckConclusionState = "SUCCESS"
	CheckTimedOut       CheckConclusionState = "TIMED_OUT"
)

// PullRequestAuthor is the parsed author info.
type PullRequestAuthor struct {
	Login      string
	DatabaseID int64
	Type       string
	Name       string
}

// PullRequest is the projection consumed by the merge evaluator.
type PullRequest struct {
	ID                string
	Number            int
	Title             string
	Body              string
	BodyText          string
	BodyHTML          string
	Author            PullRequestAuthor
	MergeStateStatus  MergeStateStatus
	State             PullRequestState
	Mergeable         MergeableState
	IsCrossRepository bool
	Labels            []string
	LatestSHA         string
	BaseRefName       string
	HeadRefName       string
	URL               string
}

// RepoInfo is the repo-level projection.
type RepoInfo struct {
	MergeCommitAllowed  bool
	RebaseMergeAllowed  bool
	SquashMergeAllowed  bool
	DeleteBranchOnMerge bool
	IsPrivate           bool
}

// PushAllowance is one entry in the push-allowance list.
type PushAllowance struct {
	ActorDatabaseID *int64
}

// BranchProtectionRule is the parsed branch-protection ruleset for the base ref.
type BranchProtectionRule struct {
	RequiresApprovingReviews     bool
	RequiredApprovingReviewCount *int
	RequiresStatusChecks         bool
	RequiredStatusCheckContexts  []string
	RequiresStrictStatusChecks   bool
	RequiresCommitSignatures     bool
	RestrictsPushes              bool
	PushAllowances               []PushAllowance
}

// PRReviewAuthor is the author of a review, with their permission level.
type PRReviewAuthor struct {
	Login      string
	Permission Permission
}

// PRReview is a single review with its author and permission resolved.
type PRReview struct {
	State     PRReviewState
	CreatedAt time.Time
	Author    PRReviewAuthor
}

// PRReviewRequest is a pending review request.
type PRReviewRequest struct {
	Name string
}

// StatusContext is a single commit-status check.
type StatusContext struct {
	Context string
	State   StatusState
}

// CheckRun is a single check_run on the head commit.
type CheckRun struct {
	Name       string
	Conclusion CheckConclusionState
}

// CommitAuthor is one author of a PR commit.
type CommitAuthor struct {
	DatabaseID *int64
	Login      string
	Name       string
	Type       string
}

// SubscriptionBlockerKind classifies why a subscription is blocked.
type SubscriptionBlockerKind string

const (
	BlockerSubscriptionExpired SubscriptionBlockerKind = "subscription_expired"
	BlockerTrialExpired        SubscriptionBlockerKind = "trial_expired"
	BlockerSeatsExceeded       SubscriptionBlockerKind = "seats_exceeded"
)

// SubscriptionBlocker is the parsed Redis-stored blocker.
type SubscriptionBlocker struct {
	Kind           SubscriptionBlockerKind
	AllowedUserIDs []int64 // populated only when Kind == BlockerSeatsExceeded
}

// EventInfo replaces the rich EventInfoResponse dataclass.
type EventInfo struct {
	Config              *config.V1
	ConfigParseErr      error // non-nil if .kodiak.toml couldn't be parsed
	ConfigStr           string
	ConfigFileExpression string
	PullRequest         PullRequest
	Repository          RepoInfo
	Subscription        *Subscription
	BranchProtection    *BranchProtectionRule
	ReviewRequests      []PRReviewRequest
	HeadExists          bool
	Reviews             []PRReview
	StatusContexts      []StatusContext
	CheckRuns           []CheckRun
	ValidSignature      bool
	ValidMergeMethods   []config.MergeMethod
	CommitAuthors       []CommitAuthor
}
