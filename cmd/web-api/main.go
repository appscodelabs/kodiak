// Command web-api is the Go port of the Django backend in web_api/.
//
// Subcommands mirror the Django management commands:
//
//	serve (default)                        run the HTTP API server
//	ingest-events                          ingest webhook events into Postgres
//	aggregate-pull-request-activity        aggregate GitHubEvents into PullRequestActivity
//	aggregate-user-pull-request-activity   generate user pull request activity analytics
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/webapi"
	"github.com/appscodelabs/kodiak/internal/webapi/ingest"
	"github.com/appscodelabs/kodiak/internal/webapi/models"
	"github.com/appscodelabs/kodiak/internal/webapi/settings"
)

func main() {
	if err := run(); err != nil {
		slog.Error("web-api failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := settings.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer db.Close()

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		return serve(ctx, cfg, db, rdb)
	case "ingest-events":
		return ingest.Run(ctx, rdb, db)
	case "aggregate-pull-request-activity":
		return models.AggregateEvents(ctx, db)
	case "aggregate-user-pull-request-activity":
		return models.GenerateUserActivity(ctx, db)
	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func serve(ctx context.Context, cfg *settings.Config, db *pgxpool.Pool, rdb *redis.Client) error {
	app := webapi.New(cfg, db, rdb)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           app.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("web-api listening", "addr", server.Addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil
	}
}
