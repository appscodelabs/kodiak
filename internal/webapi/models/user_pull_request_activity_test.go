package models

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/dbtest"
)

// upaInsertFixtures ports the four GitHubEvent pytest fixtures shared by the
// generate tests: each event payload is stored with created_at 2020-02-13.
func upaInsertFixtures(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	createdAt := time.Date(2020, 2, 13, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{
		"pull_request_total_opened.json",
		"pull_request_kodiak_updated.json",
		"pull_request_kodiak_updated_different_institution.json",
		"pull_request_total_opened_different_institution.json",
	} {
		insertGitHubEvent(t, db, "pull_request", loadEventFixture(t, name), createdAt)
	}
}

func upaCountActivityForInstallation(t *testing.T, db *pgxpool.Pool, installationID int64) int {
	t.Helper()
	var n int
	err := db.QueryRow(context.Background(),
		`SELECT count(*) FROM user_pull_request_activity WHERE github_installation_id = $1`,
		installationID).Scan(&n)
	if err != nil {
		t.Fatalf("count user activity: %v", err)
	}
	return n
}

// Ported from test_generate in web_api/web_api/test_user_pull_request_activity.py.
func TestUserPullRequestActivityGenerate(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	upaInsertFixtures(t, db)

	if got := countRows(t, db, "user_pull_request_activity_progress"); got != 0 {
		t.Fatalf("progress count = %d, want 0", got)
	}

	if err := GenerateUserActivity(ctx, db); err != nil {
		t.Fatalf("GenerateUserActivity: %v", err)
	}

	if got := countRows(t, db, "user_pull_request_activity"); got != 4 {
		t.Errorf("activity count = %d, want 4", got)
	}
	if got := upaCountActivityForInstallation(t, db, 848733); got != 2 {
		t.Errorf("activity count for installation 848733 = %d, want 2", got)
	}
	if got := upaCountActivityForInstallation(t, db, 548321); got != 2 {
		t.Errorf("activity count for installation 548321 = %d, want 2", got)
	}
	if got := countRows(t, db, "user_pull_request_activity_progress"); got != 1 {
		t.Errorf("progress count = %d, want 1", got)
	}
}

// If we have a conflict with an existing row we should leave the row and do
// nothing. A user can only be active once per day, so if we've already created
// an activity event for the day, that's all we can do.
// Ported from test_generate_with_conflict in
// web_api/web_api/test_user_pull_request_activity.py.
func TestUserPullRequestActivityGenerateWithConflict(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	upaInsertFixtures(t, db)

	if got := countRows(t, db, "user_pull_request_activity_progress"); got != 0 {
		t.Fatalf("progress count = %d, want 0", got)
	}

	if err := GenerateUserActivity(ctx, db); err != nil {
		t.Fatalf("GenerateUserActivity: %v", err)
	}

	if got := countRows(t, db, "user_pull_request_activity"); got != 4 {
		t.Errorf("activity count = %d, want 4", got)
	}
	if got := upaCountActivityForInstallation(t, db, 848733); got != 2 {
		t.Errorf("activity count for installation 848733 = %d, want 2", got)
	}
	if got := upaCountActivityForInstallation(t, db, 548321); got != 2 {
		t.Errorf("activity count for installation 548321 = %d, want 2", got)
	}
	if got := countRows(t, db, "user_pull_request_activity_progress"); got != 1 {
		t.Errorf("progress count = %d, want 1", got)
	}

	if _, err := db.Exec(ctx, `DELETE FROM user_pull_request_activity_progress`); err != nil {
		t.Fatalf("delete progress: %v", err)
	}
	// re-aggregating the same events must not fail on the existing rows.
	if err := GenerateUserActivity(ctx, db); err != nil {
		t.Fatalf("GenerateUserActivity after progress delete: %v", err)
	}
}

// Ported from test_generate_min_progress in
// web_api/web_api/test_user_pull_request_activity.py.
func TestUserPullRequestActivityGenerateMinProgress(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	upaInsertFixtures(t, db)

	_, err := db.Exec(ctx, `
INSERT INTO user_pull_request_activity_progress (id, created_at, modified_at, min_date)
VALUES ($1, NOW(), NOW(), $2)`, uuid.New(), time.Date(2050, 4, 23, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("insert progress: %v", err)
	}

	if err := GenerateUserActivity(ctx, db); err != nil {
		t.Fatalf("GenerateUserActivity: %v", err)
	}
	if got := countRows(t, db, "user_pull_request_activity_progress"); got != 2 {
		t.Errorf("progress count = %d, want 2", got)
	}
	if got := countRows(t, db, "user_pull_request_activity"); got != 0 {
		t.Errorf("activity count = %d, want 0", got)
	}
}

// upaCreateUserActivity ports create_user_activity: a user row plus the
// matching kodiak[bot] row for the same repo/PR/day.
func upaCreateUserActivity(t *testing.T, db *pgxpool.Pool, installationID, userID, prNumber int64, date time.Time) {
	t.Helper()
	ctx := context.Background()
	insert := func(login string, githubUserID int64) {
		_, err := db.Exec(ctx, `
INSERT INTO user_pull_request_activity (
    id, created_at, modified_at, github_installation_id, github_repository_name,
    github_pull_request_number, github_user_login, github_user_id,
    is_private_repository, opened_pull_request, activity_date)
VALUES ($1, NOW(), NOW(), $2, 'acme_web', $3, $4, $5, TRUE, TRUE, $6::date)`,
			uuid.New(), installationID, prNumber, login, githubUserID, date)
		if err != nil {
			t.Fatalf("insert user activity: %v", err)
		}
	}
	insert(fmt.Sprintf("acme-user-%d", userID), userID)
	insert("kodiak[bot]", 0)
}

// Ported from test_get_active_users_in_last_30_days in
// web_api/web_api/test_user_pull_request_activity.py.
func TestGetActiveUsersInLast30Days(t *testing.T) {
	db := dbtest.New(t)
	// NOTE(port): the Python test builds an unsaved Account purely for its
	// installation id; only the id is needed here.
	const installationID = int64(52324234)
	now := time.Now().UTC()

	upaCreateUserActivity(t, db, installationID, 333777, 953, now.AddDate(0, 0, -5))
	upaCreateUserActivity(t, db, installationID, 333777, 953, now)

	upaCreateUserActivity(t, db, installationID, 90322322, 883, now.AddDate(0, 0, -10))
	upaCreateUserActivity(t, db, installationID, 90322322, 883, now)

	activeUsers, err := GetActiveUsersInLast30Days(context.Background(), db, installationID)
	if err != nil {
		t.Fatalf("GetActiveUsersInLast30Days: %v", err)
	}
	if len(activeUsers) != 2 {
		t.Fatalf("len(activeUsers) = %d, want 2", len(activeUsers))
	}
	if activeUsers[0].GithubID != 90322322 {
		t.Errorf("activeUsers[0].GithubID = %d, want 90322322", activeUsers[0].GithubID)
	}
	if activeUsers[1].GithubID != 333777 {
		t.Errorf("activeUsers[1].GithubID = %d, want 333777", activeUsers[1].GithubID)
	}
}
