// Ported from web_api/web_api/test_views.py (lines 168-951): the
// environment, usage_billing, fetch_proration, update_subscription, and
// cancel_subscription tests. Helper prefix: wv1.
package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// wv1WriteJSON writes v as a JSON response in a fake Stripe handler.
func wv1WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// wv1JSON decodes a response body like Python's res.json().
func wv1JSON(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response body %q: %v", res.Body.String(), err)
	}
	return body
}

// wv1Recorder tracks fake Stripe endpoint hits, the Go analogue of
// mocker.patch(...).call_count / call_args in the Python tests.
type wv1Recorder struct {
	mu     sync.Mutex
	counts map[string]int
	values map[string]string
}

func wv1NewRecorder() *wv1Recorder {
	return &wv1Recorder{counts: map[string]int{}, values: map[string]string{}}
}

func (r *wv1Recorder) inc(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts[key]++
}

func (r *wv1Recorder) count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[key]
}

func (r *wv1Recorder) set(key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
}

func (r *wv1Recorder) value(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.values[key]
}

// wv1CreateActivity inserts a UserPullRequestActivity row
// (UserPullRequestActivity.objects.create in the Python tests). The date is
// passed as a YYYY-MM-DD string so the cast to the `date` column does not
// depend on the session time zone.
func wv1CreateActivity(t *testing.T, db *pgxpool.Pool, installationID int64, repo string,
	prNumber int64, login string, githubUserID int64, isPrivate bool,
	activityDate time.Time, openedPullRequest bool) {
	t.Helper()
	_, err := db.Exec(context.Background(), `
INSERT INTO user_pull_request_activity (
    id, created_at, modified_at, github_installation_id, github_repository_name,
    github_pull_request_number, github_user_login, github_user_id,
    is_private_repository, opened_pull_request, activity_date)
VALUES ($1, NOW(), NOW(), $2, $3, $4, $5, $6, $7, $8, $9::date)`,
		uuid.New(), installationID, repo, prNumber, login, githubUserID,
		isPrivate, openedPullRequest, activityDate.Format("2006-01-02"))
	if err != nil {
		t.Fatalf("insert user pull request activity: %v", err)
	}
}

// wv1StripeCustomerInfoCount ports StripeCustomerInformation.objects.count().
func wv1StripeCustomerInfoCount(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(),
		`SELECT count(*) FROM stripe_customer_information`).Scan(&n); err != nil {
		t.Fatalf("count stripe_customer_information: %v", err)
	}
	return n
}

// wv1UpdateBotCalls counts Account.update_bot invocations.
//
// NOTE(port): Python mocks web_api.models.Account.update_bot and asserts
// call_count; the Go port runs the real updateBot against miniredis, which
// pushes exactly one refresh message per call, so we count those instead.
func wv1UpdateBotCalls(t *testing.T, app *App) int {
	t.Helper()
	n, err := app.Redis.LLen(context.Background(),
		"kodiak:refresh_pull_requests_for_installation").Result()
	if err != nil {
		t.Fatalf("llen refresh queue: %v", err)
	}
	return int(n)
}

// wv1StripePaymentMethod ports create_stripe_payment_method.
//
// NOTE(port): the Python fixture uses string card exp fields; stripe-go
// unmarshals exp_month/exp_year as integers, so numbers are used here.
func wv1StripePaymentMethod() map[string]any {
	return map[string]any{
		"object": "payment_method",
		"id":     "pm_55yfgbc6",
		"card": map[string]any{
			"brand":     "mastercard",
			"exp_month": 4,
			"exp_year":  22,
			"last4":     "4040",
		},
	}
}

// wv1StripeCustomer ports create_stripe_customer (with the default address).
func wv1StripeCustomer() map[string]any {
	return map[string]any{
		"object": "customer",
		"id":     "cus_Gz7jQFKdh4KirU",
		"address": map[string]any{
			"line1":       "123 Main St",
			"line2":       "Apt 2B",
			"city":        "Cambridge",
			"state":       "Massachusetts",
			"postal_code": "02139",
			"country":     "United States",
		},
		"balance":       0,
		"created":       1592096376,
		"currency":      "usd",
		"email":         "accounting@acme.corp",
		"name":          "Acme Corp Inc",
		"subscriptions": map[string]any{"data": []any{map[string]any{"id": "sub_Gu1xedsfo1"}}},
	}
}

// wv1PatchStartTrial ports the patch_start_trial fixture: stub the Stripe
// Customer.create / Customer.modify endpoints with the fake customer.
func wv1PatchStartTrial(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/customers", func(w http.ResponseWriter, r *http.Request) {
		wv1WriteJSON(w, wv1StripeCustomer())
	})
	mux.HandleFunc("POST /v1/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		wv1WriteJSON(w, wv1StripeCustomer())
	})
	installFakeStripe(t, mux)
}

// Ported from test_environment.
func TestEnvironment(t *testing.T) {
	// NOTE(port): Django reads these settings from the pytest env in
	// pyproject.toml; the Go harness builds the equivalent settings.Config in
	// newTestApp, which requires the test database, so this check skips
	// without TEST_DATABASE_URL unlike its Python counterpart.
	app, _ := newTestApp(t)
	if app.Cfg.KodiakAPIGithubClientID != "Iv1.111FAKECLIENTID111" {
		t.Errorf("KodiakAPIGithubClientID = %q, want %q",
			app.Cfg.KodiakAPIGithubClientID, "Iv1.111FAKECLIENTID111")
	}
	if app.Cfg.KodiakAPIGithubClientSecret != "888INVALIDSECRET8888" {
		t.Errorf("KodiakAPIGithubClientSecret = %q, want %q",
			app.Cfg.KodiakAPIGithubClientSecret, "888INVALIDSECRET8888")
	}
}

// Ported from test_usage_billing.
func TestUsageBilling(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, userAccount.ID, user.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	today := time.Now().UTC()
	twoDaysFromToday := today.AddDate(0, 0, 2)

	// this user opened a PR on a private repository that Kodiak also acted on,
	// so we should count it.
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		642, user.GithubLogin, user.GithubID, true, today, true)
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		642, "kodiakhq[bot]", 11479, true, today, false)

	// this user did not open a pull request, so we shouldn't count it.
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		864, "jdoe", 6039209, true, twoDaysFromToday, false)
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		864, "kodiakhq[bot]", 11479, true, today, false)

	// this event is on a public repository, so we shouldn't count it.
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		755, "sgoodman", 4323112, false, twoDaysFromToday, true)
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		755, "kodiakhq[bot]", 11479, false, today, false)

	// this event should not be counted because the PR was not acted on by Kodiak.
	wv1CreateActivity(t, app.DB, userAccount.GithubInstallationID, "acme-web",
		8545, "jpiccirillo", 643453, true, twoDaysFromToday, true)

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if body["accountCanSubscribe"] != false {
		t.Errorf("accountCanSubscribe = %v, want false: user accounts should not see subscription options",
			body["accountCanSubscribe"])
	}
	todayStr := today.Format("2006-01-02")
	wantActiveUsers := []any{
		map[string]any{
			"id":              float64(user.GithubID),
			"name":            user.GithubLogin,
			"profileImgUrl":   user.ProfileImage(),
			"interactions":    float64(1),
			"firstActiveDate": todayStr,
			"lastActiveDate":  todayStr,
			"hasSeatLicense":  false,
		},
	}
	if !reflect.DeepEqual(body["activeUsers"], wantActiveUsers) {
		t.Errorf("activeUsers = %v, want %v", body["activeUsers"], wantActiveUsers)
	}
	if v, ok := body["subscriptionExemption"]; !ok || v != nil {
		t.Errorf("subscriptionExemption = %v (present=%v), want null: we should always return this field", v, ok)
	}

	userAccount.GithubAccountType = models.AccountTypeOrganization
	if err := models.SaveAccount(ctx, app.DB, userAccount); err != nil {
		t.Fatalf("save account: %v", err)
	}
	res = doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body = wv1JSON(t, res)
	if body["accountCanSubscribe"] != true {
		t.Errorf("accountCanSubscribe = %v, want true: organizations should see subscription options",
			body["accountCanSubscribe"])
	}
}

// When the Account has an active trial, we return trial information in the API response.
// Ported from test_usage_billing_trial_active.
func TestUsageBillingTrialActive(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user
	wv1PatchStartTrial(t)

	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, userAccount.ID, user.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if err := app.startTrial(ctx, userAccount, user, "b.lowe@example.com"); err != nil {
		t.Fatalf("start trial: %v", err)
	}
	if err := models.SaveAccount(ctx, app.DB, userAccount); err != nil {
		t.Fatalf("save account: %v", err)
	}

	if userAccount.TrialExpired() {
		t.Fatal("trial_expired() = true, want false")
	}
	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	trial, ok := body["trial"].(map[string]any)
	if !ok || trial == nil {
		t.Fatalf("trial = %v, want non-nil object", body["trial"])
	}
	if _, ok := trial["startDate"].(string); !ok {
		t.Errorf("trial startDate = %v (%T), want string", trial["startDate"], trial["startDate"])
	}
	if _, ok := trial["endDate"].(string); !ok {
		t.Errorf("trial endDate = %v (%T), want string", trial["endDate"], trial["endDate"])
	}
	if trial["expired"] != false {
		t.Errorf("trial expired = %v, want false", trial["expired"])
	}
	wantStartedBy := map[string]any{
		"id":            user.ID.String(),
		"name":          user.GithubLogin,
		"profileImgUrl": user.ProfileImage(),
	}
	if !reflect.DeepEqual(trial["startedBy"], wantStartedBy) {
		t.Errorf("trial startedBy = %v, want %v", trial["startedBy"], wantStartedBy)
	}
}

// When the Account has an expired trial, we return trial information in the
// API response and indicate that the trial is expired.
// Ported from test_usage_billing_trial_expired.
func TestUsageBillingTrialExpired(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user
	wv1PatchStartTrial(t)

	userAccount := createTestAccount(t, app.DB, "cus_523405923045", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, userAccount.ID, user.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if err := app.startTrial(ctx, userAccount, user, "b.lowe@example.com"); err != nil {
		t.Fatalf("start trial: %v", err)
	}
	trialStart := time.Date(2000, 4, 15, 0, 0, 0, 0, time.UTC)
	trialExpiration := time.Date(2000, 5, 4, 0, 0, 0, 0, time.UTC)
	userAccount.TrialStart = &trialStart
	userAccount.TrialExpiration = &trialExpiration
	if err := models.SaveAccount(ctx, app.DB, userAccount); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if !time.Now().After(time.Date(2000, 5, 4, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("we expect the current time to be more recent")
	}

	if !userAccount.TrialExpired() {
		t.Fatal("trial_expired() = false, want true")
	}
	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if body["trial"] == nil {
		t.Fatal("trial = nil, want non-nil")
	}
	wantTrial := map[string]any{
		"startDate": "2000-04-15T00:00:00Z",
		"endDate":   "2000-05-04T00:00:00Z",
		"expired":   true,
		"startedBy": map[string]any{
			"id":            user.ID.String(),
			"name":          user.GithubLogin,
			"profileImgUrl": user.ProfileImage(),
		},
	}
	if !reflect.DeepEqual(body["trial"], wantTrial) {
		t.Errorf("trial = %v, want %v", body["trial"], wantTrial)
	}
}

// Ported from test_usage_billing_authentication.
func TestUsageBillingAuthentication(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776") // authed_client user
	otherUser := createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7")

	userAccount := createTestAccount(t, app.DB, "cus_523405923045", otherUser.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, userAccount.ID, otherUser.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", userAccount.ID), nil, loginCookie(app, user))
	if res.Code != 404 {
		t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// When the Account has an active subscription we should return the
// subscription information in the API response.
// Ported from test_usage_billing_subscription_started.
func TestUsageBillingSubscriptionStarted(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	const oneDaySec = int64(60 * 60 * 24)
	periodStart := time.Now().Unix()
	periodEnd := periodStart + 30*oneDaySec // start plus one month.
	// NOTE(port): Python patches web_api.models.time.time to periodStart+5
	// days so the subscription reads as active; the Go Expired() uses the
	// real clock and the period end is 30 days out, so no patch is needed.
	account := createTestAccount(t, app.DB, "cus_Ged32s2xnx12", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	stripeCustomerInformation := createTestStripeCustomerInfo(t, app.DB,
		account.StripeCustomerID, "sub_Gu1xedsfo1", periodStart, periodEnd)

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	sub, ok := body["subscription"].(map[string]any)
	if !ok || sub == nil {
		t.Fatalf("subscription = %v, want non-nil object", body["subscription"])
	}
	wantNextBillingDate := time.Unix(periodEnd, 0).Format("2006-01-02T15:04:05")
	if sub["nextBillingDate"] != wantNextBillingDate {
		t.Errorf("nextBillingDate = %v, want %v", sub["nextBillingDate"], wantNextBillingDate)
	}
	if sub["expired"] != false {
		t.Errorf("expired = %v, want false", sub["expired"])
	}
	if sub["seats"] != float64(3) {
		t.Errorf("seats = %v, want 3", sub["seats"])
	}
	cost, ok := sub["cost"].(map[string]any)
	if !ok {
		t.Fatalf("cost = %v, want object", sub["cost"])
	}
	if cost["totalCents"] != float64(3*499) {
		t.Errorf("cost.totalCents = %v, want %d", cost["totalCents"], 3*499)
	}
	if cost["perSeatCents"] != float64(499) {
		t.Errorf("cost.perSeatCents = %v, want 499", cost["perSeatCents"])
	}
	if cost["currency"] != "usd" {
		t.Errorf("cost.currency = %v, want usd", cost["currency"])
	}
	if cost["planInterval"] != "month" {
		t.Errorf("cost.planInterval = %v, want month", cost["planInterval"])
	}
	if sub["billingEmail"] != "accounting@acme-corp.com" {
		t.Errorf("billingEmail = %v, want accounting@acme-corp.com", sub["billingEmail"])
	}
	if sub["contactEmails"] != "" {
		t.Errorf("contactEmails = %v, want empty string", sub["contactEmails"])
	}
	if sub["cardInfo"] != "Mastercard (4242)" {
		t.Errorf("cardInfo = %v, want Mastercard (4242)", sub["cardInfo"])
	}
	if sub["customerName"] != nil {
		t.Errorf("customerName = %v, want null", sub["customerName"])
	}
	if sub["customerAddress"] != nil {
		t.Errorf("customerAddress = %v, want null", sub["customerAddress"])
	}

	stripeCustomerInformation.CustomerCurrency = nil
	stripeCustomerInformation.PlanInterval = tstrPtr("year")
	if err := models.SaveStripeCustomerInfo(ctx, app.DB, stripeCustomerInformation); err != nil {
		t.Fatalf("save stripe customer info: %v", err)
	}
	res = doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body = wv1JSON(t, res)
	sub = body["subscription"].(map[string]any)
	cost = sub["cost"].(map[string]any)
	if cost["currency"] != "usd" {
		t.Errorf("cost.currency = %v, want usd: should default to usd if we cannot find a currency",
			cost["currency"])
	}
	if cost["planInterval"] != "year" {
		t.Errorf("cost.planInterval = %v, want year", cost["planInterval"])
	}

	stripeCustomerInformation.CustomerName = tstrPtr("Acme-corp")
	stripeCustomerInformation.CustomerAddressLine1 = tstrPtr("123 Main Street")
	stripeCustomerInformation.CustomerAddressCity = tstrPtr("Anytown")
	stripeCustomerInformation.CustomerAddressCountry = tstrPtr("United States")
	stripeCustomerInformation.CustomerAddressLine2 = tstrPtr("Apt B")
	stripeCustomerInformation.CustomerAddressPostalCode = tstrPtr("02134")
	stripeCustomerInformation.CustomerAddressState = tstrPtr("Massachusetts")
	if err := models.SaveStripeCustomerInfo(ctx, app.DB, stripeCustomerInformation); err != nil {
		t.Fatalf("save stripe customer info: %v", err)
	}
	res = doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body = wv1JSON(t, res)
	sub = body["subscription"].(map[string]any)
	if sub["customerName"] != "Acme-corp" {
		t.Errorf("customerName = %v, want Acme-corp", sub["customerName"])
	}
	wantAddress := map[string]any{
		"line1":      *stripeCustomerInformation.CustomerAddressLine1,
		"city":       *stripeCustomerInformation.CustomerAddressCity,
		"country":    *stripeCustomerInformation.CustomerAddressCountry,
		"line2":      *stripeCustomerInformation.CustomerAddressLine2,
		"postalCode": *stripeCustomerInformation.CustomerAddressPostalCode,
		"state":      *stripeCustomerInformation.CustomerAddressState,
	}
	if !reflect.DeepEqual(sub["customerAddress"], wantAddress) {
		t.Errorf("customerAddress = %v, want %v", sub["customerAddress"], wantAddress)
	}
}

// Ported from test_usage_limit_billing_access_to_owners_member.
func TestUsageLimitBillingAccessToOwnersMember(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	fetchSubscription := func() map[string]any {
		t.Helper()
		res := doRequest(t, app, http.MethodGet,
			fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
		if res.Code != 200 {
			t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
		}
		body := wv1JSON(t, res)
		sub, ok := body["subscription"].(map[string]any)
		if !ok || sub == nil {
			t.Fatalf("subscription = %v, want non-nil object", body["subscription"])
		}
		return sub
	}

	sub := fetchSubscription()
	if sub["viewerIsOrgOwner"] != false {
		t.Errorf("viewerIsOrgOwner = %v, want false", sub["viewerIsOrgOwner"])
	}
	if sub["viewerCanModify"] != true {
		t.Errorf("viewerCanModify = %v, want true", sub["viewerCanModify"])
	}
	if sub["limitBillingAccessToOwners"] != false {
		t.Errorf("limitBillingAccessToOwners = %v, want false", sub["limitBillingAccessToOwners"])
	}

	account.LimitBillingAccessToOwners = true
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	sub = fetchSubscription()
	if sub["viewerIsOrgOwner"] != false {
		t.Errorf("viewerIsOrgOwner = %v, want false", sub["viewerIsOrgOwner"])
	}
	if sub["viewerCanModify"] != false {
		t.Errorf("viewerCanModify = %v, want false", sub["viewerCanModify"])
	}
	if sub["limitBillingAccessToOwners"] != true {
		t.Errorf("limitBillingAccessToOwners = %v, want true", sub["limitBillingAccessToOwners"])
	}

	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, "admin"); err != nil {
		t.Fatalf("update membership: %v", err)
	}
	sub = fetchSubscription()
	if sub["viewerIsOrgOwner"] != true {
		t.Errorf("viewerIsOrgOwner = %v, want true", sub["viewerIsOrgOwner"])
	}
	if sub["viewerCanModify"] != true {
		t.Errorf("viewerCanModify = %v, want true", sub["viewerCanModify"])
	}
	if sub["limitBillingAccessToOwners"] != true {
		t.Errorf("limitBillingAccessToOwners = %v, want true", sub["limitBillingAccessToOwners"])
	}
}

// When a subscription exemption is enabled we'll return a null message unless
// one is specified.
//
// We shouldn't be able to subscribe with an exemption
// Ported from test_usage_billing_subscription_exemption.
func TestUsageBillingSubscriptionExemption(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	account.SubscriptionExempt = true
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if !reflect.DeepEqual(body["subscriptionExemption"], map[string]any{"message": nil}) {
		t.Errorf("subscriptionExemption = %v, want {message: null}", body["subscriptionExemption"])
	}
	if body["accountCanSubscribe"] != false {
		t.Errorf("accountCanSubscribe = %v, want false", body["accountCanSubscribe"])
	}

	account.SubscriptionExempt = true
	account.SubscriptionExemptMessage = tstrPtr(
		"As a GitHub Sponsor of Kodiak you have complimentary access.")
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	res = doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/usage_billing", account.ID), nil, loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body = wv1JSON(t, res)
	wantExemption := map[string]any{"message": *account.SubscriptionExemptMessage}
	if !reflect.DeepEqual(body["subscriptionExemption"], wantExemption) {
		t.Errorf("subscriptionExemption = %v, want %v", body["subscriptionExemption"], wantExemption)
	}
	if body["accountCanSubscribe"] != false {
		t.Errorf("accountCanSubscribe = %v, want false", body["accountCanSubscribe"])
	}
}

// Verify that our proration endpoint correctly handles proration.
// Ported from test_fetch_proration.
func TestFetchProration(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.retrieve")
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("GET /v1/invoices/upcoming", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("invoice.upcoming")
		wv1WriteJSON(w, map[string]any{
			"id":     "in_1GN9MwCoyKa1V9Y6Ox2tehjN",
			"object": "invoice",
			"lines": map[string]any{
				"object": "list",
				"data": []any{
					map[string]any{
						"id":     "il_tmp_aad05f08b40091",
						"object": "line_item",
						"amount": 4990,
						"period": map[string]any{"end": 1623632393, "start": 1592096393},
					},
				},
			},
		})
	})
	installFakeStripe(t, mux)

	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/fetch_proration", account.ID),
		[]byte(url.Values{"subscriptionQuantity": {"4"}}.Encode()), loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if body["proratedCost"] != float64(4990) {
		t.Errorf("proratedCost = %v, want 4990", body["proratedCost"])
	}
	prorationTime, ok := body["prorationTime"].(float64)
	if !ok || prorationTime != float64(int64(prorationTime)) {
		t.Errorf("prorationTime = %v, want an integer", body["prorationTime"])
	}
	if got := rec.count("subscription.retrieve"); got != 1 {
		t.Errorf("subscription retrieve call count = %d, want 1", got)
	}
	if got := rec.count("invoice.upcoming"); got != 1 {
		t.Errorf("invoice upcoming call count = %d, want 1", got)
	}
}

// Check support for different plans.
// Ported from test_fetch_proration_different_plans.
func TestFetchProrationDifferentPlans(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")

	// NOTE(port): Python patches StripeCustomerInformation.preview_proration
	// and inspects its kwargs; the Go port asserts the equivalent plan and
	// quantity parameters sent to the Stripe invoice-upcoming endpoint and
	// counts those requests instead.
	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("GET /v1/invoices/upcoming", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("preview_proration")
		rec.set("plan_id", r.URL.Query().Get("subscription_items[0][plan]"))
		rec.set("subscription_quantity", r.URL.Query().Get("subscription_items[0][quantity]"))
		wv1WriteJSON(w, map[string]any{
			"id":     "in_1GN9MwCoyKa1V9Y6Ox2tehjN",
			"object": "invoice",
			"lines": map[string]any{
				"object": "list",
				"data": []any{
					map[string]any{
						"id":     "il_tmp_aad05f08b40091",
						"object": "line_item",
						"amount": 4567,
						// period.start of 0 keeps the line inside the
						// proration window so the fake cost is summed.
						"period": map[string]any{"end": 0, "start": 0},
					},
				},
			},
		})
	})
	installFakeStripe(t, mux)

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)

	// we should default to the "monthly" plan period.
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/fetch_proration", account.ID),
		[]byte(url.Values{"subscriptionQuantity": {"4"}}.Encode()), loginCookie(app, user))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if body["proratedCost"] != float64(4567) {
		t.Errorf("proratedCost = %v, want 4567", body["proratedCost"])
	}
	prorationTime, ok := body["prorationTime"].(float64)
	if !ok || prorationTime != float64(int64(prorationTime)) {
		t.Errorf("prorationTime = %v, want an integer", body["prorationTime"])
	}
	if got := rec.count("preview_proration"); got != 1 {
		t.Errorf("preview_proration call count = %d, want 1", got)
	}
	if got := rec.value("subscription_quantity"); got != "4" {
		t.Errorf("subscription_quantity = %q, want 4", got)
	}
	if got := rec.value("plan_id"); got != app.Cfg.StripePlanID {
		t.Errorf("plan_id = %q, want %q", got, app.Cfg.StripePlanID)
	}

	// verify we respect the `subscriptionPeriod` argument for proration.
	periodPlans := []struct {
		period string
		plan   string
	}{
		{"year", app.Cfg.StripeAnnualPlanID},
		{"month", app.Cfg.StripePlanID},
	}
	for index, periodPlan := range periodPlans {
		seats := index + 5
		res := doRequest(t, app, http.MethodPost,
			fmt.Sprintf("/v1/t/%s/fetch_proration", account.ID),
			[]byte(url.Values{
				"subscriptionQuantity": {strconv.Itoa(seats)},
				"subscriptionPeriod":   {periodPlan.period},
			}.Encode()), loginCookie(app, user))
		if res.Code != 200 {
			t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
		}
		body := wv1JSON(t, res)
		if body["proratedCost"] != float64(4567) {
			t.Errorf("proratedCost = %v, want 4567", body["proratedCost"])
		}
		if got := rec.value("subscription_quantity"); got != strconv.Itoa(seats) {
			t.Errorf("subscription_quantity = %q, want %d", got, seats)
		}
		if got := rec.value("plan_id"); got != periodPlan.plan {
			t.Errorf("plan_id = %q, want %q", got, periodPlan.plan)
		}
		if got := rec.count("preview_proration"); got != index+2 {
			t.Errorf("preview_proration call count = %d, want %d", got, index+2)
		}
	}
}

// Verify that updating an Account's subscription updates Stripe.
// Ported from test_update_subscription.
func TestUpdateSubscription(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	const oneDaySec = int64(60 * 60 * 24)
	periodStart := time.Now().Unix()
	periodEnd := periodStart + 30*oneDaySec // start plus one month.

	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.retrieve")
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("POST /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.modify")
		_ = r.ParseForm()
		rec.set("modify.items.0.plan", r.PostForm.Get("items[0][plan]"))
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("POST /v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("invoice.create")
		wv1WriteJSON(w, map[string]any{"object": "invoice", "id": "in_00000000000000"})
	})
	mux.HandleFunc("GET /v1/payment_methods", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("payment_method.list")
		wv1WriteJSON(w, map[string]any{
			"object":   "list",
			"data":     []any{wv1StripePaymentMethod()},
			"has_more": false,
			"url":      "/v1/payment_methods",
		})
	})
	mux.HandleFunc("POST /v1/invoices/{id}/pay", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("invoice.pay")
		wv1WriteJSON(w, map[string]any{"object": "invoice", "id": r.PathValue("id"), "paid": true})
	})
	installFakeStripe(t, mux)

	if user.GithubLogin == "" {
		t.Fatal("user.GithubLogin should not be empty")
	}
	account := createTestAccount(t, app.DB, "cus_Ged32s2xnx12", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, "admin"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", periodStart, periodEnd)

	if got := rec.count("subscription.retrieve"); got != 0 {
		t.Fatalf("subscription retrieve call count = %d, want 0", got)
	}
	if got := rec.count("subscription.modify"); got != 0 {
		t.Fatalf("subscription modify call count = %d, want 0", got)
	}
	if got := rec.count("payment_method.list"); got != 0 {
		t.Fatalf("payment method list call count = %d, want 0", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call count = %d, want 0", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(url.Values{
			"prorationTimestamp": {strconv.FormatInt(periodStart+4*oneDaySec, 10)},
			"seats":              {"24"},
		}.Encode()), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := rec.count("subscription.retrieve"); got != 1 {
		t.Errorf("subscription retrieve call count = %d, want 1", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call count = %d, want 1", got)
	}
	if got := rec.count("subscription.modify"); got != 1 {
		t.Errorf("subscription modify call count = %d, want 1", got)
	}
	if got := rec.count("payment_method.list"); got != 1 {
		t.Errorf("payment method list call count = %d, want 1", got)
	}
	if got := rec.value("modify.items.0.plan"); got != app.Cfg.StripePlanID {
		t.Errorf("modify items[0][plan] = %q, want %q", got, app.Cfg.StripePlanID)
	}
	if got := rec.count("invoice.create"); got != 1 {
		t.Errorf("invoice create call count = %d, want 1", got)
	}
	if got := rec.count("invoice.pay"); got != 1 {
		t.Errorf("invoice pay call count = %d, want 1", got)
	}

	// verify we can specify the monthly planPeriod and that the API calls Stripe
	// with the correct plan id.
	res = doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(url.Values{
			"prorationTimestamp": {strconv.FormatInt(periodStart+4*oneDaySec, 10)},
			"seats":              {"8"},
			"planPeriod":         {"month"},
		}.Encode()), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := rec.count("subscription.retrieve"); got != 2 {
		t.Errorf("subscription retrieve call count = %d, want 2", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 2 {
		t.Errorf("update_bot call count = %d, want 2", got)
	}
	if got := rec.count("subscription.modify"); got != 2 {
		t.Errorf("subscription modify call count = %d, want 2", got)
	}
	if got := rec.value("modify.items.0.plan"); got != app.Cfg.StripePlanID {
		t.Errorf("modify items[0][plan] = %q, want %q", got, app.Cfg.StripePlanID)
	}
	if got := rec.count("invoice.create"); got != 2 {
		t.Errorf("invoice create call count = %d, want 2", got)
	}
	if got := rec.count("invoice.pay"); got != 2 {
		t.Errorf("invoice pay call count = %d, want 2", got)
	}

	// verify we can specify the yearly planPeriod and that the API calls Stripe
	// with the correct plan id.
	res = doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(url.Values{
			"prorationTimestamp": {strconv.FormatInt(periodStart+4*oneDaySec, 10)},
			"seats":              {"4"},
			"planPeriod":         {"year"},
		}.Encode()), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := rec.count("subscription.retrieve"); got != 3 {
		t.Errorf("subscription retrieve call count = %d, want 3", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 3 {
		t.Errorf("update_bot call count = %d, want 3", got)
	}
	if got := rec.count("subscription.modify"); got != 3 {
		t.Errorf("subscription modify call count = %d, want 3", got)
	}
	if got := rec.value("modify.items.0.plan"); got != app.Cfg.StripeAnnualPlanID {
		t.Errorf("modify items[0][plan] = %q, want %q", got, app.Cfg.StripeAnnualPlanID)
	}
	if got := rec.count("invoice.create"); got != 2 {
		t.Errorf("invoice create call count = %d, want 2: not hit because we're changing plans", got)
	}
	if got := rec.count("invoice.pay"); got != 2 {
		t.Errorf("invoice pay call count = %d, want 2: not hit because we're changing plans", got)
	}
}

// wv1StripeSubscription ports create_stripe_subscription from test_views.py.
//
// NOTE(port): stripe-go v78 reads plan/quantity from the subscription item,
// so the fixture nests them inside items.data[0] in addition to the legacy
// top-level fields used by the Python SDK.
func wv1StripeSubscription(app *App, interval string) map[string]any {
	plan := map[string]any{
		"id":             app.Cfg.StripeAnnualPlanID,
		"object":         "plan",
		"amount":         499,
		"interval":       interval,
		"interval_count": 1,
	}
	return map[string]any{
		"object":               "subscription",
		"id":                   "sub_Gu1xedsfo1",
		"current_period_end":   1690982549,
		"current_period_start": 1688304149,
		"customer":             "cus_f8935e914afa",
		"items": map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{
					"object":   "subscription_item",
					"id":       "si_Gx234091sd2",
					"plan":     plan,
					"quantity": 4,
				},
			},
		},
		"plan":                   plan,
		"quantity":               4,
		"start_date":             1443556775,
		"default_payment_method": "pm_22dldxf3",
	}
}

// When we switch between plans we do _not_ want to manually create an invoice
// because Stripe already charges the user. If we attempt to invoice like we do
// for intra-plan upgrades we'll get an API exception from Stripe saying
// nothing to change.
// Ported from test_update_subscription_switch_plans.
func TestUpdateSubscriptionSwitchPlans(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.retrieve")
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("POST /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.modify")
		_ = r.ParseForm()
		rec.set("modify.items.0.plan", r.PostForm.Get("items[0][plan]"))
		wv1WriteJSON(w, wv1StripeSubscription(app, "year"))
	})
	mux.HandleFunc("POST /v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("invoice.create")
		wv1WriteJSON(w, map[string]any{"object": "invoice", "id": "in_00000000000000"})
	})
	mux.HandleFunc("POST /v1/invoices/{id}/pay", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("invoice.pay")
		wv1WriteJSON(w, map[string]any{"object": "invoice", "id": r.PathValue("id"), "paid": true})
	})
	installFakeStripe(t, mux)

	account := createOrgAccountForUser(t, app.DB, user, "admin", false)
	stripeCustomerInfo := createTestStripeCustomerInfo(t, app.DB,
		account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := rec.count("subscription.retrieve"); got != 0 {
		t.Fatalf("subscription retrieve call count = %d, want 0", got)
	}
	if got := rec.count("subscription.modify"); got != 0 {
		t.Fatalf("subscription modify call count = %d, want 0", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call count = %d, want 0", got)
	}
	if stripeCustomerInfo.PlanInterval == nil || *stripeCustomerInfo.PlanInterval != "month" {
		t.Fatalf("plan_interval = %v, want month", stripeCustomerInfo.PlanInterval)
	}

	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(url.Values{
			"prorationTimestamp": {"123456789"},
			"seats":              {"4"},
			"planPeriod":         {"year"},
		}.Encode()), loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := rec.count("subscription.retrieve"); got != 1 {
		t.Errorf("subscription retrieve call count = %d, want 1", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call count = %d, want 1", got)
	}
	if got := rec.count("subscription.modify"); got != 1 {
		t.Errorf("subscription modify call count = %d, want 1", got)
	}
	if got := rec.value("modify.items.0.plan"); got != app.Cfg.StripeAnnualPlanID {
		t.Errorf("modify items[0][plan] = %q, want %q", got, app.Cfg.StripeAnnualPlanID)
	}
	if got := rec.count("invoice.create"); got != 0 {
		t.Errorf("invoice create call count = %d, want 0: not hit because we're changing plans", got)
	}
	if got := rec.count("invoice.pay"); got != 0 {
		t.Errorf("invoice pay call count = %d, want 0: not hit because we're changing plans", got)
	}
	refreshed, err := models.GetStripeCustomerInfo(ctx, app.DB, stripeCustomerInfo.CustomerID)
	if err != nil || refreshed == nil {
		t.Fatalf("refresh stripe customer info: %v", err)
	}
	if refreshed.PlanInterval == nil || *refreshed.PlanInterval != "year" {
		t.Errorf("plan_interval = %v, want year", refreshed.PlanInterval)
	}
}

// We should get an error when we try to update a subscription for an Account
// that doesn't have an associated subscription.
// Ported from test_update_subscription_missing_customer.
func TestUpdateSubscriptionMissingCustomer(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.retrieve")
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	mux.HandleFunc("POST /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.modify")
		wv1WriteJSON(w, wv1StripeSubscription(app, "month"))
	})
	installFakeStripe(t, mux)

	if user.GithubLogin == "" {
		t.Fatal("user.GithubLogin should not be empty")
	}
	account := createTestAccount(t, app.DB, "cus_Ged32s2xnx12", user.GithubLogin, models.AccountTypeUser)
	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, "admin"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Fatalf("StripeCustomerInformation count = %d, want 0: we shouldn't have an associated subscription for this test.", got)
	}
	if got := rec.count("subscription.retrieve"); got != 0 {
		t.Fatalf("subscription retrieve call count = %d, want 0", got)
	}
	if got := rec.count("subscription.modify"); got != 0 {
		t.Fatalf("subscription modify call count = %d, want 0", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(url.Values{
			"prorationTimestamp": {"1650581784"},
			"seats":              {"24"},
		}.Encode()), loginCookie(app, user))
	if res.Code != 422 {
		t.Fatalf("status = %d, want 422: %s", res.Code, res.Body.String())
	}
	body := wv1JSON(t, res)
	if body["message"] != "Subscription does not exist to modify." {
		t.Errorf("message = %v, want %q", body["message"], "Subscription does not exist to modify.")
	}
	if got := rec.count("subscription.retrieve"); got != 0 {
		t.Errorf("subscription retrieve call count = %d, want 0", got)
	}
	if got := rec.count("subscription.modify"); got != 0 {
		t.Errorf("subscription modify call count = %d, want 0", got)
	}
}

// By default any member can edit a subscription, but if
// `limit_billing_access_to_owners` is enabled on an account, only admins a.k.a
// owners can modify a subscription.
// Ported from test_update_subscription_permissions.
func TestUpdateSubscriptionPermissions(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	// NOTE(port): Python uses no Stripe mocks here because every request
	// returns before reaching Stripe; a catch-all failing handler keeps the
	// Go test hermetic if that ever regresses.
	installFakeStripe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error": {"message": "unexpected stripe call"}}`, 500)
	}))

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	payload := url.Values{
		"prorationTimestamp": {"1650581784"},
		"seats":              {"24"},
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(payload.Encode()), loginCookie(app, user))
	if res.Code != 422 {
		t.Fatalf("status = %d, want 422: we get a 422 because the account doesn't have a corresponding Stripe model. This is okay. body=%s",
			res.Code, res.Body.String())
	}

	account.LimitBillingAccessToOwners = true
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	res = doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(payload.Encode()), loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: we're a member so we shouldn't be allowed. body=%s",
			res.Code, res.Body.String())
	}

	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, "admin"); err != nil {
		t.Fatalf("update membership: %v", err)
	}
	res = doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/update_subscription", account.ID),
		[]byte(payload.Encode()), loginCookie(app, user))
	if res.Code != 422 {
		t.Fatalf("status = %d, want 422: we're an admin so we should be okay. body=%s",
			res.Code, res.Body.String())
	}
}

// Canceling a subscription should immediately cancel the subscription in Stripe.
// Ported from test_cancel_subscription.
func TestCancelSubscription(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user

	rec := wv1NewRecorder()
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		rec.inc("subscription.delete")
		wv1WriteJSON(w, map[string]any{
			"object": "subscription",
			"id":     r.PathValue("id"),
			"status": "canceled",
		})
	})
	installFakeStripe(t, mux)

	account := createOrgAccountForUser(t, app.DB, user, "admin", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := rec.count("subscription.delete"); got != 0 {
		t.Fatalf("subscription delete call count = %d, want 0", got)
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	if got := wv1UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call count = %d, want 0", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/cancel_subscription", account.ID), nil, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := wv1UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call count = %d, want 1", got)
	}
	if got := rec.count("subscription.delete"); got != 1 {
		t.Errorf("subscription delete call count = %d, want 1", got)
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Errorf("StripeCustomerInformation count = %d, want 0", got)
	}
}

// wv1PatchCancelSubscription ports the patch_cancel_subscription fixture:
// stub the Stripe Subscription.delete endpoint (Account.update_bot runs for
// real against miniredis).
func wv1PatchCancelSubscription(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		wv1WriteJSON(w, map[string]any{
			"object": "subscription",
			"id":     r.PathValue("id"),
			"status": "canceled",
		})
	})
	installFakeStripe(t, mux)
}

// By default all members can cancel a subscription.
// Ported from test_cancel_subscription_member.
func TestCancelSubscriptionMember(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user
	wv1PatchCancelSubscription(t)

	account := createOrgAccountForUser(t, app.DB, user, "member", false)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/cancel_subscription", account.ID), nil, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Errorf("StripeCustomerInformation count = %d, want 0", got)
	}
}

// If limit_billing_access_to_owners is enabled members cannot cancel a
// subscription.
// Ported from test_cancel_subscription_member_limit_billing_access_to_owners.
func TestCancelSubscriptionMemberLimitBillingAccessToOwners(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user
	wv1PatchCancelSubscription(t)

	account := createOrgAccountForUser(t, app.DB, user, "member", true)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/cancel_subscription", account.ID), nil, loginCookie(app, user))
	if res.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", res.Code, res.Body.String())
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Errorf("StripeCustomerInformation count = %d, want 1", got)
	}
}

// If limit_billing_access_to_owners is enabled admins can cancel a
// subscription.
// Ported from test_cancel_subscription_admin_limit_billing_access_to_owners.
func TestCancelSubscriptionAdminLimitBillingAccessToOwners(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	_ = createTestUser(t, app.DB, 67647, "bear", "D2F92D26-BC64-427C-93CC-13E7110F3EB7") // other_user
	wv1PatchCancelSubscription(t)

	account := createOrgAccountForUser(t, app.DB, user, "admin", true)
	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID, "sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	res := doRequest(t, app, http.MethodPost,
		fmt.Sprintf("/v1/t/%s/cancel_subscription", account.ID), nil, loginCookie(app, user))
	if res.Code != 204 {
		t.Fatalf("status = %d, want 204: %s", res.Code, res.Body.String())
	}
	if got := wv1StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Errorf("StripeCustomerInformation count = %d, want 0", got)
	}
}
