// Command kodiak is the Go port of the kodiak bot entrypoints and CLI
// (bot/kodiak/entrypoints/ and bot/kodiak/cli.py).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/logging"
	"github.com/appscodelabs/kodiak/internal/queries"
	"github.com/appscodelabs/kodiak/internal/refresh"
)

const usageText = `usage: kodiak <command> [arguments]

commands:
  ingest                  accept webhooks from GitHub and add them to the Redis queues
  worker                  process webhook events from the Redis queues
  refresh-pull-requests   listen for messages and trigger pull request refreshes
  create-jwt              create a JWT for testing GitHub API endpoints
  token-for-install       fetch the OAuth token for a given install id
  list-installs           list all installs for the Kodiak GitHub App
  validate-config         print out kodiak's view of a .kodiak.toml
`

func main() {
	if err := run(); err != nil {
		slog.Error("kodiak failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	if err := appconf.Load(); err != nil {
		return err
	}
	logging.Configure(appconf.LoggingLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "ingest":
		return runIngest(ctx)
	case "worker":
		return runWorker(ctx)
	case "refresh-pull-requests":
		return refresh.Run(ctx)
	case "create-jwt":
		return runCreateJWT()
	case "token-for-install":
		return runTokenForInstall(ctx, args)
	case "list-installs":
		return runListInstalls(ctx)
	case "validate-config":
		return runValidateConfig(args)
	case "gen-conf-json-schema":
		// NOTE(port): the Python command emits pydantic's auto-generated JSON
		// schema for the V1 config model. There is no equivalent generator for
		// the Go structs, and hand-encoding the schema is not worth the
		// maintenance burden.
		return errors.New("gen-conf-json-schema is not implemented in the Go port")
	default:
		fmt.Fprint(os.Stderr, usageText)
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

func runCreateJWT() error {
	token, err := queries.GenerateJWT(appconf.PrivateKey, appconf.GithubAppID)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}

// runTokenForInstall outputs the OAuth token for a given installation id.
// This is useful to help debug installation problems.
func runTokenForInstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("token-for-install", flag.ExitOnError)
	installID := fs.String("install-id", "", "GitHub App installation id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *installID == "" && fs.NArg() > 0 {
		// also accept the install id as a positional argument like the Python
		// CLI.
		*installID = fs.Arg(0)
	}
	if *installID == "" {
		return errors.New("--install-id is required")
	}
	token, err := queries.GetTokenForInstall(ctx, http.DefaultClient, *installID)
	if err != nil {
		return err
	}
	fmt.Println(token)
	return nil
}

var nextLinkRegexp = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func runListInstalls(ctx context.Context) error {
	appToken, err := queries.GenerateJWT(appconf.PrivateKey, appconf.GithubAppID)
	if err != nil {
		return err
	}
	type install struct {
		ID      int64 `json:"id"`
		Account struct {
			HTMLURL string `json:"html_url"`
		} `json:"account"`
	}
	url := appconf.V3URL("/app/installations")
	for url != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github.machine-man-preview+json")
		req.Header.Set("Authorization", "Bearer "+appToken)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return err
		}
		if res.StatusCode >= 400 {
			return fmt.Errorf("list installs failed with HTTP status %d", res.StatusCode)
		}
		var page []install
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, r := range page {
			fmt.Printf("install:%d for %s\n", r.ID, r.Account.HTMLURL)
		}
		url = ""
		if match := nextLinkRegexp.FindStringSubmatch(res.Header.Get("Link")); match != nil {
			url = match[1]
		}
	}
	return nil
}

// runValidateConfig parses and outputs the json representation of a Kodiak
// config.
func runValidateConfig(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: kodiak validate-config <config-path>")
	}
	cfgText, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	cfg, err := config.ParseToml(string(cfgText))
	if err != nil {
		return err
	}
	// NOTE(port): the Go config structs have no JSON tags, so the output keys
	// are the Go field names instead of the Python snake_case names.
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
