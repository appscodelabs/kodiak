// Command webapi serves the Kodiak dashboard API.
//
// Ported from web_api/web_api/wsgi.py + manage.py runserver.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	botlogging "github.com/appscodelabs/kodiak/internal/bot/logging"
	"github.com/appscodelabs/kodiak/internal/webapi/config"
	"github.com/appscodelabs/kodiak/internal/webapi/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = os.Stderr.WriteString("load config: " + err.Error() + "\n")
		os.Exit(1)
	}
	log := botlogging.New(botlogging.Level("INFO"), nil)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect to postgres")
	}
	defer pool.Close()

	srv := server.New(cfg, pool, log)
	addr := cfg.Port
	if addr[0] != ':' {
		addr = ":" + addr
	}
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info().Str("addr", addr).Msg("kodiak web_api listening")
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("http server")
	}
}
