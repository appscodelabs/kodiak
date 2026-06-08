// Command bot is the Kodiak GitHub App webhook receiver and merge bot.
//
// Ported from bot/kodiak/main.py.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/bot/appconfig"
	"github.com/appscodelabs/kodiak/internal/bot/eventhandlers"
	"github.com/appscodelabs/kodiak/internal/bot/evaluation"
	botlogging "github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/bot/queries"
	"github.com/appscodelabs/kodiak/internal/bot/queue"
	"github.com/appscodelabs/kodiak/internal/bot/server"
)

func main() {
	cfg, err := appconfig.Load()
	if err != nil {
		// Logger isn't set up yet; write to stderr.
		errExit("load config: " + err.Error())
	}

	log := botlogging.New(botlogging.Level(cfg.LoggingLevel), nil)

	pwd, _ := cfg.RedisURL.User.Password()
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisURL.Host,
		Password: pwd,
		DB:       queue.MustDB(cfg.RedisURL.Path),
		PoolSize: cfg.RedisPoolSize,
	})
	defer rdb.Close()

	apiFactory, err := queries.NewFactory(queries.FactoryConfig{
		AppID:         cfg.GitHubAppID,
		PrivateKeyPEM: cfg.PrivateKey,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("init github client factory")
	}

	evaluator := evaluation.New(apiFactory, cfg.GitHubAppID, cfg.GitHubAppName, cfg.SubscriptionsEnabled)
	q := queue.New(rdb, evaluator, log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := q.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("start queue workers")
	}

	dispatcher := eventhandlers.New(cfg, rdb, q, apiFactory)
	srv := server.New(cfg, dispatcher, log)

	addr := envOr("PORT", "3000")
	if addr[0] != ':' {
		addr = ":" + addr
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info().Str("addr", addr).Msg("kodiak bot listening")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("http server")
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func errExit(msg string) {
	_, _ = os.Stderr.WriteString(msg + "\n")
	os.Exit(1)
}
