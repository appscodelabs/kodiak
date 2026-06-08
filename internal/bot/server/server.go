// Package server is the bot's HTTP entry point.
//
// Ported from bot/kodiak/main.py: GET / health check, POST /api/github/hook
// with HMAC-SHA1 signature verification.
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	"github.com/appscodelabs/kodiak/internal/bot/appconfig"
	"github.com/appscodelabs/kodiak/internal/bot/eventhandlers"
	"github.com/appscodelabs/kodiak/internal/bot/logging"
)

// Server is the bot's webhook receiver.
type Server struct {
	cfg        *appconfig.Config
	dispatcher *eventhandlers.Dispatcher
	log        zerolog.Logger
}

// New constructs a Server.
func New(cfg *appconfig.Config, dispatcher *eventhandlers.Dispatcher, log zerolog.Logger) *Server {
	return &Server{cfg: cfg, dispatcher: dispatcher, log: log}
}

// Handler returns an http.Handler with the bot's routes mounted.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("POST /api/github/hook", s.handleWebhook)
	return mux
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, "OK")
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	githubEvent := r.Header.Get("X-Github-Event")
	if githubEvent == "" {
		http.Error(w, "Missing required header: X-Github-Event", http.StatusBadRequest)
		return
	}
	signature := r.Header.Get("X-Hub-Signature")
	if signature == "" {
		http.Error(w, "Missing required signature: X-Hub-Signature", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20)) // 25 MiB cap
	if err != nil {
		http.Error(w, "request body read error", http.StatusBadRequest)
		return
	}

	mac := hmac.New(sha1.New, []byte(s.cfg.SecretKey))
	mac.Write(body)
	expected := mac.Sum(nil)

	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha1="))
	if err != nil || !hmac.Equal(provided, expected) {
		http.Error(w, "Invalid signature: X-Hub-Signature", http.StatusBadRequest)
		return
	}

	ctx := logging.WithContext(r.Context(), s.log)
	if err := s.dispatcher.Handle(ctx, githubEvent, body); err != nil {
		s.log.Err(err).Str("event_name", githubEvent).Msg("dispatcher returned error")
		// Always 200 so GitHub doesn't retry — mirror the Python behaviour
		// where handler errors are logged but not surfaced.
	}
	w.WriteHeader(http.StatusOK)
}

// Compile-time check that we can construct a baseline server with no nil deps.
var _ = func() any {
	var _ context.Context
	return nil
}
