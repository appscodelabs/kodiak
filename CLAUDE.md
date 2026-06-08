# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository status

Kodiak is a Go codebase. The bot, dashboard API, event ingestor, and the two activity aggregators all live under `cmd/` with their packages under `internal/`. The previous Python implementations have been preserved under `legacy/bot/` and `legacy/web_api/` as reference material — they are no longer the build target and do not run in CI. The React `web_ui/` (CRA + TypeScript) and Docusaurus `docs/` are unchanged.

## Layout

```
cmd/
  bot/                       — webhook receiver entry point          (was bot/kodiak/main.py)
  webapi/                    — dashboard API entry point             (was web_api/web_api/wsgi.py)
  ingest-events/             — Redis→Postgres event ingestor         (was web_api manage.py ingest_events)
  aggregate-pr-activity/     — per-day PR activity rollup            (was web_api manage.py aggregate_pull_request_activity)
  aggregate-user-activity/   — per-user PR activity rollup           (was web_api manage.py aggregate_user_pull_request_activity)
internal/
  bot/
    appconfig/    — env-var loading                                  (← bot/kodiak/app_config.py)
    config/       — .kodiak.toml parsing                             (← bot/kodiak/config.py)
    events/       — webhook payload schemas                          (← bot/kodiak/events/)
    eventhandlers/— webhook → Redis queue dispatch                   (← bot/kodiak/event_handlers.py)
    evaluation/   — Mergeable() decision tree + retry loop           (← bot/kodiak/evaluation.py + pull_request.py)
    logging/      — zerolog setup                                    (← bot/kodiak/logging.py)
    messages/     — bot comment/status check rendering               (← bot/kodiak/messages.py)
    queries/      — GitHub REST + GraphQL + JWT installation auth    (← bot/kodiak/queries.py)
    queue/        — Redis-backed webhook + merge queues              (← bot/kodiak/queue.py)
    server/       — HTTP handler + HMAC verify                       (← bot/kodiak/main.py)
    text/         — text helpers                                     (← bot/kodiak/text.py)
    throttle/     — per-installation rate limit                      (← bot/kodiak/throttle.py)
  webapi/
    config/       — env-var loading                                  (← web_api/web_api/settings.py)
    models/       — DB row types                                     (← web_api/core/models.py)
    store/        — pgx queries                                      (← Django ORM .objects calls)
    auth/         — HMAC-signed session cookies                      (← web_api/core/auth.py)
    server/       — chi router with every route in core/urls.py      (← web_api/core/views.py)
    ingest/       — Redis→Postgres webhook envelope ingestion         (← web_api/core/event_ingestion.py)
    aggregate/    — pull_request_activity + user_pull_request_activity SQL rollups (← web_api/core/models.py:.generate)
Dockerfile.bot               — multi-stage Go build for cmd/bot
Dockerfile.webapi            — multi-stage Go build for cmd/webapi
.github/workflows/go.yml     — GitHub Actions: build/vet/test + image builds
.circleci/config.yml         — CircleCI: go_build, docs, web_ui jobs
legacy/                      — frozen Python reference (bot/, web_api/)
```

## Common commands

```shell
# Build / test / vet everything
go build ./...
go vet ./...
go test ./...

# Run binaries locally
go run ./cmd/bot                       # env: SECRET_KEY, REDIS_URL, GITHUB_APP_ID, GITHUB_APP_NAME, GITHUB_PRIVATE_KEY*
go run ./cmd/webapi                    # env: DATABASE_URL, SECRET_KEY (Stripe/GitHub OAuth vars optional)
go run ./cmd/ingest-events             # env: DATABASE_URL, REDIS_URL
go run ./cmd/aggregate-pr-activity     # env: DATABASE_URL
go run ./cmd/aggregate-user-activity   # env: DATABASE_URL

# Docker images
docker build -f Dockerfile.bot    -t kodiak-bot    .
docker build -f Dockerfile.webapi -t kodiak-webapi .
```

For the React frontend and the Docusaurus site, see `web_ui/README.md` and `docs/README.md` (commands unchanged from the Python era).

## Architecture

### Bot

`cmd/bot/main.go` wires:

1. `appconfig.Load()` reads env vars.
2. `redis.NewClient` connects to Redis using `REDIS_URL`.
3. `queries.NewFactory()` builds the installation-aware GitHub client: it parses the RSA PEM, signs RS256 JWTs scoped to the App, and caches per-installation access tokens.
4. `evaluation.New(api, appID, appName, subscriptionsEnabled)` constructs the per-PR evaluator (implements `queue.Evaluator`).
5. `queue.New(rdb, evaluator, log)` wires the per-installation webhook queue and per-repo merge queue (both Redis sorted sets via `BZPOPMIN`).
6. `queue.Start(ctx)` restores any workers that existed before restart by reading `kodiak_webhook_queue_names` and `kodiak_merge_queue_names`.
7. `eventhandlers.New(...)` dispatches `pull_request`, `pull_request_review`, `check_run`, `status`, `push` into the queue.
8. `server.New(...).Handler()` serves `GET /` and `POST /api/github/hook` (HMAC-SHA1 verified against `SECRET_KEY`).

Event flow: webhook → `eventhandlers.Dispatcher.Handle` → `queue.Enqueue` → worker `BZPOPMIN`s → `evaluation.Evaluator.EvaluatePR` runs the retry loop (`ErrRetryForSkippableChecks` / `ErrPollForever` / `ErrAPICall`) → `Mergeable()` (the full state machine in `internal/bot/evaluation/mergeable.go`) drives the `PRAPI` adapter (`internal/bot/evaluation/pullrequest.go`) which calls `queries.Client` for merges, branch updates, labels, comments, and status check updates.

### Web API

`cmd/webapi/main.go` wires `pgxpool` → `server.New(cfg, pool, log).Router()`. The router (in `internal/webapi/server`) mounts every route from the original Django `core/urls.py` under `/v1/`; login-gated routes are wrapped with `auth.LoginRequired`. Auth uses an HMAC-signed cookie (`internal/webapi/auth`) keyed off `SECRET_KEY`. The DB rows are typed in `internal/webapi/models` and read via the `store.Store` (pgx) facade. Handlers that talk to Stripe (`update_subscription`, `start_checkout`, `cancel_subscription`, etc.) are intentionally returning HTTP 501 with a one-line description of what each needs to do — wire stripe-go into them as they're ported.

`cmd/ingest-events` runs the bot's `kodiak:webhook_event` Redis list to Postgres' `github_event` table. `cmd/aggregate-pr-activity` and `cmd/aggregate-user-activity` are one-shot binaries meant to run on cron; both execute a single `INSERT … SELECT` rollup whose SQL lives in `internal/webapi/aggregate`.

## CI

`.github/workflows/go.yml` runs `go build`, `go vet`, `go test` and builds both Docker images on every push/PR. `.circleci/config.yml` has the same `go_build` job plus the unchanged docs/web_ui pipelines. The old `bot_*` and `web_api_*` CircleCI jobs were removed when the Python sources moved to `legacy/`.

## Legacy reference

`legacy/bot/` and `legacy/web_api/` are the original Python implementations. They no longer build in CI, but they remain useful when:

- Validating exact wire compatibility of a port (compare a Go handler's behaviour against the matching Django view).
- Filling in the Stripe handlers in `internal/webapi/server` (the canonical implementations are still in `legacy/web_api/core/views.py`).
- Understanding the original GraphQL response shape (`legacy/bot/kodiak/queries.py:60` — `GET_EVENT_INFO_QUERY`).

These files are not edited as part of normal work — they're frozen reference material.
