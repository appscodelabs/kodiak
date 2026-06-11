// Package config defines the .kodiak.toml repository configuration schema,
// ported from bot/kodiak/config.py.
package config

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/BurntSushi/toml"
)

// AppIDStr coerces TOML integer values to a string, matching the lenient
// string handling of pydantic used by the Python implementation.
type AppIDStr string

func (a *AppIDStr) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		*a = AppIDStr(t)
		return nil
	case int64:
		*a = AppIDStr(strconv.FormatInt(t, 10))
		return nil
	}
	return fmt.Errorf("expected string for app_id, got %v", v)
}

type MergeMethod string

const (
	MergeMethodMerge             MergeMethod = "merge"
	MergeMethodSquash            MergeMethod = "squash"
	MergeMethodRebase            MergeMethod = "rebase"
	MergeMethodRebaseFastForward MergeMethod = "rebase_fast_forward"
)

func (m *MergeMethod) UnmarshalText(text []byte) error {
	v := MergeMethod(text)
	switch v {
	case MergeMethodMerge, MergeMethodSquash, MergeMethodRebase, MergeMethodRebaseFastForward:
		*m = v
		return nil
	}
	return fmt.Errorf("invalid merge.method: %q; permitted values: 'merge', 'squash', 'rebase', 'rebase_fast_forward'", text)
}

type MergeTitleStyle string

const (
	MergeTitleStyleGithubDefault    MergeTitleStyle = "github_default"
	MergeTitleStylePullRequestTitle MergeTitleStyle = "pull_request_title"
)

func (m *MergeTitleStyle) UnmarshalText(text []byte) error {
	v := MergeTitleStyle(text)
	switch v {
	case MergeTitleStyleGithubDefault, MergeTitleStylePullRequestTitle:
		*m = v
		return nil
	}
	return fmt.Errorf("invalid merge.message.title: %q; permitted values: 'github_default', 'pull_request_title'", text)
}

type MergeBodyStyle string

const (
	MergeBodyStyleGithubDefault   MergeBodyStyle = "github_default"
	MergeBodyStylePullRequestBody MergeBodyStyle = "pull_request_body"
	MergeBodyStyleEmpty           MergeBodyStyle = "empty"
)

func (m *MergeBodyStyle) UnmarshalText(text []byte) error {
	v := MergeBodyStyle(text)
	switch v {
	case MergeBodyStyleGithubDefault, MergeBodyStylePullRequestBody, MergeBodyStyleEmpty:
		*m = v
		return nil
	}
	return fmt.Errorf("invalid merge.message.body: %q; permitted values: 'github_default', 'pull_request_body', 'empty'", text)
}

type BodyText string

const (
	BodyTextPlainText BodyText = "plain_text"
	BodyTextMarkdown  BodyText = "markdown"
	BodyTextHTML      BodyText = "html"
)

func (b *BodyText) UnmarshalText(text []byte) error {
	v := BodyText(text)
	switch v {
	case BodyTextPlainText, BodyTextMarkdown, BodyTextHTML:
		*b = v
		return nil
	}
	return fmt.Errorf("invalid merge.message.body_type: %q; permitted values: 'plain_text', 'markdown', 'html'", text)
}

// MergeMessage configures the commit message used when merging.
//
// https://developer.github.com/v3/pulls/#merge-a-pull-request-merge-button
type MergeMessage struct {
	Title                    MergeTitleStyle `toml:"title"`
	Body                     MergeBodyStyle  `toml:"body"`
	IncludePRNumber          bool            `toml:"include_pr_number"`
	BodyType                 BodyText        `toml:"body_type"`
	StripHTMLComments        bool            `toml:"strip_html_comments"`
	IncludePullRequestAuthor bool            `toml:"include_pull_request_author"`
	IncludeCoauthors         bool            `toml:"include_coauthors"`
	IncludePullRequestURL    bool            `toml:"include_pull_request_url"`
	CutBodyBefore            string          `toml:"cut_body_before"`
	CutBodyAfter             string          `toml:"cut_body_after"`
	CutBodyAndText           bool            `toml:"cut_body_and_text"`
}

func DefaultMergeMessage() MergeMessage {
	return MergeMessage{
		Title:           MergeTitleStyleGithubDefault,
		Body:            MergeBodyStyleGithubDefault,
		IncludePRNumber: true,
		BodyType:        BodyTextMarkdown,
	}
}

// UnsetTitleRegex indicates that the user has the field unset.
const UnsetTitleRegex = ":::|||kodiak|||internal|||reserved|||:::"

const DefaultTitleRegex = "^WIP:.*"

type UpgradeVersion string

func (u *UpgradeVersion) UnmarshalText(text []byte) error {
	switch string(text) {
	case "major", "minor", "patch":
		*u = UpgradeVersion(text)
		return nil
	}
	return fmt.Errorf("invalid version: %q; permitted values: 'major', 'minor', 'patch'", text)
}

type AutomergeDependencies struct {
	Versions  []UpgradeVersion `toml:"versions"`
	Usernames []string         `toml:"usernames"`
}

// Labels holds a configuration value that may be either a single string or a
// list of strings (Union[str, List[str]] in the Python implementation).
type Labels []string

func (l *Labels) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		*l = Labels{t}
		return nil
	case []any:
		out := make(Labels, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return fmt.Errorf("expected string or list of strings, got list item %v", item)
			}
			out = append(out, s)
		}
		*l = out
		return nil
	}
	return fmt.Errorf("expected string or list of strings, got %v", v)
}

type Merge struct {
	// label or labels to enable merging of pull request.
	AutomergeLabel        Labels                `toml:"automerge_label"`
	AutomergeDependencies AutomergeDependencies `toml:"automerge_dependencies"`
	// if disabled, kodiak won't require a label to queue a PR for merge
	RequireAutomergeLabel bool `toml:"require_automerge_label"`
	// Show message when automerge label is missing on a PR
	ShowMissingAutomergeLabelMessage bool `toml:"show_missing_automerge_label_message"`
	// regex to match against title and block merging. Set to empty string to
	// disable check.
	BlacklistTitleRegex string `toml:"blacklist_title_regex"` // deprecated for blocking_title_regex
	BlockingTitleRegex  string `toml:"blocking_title_regex"`
	// labels to block merging of pull request
	BlacklistLabels []string `toml:"blacklist_labels"` // deprecated for blocking_labels
	BlockingLabels  []string `toml:"blocking_labels"`
	// action to take when attempting to merge PR. An error will occur if
	// method is disabled for repository
	Method *MergeMethod `toml:"method"`
	// delete branch when PR is merged
	DeleteBranchOnMerge bool `toml:"delete_branch_on_merge"`
	// DEPRECATED
	// this feature is flawed and cannot be fixed. see
	// https://github.com/chdsbd/kodiak/issues/153#issuecomment-523057332.
	//
	// block merging if there are outstanding review requests
	BlockOnReviewsRequested bool `toml:"block_on_reviews_requested"`
	// block merging if there are neutral required check runs.
	BlockOnNeutralRequiredCheckRuns bool `toml:"block_on_neutral_required_check_runs"`
	// comment on merge conflict and remove automerge label
	NotifyOnConflict bool `toml:"notify_on_conflict"`
	// don't wait for status checks to run before updating branch
	OptimisticUpdates bool `toml:"optimistic_updates"`
	// configuration for commit message of merge
	Message MergeMessage `toml:"message"`
	// status checks that we don't want to wait to complete when they are in a
	// pending state. This is useful for checks that will complete in an
	// indefinite amount of time, like the wip-app checks or status checks
	// requiring manual approval.
	DontWaitOnStatusChecks []string `toml:"dont_wait_on_status_checks"`
	// DEPRECATED
	// This setting only updates PRs that are passing all requirements or
	// waiting for status checks to pass. `update.always = true` will deliver
	// better behavior in many use cases.
	//
	// immediately update a PR whenever the target updates
	UpdateBranchImmediately bool `toml:"update_branch_immediately"`
	// if a PR is passing all checks and is able to be merged, merge it
	// without placing it in the queue. This will introduce some unfairness
	// where those waiting in the queue the longest will not be served first.
	PrioritizeReadyToMerge bool `toml:"prioritize_ready_to_merge"`
	// when applied to a PR, add the PR to the front of the merge queue.
	PriorityMergeLabel *string `toml:"priority_merge_label"`
	// never merge a PR. This can be used with merge.update_branch_immediately
	// to automatically update a PR without merging.
	DoNotMerge bool `toml:"do_not_merge"`
}

func DefaultMerge() Merge {
	return Merge{
		AutomergeLabel:                   Labels{"automerge"},
		RequireAutomergeLabel:            true,
		ShowMissingAutomergeLabelMessage: true,
		BlacklistTitleRegex:              UnsetTitleRegex,
		BlockingTitleRegex:               UnsetTitleRegex,
		NotifyOnConflict:                 true,
		OptimisticUpdates:                true,
		Message:                          DefaultMergeMessage(),
	}
}

type Update struct {
	// update PR whenever the PR is out of date with the base branch. PR will
	// be updated regardless of failing requirements for merge (e.g. failing
	// status checks, missing reviews, blacklist labels). Kodiak will only
	// update the PR if the automerge label is enabled or
	// `update.require_automerge_label` is false.
	Always                bool    `toml:"always"`
	RequireAutomergeLabel bool    `toml:"require_automerge_label"`
	AutoupdateLabel       *string `toml:"autoupdate_label"`
	// Do not update PRs created by a listed user.
	BlacklistUsernames []string `toml:"blacklist_usernames"` // deprecated for ignored_usernames
	IgnoredUsernames   []string `toml:"ignored_usernames"`
}

func DefaultUpdate() Update {
	return Update{RequireAutomergeLabel: true}
}

type Approve struct {
	// auto approve any PR created by a listed user.
	AutoApproveUsernames []string `toml:"auto_approve_usernames"`
	// auto approve any PR to which a listed label is assigned.
	AutoApproveLabels []string `toml:"auto_approve_labels"`
}

type V1 struct {
	Version int `toml:"version"`
	// _internal_ value to require a certain github app ID to process this
	// repo. This is useful for development, so we can globally install the
	// production kodiak, but also run the development version on a specific
	// repo. By setting app_id to the development github app ID, we can
	// prevent the production kodiak instance from interfering.
	AppID   *AppIDStr `toml:"app_id"`
	Merge   Merge     `toml:"merge"`
	Update  Update    `toml:"update"`
	Approve Approve   `toml:"approve"`
	// when added to a Pull Request Kodiak will be prevented from taking any
	// action (approvals, updates, merges, comments, labels). Kodiak will
	// still set status checks. A user should generally not need to change
	// this label as it should rarely be applied.
	DisableBotLabel string `toml:"disable_bot_label"`
}

func DefaultV1() V1 {
	return V1{
		Version:         1,
		Merge:           DefaultMerge(),
		Update:          DefaultUpdate(),
		DisableBotLabel: "kodiak:disabled",
	}
}

var ErrInvalidVersion = errors.New("Version must be `1`")

// ParseToml parses and validates a .kodiak.toml configuration document.
func ParseToml(content string) (*V1, error) {
	cfg := DefaultV1()
	md, err := toml.Decode(content, &cfg)
	if err != nil {
		return nil, err
	}
	if !md.IsDefined("version") {
		return nil, errors.New("field required: version")
	}
	if cfg.Version != 1 {
		return nil, ErrInvalidVersion
	}
	return &cfg, nil
}
