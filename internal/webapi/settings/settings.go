// Package settings loads environment configuration for the web API,
// mirroring web_api/web_api/settings.py.
package settings

import (
	"fmt"
	"net/url"
	"os"

	"github.com/joho/godotenv"
)

// devSecretKey matches the Django DEBUG fallback secret key.
const devSecretKey = "gjzyca8n8$gg^d9u-ts6x5_+=)x*h!=ae&y$%m*ecsjly&*8j3"

type Config struct {
	Debug     bool
	SecretKey string

	DatabaseURL string
	RedisURL    string

	KodiakAPIGithubClientID     string
	KodiakAPIGithubClientSecret string
	KodiakWebAppURL             string
	KodiakWebAuthedLandingPath  string

	StripeSecretKey         string
	StripePlanID            string
	StripeAnnualPlanID      string
	StripeWebhookSecret     string
	StripePublishableAPIKey string
}

func requireEnv(name string, errs *[]error) string {
	v, ok := os.LookupEnv(name)
	if !ok {
		*errs = append(*errs, fmt.Errorf("missing required environment variable %s", name))
	}
	return v
}

func Load() (*Config, error) {
	// settings.py calls load_dotenv().
	_ = godotenv.Load()

	var errs []error
	cfg := &Config{
		Debug:                       os.Getenv("DEBUG") != "",
		DatabaseURL:                 requireEnv("DATABASE_URL", &errs),
		RedisURL:                    requireEnv("REDIS_URL", &errs),
		KodiakAPIGithubClientID:     requireEnv("KODIAK_API_GITHUB_CLIENT_ID", &errs),
		KodiakAPIGithubClientSecret: requireEnv("KODIAK_API_GITHUB_CLIENT_SECRET", &errs),
		KodiakWebAppURL:             requireEnv("KODIAK_WEB_APP_URL", &errs),
		StripeSecretKey:             requireEnv("STRIPE_SECRET_KEY", &errs),
		StripePlanID:                requireEnv("STRIPE_PLAN_ID", &errs),
		StripeAnnualPlanID:          requireEnv("STRIPE_ANNUAL_PLAN_ID", &errs),
		StripeWebhookSecret:         requireEnv("STRIPE_WEBHOOK_SECRET", &errs),
		StripePublishableAPIKey:     requireEnv("STRIPE_PUBLISHABLE_API_KEY", &errs),
	}
	if cfg.Debug {
		cfg.SecretKey = devSecretKey
		if v := os.Getenv("SECRET_KEY"); v != "" {
			cfg.SecretKey = v
		}
	} else {
		cfg.SecretKey = requireEnv("SECRET_KEY", &errs)
	}
	if len(errs) > 0 {
		return nil, errs[0]
	}

	u, err := url.Parse(cfg.KodiakWebAppURL)
	if err != nil {
		return nil, fmt.Errorf("invalid KODIAK_WEB_APP_URL: %w", err)
	}
	u.Path = "/oauth"
	u.RawQuery = ""
	u.Fragment = ""
	cfg.KodiakWebAuthedLandingPath = u.String()
	return cfg, nil
}
