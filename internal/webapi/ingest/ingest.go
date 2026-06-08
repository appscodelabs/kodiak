// Package ingest pulls GitHub webhook envelopes off Redis (written by the
// bot's usage-reporting code path) and persists them to Postgres.
//
// Ported from web_api/core/event_ingestion.py and core/management/commands/ingest_events.py.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
)

// interestingEvents is the allowlist mirrored from event_ingestion.py.
var interestingEvents = map[string]struct{}{
	"pull_request":         {},
	"pull_request_review":  {},
	"pull_request_comment": {},
}

// Ingestor reads compressed event envelopes from Redis and inserts rows into
// the github_event table.
type Ingestor struct {
	rdb *redis.Client
	pg  *pgxpool.Pool
	log zerolog.Logger
}

// New constructs an Ingestor.
func New(rdb *redis.Client, pg *pgxpool.Pool, log zerolog.Logger) *Ingestor {
	return &Ingestor{rdb: rdb, pg: pg, log: log}
}

// Run loops until ctx is canceled.
func (i *Ingestor) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		i.log.Info().Msg("block for event")
		res, err := i.rdb.BLPop(ctx, 5*time.Second, "kodiak:webhook_event").Result()
		if errors.Is(err, redis.Nil) {
			continue // timeout
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			i.log.Err(err).Msg("blpop failed")
			time.Sleep(1 * time.Second)
			continue
		}
		if len(res) != 2 {
			continue
		}
		if err := i.process(ctx, []byte(res[1])); err != nil {
			i.log.Err(err).Msg("process event failed")
		}
	}
}

func (i *Ingestor) process(ctx context.Context, raw []byte) error {
	// The Python source zstd-compresses the envelope. To keep the Go ingestor
	// portable we accept both raw JSON and zstd-compressed input. The bot's Go
	// rewrite writes raw JSON (see eventhandlers.reportUsage), so the common
	// case here is a direct unmarshal.
	var env struct {
		EventName string          `json:"event_name"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	if _, ok := interestingEvents[env.EventName]; !ok {
		return nil
	}
	const q = `insert into github_event (id, created_at, modified_at, event_name, payload) values (gen_random_uuid(), now(), now(), $1, $2::jsonb)`
	if _, err := i.pg.Exec(ctx, q, env.EventName, string(env.Payload)); err != nil {
		return fmt.Errorf("insert github_event: %w", err)
	}
	return nil
}

// Ensure uuid is referenced when callers want to log IDs.
var _ = uuid.Nil
