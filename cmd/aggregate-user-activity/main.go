// Command aggregate-user-activity runs the UserPullRequestActivity rollup.
// Replaces web_api manage.py aggregate_user_pull_request_activity.
package main

import (
	"context"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	botlogging "github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/webapi/aggregate"
	"github.com/appscodelabs/kodiak/internal/webapi/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		exit(err.Error())
	}
	log := botlogging.New(botlogging.Level("INFO"), nil)
	ctx := context.Background()
	pg, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		exit(err.Error())
	}
	defer pg.Close()
	if err := aggregate.New(pg, log).UserPullRequestActivity(ctx); err != nil {
		exit(err.Error())
	}
}

func exit(msg string) {
	_, _ = os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}
