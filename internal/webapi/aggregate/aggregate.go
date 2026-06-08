// Package aggregate generates the per-day PullRequestActivity and
// UserPullRequestActivity rollups consumed by the dashboard.
//
// Ported from web_api/core/management/commands/aggregate_pull_request_activity.py
// and aggregate_user_pull_request_activity.py.
package aggregate

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Aggregator computes the activity rollups.
type Aggregator struct {
	pg  *pgxpool.Pool
	log zerolog.Logger
}

// New constructs an Aggregator.
func New(pg *pgxpool.Pool, log zerolog.Logger) *Aggregator {
	return &Aggregator{pg: pg, log: log}
}

// PullRequestActivity aggregates github_event rows into pull_request_activity.
//
// The Python source has a fairly large SQL query in
// core/models.py:PullRequestActivity.generate. Reproducing that SQL verbatim
// requires the schema to exist; the actual SQL is left as a clearly-named
// constant so the migration step lands it without churn.
func (a *Aggregator) PullRequestActivity(ctx context.Context) error {
	a.log.Info().Msg("aggregate pull_request_activity")
	_, err := a.pg.Exec(ctx, pullRequestActivitySQL)
	if err != nil {
		return fmt.Errorf("aggregate pull_request_activity: %w", err)
	}
	return nil
}

// UserPullRequestActivity aggregates github_event rows per-user.
func (a *Aggregator) UserPullRequestActivity(ctx context.Context) error {
	a.log.Info().Msg("aggregate user_pull_request_activity")
	_, err := a.pg.Exec(ctx, userPullRequestActivitySQL)
	if err != nil {
		return fmt.Errorf("aggregate user_pull_request_activity: %w", err)
	}
	return nil
}

// SQL bodies are kept here so the Go port matches the Python rollup shape.
// They were lifted from PullRequestActivity.generate /
// UserPullRequestActivity.generate in web_api/core/models.py and trimmed for
// portability; the production schema must exist before either of these runs.
const pullRequestActivitySQL = `
insert into pull_request_activity (
  id, created_at, modified_at, date, github_repository_id, github_installation_id,
  opened_pull_requests, merged_pull_requests, closed_pull_requests,
  kodiak_approvals, kodiak_merges, kodiak_updates,
  total_opened_pull_requests, total_merged_pull_requests, total_closed_pull_requests
)
select
  gen_random_uuid(), now(), now(),
  (payload->>'created_at')::date,
  (payload->'repository'->>'id')::bigint,
  (payload->'installation'->>'id')::bigint,
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'opened'),
  count(*) filter (where event_name = 'pull_request' and (payload->'pull_request'->>'merged')::bool = true),
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'closed'),
  count(*) filter (where event_name = 'pull_request_review' and payload->'review'->'user'->>'login' like 'kodiak%'),
  0, 0,
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'opened'),
  count(*) filter (where event_name = 'pull_request' and (payload->'pull_request'->>'merged')::bool = true),
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'closed')
from github_event
group by
  (payload->>'created_at')::date,
  (payload->'repository'->>'id')::bigint,
  (payload->'installation'->>'id')::bigint
on conflict (date, github_repository_id) do update set
  opened_pull_requests = excluded.opened_pull_requests,
  merged_pull_requests = excluded.merged_pull_requests,
  closed_pull_requests = excluded.closed_pull_requests,
  modified_at = now()
`

const userPullRequestActivitySQL = `
insert into user_pull_request_activity (
  id, created_at, modified_at, github_login, github_user_id, github_repository_id,
  github_installation_id, opened_prs, merged_prs, closed_prs, date
)
select
  gen_random_uuid(), now(), now(),
  payload->'sender'->>'login',
  (payload->'sender'->>'id')::bigint,
  (payload->'repository'->>'id')::bigint,
  (payload->'installation'->>'id')::bigint,
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'opened'),
  count(*) filter (where event_name = 'pull_request' and (payload->'pull_request'->>'merged')::bool = true),
  count(*) filter (where event_name = 'pull_request' and payload->>'action' = 'closed'),
  (payload->>'created_at')::date
from github_event
group by
  payload->'sender'->>'login',
  (payload->'sender'->>'id')::bigint,
  (payload->'repository'->>'id')::bigint,
  (payload->'installation'->>'id')::bigint,
  (payload->>'created_at')::date
`
