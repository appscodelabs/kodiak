// Package store wraps pgxpool with the queries the dashboard API needs.
//
// One file per logical group of queries (users, accounts, activity, stripe).
// Ported from web_api/core/models.py — the static .objects.filter()/
// .objects.get() calls become explicit SQL here.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// Store holds the pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ErrNotFound is returned when a lookup misses.
var ErrNotFound = errors.New("not found")

// UserByID fetches a user by primary key.
func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (*models.User, error) {
	const q = `select id, created_at, modified_at, github_id, github_login, github_access_token from "user" where id = $1`
	var u models.User
	err := s.pool.QueryRow(ctx, q, id).Scan(&u.ID, &u.CreatedAt, &u.ModifiedAt, &u.GitHubID, &u.GitHubLogin, &u.GitHubAccessToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("user by id: %w", err)
	}
	return &u, nil
}

// UpsertUserByGitHubID is used after OAuth login to find-or-create the user.
func (s *Store) UpsertUserByGitHubID(ctx context.Context, githubID int64, login, accessToken string) (*models.User, error) {
	const q = `
insert into "user" (id, created_at, modified_at, github_id, github_login, github_access_token)
values (gen_random_uuid(), now(), now(), $1, $2, $3)
on conflict (github_id) do update set
  github_login = excluded.github_login,
  github_access_token = excluded.github_access_token,
  modified_at = now()
returning id, created_at, modified_at, github_id, github_login, github_access_token`
	var u models.User
	err := s.pool.QueryRow(ctx, q, githubID, login, accessToken).
		Scan(&u.ID, &u.CreatedAt, &u.ModifiedAt, &u.GitHubID, &u.GitHubLogin, &u.GitHubAccessToken)
	if err != nil {
		return nil, fmt.Errorf("upsert user: %w", err)
	}
	return &u, nil
}

// AccountsForUser returns every account a user has membership in.
func (s *Store) AccountsForUser(ctx context.Context, userID uuid.UUID) ([]models.Account, error) {
	const q = `
select a.id, a.created_at, a.modified_at, a.github_installation_id,
       a.github_account_id, a.github_account_login, a.github_account_type,
       a.stripe_customer_id, a.trial_expiration, a.trial_start,
       a.trial_started_by_id, a.subscription_exempt, a.subscription_exempt_message
from account a
join account_membership m on m.account_id = a.id
where m.user_id = $1
order by a.github_account_login`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("accounts for user: %w", err)
	}
	defer rows.Close()
	var out []models.Account
	for rows.Next() {
		var a models.Account
		if err := rows.Scan(
			&a.ID, &a.CreatedAt, &a.ModifiedAt, &a.GitHubInstallationID,
			&a.GitHubAccountID, &a.GitHubAccountLogin, &a.GitHubAccountType,
			&a.StripeCustomerID, &a.TrialExpiration, &a.TrialStart,
			&a.TrialStartedBy, &a.SubscriptionExempt, &a.SubscriptionExemptMessage,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AccountForTeam returns the account when the user is a member.
func (s *Store) AccountForTeam(ctx context.Context, userID, teamID uuid.UUID) (*models.Account, error) {
	const q = `
select a.id, a.created_at, a.modified_at, a.github_installation_id,
       a.github_account_id, a.github_account_login, a.github_account_type,
       a.stripe_customer_id, a.trial_expiration, a.trial_start,
       a.trial_started_by_id, a.subscription_exempt, a.subscription_exempt_message
from account a
join account_membership m on m.account_id = a.id
where m.user_id = $1 and a.id = $2`
	var a models.Account
	err := s.pool.QueryRow(ctx, q, userID, teamID).Scan(
		&a.ID, &a.CreatedAt, &a.ModifiedAt, &a.GitHubInstallationID,
		&a.GitHubAccountID, &a.GitHubAccountLogin, &a.GitHubAccountType,
		&a.StripeCustomerID, &a.TrialExpiration, &a.TrialStart,
		&a.TrialStartedBy, &a.SubscriptionExempt, &a.SubscriptionExemptMessage,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("account for team: %w", err)
	}
	return &a, nil
}

// PullRequestActivityForInstallation returns the per-day rollup used by the
// activity chart.
func (s *Store) PullRequestActivityForInstallation(ctx context.Context, installationID int64) ([]models.PullRequestActivity, error) {
	const q = `
select id, created_at, modified_at, date, github_repository_id, github_installation_id,
       opened_pull_requests, merged_pull_requests, closed_pull_requests,
       kodiak_approvals, kodiak_merges, kodiak_updates,
       total_opened_pull_requests, total_merged_pull_requests, total_closed_pull_requests
from pull_request_activity
where github_installation_id = $1
order by date`
	rows, err := s.pool.Query(ctx, q, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.PullRequestActivity
	for rows.Next() {
		var a models.PullRequestActivity
		if err := rows.Scan(
			&a.ID, &a.CreatedAt, &a.ModifiedAt, &a.Date, &a.GitHubRepositoryID, &a.GitHubInstallationID,
			&a.OpenedPullRequests, &a.MergedPullRequests, &a.ClosedPullRequests,
			&a.KodiakApprovals, &a.KodiakMerges, &a.KodiakUpdates,
			&a.TotalOpenedPullRequests, &a.TotalMergedPullRequests, &a.TotalClosedPullRequests,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
