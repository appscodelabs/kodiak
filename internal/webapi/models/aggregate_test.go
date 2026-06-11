package models

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/dbtest"
)

// insertGitHubEvent mirrors the pull_request_kodiak_updated pytest fixture:
// GitHubEvent.objects.create(...) followed by an explicit created_at update.
func insertGitHubEvent(t *testing.T, db *pgxpool.Pool, eventName string, payload []byte, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
INSERT INTO github_event (id, created_at, modified_at, event_name, payload)
VALUES ($1, $2, NOW(), $3, $4::jsonb)`, uuid.New(), createdAt, eventName, string(payload))
	if err != nil {
		t.Fatalf("insert github_event: %v", err)
	}
}

func countRows(t *testing.T, db *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func loadEventFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../web_api/web_api/tests/fixtures/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// Ported from test_analytics_aggregator in
// web_api/web_api/test_analytics_aggregator.py.
func TestAnalyticsAggregator(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	insertGitHubEvent(t, db, "pull_request",
		loadEventFixture(t, "pull_request_kodiak_updated.json"),
		time.Date(2020, 2, 13, 0, 0, 0, 0, time.UTC))

	if got := countRows(t, db, "pull_request_activity_progress"); got != 0 {
		t.Fatalf("progress count = %d, want 0", got)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Fatalf("activity count = %d, want 0", got)
	}

	if err := AggregateEvents(ctx, db); err != nil {
		t.Fatalf("AggregateEvents: %v", err)
	}

	if got := countRows(t, db, "pull_request_activity_progress"); got != 1 {
		t.Errorf("progress count = %d, want 1", got)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 1 {
		t.Fatalf("activity count = %d, want 1", got)
	}

	var minDate time.Time
	if err := db.QueryRow(ctx, `SELECT min_date FROM pull_request_activity_progress`).Scan(&minDate); err != nil {
		t.Fatalf("scan min_date: %v", err)
	}
	// NOTE(port): Python compares against datetime.date.today(); the progress
	// row is written via NOW()::date in the database session (UTC), so the
	// comparison uses the UTC date to avoid timezone flake.
	if got, want := minDate.Format("2006-01-02"), time.Now().UTC().Format("2006-01-02"); got != want {
		t.Errorf("progress min_date = %s, want %s", got, want)
	}

	var a PullRequestActivity
	err := db.QueryRow(ctx, `
SELECT total_opened, total_merged, total_closed, kodiak_approved, kodiak_merged, kodiak_updated
FROM pull_request_activity`).Scan(
		&a.TotalOpened, &a.TotalMerged, &a.TotalClosed,
		&a.KodiakApproved, &a.KodiakMerged, &a.KodiakUpdated)
	if err != nil {
		t.Fatalf("scan pull_request_activity: %v", err)
	}
	if a.TotalOpened != 0 {
		t.Errorf("total_opened = %d, want 0", a.TotalOpened)
	}
	if a.TotalMerged != 0 {
		t.Errorf("total_merged = %d, want 0", a.TotalMerged)
	}
	if a.TotalClosed != 0 {
		t.Errorf("total_closed = %d, want 0", a.TotalClosed)
	}
	if a.KodiakApproved != 0 {
		t.Errorf("kodiak_approved = %d, want 0", a.KodiakApproved)
	}
	if a.KodiakMerged != 0 {
		t.Errorf("kodiak_merged = %d, want 0", a.KodiakMerged)
	}
	if a.KodiakUpdated != 1 {
		t.Errorf("kodiak_updated = %d, want 1", a.KodiakUpdated)
	}
}

// Ported from test_analytics_aggregator_min_date in
// web_api/web_api/test_analytics_aggregator.py.
func TestAnalyticsAggregatorMinDate(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()

	insertGitHubEvent(t, db, "pull_request",
		loadEventFixture(t, "pull_request_kodiak_updated.json"),
		time.Date(2020, 2, 13, 0, 0, 0, 0, time.UTC))

	insertProgress := func(minDate time.Time) {
		_, err := db.Exec(ctx, `
INSERT INTO pull_request_activity_progress (id, created_at, modified_at, min_date)
VALUES ($1, NOW(), NOW(), $2::date)`, uuid.New(), minDate)
		if err != nil {
			t.Fatalf("insert progress: %v", err)
		}
	}
	insertProgress(time.Date(2020, 2, 10, 0, 0, 0, 0, time.UTC))
	insertProgress(time.Now().UTC())

	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Fatalf("activity count = %d, want 0", got)
	}
	if err := AggregateEvents(ctx, db); err != nil {
		t.Fatalf("AggregateEvents: %v", err)
	}
	if got := countRows(t, db, "pull_request_activity"); got != 0 {
		t.Errorf("activity count = %d, want 0", got)
	}
}
