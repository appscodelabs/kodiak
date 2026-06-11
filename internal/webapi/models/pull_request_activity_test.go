// Ported from web_api/web_api/test_pull_request_activity.py.
package models

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/dbtest"
)

// paInsertFixtureEvent mirrors the pytest fixtures in
// web_api/web_api/test_pull_request_activity.py and
// web_api/web_api/test_user_pull_request_activity.py: each creates a
// GitHubEvent from a fixture payload and then overwrites created_at with
// 2020-02-13.
func paInsertFixtureEvent(t *testing.T, db *pgxpool.Pool, eventName, fixtureName string) {
	t.Helper()
	insertGitHubEvent(t, db, eventName, loadEventFixture(t, fixtureName),
		time.Date(2020, 2, 13, 0, 0, 0, 0, time.UTC))
}

// paGetPullRequestActivity ports PullRequestActivity.objects.get(): fetch the
// only pull_request_activity row, failing the test if there is not exactly
// one.
func paGetPullRequestActivity(t *testing.T, db *pgxpool.Pool) PullRequestActivity {
	t.Helper()
	if got := countRows(t, db, "pull_request_activity"); got != 1 {
		t.Fatalf("pull_request_activity count = %d, want 1", got)
	}
	var a PullRequestActivity
	err := db.QueryRow(context.Background(), `
SELECT date, total_opened, total_merged, total_closed, kodiak_approved, kodiak_merged,
       kodiak_updated, github_installation_id
FROM pull_request_activity`).Scan(
		&a.Date, &a.TotalOpened, &a.TotalMerged, &a.TotalClosed,
		&a.KodiakApproved, &a.KodiakMerged, &a.KodiakUpdated, &a.GithubInstallationID)
	if err != nil {
		t.Fatalf("scan pull_request_activity: %v", err)
	}
	return a
}

// Ported from test_generate_activity_data in
// web_api/web_api/test_pull_request_activity.py.
func TestGenerateActivityData(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	paInsertFixtureEvent(t, db, "pull_request", "pull_request_kodiak_updated.json")
	paInsertFixtureEvent(t, db, "pull_request", "pull_request_kodiak_merged.json")
	// tood fixme installation wrong
	paInsertFixtureEvent(t, db, "pull_request_review", "pull_request_review_kodiak_approved.json")
	paInsertFixtureEvent(t, db, "pull_request", "pull_request_total_opened.json")
	paInsertFixtureEvent(t, db, "pull_request", "pull_request_total_merged.json")
	paInsertFixtureEvent(t, db, "pull_request", "pull_request_total_closed.json")

	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Fatalf("pull_request_activity count = %d, want 0", got)
	}
	if err := GenerateActivityData(ctx, db, nil, nil); err != nil {
		t.Fatalf("GenerateActivityData: %v", err)
	}

	if got := countRows(t, db, "pull_request_activity"); got != 1 {
		t.Fatalf("pull_request_activity count = %d, want 1", got)
	}

	checkResponse := func(a PullRequestActivity) {
		t.Helper()
		if got, want := a.Date.Format("2006-01-02"), "2020-02-13"; got != want {
			t.Errorf("date = %s, want %s", got, want)
		}
		if a.TotalOpened != 1 {
			t.Errorf("total_opened = %d, want 1", a.TotalOpened)
		}
		if a.TotalMerged != 2 {
			t.Errorf("total_merged = %d, want 2 (one user merge and one from kodiak)", a.TotalMerged)
		}
		if a.TotalClosed != 1 {
			t.Errorf("total_closed = %d, want 1", a.TotalClosed)
		}
		if a.KodiakApproved != 1 {
			t.Errorf("kodiak_approved = %d, want 1", a.KodiakApproved)
		}
		if a.KodiakMerged != 2 {
			t.Errorf("kodiak_merged = %d, want 2", a.KodiakMerged)
		}
		if a.KodiakUpdated != 1 {
			t.Errorf("kodiak_updated = %d, want 1", a.KodiakUpdated)
		}
		// NOTE(chdsbd): I don't like how this field is called "account_id" when the
		// foreign key is on github_installation_id of an account.
		if a.GithubInstallationID != 848733 {
			t.Errorf("github_installation_id = %d, want 848733 (the ID of the installation from the json files)",
				a.GithubInstallationID)
		}
	}

	checkResponse(paGetPullRequestActivity(t, db))

	// running twice should update results and not hit database integrity errors
	// because we use INSERT ON CONFLICT.
	_, err := db.Exec(ctx, `
UPDATE pull_request_activity
SET total_opened = 0, total_merged = 0, total_closed = 0,
    kodiak_approved = 0, kodiak_merged = 0, kodiak_updated = 0`)
	if err != nil {
		t.Fatalf("zero pull_request_activity totals: %v", err)
	}

	if err := GenerateActivityData(ctx, db, nil, nil); err != nil {
		t.Fatalf("GenerateActivityData: %v", err)
	}

	checkResponse(paGetPullRequestActivity(t, db))
}

// Ported from test_generate_activity_data_with_args in
// web_api/web_api/test_pull_request_activity.py.
func TestGenerateActivityDataWithArgs(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	paInsertFixtureEvent(t, db, "pull_request", "pull_request_total_closed.json")

	account, err := CreateAccount(ctx, db, 848733, 49153, "chdsbd", AccountTypeUser)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	otherAccount, err := CreateAccount(ctx, db, 377930, 900966, "recipeyak", AccountTypeUser)
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Fatalf("pull_request_activity count = %d, want 0", got)
	}

	// try with a date in the future of the pull request
	futureDate := time.Date(2050, 5, 5, 0, 0, 0, 0, time.UTC)
	if err := GenerateActivityData(ctx, db, &futureDate, nil); err != nil {
		t.Fatalf("GenerateActivityData: %v", err)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Errorf("pull_request_activity count = %d, want 0 (our event should be excluded)", got)
	}

	// try with account for different organization
	if err := GenerateActivityData(ctx, db, nil, &otherAccount.GithubInstallationID); err != nil {
		t.Fatalf("GenerateActivityData: %v", err)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Errorf("pull_request_activity count = %d, want 0 (we shouldn't have any events because this is a different account)", got)
	}

	minDate := time.Date(2000, 4, 13, 0, 0, 0, 0, time.UTC)
	if err := GenerateActivityData(ctx, db, &minDate, &account.GithubInstallationID); err != nil {
		t.Fatalf("GenerateActivityData: %v", err)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 1 {
		t.Fatalf("pull_request_activity count = %d, want 1 (we should have an event because our pull_request_total_closed is after the min date and is on the same account)", got)
	}
	a := paGetPullRequestActivity(t, db)

	if got, want := a.Date.Format("2006-01-02"), "2020-02-13"; got != want {
		t.Errorf("date = %s, want %s", got, want)
	}
	if a.TotalOpened != 0 {
		t.Errorf("total_opened = %d, want 0", a.TotalOpened)
	}
	if a.TotalMerged != 0 {
		t.Errorf("total_merged = %d, want 0", a.TotalMerged)
	}
	if a.TotalClosed != 1 {
		t.Errorf("total_closed = %d, want 1", a.TotalClosed)
	}
	if a.KodiakApproved != 0 {
		t.Errorf("kodiak_approved = %d, want 0", a.KodiakApproved)
	}
	if a.KodiakMerged != 0 {
		t.Errorf("kodiak_merged = %d, want 0", a.KodiakMerged)
	}
	if a.KodiakUpdated != 0 {
		t.Errorf("kodiak_updated = %d, want 0", a.KodiakUpdated)
	}
}
