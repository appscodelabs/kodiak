// Package events defines the GitHub webhook event payloads the bot consumes,
// ported from bot/kodiak/events/.
package events

type Installation struct {
	ID int64 `json:"id"`
}

// GithubEvent is the common payload base for all webhook events.
type GithubEvent struct {
	Installation Installation `json:"installation"`
}

type Owner struct {
	Login string `json:"login"`
}

type Repository struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
}

type PullRequestRepository struct {
	ID int64 `json:"id"`
}

type Ref struct {
	Ref  string                 `json:"ref"`
	Repo *PullRequestRepository `json:"repo,omitempty"`
}

type PullRequest struct {
	Number int `json:"number"`
	Base   Ref `json:"base"`
}

type CheckRun struct {
	Name         string        `json:"name"`
	PullRequests []PullRequest `json:"pull_requests"`
}

// CheckRunEvent corresponds to
// https://developer.github.com/v3/activity/events/types/#checkrunevent
type CheckRunEvent struct {
	GithubEvent
	CheckRun   CheckRun   `json:"check_run"`
	Repository Repository `json:"repository"`
}

// PullRequestEvent corresponds to
// https://developer.github.com/v3/activity/events/types/#pullrequestevent
type PullRequestEvent struct {
	GithubEvent
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
}

// PullRequestReviewEvent corresponds to
// https://developer.github.com/v3/activity/events/types/#pullrequestreviewevent
type PullRequestReviewEvent struct {
	GithubEvent
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
}

// PullRequestReviewThreadEvent is currently undocumented as of 2021-07-20.
type PullRequestReviewThreadEvent struct {
	GithubEvent
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
}

// PushEvent corresponds to
// https://developer.github.com/v3/activity/events/types/#pushevent
type PushEvent struct {
	GithubEvent
	Ref        string     `json:"ref"`
	Repository Repository `json:"repository"`
}

type Commit struct {
	SHA *string `json:"sha"`
}

type Branch struct {
	Name   string `json:"name"`
	Commit Commit `json:"commit"`
}

// StatusEvent corresponds to
// https://developer.github.com/v3/activity/events/types/#statusevent
type StatusEvent struct {
	GithubEvent
	ID         int64      `json:"id"`
	SHA        string     `json:"sha"`
	Branches   []Branch   `json:"branches"`
	Repository Repository `json:"repository"`
}
