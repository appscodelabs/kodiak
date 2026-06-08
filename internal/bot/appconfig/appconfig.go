// Package appconfig loads runtime configuration from environment variables.
//
// Ported from bot/kodiak/app_config.py.
package appconfig

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds the bot's runtime settings.
type Config struct {
	RedisURL                  *url.URL
	RedisPoolSize             int
	SecretKey                 string
	GitHubAppID               string
	GitHubAppName             string
	PrivateKey                string
	LoggingLevel              string
	UsageReporting            bool
	UsageReportingEvents      map[string]struct{}
	UsageReportingPoolSize    int
	UsageReportingQueueLength int
	SubscriptionsEnabled      bool
}

// Load reads the configuration from the process environment.
func Load() (*Config, error) {
	cfg := &Config{
		RedisPoolSize:             envInt("REDIS_POOL_SIZE", 20),
		SecretKey:                 os.Getenv("SECRET_KEY"),
		GitHubAppID:               os.Getenv("GITHUB_APP_ID"),
		GitHubAppName:             os.Getenv("GITHUB_APP_NAME"),
		LoggingLevel:              envOr("LOGGING_LEVEL", "INFO"),
		UsageReporting:            envBool("USAGE_REPORTING", false),
		UsageReportingPoolSize:    envInt("USAGE_REPORTING_POOL_SIZE", 50),
		UsageReportingQueueLength: envInt("USAGE_REPORTING_QUEUE_LENGTH", 10_000),
		SubscriptionsEnabled:      envBool("SUBSCRIPTIONS_ENABLED", false),
	}

	raw := envOr("REDIS_URL", os.Getenv("REDISCLOUD_URL"))
	if raw == "" {
		return nil, fmt.Errorf("REDIS_URL or REDISCLOUD_URL must be set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	cfg.RedisURL = u

	events := envOr("USAGE_REPORTING_EVENTS", "pull_request,pull_request_review,pull_request_comment")
	cfg.UsageReportingEvents = map[string]struct{}{}
	for name := range strings.SplitSeq(events, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			cfg.UsageReportingEvents[name] = struct{}{}
		}
	}

	pk, err := loadPrivateKey()
	if err != nil {
		return nil, err
	}
	cfg.PrivateKey = pk

	return cfg, nil
}

func loadPrivateKey() (string, error) {
	if v := os.Getenv("GITHUB_PRIVATE_KEY"); v != "" {
		return v, nil
	}
	if path := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read GITHUB_PRIVATE_KEY_PATH: %w", err)
		}
		return string(data), nil
	}
	if encoded := os.Getenv("GITHUB_PRIVATE_KEY_BASE64"); encoded != "" {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", fmt.Errorf("decode GITHUB_PRIVATE_KEY_BASE64: %w", err)
		}
		return string(decoded), nil
	}
	return "", fmt.Errorf("one of GITHUB_PRIVATE_KEY, GITHUB_PRIVATE_KEY_PATH, or GITHUB_PRIVATE_KEY_BASE64 must be set")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
