// Package events holds the minimal GitHub webhook payload schemas Kodiak
// needs. Ported from bot/kodiak/events/.
//
// The schemas intentionally model only the fields the bot reads. Repository
// structs are duplicated across event types so a future change to one event's
// payload doesn't accidentally affect another.
package events

// Installation is the GitHub App installation reference shared by all events.
type Installation struct {
	ID int64 `json:"id"`
}

// Base is embedded in every Kodiak event schema.
type Base struct {
	Installation Installation `json:"installation"`
}

type prRepoOwner struct {
	Login string `json:"login"`
}

type prRepository struct {
	Name  string      `json:"name"`
	Owner prRepoOwner `json:"owner"`
}

// PullRequestEvent — https://developer.github.com/v3/activity/events/types/#pullrequestevent
type PullRequestEvent struct {
	Base
	Number     int          `json:"number"`
	Repository prRepository `json:"repository"`
}

type reviewPullRequest struct {
	Number int `json:"number"`
}

// PullRequestReviewEvent — https://developer.github.com/v3/activity/events/types/#pullrequestreviewevent
type PullRequestReviewEvent struct {
	Base
	PullRequest reviewPullRequest `json:"pull_request"`
	Repository  prRepository      `json:"repository"`
}

type checkRunPullRequest struct {
	Number int `json:"number"`
}

// CheckRun is the embedded check_run payload.
type CheckRun struct {
	Name         string                `json:"name"`
	PullRequests []checkRunPullRequest `json:"pull_requests"`
}

// CheckRunEvent — https://developer.github.com/v3/activity/events/types/#checkrunevent
type CheckRunEvent struct {
	Base
	CheckRun   CheckRun     `json:"check_run"`
	Repository prRepository `json:"repository"`
}

// Commit references a single git commit by sha.
type Commit struct {
	SHA string `json:"sha"`
}

// Branch is the branch ref shape used inside the status event.
type Branch struct {
	Name   string `json:"name"`
	Commit Commit `json:"commit"`
}

// StatusEvent — https://developer.github.com/v3/activity/events/types/#statusevent
type StatusEvent struct {
	Base
	ID         int64        `json:"id"`
	SHA        string       `json:"sha"`
	Branches   []Branch     `json:"branches"`
	Repository prRepository `json:"repository"`
}

// PushEvent — https://developer.github.com/v3/activity/events/types/#pushevent
type PushEvent struct {
	Base
	Ref        string       `json:"ref"`
	Repository prRepository `json:"repository"`
}

// RepoOwner returns the owner login. Centralized so call sites stay readable.
func (e *PullRequestEvent) RepoOwner() string  { return e.Repository.Owner.Login }
func (e *PullRequestEvent) RepoName() string   { return e.Repository.Name }
func (e *PullRequestReviewEvent) RepoOwner() string {
	return e.Repository.Owner.Login
}
func (e *PullRequestReviewEvent) RepoName() string  { return e.Repository.Name }
func (e *CheckRunEvent) RepoOwner() string          { return e.Repository.Owner.Login }
func (e *CheckRunEvent) RepoName() string           { return e.Repository.Name }
func (e *StatusEvent) RepoOwner() string            { return e.Repository.Owner.Login }
func (e *StatusEvent) RepoName() string             { return e.Repository.Name }
func (e *PushEvent) RepoOwner() string              { return e.Repository.Owner.Login }
func (e *PushEvent) RepoName() string               { return e.Repository.Name }

// PullRequestNumbers returns the pull-request numbers attached to the check run.
func (e *CheckRunEvent) PullRequestNumbers() []int {
	out := make([]int, 0, len(e.CheckRun.PullRequests))
	for _, pr := range e.CheckRun.PullRequests {
		out = append(out, pr.Number)
	}
	return out
}
