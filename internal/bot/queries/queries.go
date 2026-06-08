// Package queries is the GitHub GraphQL/REST client used by the bot.
//
// This is the Go skeleton for bot/kodiak/queries.py (the Python source is
// ~1,200 lines and is the densest module in the bot). The interface, types,
// and stub methods here mirror the surface area used by event_handlers,
// pull_request, and evaluation so the rest of the bot can compile and be
// tested while the implementations are filled in incrementally.
package queries

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// CheckRunName is the name Kodiak uses for its own GitHub check run; events
// from a check_run with this name must be ignored to avoid an infinite loop.
const CheckRunName = "kodiakhq: status"

// OpenPullRequestsFilter narrows GetOpenPullRequests.
type OpenPullRequestsFilter struct {
	Head string // optional `head` filter, e.g. "owner:branch"
	Base string // optional `base` branch filter
}

// OpenPullRequest is the projection returned by GetOpenPullRequests.
type OpenPullRequest struct {
	Number int
}

// ClientFactory hands out installation-scoped clients. Implementations cache
// installation auth tokens.
type ClientFactory interface {
	Client(ctx context.Context, owner, repo, installationID string) (Client, error)
}

// Client is the per-installation GitHub client used by the bot.
type Client interface {
	GetOpenPullRequests(ctx context.Context, f OpenPullRequestsFilter) ([]OpenPullRequest, error)
	GetEventInfo(ctx context.Context, number int) (*EventInfo, error)
	Merge(ctx context.Context, number int, in MergeInput) error
	UpdateBranch(ctx context.Context, number int) error
	CreateComment(ctx context.Context, number int, body string) error
	AddLabel(ctx context.Context, number int, label string) error
	DeleteLabel(ctx context.Context, number int, label string) error
	CreateOrUpdateCheckRun(ctx context.Context, in CheckRunInput) error
	Close() error
}

// Subscription mirrors Kodiak's billing subscription record (loaded from Redis,
// not GraphQL — kept here so Client can return it via GetEventInfo).
type Subscription struct {
	AccountID string
	Blocker   *SubscriptionBlocker
}

// MergeInput is the input for the merge mutation.
type MergeInput struct {
	CommitTitle  string
	CommitBody   string
	MergeMethod  string // MERGE, SQUASH, REBASE
	ExpectedHeadOID string
}

// CheckRunInput is the input for creating or updating Kodiak's status check.
type CheckRunInput struct {
	HeadSHA    string
	Status     string // queued, in_progress, completed
	Conclusion string // success, failure, neutral, ...
	Title      string
	Summary    string
}

// FactoryConfig is the wiring for a real ClientFactory.
type FactoryConfig struct {
	AppID         string
	PrivateKeyPEM string
	BaseURL       string // defaults to https://api.github.com
	HTTPClient    *http.Client
}

// factory implements ClientFactory.
type factory struct {
	cfg        FactoryConfig
	httpClient *http.Client
	signer     *rsa.PrivateKey

	mu     sync.Mutex
	tokens map[string]cachedToken
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

// NewFactory builds a real ClientFactory.
func NewFactory(cfg FactoryConfig) (ClientFactory, error) {
	if cfg.AppID == "" {
		return nil, errors.New("queries: AppID is required")
	}
	if cfg.PrivateKeyPEM == "" {
		return nil, errors.New("queries: PrivateKeyPEM is required")
	}
	signer, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.github.com"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &factory{
		cfg:        cfg,
		httpClient: httpClient,
		signer:     signer,
		tokens:     make(map[string]cachedToken),
	}, nil
}

func (f *factory) Client(ctx context.Context, owner, repo, installationID string) (Client, error) {
	token, err := f.installationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}
	return &client{
		httpClient: f.httpClient,
		baseURL:    f.cfg.BaseURL,
		owner:      owner,
		repo:       repo,
		install:    installationID,
		token:      token,
	}, nil
}

func (f *factory) installationToken(ctx context.Context, installationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if t, ok := f.tokens[installationID]; ok && t.expiresAt.After(now.Add(2*time.Minute)) {
		return t.value, nil
	}
	jwtToken, err := f.signJWT(now)
	if err != nil {
		return "", err
	}
	value, expires, err := exchangeInstallationToken(ctx, f.httpClient, f.cfg.BaseURL, installationID, jwtToken)
	if err != nil {
		return "", err
	}
	f.tokens[installationID] = cachedToken{value: value, expiresAt: expires}
	return value, nil
}

func (f *factory) signJWT(now time.Time) (string, error) {
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
		Issuer:    f.cfg.AppID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(f.signer)
}

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rsaKey, nil
}

// exchangeInstallationToken is split out so unit tests can stub it.
var exchangeInstallationToken = func(_ context.Context, _ *http.Client, _, installationID, _ string) (string, time.Time, error) {
	// TODO: POST /app/installations/{installation_id}/access_tokens with Bearer JWT.
	return "", time.Time{}, fmt.Errorf("installation token exchange not yet implemented for installation=%s", installationID)
}

// client is the per-installation HTTP-backed implementation.
//
// The methods are intentionally unimplemented for now — they exist so the
// rest of the bot can be wired against the interface and so each query can
// be ported one at a time without churn.
type client struct {
	httpClient *http.Client
	baseURL    string
	owner      string
	repo       string
	install    string
	token      string
}

func (c *client) Close() error { return nil }

func (c *client) GetOpenPullRequests(ctx context.Context, f OpenPullRequestsFilter) ([]OpenPullRequest, error) {
	return c.getOpenPullRequestsImpl(ctx, f)
}

// GetEventInfo runs the GetEventInfo GraphQL query and returns the rich
// projection used by the merge evaluator.
func (c *client) GetEventInfo(ctx context.Context, number int) (*EventInfo, error) {
	return c.getEventInfoImpl(ctx, number)
}

func (c *client) Merge(ctx context.Context, number int, in MergeInput) error {
	return c.mergeImpl(ctx, number, in)
}

func (c *client) UpdateBranch(ctx context.Context, number int) error {
	return c.updateBranchImpl(ctx, number)
}

func (c *client) CreateComment(ctx context.Context, number int, body string) error {
	return c.createCommentImpl(ctx, number, body)
}

func (c *client) AddLabel(ctx context.Context, number int, label string) error {
	return c.addLabelImpl(ctx, number, label)
}

func (c *client) DeleteLabel(ctx context.Context, number int, label string) error {
	return c.deleteLabelImpl(ctx, number, label)
}

func (c *client) CreateOrUpdateCheckRun(ctx context.Context, in CheckRunInput) error {
	return c.createCheckRunImpl(ctx, in)
}

func errNotImplemented(name string) error {
	return fmt.Errorf("queries.%s: not yet ported from bot/kodiak/queries.py", name)
}
