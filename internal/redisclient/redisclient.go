// Package redisclient provides the shared Redis connections for the bot,
// ported from bot/kodiak/redis_client.py.
package redisclient

import (
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/appconf"
)

func New() (*redis.Client, error) {
	opts, err := redis.ParseURL(appconf.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parsing REDIS_URL: %w", err)
	}
	opts.DialTimeout = appconf.RedisSocketConnectTimeout
	opts.ReadTimeout = appconf.RedisSocketTimeout
	opts.WriteTimeout = appconf.RedisSocketTimeout
	return redis.NewClient(opts), nil
}

var (
	botOnce sync.Once
	botConn *redis.Client
	botErr  error

	webAPIOnce sync.Once
	webAPIConn *redis.Client
	webAPIErr  error
)

// Bot returns the shared connection used for bot queues (redis_bot in Python).
func Bot() (*redis.Client, error) {
	botOnce.Do(func() {
		botConn, botErr = New()
	})
	return botConn, botErr
}

// WebAPI returns the shared connection used for web API data such as
// subscriptions and usage reporting (redis_web_api in Python).
func WebAPI() (*redis.Client, error) {
	webAPIOnce.Do(func() {
		webAPIConn, webAPIErr = New()
	})
	return webAPIConn, webAPIErr
}
