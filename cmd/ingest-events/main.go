// Command ingest-events tails the bot's webhook envelope queue in Redis and
// writes events to Postgres. Ports web_api manage.py ingest_events.
package main

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	botlogging "github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/bot/queue"
	"github.com/appscodelabs/kodiak/internal/webapi/config"
	"github.com/appscodelabs/kodiak/internal/webapi/ingest"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		exit(err)
	}
	log := botlogging.New(botlogging.Level("INFO"), nil)

	if cfg.RedisURL == "" {
		exit(errors.New("REDIS_URL is required"))
	}
	u, err := url.Parse(cfg.RedisURL)
	if err != nil {
		exit(err)
	}
	pwd, _ := u.User.Password()
	rdb := redis.NewClient(&redis.Options{
		Addr: u.Host, Password: pwd, DB: queue.MustDB(u.Path),
	})
	defer rdb.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pg, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exit(err)
	}
	defer pg.Close()

	if err := ingest.New(rdb, pg, log).Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		exit(err)
	}
}

func exit(err error) {
	_, _ = os.Stderr.WriteString(err.Error() + "\n")
	os.Exit(1)
}
