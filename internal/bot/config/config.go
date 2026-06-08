// Package config parses the per-repository .kodiak.toml file.
//
// Ported from bot/kodiak/config.py.
package config

import (
	"fmt"

	"github.com/BurntSushi/toml"
)

// MergeMethod selects the GitHub merge strategy.
type MergeMethod string

const (
	MergeMethodMerge  MergeMethod = "merge"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodRebase MergeMethod = "rebase"
)

// MergeTitleStyle selects how the merge commit title is built.
type MergeTitleStyle string

const (
	MergeTitleGitHubDefault     MergeTitleStyle = "github_default"
	MergeTitlePullRequestTitle  MergeTitleStyle = "pull_request_title"
)

// MergeBodyStyle selects how the merge commit body is built.
type MergeBodyStyle string

const (
	MergeBodyGitHubDefault    MergeBodyStyle = "github_default"
	MergeBodyPullRequestBody  MergeBodyStyle = "pull_request_body"
	MergeBodyEmpty            MergeBodyStyle = "empty"
)

// BodyText selects how the merge commit body is interpreted.
type BodyText string

const (
	BodyTextPlain    BodyText = "plain_text"
	BodyTextMarkdown BodyText = "markdown"
	BodyTextHTML     BodyText = "html"
)

// MergeMessage controls the merge commit's title and body.
type MergeMessage struct {
	Title                    MergeTitleStyle `toml:"title"`
	Body                     MergeBodyStyle  `toml:"body"`
	IncludePRNumber          bool            `toml:"include_pr_number"`
	BodyType                 BodyText        `toml:"body_type"`
	StripHTMLComments        bool            `toml:"strip_html_comments"`
	IncludePullRequestAuthor bool            `toml:"include_pull_request_author"`
	IncludeCoauthors         bool            `toml:"include_coauthors"`
	IncludePullRequestURL    bool            `toml:"include_pull_request_url"`
}

func defaultMergeMessage() MergeMessage {
	return MergeMessage{
		Title:           MergeTitleGitHubDefault,
		Body:            MergeBodyGitHubDefault,
		IncludePRNumber: true,
		BodyType:        BodyTextMarkdown,
	}
}

// Merge describes the merge-time behaviour.
type Merge struct {
	AutomergeLabel           string       `toml:"automerge_label"`
	RequireAutomergeLabel    bool         `toml:"require_automerge_label"`
	BlacklistTitleRegex      string       `toml:"blacklist_title_regex"`
	BlacklistLabels          []string     `toml:"blacklist_labels"`
	Method                   MergeMethod  `toml:"method"`
	DeleteBranchOnMerge      bool         `toml:"delete_branch_on_merge"`
	BlockOnReviewsRequested  bool         `toml:"block_on_reviews_requested"`
	NotifyOnConflict         bool         `toml:"notify_on_conflict"`
	OptimisticUpdates        bool         `toml:"optimistic_updates"`
	Message                  MergeMessage `toml:"message"`
	DontWaitOnStatusChecks   []string     `toml:"dont_wait_on_status_checks"`
	UpdateBranchImmediately  bool         `toml:"update_branch_immediately"`
	PrioritizeReadyToMerge   bool         `toml:"prioritize_ready_to_merge"`
	DoNotMerge               bool         `toml:"do_not_merge"`
}

func defaultMerge() Merge {
	return Merge{
		AutomergeLabel:        "automerge",
		RequireAutomergeLabel: true,
		BlacklistTitleRegex:   "^WIP:.*",
		Method:                MergeMethodMerge,
		NotifyOnConflict:      true,
		OptimisticUpdates:     true,
		Message:               defaultMergeMessage(),
	}
}

// Update describes the auto-update behaviour.
type Update struct {
	Always                bool     `toml:"always"`
	RequireAutomergeLabel bool     `toml:"require_automerge_label"`
	BlacklistUsernames    []string `toml:"blacklist_usernames"`
}

func defaultUpdate() Update {
	return Update{RequireAutomergeLabel: true}
}

// Approve describes the auto-approve behaviour.
type Approve struct {
	AutoApproveUsernames []string `toml:"auto_approve_usernames"`
}

// V1 is the top-level configuration schema (version 1).
type V1 struct {
	Version         int     `toml:"version"`
	AppID           string  `toml:"app_id"`
	Merge           Merge   `toml:"merge"`
	Update          Update  `toml:"update"`
	Approve         Approve `toml:"approve"`
	DisableBotLabel string  `toml:"disable_bot_label"`
}

// ErrInvalidVersion is returned when the config version is not 1.
var ErrInvalidVersion = fmt.Errorf("version must be `1`")

// ParseTOML decodes a .kodiak.toml document into V1, applying defaults.
func ParseTOML(content string) (*V1, error) {
	cfg := &V1{
		Merge:           defaultMerge(),
		Update:          defaultUpdate(),
		DisableBotLabel: "kodiak:disabled",
	}
	if _, err := toml.Decode(content, cfg); err != nil {
		return nil, fmt.Errorf("decode toml: %w", err)
	}
	if cfg.Version != 1 {
		return nil, ErrInvalidVersion
	}
	return cfg, nil
}
