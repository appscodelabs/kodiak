// Package appconf loads bot configuration from environment variables (with
// optional .env file support), ported from bot/kodiak/app_config.py.
package appconf

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/appscodelabs/kodiak/internal/logging"
)

var (
	Port                      int
	RedisURL                  string
	SecretKey                 string
	GithubAppID               string
	LoggingLevel              slog.Level
	GithubAppName             string
	UsageReporting            bool
	UsageReportingEvents      map[string]bool
	UsageReportingQueueLength int
	IngestQueueLength         int
	RedisBlockingPopTimeout   time.Duration
	RedisSocketTimeout        time.Duration
	RedisSocketConnectTimeout time.Duration
	SubscriptionsEnabled      bool
	GithubV3APIRoot           string
	GithubV4APIURL            string
	GithubAPIHeaderName       string
	GithubAPIHeaderValue      string
	PrivateKey                string
)

func get(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %q", key, v)
	}
	return n, nil
}

func getBool(key string, fallback bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		return false, fmt.Errorf("invalid boolean for %s: %q", key, v)
	}
	return b, nil
}

// Load reads configuration from a .env file (if present) and the process
// environment. It must be called before the configuration variables above are
// used.
func Load() error {
	// Like starlette.config.Config(".env"), real environment variables take
	// precedence over the .env file.
	_ = godotenv.Load(".env")

	var err error
	if Port, err = getInt("PORT", 8000); err != nil {
		return err
	}

	RedisURL = get("REDIS_URL", "")
	if RedisURL == "" {
		RedisURL = os.Getenv("REDISCLOUD_URL")
	}
	if RedisURL == "" {
		return errors.New("REDIS_URL or REDISCLOUD_URL must be set")
	}

	SecretKey = os.Getenv("SECRET_KEY")
	if SecretKey == "" {
		return errors.New("SECRET_KEY must be set")
	}
	GithubAppID = os.Getenv("GITHUB_APP_ID")
	if GithubAppID == "" {
		return errors.New("GITHUB_APP_ID must be set")
	}
	GithubAppName = os.Getenv("GITHUB_APP_NAME")
	if GithubAppName == "" {
		return errors.New("GITHUB_APP_NAME must be set")
	}

	if LoggingLevel, err = logging.GetLoggingLevel(get("LOGGING_LEVEL", "INFO")); err != nil {
		return err
	}

	if UsageReporting, err = getBool("USAGE_REPORTING", false); err != nil {
		return err
	}
	UsageReportingEvents = map[string]bool{}
	for name := range strings.SplitSeq(get("USAGE_REPORTING_EVENTS", "pull_request,pull_request_review,pull_request_comment"), ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			UsageReportingEvents[name] = true
		}
	}
	if UsageReportingQueueLength, err = getInt("USAGE_REPORTING_QUEUE_LENGTH", 10000); err != nil {
		return err
	}
	if IngestQueueLength, err = getInt("INGEST_QUEUE_LENGTH", 1000); err != nil {
		return err
	}

	redisBlockingPopTimeoutSec, err := getInt("REDIS_BLOCKING_POP_TIMEOUT_SEC", 10)
	if err != nil {
		return err
	}
	RedisBlockingPopTimeout = time.Duration(redisBlockingPopTimeoutSec) * time.Second

	// if we don't get a reply from Redis within a short period, we have an
	// error because we always expect short response times from redis.
	redisSocketTimeoutSec, err := getInt("REDIS_SOCKET_TIMEOUT_SEC", 90)
	if err != nil {
		return err
	}
	RedisSocketTimeout = time.Duration(redisSocketTimeoutSec) * time.Second

	// if we can't open a TCP connection quickly, we should raise a timeout.
	redisSocketConnectTimeoutSec, err := getInt("REDIS_SOCKET_CONNECT_TIMEOUT_SEC", 30)
	if err != nil {
		return err
	}
	RedisSocketConnectTimeout = time.Duration(redisSocketConnectTimeoutSec) * time.Second

	if SubscriptionsEnabled, err = getBool("SUBSCRIPTIONS_ENABLED", false); err != nil {
		return err
	}

	// For GitHub Enterprise, the v3 API root has the form
	// http(s)://[hostname]/api/v3 and the v4 API http(s)://[hostname]/api/graphql.
	GithubV3APIRoot = get("GITHUB_V3_API_ROOT", "https://api.github.com")
	GithubV4APIURL = get("GITHUB_V4_API_URL", "https://api.github.com/graphql")

	// An extra header to send with GitHub API requests.
	GithubAPIHeaderName = get("GITHUB_API_HEADER_NAME", "")
	GithubAPIHeaderValue = get("GITHUB_API_HEADER_VALUE", "")

	if pk := os.Getenv("GITHUB_PRIVATE_KEY"); pk != "" {
		PrivateKey = pk
	} else if path := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading GITHUB_PRIVATE_KEY_PATH: %w", err)
		}
		PrivateKey = string(data)
	} else if b64 := os.Getenv("GITHUB_PRIVATE_KEY_BASE64"); b64 != "" {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return fmt.Errorf("decoding GITHUB_PRIVATE_KEY_BASE64: %w", err)
		}
		PrivateKey = string(data)
	} else {
		return errors.New("either GITHUB_PRIVATE_KEY_PATH, GITHUB_PRIVATE_KEY, or GITHUB_PRIVATE_KEY_BASE64 must be set")
	}

	return nil
}

// V3URL returns the full URL for a GitHub REST (v3) API path.
func V3URL(path string) string {
	return GithubV3APIRoot + path
}
