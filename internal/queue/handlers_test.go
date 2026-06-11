package queue

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/appscodelabs/kodiak/internal/events"
)

// ported from bot/kodiak/test_queue.py
func TestInstallationIDFromQueue(t *testing.T) {
	tests := []struct {
		queueName              string
		expectedInstallationID string
	}{
		{"merge_queue:11256551.sbdchd/squawk/main.test.foo", "11256551"},
		{"merge_queue:11256551.sbdchd/squawk", "11256551"},
		{"merge_queue:11256551.sbdchd/squawk:repo/main:test.branch", "11256551"},
		{"webhook:11256551", "11256551"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := InstallationIDFromQueue(tt.queueName); got != tt.expectedInstallationID {
			t.Errorf("InstallationIDFromQueue(%q) = %q, want %q", tt.queueName, got, tt.expectedInstallationID)
		}
	}
}

// ported from bot/kodiak/test_event_handlers.py
func TestGetBranchName(t *testing.T) {
	if got, ok := getBranchName("refs/heads/master"); !ok || got != "master" {
		t.Errorf("getBranchName(refs/heads/master) = (%q, %v), want (master, true)", got, ok)
	}
	if got, ok := getBranchName("refs/heads/master/refs/heads/123"); !ok || got != "master/refs/heads/123" {
		t.Errorf("getBranchName(refs/heads/master/refs/heads/123) = (%q, %v)", got, ok)
	}
	if got, ok := getBranchName("refs/tags/v0.1.0"); ok {
		t.Errorf("getBranchName(refs/tags/v0.1.0) = (%q, %v), want not ok", got, ok)
	}
}

// ported from bot/kodiak/tests/event_handlers/test_check_run.py
//
// We should filter out pull requests that don't belong to our repository.
func TestCheckRunIgnoreExternalPullRequests(t *testing.T) {
	const internalRepositoryID = 554453
	internalPullRequest := events.PullRequest{
		Number: 123,
		Base:   events.Ref{Ref: "main", Repo: &events.PullRequestRepository{ID: internalRepositoryID}},
	}
	externalPullRequest := events.PullRequest{
		Number: 534,
		Base:   events.Ref{Ref: "main", Repo: &events.PullRequestRepository{ID: 22}},
	}
	event := &events.CheckRunEvent{
		GithubEvent: events.GithubEvent{Installation: events.Installation{ID: 69039045}},
		CheckRun: events.CheckRun{
			Name:         "circleci: build",
			PullRequests: []events.PullRequest{externalPullRequest, internalPullRequest},
		},
		Repository: events.Repository{
			ID:    internalRepositoryID,
			Name:  "cake-api",
			Owner: events.Owner{Login: "acme-corp"},
		},
	}
	want := []WebhookEvent{
		{
			RepoOwner:         "acme-corp",
			RepoName:          "cake-api",
			PullRequestNumber: internalPullRequest.Number,
			InstallationID:    "69039045",
			TargetName:        "main",
		},
	}
	if diff := cmp.Diff(want, checkRun(event)); diff != "" {
		t.Errorf("checkRun mismatch (-want +got):\n%s", diff)
	}
}
