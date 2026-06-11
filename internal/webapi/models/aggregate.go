package models

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AggregateEvents ports PullRequestActivity.aggregate_events: create
// PullRequestActivity rows from GitHubEvent information.
func AggregateEvents(ctx context.Context, db *pgxpool.Pool) error {
	startTime := time.Now()

	var d time.Time
	err := db.QueryRow(ctx, `
SELECT min_date FROM pull_request_activity_progress ORDER BY min_date DESC LIMIT 1`).Scan(&d)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var minDate *time.Time
	var eventsAggregated int64
	if err == nil {
		// progress stores a date; aggregate from midnight UTC of that day.
		m := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		minDate = &m
		if err := db.QueryRow(ctx, `SELECT count(*) FROM github_event WHERE created_at >= $1`, m).Scan(&eventsAggregated); err != nil {
			return err
		}
	} else if err := db.QueryRow(ctx, `SELECT count(*) FROM github_event`).Scan(&eventsAggregated); err != nil {
		return err
	}

	newMinDate := time.Now()
	if err := GenerateActivityData(ctx, db, minDate, nil); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
INSERT INTO pull_request_activity_progress (id, created_at, modified_at, min_date)
VALUES ($1, NOW(), NOW(), $2::date)`, uuid.New(), newMinDate)
	if err != nil {
		return err
	}
	slog.Info("generate_activity_data",
		"events_aggregated", eventsAggregated,
		"min_date", minDate,
		"new_min_date", newMinDate.Format("2006-01-02"),
		"duration_seconds", time.Since(startTime).Seconds())
	return nil
}

// GenerateActivityData ports PullRequestActivity.generate_activity_data:
// generate/update PullRequestActivity using data from the github_event table.
func GenerateActivityData(ctx context.Context, db *pgxpool.Pool, minDate *time.Time, installationID *int64) error {
	var whereClause []string
	var args []any
	if minDate != nil {
		args = append(args, *minDate)
		whereClause = append(whereClause, fmt.Sprintf("created_at > $%d::date", len(args)))
	}
	if installationID != nil {
		args = append(args, *installationID)
		whereClause = append(whereClause, fmt.Sprintf("(payload -> 'installation' ->> 'id')::integer = $%d", len(args)))
	}
	where := ""
	if len(whereClause) > 0 {
		where = " WHERE " + strings.Join(whereClause, " AND ")
	}
	_, err := db.Exec(ctx, `
INSERT INTO pull_request_activity (
    "id",
    "created_at",
    "modified_at",
    "date",
    "github_installation_id",
    kodiak_updated,
    kodiak_merged,
    kodiak_approved,
    "total_opened",
    "total_merged",
    total_closed)
SELECT
    uuid_generate_v4 () "id",
    NOW() created_at,
    NOW() modified_at,
    created_at::date "date",
    (payload -> 'installation' ->> 'id')::integer github_installation_id,
sum(
    CASE WHEN (event_name = 'pull_request'
        AND payload -> 'sender' ->> 'login' LIKE 'kodiak%[bot]'
        AND payload ->> 'action' = 'synchronize') THEN
        1
    ELSE
        0
    END) kodiak_updated,
sum(
    CASE WHEN (event_name = 'pull_request'
        AND payload -> 'sender' ->> 'login' LIKE 'kodiak%[bot]'
        AND payload ->> 'action' = 'closed'
        AND payload -> 'pull_request' -> 'merged' = to_jsonb (TRUE)) THEN
        1
    ELSE
        0
    END) kodiak_merged,
sum(
    CASE WHEN (event_name = 'pull_request_review'
        AND payload -> 'sender' ->> 'login' LIKE 'kodiak%[bot]') THEN
        1
    ELSE
        0
    END) kodiak_approved,
sum(
    CASE WHEN (event_name = 'pull_request'
        AND payload ->> 'action' = 'opened') THEN
        1
    ELSE
        0
    END) total_opened,
sum(
    CASE WHEN (event_name = 'pull_request'
        AND payload ->> 'action' = 'closed'
        AND payload -> 'pull_request' -> 'merged' = to_jsonb (TRUE)) THEN
        1
    ELSE
        0
    END) total_merged,
sum(
    CASE WHEN (event_name = 'pull_request'
        AND payload ->> 'action' = 'closed'
        AND payload -> 'pull_request' -> 'merged' = to_jsonb (FALSE)) THEN
        1
    ELSE
        0
    END) total_closed
FROM
    github_event
`+where+`
GROUP BY
    (payload -> 'installation' ->> 'id')::integer,
    created_at::date
ON CONFLICT ON CONSTRAINT unique_pull_request_activity
DO UPDATE
    SET
        kodiak_updated = excluded.kodiak_updated,
        kodiak_merged = excluded.kodiak_merged,
        kodiak_approved = excluded.kodiak_approved,
        total_opened = excluded.total_opened,
        total_merged = excluded.total_merged,
        total_closed = excluded.total_closed`, args...)
	return err
}

// GenerateUserActivity ports UserPullRequestActivity.generate: find all pull
// requests acted on by Kodiak.
func GenerateUserActivity(ctx context.Context, db *pgxpool.Pool) error {
	var progressMinDate time.Time
	err := db.QueryRow(ctx, `
SELECT min_date FROM user_pull_request_activity_progress ORDER BY min_date DESC LIMIT 1`).Scan(&progressMinDate)
	hasProgress := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	where := ""
	var args []any
	if hasProgress {
		args = append(args, progressMinDate)
		where = " AND created_at > $1"
	}
	_, err = db.Exec(ctx, `
INSERT INTO user_pull_request_activity (
    "id",
    "created_at",
    "modified_at",
    "github_installation_id",
    "github_repository_name",
    "github_pull_request_number",
    "github_user_login",
    "github_user_id",
    "is_private_repository",
    "activity_date",
    "opened_pull_request")
SELECT
    uuid_generate_v4 () "id",
    NOW() created_at,
    NOW() modified_at,
    (payload -> 'installation' ->> 'id')::int github_installation_id,
    (payload -> 'repository' ->> 'name') github_repository_name,
    (payload -> 'pull_request' ->> 'number')::int github_pull_request_number,
    max(payload -> 'sender' ->> 'login') github_user_login,
    (payload -> 'sender' ->> 'id')::integer github_user_id,
    bool_or((payload -> 'repository' ->> 'private')::boolean) is_private_repository,
    created_at::date activity_date,
    bool_or(payload ->> 'action' = 'opened') opened_pull_request
FROM
    github_event
WHERE
    event_name in ('pull_request', 'pull_request_review')
    `+where+`
GROUP BY
    github_installation_id,
    github_repository_name,
    github_pull_request_number,
    github_user_id,
    activity_date
HAVING (payload -> 'installation' ->> 'id')::int IS NOT NULL
    AND (payload -> 'repository' ->> 'name') IS NOT NULL
    AND (payload -> 'pull_request' ->> 'number')::int IS NOT NULL
    AND max(payload -> 'sender' ->> 'login') IS NOT NULL
    AND (payload -> 'sender' ->> 'id')::integer IS NOT NULL
ON CONFLICT ON CONSTRAINT unique_user_pull_request_activity
    DO UPDATE
        SET opened_pull_request = (excluded.opened_pull_request OR user_pull_request_activity.opened_pull_request),
        is_private_repository = (excluded.is_private_repository OR user_pull_request_activity.is_private_repository)`, args...)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `
INSERT INTO user_pull_request_activity_progress (id, created_at, modified_at, min_date)
VALUES ($1, NOW(), NOW(), NOW())`, uuid.New())
	return err
}
