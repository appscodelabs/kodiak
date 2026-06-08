// Package config loads runtime configuration for the web_api service.
//
// Ported from web_api/web_api/settings.py — only the settings the application
// itself uses at runtime (DATABASE_URL, REDIS_URL, secrets, Stripe keys, host
// allowlist).
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config is the runtime configuration loaded from the environment.
type Config struct {
	DatabaseURL          string
	RedisURL             string
	SecretKey            string
	AllowedHosts         []string
	GitHubClientID       string
	GitHubClientSecret   string
	StripeSecretKey      string
	StripeWebhookSecret  string
	StripePublishableKey string
	FrontendURL          string
	Port                 string
}

// Load reads configuration from the process environment.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		RedisURL:             os.Getenv("REDIS_URL"),
		SecretKey:            os.Getenv("SECRET_KEY"),
		GitHubClientID:       os.Getenv("KODIAK_API_GITHUB_CLIENT_ID"),
		GitHubClientSecret:   os.Getenv("KODIAK_API_GITHUB_CLIENT_SECRET"),
		StripeSecretKey:      os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripePublishableKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
		FrontendURL:          envOr("KODIAK_WEB_AUTHED_LANDING_PATH", "http://app.localhost.kodiakhq.com:3000"),
		Port:                 envOr("PORT", "8000"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("SECRET_KEY is required")
	}

	allowed := os.Getenv("ALLOWED_HOSTS")
	if allowed != "" {
		for h := range strings.SplitSeq(allowed, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				cfg.AllowedHosts = append(cfg.AllowedHosts, h)
			}
		}
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
