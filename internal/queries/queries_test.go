// Ported from bot/kodiak/test_queries.py.
package queries

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/appscodelabs/kodiak/internal/appconf"
	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/throttle"
)

const githubInstallationID = "8912353"

var testRedis *miniredis.Miniredis

func TestMain(m *testing.M) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	testRedis = mr
	// NOTE(port): the Python tests use a real redis (requires_redis); we use
	// miniredis through the regular redisclient connection path.
	appconf.RedisURL = "redis://" + mr.Addr()
	code := m.Run()
	mr.Close()
	os.Exit(code)
}

func ptr[T any](v T) *T { return &v }

func loadEventFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "bot", "kodiak", "test", "fixtures", "api", "get_event", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return data
}

// newTestClient is the api_client fixture. Overriding the throttler stands in
// for the Python FakeThottler patch.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	c := NewClient("foo", "foo", githubInstallationID)
	c.throttler = throttle.New(1e9)
	return c
}

// seedInstallationToken is the mock_get_token_for_install fixture; it avoids
// HTTP token fetches by pre-populating the installation token cache.
func seedInstallationToken(t *testing.T) {
	t.Helper()
	installationCacheMu.Lock()
	installationCache[githubInstallationID] = &TokenResponse{
		Token:     "some-json-web-token",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	installationCacheMu.Unlock()
	t.Cleanup(func() {
		installationCacheMu.Lock()
		delete(installationCache, githubInstallationID)
		installationCacheMu.Unlock()
	})
}

func resetAPIFeaturesCache(t *testing.T) {
	t.Helper()
	apiFeaturesCacheMu.Lock()
	apiFeaturesCache = nil
	apiFeaturesCacheMu.Unlock()
}

// setGraphQLResponse points the GraphQL API URL at a test server returning
// body for every query, replacing the Python send_query mocks.
func setGraphQLResponse(t *testing.T, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	prev := appconf.GithubV4APIURL
	appconf.GithubV4APIURL = srv.URL
	t.Cleanup(func() {
		appconf.GithubV4APIURL = prev
		srv.Close()
	})
}

func enableSubscriptions(t *testing.T) {
	t.Helper()
	prev := appconf.SubscriptionsEnabled
	appconf.SubscriptionsEnabled = true
	t.Cleanup(func() { appconf.SubscriptionsEnabled = prev })
}

// setupRedisSubscription is the setup_redis fixture.
func setupRedisSubscription(t *testing.T) {
	t.Helper()
	setRedisSubscription(t, map[string]string{
		"account_id":           "D1606A79-A1A1-4550-BA7B-C9ED0D792B1E",
		"subscription_blocker": "",
	})
}

func setRedisSubscription(t *testing.T, fields map[string]string) {
	t.Helper()
	key := "kodiak:subscription:" + githubInstallationID
	for k, v := range fields {
		testRedis.HSet(key, k, v)
	}
	t.Cleanup(func() { testRedis.Del(key) })
}

// logCapture is the caplog equivalent: it records slog output so tests can
// assert on warning/error events.
type capturedLog struct {
	Level slog.Level
	Msg   string
}

type logCapture struct {
	mu      sync.Mutex
	records []capturedLog
}

func (l *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (l *logCapture) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, capturedLog{Level: r.Level, Msg: r.Message})
	return nil
}

func (l *logCapture) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *logCapture) WithGroup(string) slog.Handler      { return l }

func (l *logCapture) warnings() []capturedLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []capturedLog
	for _, r := range l.records {
		if r.Level >= slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

func installLogCapture(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return capture
}

// expectedEventErrors corresponds to EXPECTED_ERRORS in the Python tests.
var expectedEventErrors = []capturedLog{
	{Level: slog.LevelWarn, Msg: "problem parsing api features"},
	{Level: slog.LevelError, Msg: "problem parsing commit authors"},
}

func assertLogs(t *testing.T, capture *logCapture, expected []capturedLog) {
	t.Helper()
	if diff := cmp.Diff(expected, capture.warnings(), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("log mismatch (-expected +actual):\n%s", diff)
	}
}

// We should return None when there is an error.
func TestGetConfigForRefError(t *testing.T) {
	setGraphQLResponse(t, []byte(`{"data": null, "errors": [{"test": 123}]}`))
	seedInstallationToken(t)
	client := newTestClient(t)

	res, err := client.GetConfigForRef(context.Background(), "main", nil)
	// NOTE(port): the Python client returns None; the Go port returns an
	// error alongside the nil result.
	if err == nil {
		t.Fatal("expected error")
	}
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
}

// We should be able to parse from .github/.kodiak.toml
func TestGetConfigForRefDotGithub(t *testing.T) {
	setGraphQLResponse(t, []byte(`{
  "data": {
    "repository": {
      "rootConfigFile": null,
      "githubConfigFile": {
        "text": "# .github/.kodiak.toml\nversion = 1\nmerge.method = 'rebase'"
      }
    }
  }
}`))
	seedInstallationToken(t)
	client := newTestClient(t)

	res, err := client.GetConfigForRef(context.Background(), "main", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if res.Parsed == nil {
		t.Fatalf("expected parsed config, got parse error: %v", res.ParseError)
	}
	if res.Parsed.Merge.Method == nil || *res.Parsed.Merge.Method != config.MergeMethodRebase {
		t.Fatalf("expected merge.method rebase, got %v", res.Parsed.Merge.Method)
	}
}

// getBlockEvent is the get_block_event helper from the Python tests.
func getBlockEvent() *EventInfoResponse {
	cfg := config.DefaultV1()
	cfg.Merge.Method = ptr(config.MergeMethodSquash)
	pr := PullRequest{
		ID:                "e14ff7599399478fb9dbc2dacb87da72",
		Number:            100,
		Author:            &PullRequestAuthor{Login: "arnold", DatabaseID: 49118, Type: "Bot"},
		MergeStateStatus:  MergeStateStatusBehind,
		State:             PullRequestStateOpen,
		IsDraft:           false,
		Mergeable:         MergeableStateMergeable,
		IsCrossRepository: false,
		Labels:            []string{"automerge"},
		LatestSha:         "8d728d017cac4f5ba37533debe65730abe65730a",
		BaseRefName:       "master",
		HeadRefName:       "df825f90-9825-424c-a97e-733522027e4c",
		Title:             "Update README.md",
		Body:              "",
		BodyText:          "",
		BodyHTML:          "",
		URL:               "https://github.com/delos-corp/hive-mind/pull/324",
		ReviewThreads: ReviewThreadConnection{
			Nodes: []ReviewThread{{IsCollapsed: true}, {IsCollapsed: false}},
		},
	}
	repoInfo := RepoInfo{
		MergeCommitAllowed:  false,
		RebaseMergeAllowed:  false,
		SquashMergeAllowed:  true,
		DeleteBranchOnMerge: true,
		IsPrivate:           true,
	}
	branchProtection := &BranchProtectionRule{
		RequiresStatusChecks: true,
		RequiredStatusCheckContexts: []string{
			"ci/circleci: backend_lint",
			"ci/circleci: backend_test",
			"ci/circleci: frontend_lint",
			"ci/circleci: frontend_test",
			"WIP (beta)",
		},
		RequiresStrictStatusChecks:     true,
		RequiresCommitSignatures:       false,
		RequiresConversationResolution: ptr(false),
		RestrictsPushes:                true,
		PushAllowances: NodeListPushAllowance{
			Nodes: []PushAllowance{
				{Actor: PushAllowanceActor{DatabaseID: nil}},
				{Actor: PushAllowanceActor{DatabaseID: ptr(int64(53453))}},
			},
		},
	}
	return &EventInfoResponse{
		Config:               &cfg,
		ConfigStr:            "version = 1\n[merge]\nmethod = \"squash\"\n",
		ConfigFileExpression: "master:.kodiak.toml",
		HeadExists:           true,
		PullRequest:          pr,
		Repository:           repoInfo,
		Subscription: &Subscription{
			AccountID:           "D1606A79-A1A1-4550-BA7B-C9ED0D792B1E",
			SubscriptionBlocker: nil,
		},
		BranchProtection: branchProtection,
		RulesetRules:     []RulesetRule{},
		ReviewRequests: []PRReviewRequest{
			{Name: "ghost"},
			{Name: "ghost-team"},
			{Name: "ghost-bot"},
			{Name: "ghost-mannequin"},
		},
		BotReviews: []PRReview{
			{
				CreatedAt: time.Date(2019, 5, 24, 10, 21, 32, 0, time.UTC),
				State:     PRReviewStateApproved,
				Author:    PRReviewAuthor{Login: "kodiakhq"},
			},
		},
		StatusContexts: []StatusContext{
			{Context: "ci/circleci: backend_lint", State: StatusStateSuccess},
			{Context: "ci/circleci: backend_test", State: StatusStateSuccess},
			{Context: "ci/circleci: frontend_lint", State: StatusStateSuccess},
			{Context: "ci/circleci: frontend_test", State: StatusStateSuccess},
		},
		CheckRuns: []CheckRun{
			{Name: "WIP (beta)", Conclusion: ptr(CheckConclusionStateSuccess)},
		},
		ValidMergeMethods: []config.MergeMethod{config.MergeMethodSquash},
	}
}

func runGetEventInfo(t *testing.T, fixture string) (*EventInfoResponse, error, *logCapture) {
	t.Helper()
	capture := installLogCapture(t)
	setGraphQLResponse(t, loadEventFixture(t, fixture))
	seedInstallationToken(t)
	resetAPIFeaturesCache(t)
	enableSubscriptions(t)
	setupRedisSubscription(t)
	client := newTestClient(t)
	res, err := client.GetEventInfo(context.Background(), 100)
	return res, err, capture
}

func TestGetEventInfoBlocked(t *testing.T) {
	res, err, capture := runGetEventInfo(t, "behind.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if diff := cmp.Diff(getBlockEvent(), res, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("event mismatch (-expected +actual):\n%s", diff)
	}
	assertLogs(t, capture, expectedEventErrors)
}

func TestGetEventInfoBlockedGraphql(t *testing.T) {
	blockEvent := getBlockEvent()
	repositoryRuleset := &RepositoryRuleset{
		BypassActors: &RepositoryRulesetBypassActorConnection{
			Nodes: []*RepositoryRulesetBypassActor{
				{Actor: &BypassActor{DatabaseID: ptr(int64(29196))}},
			},
		},
	}
	blockEvent.RulesetRules = []RulesetRule{
		{Type: "UPDATE", RepositoryRuleset: repositoryRuleset},
		{
			Type: "REQUIRED_STATUS_CHECKS",
			Parameters: &RequiredStatusChecksParameters{
				RequiredStatusChecks:             []StatusCheckConfiguration{{Context: "test"}},
				StrictRequiredStatusChecksPolicy: true,
			},
			RepositoryRuleset: repositoryRuleset,
		},
		{
			Type: "PULL_REQUEST",
			Parameters: &PullRequestParameters{
				RequiredReviewThreadResolution: false,
			},
			RepositoryRuleset: repositoryRuleset,
		},
		{Type: "NON_FAST_FORWARD", RepositoryRuleset: repositoryRuleset},
		{Type: "DELETION", RepositoryRuleset: repositoryRuleset},
	}

	res, err, capture := runGetEventInfo(t, "behind_graphql.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if diff := cmp.Diff(blockEvent, res, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("event mismatch (-expected +actual):\n%s", diff)
	}
	assertLogs(t, capture, expectedEventErrors)
}

// When a PR account author is deleted, the PR's author becomes null, so we
// need to handle that.
func TestGetEventInfoNoAuthor(t *testing.T) {
	blockEvent := getBlockEvent()
	blockEvent.PullRequest.Author = nil

	res, err, capture := runGetEventInfo(t, "no_author.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(blockEvent, res, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("event mismatch (-expected +actual):\n%s", diff)
	}
	assertLogs(t, capture, expectedEventErrors)
}

// When a PR doesn't have a changeset aka a diff, the latest_sha will be None.
func TestGetEventInfoNoLatestSha(t *testing.T) {
	res, err, capture := runGetEventInfo(t, "no_latest_sha.json")
	if err == nil {
		t.Fatal("expected error for missing sha")
	}
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	assertLogs(t, capture, []capturedLog{
		{Level: slog.LevelWarn, Msg: "problem parsing api features"},
	})
}

func getSubscription(t *testing.T) (*Subscription, error) {
	t.Helper()
	client := newTestClient(t)
	return client.GetSubscription(context.Background())
}

func assertSubscription(t *testing.T, expected *Subscription, actual *Subscription, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff := cmp.Diff(expected, actual, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("subscription mismatch (-expected +actual):\n%s", diff)
	}
}

// We set subscription_blocker to empty string from the web_api. This should be
// consider equivalent to a missing subscription blocker.
func TestGetSubscriptionMissingBlocker(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "",
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID: "DF5C23EB-585B-4031-B082-7FF951B4DE15",
	}, res, err)
}

// Check with empty string for data
func TestGetSubscriptionMissingBlockerAndData(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "",
		"data":                 "",
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID: "DF5C23EB-585B-4031-B082-7FF951B4DE15",
	}, res, err)
}

// If a user is new to Kodiak we will not have set subscription information in
// Redis. We should handle this case by returning an empty subscription.
func TestGetSubscriptionMissingBlockerFully(t *testing.T) {
	res, err := getSubscription(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil subscription, got %+v", res)
	}
}

// When a user exceeds their seat we will specify allowed users for those users
// that occupy a seat.
func TestGetSubscriptionSeatsExceeded(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "seats_exceeded",
		"data":                 `{"kind":"seats_exceeded", "allowed_user_ids": [5234234]}`,
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID:           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		SubscriptionBlocker: &SeatsExceeded{AllowedUserIDs: []int64{5234234}},
	}, res, err)
}

// When an account has 0 seats we will not have any allowed_user_ids.
func TestGetSubscriptionSeatsExceededNoSeats(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "seats_exceeded",
		"data":                 `{"kind":"seats_exceeded", "allowed_user_ids": []}`,
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID:           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		SubscriptionBlocker: &SeatsExceeded{AllowedUserIDs: []int64{}},
	}, res, err)
}

// For backwards compatibility we cannot guarantee that "seats_exceeded" will
// have the data parameter.
func TestGetSubscriptionSeatsExceededMissingData(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "seats_exceeded",
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID:           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		SubscriptionBlocker: &SeatsExceeded{AllowedUserIDs: []int64{}},
	}, res, err)
}

// Handle invalid data gracefully.
func TestGetSubscriptionSeatsExceededInvalidData(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "seats_exceeded",
		"data":                 "*(invalid-data4#",
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID:           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		SubscriptionBlocker: &SeatsExceeded{AllowedUserIDs: []int64{}},
	}, res, err)
}

// Handle mismatch in subscription_blocker types between data parameter and
// subscription_blocker.
func TestGetSubscriptionSeatsExceededInvalidKind(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "seats_exceeded",
		"data":                 `{"kind":"trial_expired", "allowed_user_ids": [5234234]}`,
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID:           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		SubscriptionBlocker: &SeatsExceeded{AllowedUserIDs: []int64{}},
	}, res, err)
}

// Handle unknown blocker by allowing user access.
func TestGetSubscriptionUnknownBlocker(t *testing.T) {
	setRedisSubscription(t, map[string]string{
		"account_id":           "DF5C23EB-585B-4031-B082-7FF951B4DE15",
		"subscription_blocker": "invalid_subscription_blocker",
	})
	res, err := getSubscription(t)
	assertSubscription(t, &Subscription{
		AccountID: "DF5C23EB-585B-4031-B082-7FF951B4DE15",
	}, res, err)
}

// setOpenPullRequestServer creates a fake list-pull-requests API where page N
// returns prsForPage(N) pull request numbers.
func setOpenPullRequestServer(t *testing.T, prsForPage func(page int) []int) *int32 {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil {
			t.Errorf("invalid page param: %v", err)
		}
		body := []byte("[")
		for i, number := range prsForPage(page) {
			if i > 0 {
				body = append(body, ',')
			}
			body = append(body, []byte(`{"number": `+strconv.Itoa(number)+`, "base": {"ref": "main"}}`)...)
		}
		body = append(body, ']')
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	prev := appconf.GithubV3APIRoot
	appconf.GithubV3APIRoot = srv.URL
	t.Cleanup(func() {
		appconf.GithubV3APIRoot = prev
		srv.Close()
	})
	return &calls
}

func pageRange(start, stop int) []int {
	var out []int
	for i := start; i < stop; i++ {
		out = append(out, i)
	}
	return out
}

// We should stop calling the API after reaching an empty page.
func TestGetOpenPullRequests(t *testing.T) {
	calls := setOpenPullRequestServer(t, func(page int) []int {
		switch page {
		case 1:
			return pageRange(1, 101)
		case 2:
			return pageRange(101, 201)
		case 3:
			return pageRange(201, 251)
		default:
			return nil
		}
	})
	seedInstallationToken(t)
	client := newTestClient(t)

	res, err := client.GetOpenPullRequests(context.Background(), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 250 {
		t.Errorf("expected 250 prs, got %d", len(res))
	}
	if got := atomic.LoadInt32(calls); got != 4 {
		t.Errorf("expected 4 api calls, got %d", got)
	}
}

// We should fetch at most 20 pages.
func TestGetOpenPullRequestsPageLimit(t *testing.T) {
	calls := setOpenPullRequestServer(t, func(page int) []int {
		start := (page-1)*100 + 1
		return pageRange(start, start+100)
	})
	seedInstallationToken(t)
	client := newTestClient(t)

	res, err := client.GetOpenPullRequests(context.Background(), "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 2000 {
		t.Errorf("expected 2000 prs, got %d", len(res))
	}
	if got := atomic.LoadInt32(calls); got != 20 {
		t.Errorf("expected to stop calling after 20 pages, got %d calls", got)
	}
}
