// Ported from web_api/web_api/test_stripe_views.py.
package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// svCreateAccount ports create_account from test_stripe_views.py.
func svCreateAccount(t *testing.T, db *pgxpool.Pool) (*models.User, *models.Account) {
	t.Helper()
	ctx := context.Background()
	user := createTestUser(t, db, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	account, err := models.CreateAccount(ctx, db, 377930, 900966, user.GithubLogin, models.AccountTypeUser)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := models.UpsertMembership(ctx, db, account.ID, user.ID, "member"); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	return user, account
}

// svCreateActiveUser ports create_active_user: a user activity row plus the
// matching kodiak[bot] row for the same repo/PR, dated today.
func svCreateActiveUser(t *testing.T, db *pgxpool.Pool, installationID, userID, prNumber int64) {
	t.Helper()
	ctx := context.Background()
	insert := func(login string, githubUserID int64) {
		_, err := db.Exec(ctx, `
INSERT INTO user_pull_request_activity (
    id, created_at, modified_at, github_installation_id, github_repository_name,
    github_pull_request_number, github_user_login, github_user_id,
    is_private_repository, opened_pull_request, activity_date)
VALUES ($1, NOW(), NOW(), $2, 'acme_web', $3, $4, $5, TRUE, TRUE, NOW()::date)`,
			uuid.New(), installationID, prNumber, login, githubUserID)
		if err != nil {
			t.Fatalf("insert user activity: %v", err)
		}
	}
	insert(fmt.Sprintf("acme-user-%d", userID), userID)
	insert("kodiak[bot]", 0)
}

func svSubscriptionInfo(t *testing.T, app *App, user *models.User, account *models.Account) (int, map[string]any) {
	t.Helper()
	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/subscription_info", account.ID), nil, loginCookie(app, user))
	var body map[string]any
	if res.Code == 200 {
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal subscription_info body: %v", err)
		}
	}
	return res.Code, body
}

// Smoke test to ensure the redirect URL for the stripe billing portal works
// in the success case.
// Ported from test_stripe_self_serve_redirect_view.
func TestStripeSelfServeRedirectView(t *testing.T) {
	app, _ := newTestApp(t)
	const portalURL = "https://billing.stripe.com/session/O4pTob2jXrlVdYdh1grBH1mXJiOIDgwS"

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/billing_portal/sessions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created":    1588895592,
			"customer":   "cus_HEmOJM4AntPHdz",
			"id":         "bps_1GgJWeCoyKa1V9Y6Bfnab1L3",
			"livemode":   false,
			"object":     "billing_portal.session",
			"return_url": "http://app.localhost.kodiakhq.com:3000/t/134f9ff9-327b-4cb3-a0b3-edf63f23a96e/usage",
			"url":        portalURL,
		})
	})
	installFakeStripe(t, mux)

	user, account := svCreateAccount(t, app.DB)

	res := doRequest(t, app, http.MethodGet,
		fmt.Sprintf("/v1/t/%s/stripe_self_serve_redirect", account.ID), nil, loginCookie(app, user))
	if res.Code != 302 {
		t.Fatalf("status = %d, want 302: %s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Location"); got != portalURL {
		t.Errorf("Location = %q, want %q", got, portalURL)
	}
}

// all personal accounts have valid subscriptions
// Ported from test_get_subscription_info_view_valid_account_personal_user.
func TestGetSubscriptionInfoViewValidAccountPersonalUser(t *testing.T) {
	app, _ := newTestApp(t)
	user, account := svCreateAccount(t, app.DB)
	account.GithubAccountType = models.AccountTypeUser
	if err := models.SaveAccount(context.Background(), app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	svCreateActiveUser(t, app.DB, account.GithubInstallationID, user.GithubID, 2)

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 1 || body["type"] != "VALID_SUBSCRIPTION" {
		t.Errorf("body = %v, want {type: VALID_SUBSCRIPTION}", body)
	}
}

// trial users should have a valid subscription
// Ported from test_get_subscription_info_view_valid_account_trial_user.
func TestGetSubscriptionInfoViewValidAccountTrialUser(t *testing.T) {
	app, _ := newTestApp(t)
	user, account := svCreateAccount(t, app.DB)
	account.GithubAccountType = models.AccountTypeOrganization
	trialExpiration := time.Now().Add(10 * 24 * time.Hour)
	account.TrialExpiration = &trialExpiration
	if err := models.SaveAccount(context.Background(), app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if !account.ActiveTrial() {
		t.Fatal("account should have an active trial")
	}

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 1 || body["type"] != "VALID_SUBSCRIPTION" {
		t.Errorf("body = %v, want {type: VALID_SUBSCRIPTION}", body)
	}
}

// valid subscription user, aka they aren't over their subscribed number of seats
// Ported from test_get_subscription_info_view_valid_account_subscription_user.
func TestGetSubscriptionInfoViewValidAccountSubscriptionUser(t *testing.T) {
	app, _ := newTestApp(t)
	user, account := svCreateAccount(t, app.DB)
	account.GithubAccountType = models.AccountTypeOrganization
	account.TrialExpiration = nil
	if err := models.SaveAccount(context.Background(), app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if account.ActiveTrial() || account.TrialExpired() {
		t.Fatal("account should have no trial state")
	}

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 1 || body["type"] != "VALID_SUBSCRIPTION" {
		t.Errorf("body = %v, want {type: VALID_SUBSCRIPTION}", body)
	}
}

// ensure trial expiration is handled explicitly in the response
// Ported from test_get_subscription_info_view_trial_expired.
func TestGetSubscriptionInfoViewTrialExpired(t *testing.T) {
	app, _ := newTestApp(t)
	user, account := svCreateAccount(t, app.DB)
	account.GithubAccountType = models.AccountTypeOrganization
	trialExpiration := time.Now().Add(-10 * 24 * time.Hour)
	account.TrialExpiration = &trialExpiration
	if err := models.SaveAccount(context.Background(), app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if account.ActiveTrial() || !account.TrialExpired() {
		t.Fatal("account trial should be expired")
	}

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 1 || body["type"] != "TRIAL_EXPIRED" {
		t.Errorf("body = %v, want {type: TRIAL_EXPIRED}", body)
	}
}

// response when user's account subscription has expired
// Ported from test_get_subscription_info_view_subscription_expired.
func TestGetSubscriptionInfoViewSubscriptionExpired(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user, account := svCreateAccount(t, app.DB)
	info := createTestStripeCustomerInfo(t, app.DB, "cus_H2pvQ2kt7nk0JY", "sub_Gu1xedsfo1", 0, 100)
	account.StripeCustomerID = info.CustomerID
	account.GithubAccountType = models.AccountTypeOrganization
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}

	// bunch of checks to ensure we don't hit the other subscription cases
	customerInfo, err := models.GetStripeCustomerInfo(ctx, app.DB, account.StripeCustomerID)
	if err != nil || customerInfo == nil {
		t.Fatalf("stripe customer info missing: %v", err)
	}
	if !customerInfo.Expired() {
		t.Fatal("customer info should be expired")
	}
	if account.ActiveTrial() {
		t.Fatal("account should not have an active trial")
	}

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	if len(body) != 1 || body["type"] != "SUBSCRIPTION_EXPIRED" {
		t.Errorf("body = %v, want {type: SUBSCRIPTION_EXPIRED}", body)
	}
}

// check the API response when a user's account has exceeded the number of
// seats
// Ported from test_get_subscription_info_view_subscription_overage.
func TestGetSubscriptionInfoViewSubscriptionOverage(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user, account := svCreateAccount(t, app.DB)
	info := createTestStripeCustomerInfo(t, app.DB, "cus_H2pvQ2kt7nk0JY", "sub_Gu1xedsfo1", 0, 100)
	account.StripeCustomerID = info.CustomerID
	account.GithubAccountType = models.AccountTypeOrganization
	if err := models.SaveAccount(ctx, app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	for i := int64(1); i < 6; i++ {
		svCreateActiveUser(t, app.DB, account.GithubInstallationID, i, i)
	}

	if account.ActiveTrial() {
		t.Fatal("account should not have an active trial")
	}
	activeUsers, err := models.GetActiveUsersInLast30Days(ctx, app.DB, account.GithubInstallationID)
	if err != nil {
		t.Fatalf("GetActiveUsersInLast30Days: %v", err)
	}
	if int64(len(activeUsers)) <= info.SubscriptionQuantity {
		t.Fatalf("active users (%d) should exceed subscription quantity (%d)",
			len(activeUsers), info.SubscriptionQuantity)
	}

	code, body := svSubscriptionInfo(t, app, user, account)
	if code != 200 {
		t.Fatalf("status = %d, want 200", code)
	}
	want := map[string]any{
		"type":            "SUBSCRIPTION_OVERAGE",
		"activeUserCount": float64(5),
		"licenseCount":    float64(3),
	}
	if body["type"] != want["type"] || body["activeUserCount"] != want["activeUserCount"] || body["licenseCount"] != want["licenseCount"] {
		t.Errorf("body = %v, want %v", body, want)
	}
}
