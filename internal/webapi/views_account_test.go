// Ported from web_api/web_api/test_views.py lines 952-1744: the activity,
// checkout, billing-info update, accounts, sync_accounts, current_account,
// and start_trial view tests.
package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// wv2FakeCheckoutSessionID ports the FakeCheckoutSession class used by the
// checkout tests.
const wv2FakeCheckoutSessionID = "cs_tgn3bJHRrXhqgdVSc4tsY"

// wv2Body decodes a JSON object response body (res.json()).
func wv2Body(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response body: %v: %s", err, res.Body.String())
	}
	return body
}

// wv2BodyList decodes a JSON array response body.
func wv2BodyList(t *testing.T, res *httptest.ResponseRecorder) []any {
	t.Helper()
	var body []any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response body: %v: %s", err, res.Body.String())
	}
	return body
}

// wv2JSONNorm round-trips v through JSON so expected values use the same
// dynamic types (float64, []any, map[string]any) as decoded responses.
func wv2JSONNorm(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal expected value: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal expected value: %v", err)
	}
	return out
}

// wv2Form builds an application/x-www-form-urlencoded body; the Django test
// client posts dict payloads as form data.
func wv2Form(pairs map[string]string) []byte {
	v := url.Values{}
	for key, value := range pairs {
		v.Set(key, value)
	}
	return []byte(v.Encode())
}

// wv2CreatePullRequestActivity ports PullRequestActivity.objects.create via
// direct SQL (see internal/webapi/dbtest/schema.sql).
func wv2CreatePullRequestActivity(t *testing.T, db *pgxpool.Pool, date string,
	totalOpened, totalMerged, totalClosed, kodiakApproved, kodiakMerged, kodiakUpdated int,
	installationID int64) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
INSERT INTO pull_request_activity (
    id, created_at, modified_at, date, total_opened, total_merged, total_closed,
    kodiak_approved, kodiak_merged, kodiak_updated, github_installation_id)
VALUES ($1, NOW(), NOW(), $2::date, $3, $4, $5, $6, $7, $8, $9)`,
		uuid.New(), date, totalOpened, totalMerged, totalClosed,
		kodiakApproved, kodiakMerged, kodiakUpdated, installationID)
	if err != nil {
		t.Fatalf("insert pull request activity: %v", err)
	}
}

// wv2AddMember ports AccountMembership.objects.create / membership.save().
func wv2AddMember(t *testing.T, db *pgxpool.Pool, account *models.Account, user *models.User, role string) {
	t.Helper()
	if err := models.UpsertMembership(context.Background(), db, account.ID, user.ID, role); err != nil {
		t.Fatalf("upsert membership: %v", err)
	}
}

// wv2RefreshAccount ports account.refresh_from_db().
func wv2RefreshAccount(t *testing.T, db *pgxpool.Pool, id uuid.UUID) *models.Account {
	t.Helper()
	account, err := models.GetAccountByID(context.Background(), db, id)
	if err != nil || account == nil {
		t.Fatalf("get account %s: %v", id, err)
	}
	return account
}

// wv2RefreshStripeInfo ports stripe_customer_info.refresh_from_db().
func wv2RefreshStripeInfo(t *testing.T, db *pgxpool.Pool, customerID string) *models.StripeCustomerInformation {
	t.Helper()
	info, err := models.GetStripeCustomerInfo(context.Background(), db, customerID)
	if err != nil || info == nil {
		t.Fatalf("get stripe customer info %q: %v", customerID, err)
	}
	return info
}

func wv2SaveAccount(t *testing.T, db *pgxpool.Pool, account *models.Account) {
	t.Helper()
	if err := models.SaveAccount(context.Background(), db, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
}

func wv2SaveStripeInfo(t *testing.T, db *pgxpool.Pool, info *models.StripeCustomerInformation) {
	t.Helper()
	if err := models.SaveStripeCustomerInfo(context.Background(), db, info); err != nil {
		t.Fatalf("save stripe customer info: %v", err)
	}
}

// wv2PStr formats a possibly-nil string pointer for assertion messages.
func wv2PStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// wv2InstallFakeCheckoutSession ports
// mocker.patch("web_api.views.stripe.checkout.Session.create",
// return_value=FakeCheckoutSession). The returned values record the form
// parameters of the most recent call (checkout_session_create.call_args).
func wv2InstallFakeCheckoutSession(t *testing.T) *url.Values {
	t.Helper()
	lastParams := &url.Values{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/checkout/sessions", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err == nil {
			*lastParams = r.PostForm
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id": "`+wv2FakeCheckoutSessionID+`", "object": "checkout.session"}`)
	})
	installFakeStripe(t, mux)
	return lastParams
}

// wv2InstallFakeStripeCustomer ports the patch_stripe_customer_modify and
// patch_start_trial fixtures: stripe.Customer.create and
// stripe.Customer.modify succeed, returning the fake customer built by
// create_stripe_customer().
func wv2InstallFakeStripeCustomer(t *testing.T) {
	t.Helper()
	respond := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "object": "customer",
  "id": "cus_Gz7jQFKdh4KirU",
  "address": {
    "line1": "123 Main St",
    "line2": "Apt 2B",
    "city": "Cambridge",
    "state": "Massachusetts",
    "postal_code": "02139",
    "country": "United States"
  },
  "balance": 0,
  "created": 1592096376,
  "currency": "usd",
  "email": "accounting@acme.corp",
  "name": "Acme Corp Inc",
  "subscriptions": {"data": [{"id": "sub_Gu1xedsfo1"}]}
}`)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/customers", respond)      // stripe.Customer.create
	mux.HandleFunc("POST /v1/customers/{id}", respond) // stripe.Customer.modify
	installFakeStripe(t, mux)
}

// wv2SimilarDates ports similar_dates: dates are equal if they are within 5
// minutes of each other.
func wv2SimilarDates(t *testing.T, a, b *time.Time) bool {
	t.Helper()
	if a == nil || b == nil {
		t.Fatal("expected non-nil dates")
	}
	diff := a.Unix() - b.Unix()
	if diff < 0 {
		diff = -diff
	}
	return diff < 60*5
}

// Ported from test_activity in web_api/web_api/test_views.py.
func TestActivity(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")
	wv2CreatePullRequestActivity(t, app.DB, "2020-02-03", 15, 13, 2, 3, 12, 2, userAccount.GithubInstallationID)

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/activity", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	kodiakActivity, _ := body["kodiakActivity"].(map[string]any)
	if got, want := kodiakActivity["labels"], wv2JSONNorm(t, []string{"2020-02-03"}); !reflect.DeepEqual(got, want) {
		t.Errorf("kodiakActivity.labels = %v, want %v", got, want)
	}
	if got, want := kodiakActivity["datasets"], wv2JSONNorm(t, map[string]any{
		"approved": []int{3},
		"merged":   []int{12},
		"updated":  []int{2},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("kodiakActivity.datasets = %v, want %v", got, want)
	}
	pullRequestActivity, _ := body["pullRequestActivity"].(map[string]any)
	if got, want := pullRequestActivity["labels"], wv2JSONNorm(t, []string{"2020-02-03"}); !reflect.DeepEqual(got, want) {
		t.Errorf("pullRequestActivity.labels = %v, want %v", got, want)
	}
	if got, want := pullRequestActivity["datasets"], wv2JSONNorm(t, map[string]any{
		"opened": []int{15},
		"merged": []int{13},
		"closed": []int{2},
	}); !reflect.DeepEqual(got, want) {
		t.Errorf("pullRequestActivity.datasets = %v, want %v", got, want)
	}
	if got, want := body["activeMergeQueues"], wv2JSONNorm(t, []any{}); !reflect.DeepEqual(got, want) {
		t.Errorf("activeMergeQueues = %v, want %v", got, want)
	}
}

// We should return active merge queues from Redis if available.
// Ported from test_activity_with_merge_queues in web_api/web_api/test_views.py.
func TestActivityWithMergeQueues(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")

	installID := userAccount.GithubInstallationID
	queue := fmt.Sprintf("merge_queue:%d.sbdchd/squawk/main", installID)
	emptyQueue := fmt.Sprintf("merge_queue:%d.sbdchd/time-to-deploy/main", installID)
	if err := app.Redis.SAdd(ctx, fmt.Sprintf("merge_queue_by_install:%d", installID), queue, emptyQueue).Err(); err != nil {
		t.Fatalf("seed merge_queue_by_install: %v", err)
	}
	// NOTE(port): pull_request_number and installation_id are JSON integers,
	// exactly as in the Python test (and as the bot serializes WebhookEvent;
	// see bot/kodiak/queue.py where pull_request_number is an int).
	//
	// KNOWN DIVERGENCE: this test fails against the current Go production
	// code. Python's KodiakQueueEntry (web_api/web_api/merge_queues.py) is a
	// pydantic model with pull_request_number: str, which coerces the integer
	// 55 to "55". The Go port's kodiakQueueEntry
	// (internal/webapi/mergequeue/mergequeue.go) declares PullRequestNumber
	// string, and encoding/json refuses to unmarshal a JSON number into a
	// string, so every entry is dropped and activeMergeQueues comes back
	// empty.
	mergingPR := fmt.Sprintf(`{"repo_owner": "sbdchd", "repo_name": "squawk", "pull_request_number": 55, "installation_id": %d, "target_name": "main"}`, installID)
	if err := app.Redis.Set(ctx, queue+":target", mergingPR, 0).Err(); err != nil {
		t.Fatalf("seed merging pull request: %v", err)
	}
	waitingPR := fmt.Sprintf(`{"repo_owner": "sbdchd", "repo_name": "squawk", "pull_request_number": 57, "installation_id": %d, "target_name": "main"}`, installID)
	score := 1614997354.8109288
	if err := app.Redis.ZAdd(ctx, queue, redis.Z{Score: score, Member: waitingPR}).Err(); err != nil {
		t.Fatalf("seed waiting pull request: %v", err)
	}
	if err := app.Redis.ZAdd(ctx, queue, redis.Z{Score: score + 1000, Member: mergingPR}).Err(); err != nil {
		t.Fatalf("seed merging pull request in waiting set: %v", err)
	}

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/activity", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	want := wv2JSONNorm(t, []any{
		map[string]any{
			"owner": "sbdchd",
			"repo":  "squawk",
			"queues": []any{
				map[string]any{
					"branch": "main",
					"pull_requests": []any{
						map[string]any{"number": "55", "added_at_timestamp": nil, "merging": true},
						map[string]any{"number": "57", "added_at_timestamp": 1614997354.8109288, "merging": false},
					},
				},
			},
		},
	})
	if got := body["activeMergeQueues"]; !reflect.DeepEqual(got, want) {
		t.Errorf("activeMergeQueues = %v, want %v", got, want)
	}
}

// We should ignore pull requests we can't parse so the web UI is robust.
// Ported from test_activity_with_merge_queues_invalid_parsing in
// web_api/web_api/test_views.py.
func TestActivityWithMergeQueuesInvalidParsing(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")

	installID := userAccount.GithubInstallationID
	queue := fmt.Sprintf("merge_queue:%d.sbdchd/squawk/main", installID)
	emptyQueue := fmt.Sprintf("merge_queue:%d.sbdchd/time-to-deploy/main", installID)
	if err := app.Redis.SAdd(ctx, fmt.Sprintf("merge_queue_by_install:%d", installID), queue, emptyQueue).Err(); err != nil {
		t.Fatalf("seed merge_queue_by_install: %v", err)
	}
	waitingPRWithInvalidStructure := fmt.Sprintf(`{"repo_owner": "sbdchd", "installation_id": %d}`, installID)
	if err := app.Redis.ZAdd(ctx, queue, redis.Z{Score: 1614997354.8109288, Member: waitingPRWithInvalidStructure}).Err(); err != nil {
		t.Fatalf("seed invalid waiting pull request: %v", err)
	}

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/activity", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	if got, want := body["activeMergeQueues"], wv2JSONNorm(t, []any{}); !reflect.DeepEqual(got, want) {
		t.Errorf("activeMergeQueues = %v, want %v", got, want)
	}
}

// Ported from test_activity_authentication in web_api/web_api/test_views.py.
func TestActivityAuthentication(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	otherUser := createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", otherUser.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, otherUser, "member")

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/activity", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 404 {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// Start a Stripe checkout session and return the required credentials to the
// frontend.
// Ported from test_start_checkout in web_api/web_api/test_views.py.
func TestStartCheckout(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")

	checkoutParams := wv2InstallFakeCheckoutSession(t)
	target := fmt.Sprintf("/v1/t/%s/start_checkout", userAccount.ID)

	// NOTE(port): Python asserts the mocked stripe.checkout.Session.create
	// kwargs subscription_data["items"][0]{"plan","quantity"}; the Go port
	// passes mode=subscription with line_items (see the NOTE(port) in
	// views.go startCheckout), so we assert the equivalent line_items
	// parameters received by the fake Stripe backend.
	check := func(form map[string]string, wantQuantity, wantPlan string) {
		t.Helper()
		res := doRequest(t, app, http.MethodPost, target, wv2Form(form), loginCookie(app, user))
		if res.Code != 200 {
			t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
		}
		body := wv2Body(t, res)
		if body["stripeCheckoutSessionId"] != wv2FakeCheckoutSessionID {
			t.Errorf("stripeCheckoutSessionId = %v, want %q", body["stripeCheckoutSessionId"], wv2FakeCheckoutSessionID)
		}
		if body["stripePublishableApiKey"] != app.Cfg.StripePublishableAPIKey {
			t.Errorf("stripePublishableApiKey = %v, want %q", body["stripePublishableApiKey"], app.Cfg.StripePublishableAPIKey)
		}
		if got := checkoutParams.Get("line_items[0][quantity]"); got != wantQuantity {
			t.Errorf("line_items quantity = %q, want %q", got, wantQuantity)
		}
		if got := checkoutParams.Get("line_items[0][price]"); got != wantPlan {
			t.Errorf("line_items price = %q, want %q", got, wantPlan)
		}
	}

	// start checkout without a plan to check backwards compatibility. We
	// should default to using the monthly plan.
	check(map[string]string{"seatCount": "3"}, "3", app.Cfg.StripePlanID)
	// start checkout with a monthly plan.
	check(map[string]string{"seatCount": "5", "planPeriod": "month"}, "5", app.Cfg.StripePlanID)
	// start checkout with a yearly plan.
	check(map[string]string{"seatCount": "2", "planPeriod": "year"}, "2", app.Cfg.StripeAnnualPlanID)
}

// Start a new checkout session in "setup" mode to update the payment
// information.
// Ported from test_modify_payment_details in web_api/web_api/test_views.py.
func TestModifyPaymentDetails(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	wv2InstallFakeCheckoutSession(t)

	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/modify_payment_details", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	if body["stripeCheckoutSessionId"] != wv2FakeCheckoutSessionID {
		t.Errorf("stripeCheckoutSessionId = %v, want %q", body["stripeCheckoutSessionId"], wv2FakeCheckoutSessionID)
	}
	if body["stripePublishableApiKey"] != app.Cfg.StripePublishableAPIKey {
		t.Errorf("stripePublishableApiKey = %v, want %q", body["stripePublishableApiKey"], app.Cfg.StripePublishableAPIKey)
	}
}

// Ported from test_modify_payment_details_limit_billing_access_to_owners in
// web_api/web_api/test_views.py.
func TestModifyPaymentDetailsLimitBillingAccessToOwners(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", true)
	wv2InstallFakeCheckoutSession(t)
	target := fmt.Sprintf("/v1/t/%s/modify_payment_details", account.ID)

	res := doRequest(t, app, http.MethodPost, target, nil, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}

	wv2AddMember(t, app.DB, account, user, "admin")
	res = doRequest(t, app, http.MethodPost, target, nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	if body["stripeCheckoutSessionId"] != wv2FakeCheckoutSessionID {
		t.Errorf("stripeCheckoutSessionId = %v, want %q", body["stripeCheckoutSessionId"], wv2FakeCheckoutSessionID)
	}
	if body["stripePublishableApiKey"] != app.Cfg.StripePublishableAPIKey {
		t.Errorf("stripePublishableApiKey = %v, want %q", body["stripePublishableApiKey"], app.Cfg.StripePublishableAPIKey)
	}
}

// Ported from test_update_stripe_customer_info_permission in
// web_api/web_api/test_views.py.
func TestUpdateStripeCustomerInfoPermission(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	if account.LimitBillingAccessToOwners != false {
		t.Fatalf("limit_billing_access_to_owners = %v, want false", account.LimitBillingAccessToOwners)
	}
	payload := map[string]any{"limitBillingAccessToOwners": true}
	target := fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID)

	res := doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	if account.LimitBillingAccessToOwners != false {
		t.Errorf("limit_billing_access_to_owners = %v, want false", account.LimitBillingAccessToOwners)
	}

	wv2AddMember(t, app.DB, account, user, "admin")
	res = doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	if account.LimitBillingAccessToOwners != true {
		t.Errorf("limit_billing_access_to_owners = %v, want true", account.LimitBillingAccessToOwners)
	}
}

// Ported from test_update_stripe_customer_info_limit_billing_access_to_owners
// in web_api/web_api/test_views.py.
func TestUpdateStripeCustomerInfoLimitBillingAccessToOwners(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	info := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	const originalEmail = "invoices@acme-inc.corp"
	info.CustomerEmail = originalEmail
	wv2SaveStripeInfo(t, app.DB, info)
	account.LimitBillingAccessToOwners = true
	wv2SaveAccount(t, app.DB, account)

	payload := map[string]any{"email": "billing@kodiakhq.com"}
	target := fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID)

	res := doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	if info.CustomerEmail != originalEmail {
		t.Errorf("customer_email = %q, want %q", info.CustomerEmail, originalEmail)
	}

	wv2AddMember(t, app.DB, account, user, "admin")
	res = doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	if info.CustomerEmail != "billing@kodiakhq.com" {
		t.Errorf("customer_email = %q, want %q", info.CustomerEmail, "billing@kodiakhq.com")
	}
}

// A user should be able to modifying billing email.
// Ported from test_update_billing_email in web_api/web_api/test_views.py.
func TestUpdateBillingEmail(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	info := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	info.CustomerEmail = "invoices@acme-inc.corp"
	wv2SaveStripeInfo(t, app.DB, info)

	payload := map[string]any{"email": "billing@kodiakhq.com"}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID), payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	if info.CustomerEmail != "billing@kodiakhq.com" {
		t.Errorf("customer_email = %q, want %q", info.CustomerEmail, "billing@kodiakhq.com")
	}
}

// We should error if the user tries to update an email to empty string.
// Ported from test_update_billing_email_empty in web_api/web_api/test_views.py.
func TestUpdateBillingEmailEmpty(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	info := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	const originalEmail = "invoices@acme-inc.corp"
	info.CustomerEmail = originalEmail
	wv2SaveStripeInfo(t, app.DB, info)

	payload := map[string]any{"email": ""}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID), payload, loginCookie(app, user))
	if res.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	if info.CustomerEmail != originalEmail {
		t.Errorf("customer_email = %q, want %q", info.CustomerEmail, originalEmail)
	}
}

// User should be able to set contact emails.
// Ported from test_update_contact_emails in web_api/web_api/test_views.py.
func TestUpdateContactEmails(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)

	account.ContactEmails = "j.doe@acme-inc.corp"
	wv2SaveAccount(t, app.DB, account)

	// by default a user should be able to set the contact emails fields.
	const contactEmails = "a.hamilton@treasury.gov\ng.washington@whitehouse.gov"
	payload := map[string]any{"contactEmails": contactEmails}
	target := fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID)

	res := doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	if account.ContactEmails != contactEmails {
		t.Errorf("contact_emails = %q, want %q", account.ContactEmails, contactEmails)
	}

	// if we limit to billing access and the user is not an admin they should
	// be prevented from updating.
	account.LimitBillingAccessToOwners = true
	wv2SaveAccount(t, app.DB, account)

	res = doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}

	// an admin can set the field when limit_billing_access_to_owners is
	// enabled.
	wv2AddMember(t, app.DB, account, user, "admin")
	res = doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
}

// A user should be able to modify the company name.
// Ported from test_update_company_name in web_api/web_api/test_views.py.
func TestUpdateCompanyName(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	info := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	info.CustomerName = tstrPtr("Acme Corp Inc.")
	wv2SaveStripeInfo(t, app.DB, info)

	payload := map[string]any{"name": "Kodiak Bait & Tackle"}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID), payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	if info.CustomerName == nil || *info.CustomerName != "Kodiak Bait & Tackle" {
		t.Errorf("customer_name = %q, want %q", wv2PStr(info.CustomerName), "Kodiak Bait & Tackle")
	}
}

// A user should be able to modifying postal address.
// Ported from test_update_address in web_api/web_api/test_views.py.
func TestUpdateAddress(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	info := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	info.CustomerAddressLine1 = nil
	info.CustomerAddressCity = nil
	info.CustomerAddressCountry = nil
	info.CustomerAddressLine2 = nil
	info.CustomerAddressPostalCode = nil
	info.CustomerAddressState = nil
	wv2SaveStripeInfo(t, app.DB, info)

	payload := map[string]any{
		"address": map[string]any{
			"line1":      "123 Main St",
			"line2":      "Apt 3B",
			"city":       "Anytown",
			"postalCode": "12345",
			"state":      "Massachusetts",
			"country":    "United States",
		},
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID), payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	info = wv2RefreshStripeInfo(t, app.DB, account.StripeCustomerID)
	checks := []struct {
		field string
		got   *string
		want  string
	}{
		{"customer_address_line1", info.CustomerAddressLine1, "123 Main St"},
		{"customer_address_city", info.CustomerAddressCity, "Anytown"},
		{"customer_address_country", info.CustomerAddressCountry, "United States"},
		{"customer_address_line2", info.CustomerAddressLine2, "Apt 3B"},
		{"customer_address_postal_code", info.CustomerAddressPostalCode, "12345"},
		{"customer_address_state", info.CustomerAddressState, "Massachusetts"},
	}
	for _, c := range checks {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, wv2PStr(c.got), c.want)
		}
	}
}

// Only GitHub organization Owners should be able to modify this field.
// Ported from test_update_limit_billing_access_to_owners in
// web_api/web_api/test_views.py.
func TestUpdateLimitBillingAccessToOwners(t *testing.T) {
	app, _ := newTestApp(t)
	wv2InstallFakeStripeCustomer(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	payload := map[string]any{"limitBillingAccessToOwners": true}
	target := fmt.Sprintf("/v1/t/%s/update_stripe_customer_info", account.ID)

	res := doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}

	wv2AddMember(t, app.DB, account, user, "admin")
	res = doRequest(t, app, http.MethodPost, target, payload, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	if account.LimitBillingAccessToOwners != true {
		t.Errorf("limit_billing_access_to_owners = %v, want true", account.LimitBillingAccessToOwners)
	}
}

// Ensure we order the accounts in the response by name.
//
// We could also sort them on the frontend.
// Ported from test_accounts_endpoint_ordering in web_api/web_api/test_views.py.
func TestAccountsEndpointOrdering(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	industries := createTestAccount(t, app.DB, "cus_523405923045", "industries", models.AccountTypeOrganization)
	wv2AddMember(t, app.DB, industries, user, "member")
	acme := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", models.AccountTypeOrganization)
	wv2AddMember(t, app.DB, acme, user, "member")
	market := createTestAccount(t, app.DB, "cus_523405923045", "market", models.AccountTypeOrganization)
	wv2AddMember(t, app.DB, market, user, "member")

	res := doRequest(t, app, http.MethodGet, "/v1/accounts", nil, loginCookie(app, user))
	got := wv2BodyList(t, res)
	want := wv2JSONNorm(t, []any{
		map[string]any{
			"id":            acme.ID.String(),
			"name":          "acme-corp",
			"profileImgUrl": fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", acme.GithubAccountID),
		},
		map[string]any{
			"id":            industries.ID.String(),
			"name":          "industries",
			"profileImgUrl": fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", industries.GithubAccountID),
		},
		map[string]any{
			"id":            market.ID.String(),
			"name":          "market",
			"profileImgUrl": fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", market.GithubAccountID),
		},
	})
	if !reflect.DeepEqual(wv2JSONNorm(t, got), want) {
		t.Errorf("accounts = %v, want %v", got, want)
	}
}

// Ported from test_sync_accounts_success in web_api/web_api/test_views.py.
// Reuses the wv4SuccessfulSyncAccountsResponse fixture ported in
// views_oauth_test.go (successful_sync_accounts_response in Python).
func TestSyncAccountsSuccess(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	mux := http.NewServeMux()
	wv4SuccessfulSyncAccountsResponse(mux)
	installFakeHTTP(app, mux)

	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Fatalf("Account count = %d, want 0", n)
	}
	res := doRequest(t, app, http.MethodPost, "/v1/sync_accounts", nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if n := wv4AccountCount(t, app.DB); n != 1 {
		t.Errorf("Account count = %d, want 1", n)
	}
}

// Ported from test_sync_accounts_failure in web_api/web_api/test_views.py.
// Reuses the wv4FailingSyncAccountsResponse fixture ported in
// views_oauth_test.go (failing_sync_accounts_response in Python).
func TestSyncAccountsFailure(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	mux := http.NewServeMux()
	wv4FailingSyncAccountsResponse(mux)
	installFakeHTTP(app, mux)

	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Fatalf("Account count = %d, want 0", n)
	}
	res := doRequest(t, app, http.MethodPost, "/v1/sync_accounts", nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	if body["ok"] != false {
		t.Errorf("ok = %v, want false", body["ok"])
	}
	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Errorf("Account count = %d, want 0", n)
	}
}

// Ported from test_current_account in web_api/web_api/test_views.py.
func TestCurrentAccount(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")
	orgAccount := createTestAccount(t, app.DB, "cus_523405923045", "recipeyak", models.AccountTypeOrganization)
	wv2AddMember(t, app.DB, orgAccount, user, "member")

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/current_account", orgAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv2Body(t, res)
	userResp, _ := body["user"].(map[string]any)
	if userResp["id"] != user.ID.String() {
		t.Errorf("user.id = %v, want %q", userResp["id"], user.ID.String())
	}
	if userResp["name"] != user.GithubLogin {
		t.Errorf("user.name = %v, want %q", userResp["name"], user.GithubLogin)
	}
	if want := fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", user.GithubID); userResp["profileImgUrl"] != want {
		t.Errorf("user.profileImgUrl = %v, want %q", userResp["profileImgUrl"], want)
	}
	orgResp, _ := body["org"].(map[string]any)
	if orgResp["id"] != orgAccount.ID.String() {
		t.Errorf("org.id = %v, want %q", orgResp["id"], orgAccount.ID.String())
	}
	if orgResp["name"] != orgAccount.GithubAccountLogin {
		t.Errorf("org.name = %v, want %q", orgResp["name"], orgAccount.GithubAccountLogin)
	}
	if want := fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", orgAccount.GithubAccountID); orgResp["profileImgUrl"] != want {
		t.Errorf("org.profileImgUrl = %v, want %q", orgResp["profileImgUrl"], want)
	}

	accountsResp, _ := body["accounts"].([]any)
	if len(accountsResp) != 2 {
		t.Fatalf("accounts length = %d, want 2", len(accountsResp))
	}
	sort.Slice(accountsResp, func(i, j int) bool {
		a, _ := accountsResp[i].(map[string]any)
		b, _ := accountsResp[j].(map[string]any)
		an, _ := a["name"].(string)
		bn, _ := b["name"].(string)
		return an < bn
	})
	first, _ := accountsResp[0].(map[string]any)
	if first["id"] != userAccount.ID.String() {
		t.Errorf("accounts[0].id = %v, want %q", first["id"], userAccount.ID.String())
	}
	if first["name"] != userAccount.GithubAccountLogin {
		t.Errorf("accounts[0].name = %v, want %q", first["name"], userAccount.GithubAccountLogin)
	}
	if want := fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", userAccount.GithubAccountID); first["profileImgUrl"] != want {
		t.Errorf("accounts[0].profileImgUrl = %v, want %q", first["profileImgUrl"], want)
	}
}

// Ported from test_current_account_authentication in
// web_api/web_api/test_views.py.
func TestCurrentAccountAuthentication(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	otherUser := createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", otherUser.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, otherUser, "member")

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/current_account", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 404 {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// Ported from test_accounts in web_api/web_api/test_views.py.
func TestAccounts(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, userAccount, user, "member")
	orgAccount := createTestAccount(t, app.DB, "cus_523405923045", "recipeyak", models.AccountTypeOrganization)
	wv2AddMember(t, app.DB, orgAccount, user, "member")

	res := doRequest(t, app, http.MethodGet, "/v1/accounts", nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	accountsResp := wv2BodyList(t, res)
	if len(accountsResp) != 2 {
		t.Fatalf("accounts length = %d, want 2", len(accountsResp))
	}
	sort.Slice(accountsResp, func(i, j int) bool {
		a, _ := accountsResp[i].(map[string]any)
		b, _ := accountsResp[j].(map[string]any)
		an, _ := a["name"].(string)
		bn, _ := b["name"].(string)
		return an < bn
	})
	first, _ := accountsResp[0].(map[string]any)
	if first["id"] != userAccount.ID.String() {
		t.Errorf("accounts[0].id = %v, want %q", first["id"], userAccount.ID.String())
	}
	if first["name"] != userAccount.GithubAccountLogin {
		t.Errorf("accounts[0].name = %v, want %q", first["name"], userAccount.GithubAccountLogin)
	}
	if want := fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", userAccount.GithubAccountID); first["profileImgUrl"] != want {
		t.Errorf("accounts[0].profileImgUrl = %v, want %q", first["profileImgUrl"], want)
	}
}

// When a user starts a trial we should update their account.
// Ported from test_start_trial in web_api/web_api/test_views.py.
func TestStartTrial(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	wv2InstallFakeStripeCustomer(t) // patch_start_trial
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, account, user, "member")

	if account.TrialStart != nil {
		t.Fatalf("trial_start = %v, want nil", account.TrialStart)
	}
	if account.TrialExpiration != nil {
		t.Fatalf("trial_expiration = %v, want nil", account.TrialExpiration)
	}
	if account.TrialStartedByID != nil {
		t.Fatalf("trial_started_by = %v, want nil", account.TrialStartedByID)
	}
	if account.TrialExpired() != false {
		t.Fatal("when a trial is inactive, it shouldn't have expired.")
	}

	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/start_trial", account.ID),
		wv2Form(map[string]string{"billingEmail": "b.lowe@example.com"}), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	// NOTE(port): Python asserts update_bot.call_count == 1 on a mock; the Go
	// port checks the observable effect of update_bot: exactly one refresh
	// message pushed to Redis.
	if n, err := app.Redis.LLen(ctx, "kodiak:refresh_pull_requests_for_installation").Result(); err != nil || n != 1 {
		t.Errorf("update_bot refresh message count = %d (err %v), want 1", n, err)
	}

	account = wv2RefreshAccount(t, app.DB, account.ID)
	if account.TrialStart == nil || account.TrialExpiration == nil {
		t.Fatalf("trial_start = %v, trial_expiration = %v, want non-nil", account.TrialStart, account.TrialExpiration)
	}
	// literal port of the Python arithmetic assertion.
	if !(account.TrialStart.Sub(*account.TrialExpiration).Seconds()-(30*24*time.Hour).Seconds() < 60*60) {
		t.Error("times should be within an hour of each other. This should hopefully avoid flakiness around dates.")
	}
	if account.TrialStartedByID == nil || *account.TrialStartedByID != user.ID {
		t.Errorf("trial_started_by = %v, want %s", account.TrialStartedByID, user.ID)
	}
	if account.TrialExpired() != false {
		t.Error("trial_expired() = true, want false")
	}
	if account.TrialEmail != "b.lowe@example.com" {
		t.Errorf("trial_email = %q, want %q", account.TrialEmail, "b.lowe@example.com")
	}
}

// Starting a trial when there is already an existing trial shouldn't alter
// current trial.
// Ported from test_start_trial_existing_trial in web_api/web_api/test_views.py.
func TestStartTrialExistingTrial(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	wv2InstallFakeStripeCustomer(t) // patch_start_trial
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	wv2AddMember(t, app.DB, account, user, "member")

	// NOTE(port): Python calls account.start_trial(..., length_days=2); the
	// Go startTrial has a fixed 30-day length. The difference doesn't affect
	// the assertions, which only compare the trial state before and after the
	// second start_trial request.
	if err := app.startTrial(ctx, account, user, "b.lowe@example.com"); err != nil {
		t.Fatalf("start trial: %v", err)
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	originalTrialStart := account.TrialStart
	originalTrialExpiration := account.TrialExpiration
	originalTrialStartedBy := account.TrialStartedByID

	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/start_trial", account.ID),
		wv2Form(map[string]string{"billingEmail": "d.abernathy@example.com"}), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	account = wv2RefreshAccount(t, app.DB, account.ID)
	if !wv2SimilarDates(t, account.TrialStart, originalTrialStart) {
		t.Errorf("trial_start = %v, want similar to %v", account.TrialStart, originalTrialStart)
	}
	if !wv2SimilarDates(t, account.TrialExpiration, originalTrialExpiration) {
		t.Errorf("trial_expiration = %v, want similar to %v", account.TrialExpiration, originalTrialExpiration)
	}
	if originalTrialStartedBy == nil || account.TrialStartedByID == nil ||
		*account.TrialStartedByID != *originalTrialStartedBy {
		t.Errorf("trial_started_by = %v, want %v", account.TrialStartedByID, originalTrialStartedBy)
	}
}
