// Port of web_api/web_api/test_account.py (except test_account_can_subscribe,
// which lives in internal/webapi/models/models_test.go).
package webapi

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// acDate is the Go analogue of make_aware(datetime.datetime(y, m, d)).
func acDate(year int, month time.Month, day int) *time.Time {
	d := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	return &d
}

// acCreateAccount ports the file-local create_account factory from
// test_account.py. The Python default for trialExpiration is 1900-02-13;
// callers pass it explicitly.
func acCreateAccount(t *testing.T, db *pgxpool.Pool, trialExpiration *time.Time) *models.Account {
	t.Helper()
	ctx := context.Background()
	account, err := models.CreateAccount(ctx, db, 1066615, 523412234, "acme-corp", "Organization")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	account.StripeCustomerID = "cus_H2pvQ2kt7nk0JY"
	account.TrialExpiration = trialExpiration
	if err := models.SaveAccount(ctx, db, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	return account
}

// acSeedActiveUsers ports the patched_get_active_users_in_last_30_days
// fixture. The Python fixture patches the query to return five canned
// ActiveUsers; the Go port seeds real user_pull_request_activity rows that
// make GetActiveUsersInLast30Days return five active users.
// NOTE(port): the Python mock includes a duplicate github_id (alpha and
// bravo both use id 1), which the real SQL would collapse via GROUP BY
// github_user_id, so the seeded rows use distinct ids 1-5 instead.
func acSeedActiveUsers(t *testing.T, db *pgxpool.Pool, installationID int64) {
	t.Helper()
	ctx := context.Background()
	logins := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for i, login := range logins {
		// Increasing first-activity dates keep the ordering (ORDER BY
		// min(activity_date) ASC) deterministic: alpha first, echo last.
		activityDate := time.Now().AddDate(0, 0, -10+i)
		prNumber := 100 + i
		// Pull request author activity (the row matched as table `a`).
		_, err := db.Exec(ctx, `
INSERT INTO user_pull_request_activity (
    id, github_installation_id, github_repository_name, github_pull_request_number,
    github_user_login, github_user_id, is_private_repository, opened_pull_request, activity_date)
VALUES ($1, $2, 'acme-web', $3, $4, $5, TRUE, TRUE, $6)`,
			uuid.New(), installationID, prNumber, login, i+1, activityDate)
		if err != nil {
			t.Fatalf("insert activity for %s: %v", login, err)
		}
		// Kodiak bot activity on the same pull request (the row matched as
		// table `b`).
		_, err = db.Exec(ctx, `
INSERT INTO user_pull_request_activity (
    id, github_installation_id, github_repository_name, github_pull_request_number,
    github_user_login, github_user_id, is_private_repository, opened_pull_request, activity_date)
VALUES ($1, $2, 'acme-web', $3, 'kodiakhq[bot]', 11479, TRUE, FALSE, $4)`,
			uuid.New(), installationID, prNumber, activityDate)
		if err != nil {
			t.Fatalf("insert bot activity for pr %d: %v", prNumber, err)
		}
	}
}

// acGetBlocker calls app.getSubscriptionBlocker, failing the test on error.
func acGetBlocker(t *testing.T, app *App, account *models.Account) *subscriptionBlocker {
	t.Helper()
	blocker, err := app.getSubscriptionBlocker(context.Background(), account)
	if err != nil {
		t.Fatalf("get subscription blocker: %v", err)
	}
	return blocker
}

// acActiveUsers ports UserPullRequestActivity.get_active_users_in_last_30_days
// lookups, failing the test on error.
func acActiveUsers(t *testing.T, app *App, account *models.Account) []models.ActiveUser {
	t.Helper()
	users, err := models.GetActiveUsersInLast30Days(context.Background(), app.DB, account.GithubInstallationID)
	if err != nil {
		t.Fatalf("get active users: %v", err)
	}
	return users
}

// TestUpdateBot ports test_update_bot.
//
// Should update subscription information in Redis
func TestUpdateBot(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	// NOTE(port): the Python test flushes the shared Redis; newTestApp
	// provides a fresh miniredis per test.
	account := acCreateAccount(t, app.DB, nil)

	key := fmt.Sprintf("kodiak:subscription:%d", account.GithubInstallationID)
	before, err := app.Redis.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatalf("hgetall: %v", err)
	}
	if len(before) != 0 {
		t.Errorf("expected empty subscription hash before update_bot, got %v", before)
	}
	exists, err := app.Redis.Exists(ctx, "kodiak:refresh_pull_requests_for_installation").Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 0 {
		t.Errorf("expected refresh queue to not exist, got exists=%d", exists)
	}

	if err := app.updateBot(ctx, account); err != nil {
		t.Fatalf("update bot: %v", err)
	}

	after, err := app.Redis.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatalf("hgetall: %v", err)
	}
	want := map[string]string{
		"account_id":           account.ID.String(),
		"data":                 "",
		"subscription_blocker": "",
	}
	if !reflect.DeepEqual(after, want) {
		t.Errorf("subscription hash = %v, want %v", after, want)
	}
	exists, err = app.Redis.Exists(ctx, "kodiak:refresh_pull_requests_for_installation").Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 1 {
		t.Errorf("expected refresh queue to exist, got exists=%d", exists)
	}
	items, err := app.Redis.LRange(ctx, "kodiak:refresh_pull_requests_for_installation", 0, -1).Result()
	if err != nil {
		t.Fatalf("lrange: %v", err)
	}
	// NOTE(port): Python's pydantic .json() renders
	// {"installation_id": "..."} with a space after the colon; Go's
	// json.Marshal does not.
	wantMsg := fmt.Sprintf(`{"installation_id":"%d"}`, account.GithubInstallationID)
	if len(items) != 1 || items[0] != wantMsg {
		t.Errorf("refresh queue = %v, want [%s]", items, wantMsg)
	}
}

// TestGetSubscriptionBlockerOk ports test_get_subscription_blocker_ok.
func TestGetSubscriptionBlockerOk(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, nil)
	if blocker := acGetBlocker(t, app, account); blocker != nil {
		t.Errorf("expected nil blocker, got %+v", blocker)
	}
}

// TestGetSubscriptionBlockerSubscriptionExpired ports
// test_get_subscription_blocker_subscription_expired.
func TestGetSubscriptionBlockerSubscriptionExpired(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, nil)
	stripeCustomerInformation := createTestStripeCustomerInfo(t, app.DB,
		"cus_H2pvQ2kt7nk0JY", "sub_Gu1xedsfo1", 0, 100)

	if !stripeCustomerInformation.Expired() {
		t.Errorf("expected stripe customer information to be expired")
	}
	blocker := acGetBlocker(t, app, account)
	if blocker == nil {
		t.Fatalf("expected blocker, got nil")
	}
	if blocker.Kind != blockerSubscriptionExpired {
		t.Errorf("blocker kind = %q, want %q", blocker.Kind, blockerSubscriptionExpired)
	}
}

// TestGetSubscriptionBlockerTrialExpired ports
// test_get_subscription_blocker_trial_expired.
func TestGetSubscriptionBlockerTrialExpired(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, acDate(1900, time.February, 13))
	if !account.TrialExpired() {
		t.Errorf("expected trial to be expired")
	}
	blocker := acGetBlocker(t, app, account)
	if blocker == nil {
		t.Fatalf("expected blocker, got nil")
	}
	if blocker.Kind != blockerTrialExpired {
		t.Errorf("blocker kind = %q, want %q", blocker.Kind, blockerTrialExpired)
	}
}

// TestGetSubscriptionBlockerSeatsExceeded ports
// test_get_subscription_blocker_seats_exceeded.
// NOTE(port): the Python mock call_count assertions have no analogue here;
// the Go port seeds real database rows instead of patching the query.
func TestGetSubscriptionBlockerSeatsExceeded(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, nil)
	acSeedActiveUsers(t, app.DB, account.GithubInstallationID)
	stripeCustomerInformation := createTestStripeCustomerInfo(t, app.DB,
		"cus_H2pvQ2kt7nk0JY", "sub_Gu1xedsfo1", 0, 1987081359)

	if stripeCustomerInformation.Expired() {
		t.Errorf("expected stripe customer information to not be expired")
	}
	if got := len(acActiveUsers(t, app, account)); got != 5 {
		t.Errorf("active users = %d, want 5", got)
	}
	blocker := acGetBlocker(t, app, account)
	if blocker == nil {
		t.Fatalf("expected blocker, got nil")
	}
	if blocker.Kind != blockerSeatsExceeded {
		t.Errorf("blocker kind = %q, want %q", blocker.Kind, blockerSeatsExceeded)
	}
}

// TestGetSubscriptionBlockerSeatsExceededNoSubOrTrial ports
// test_get_subscription_blocker_seats_exceeded_no_sub_or_trial.
//
// If an account has active users but no trial or subscription, that should
// trigger the paywall when the active user count has been exceeded.
func TestGetSubscriptionBlockerSeatsExceededNoSubOrTrial(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, nil)
	acSeedActiveUsers(t, app.DB, account.GithubInstallationID)
	if account.GithubAccountType != models.AccountTypeOrganization {
		t.Fatalf("account type = %q, want %q", account.GithubAccountType, models.AccountTypeOrganization)
	}
	blocker := acGetBlocker(t, app, account)
	if blocker == nil {
		t.Fatalf("expected blocker, got nil")
	}
	if blocker.Kind != blockerSeatsExceeded {
		t.Errorf("blocker kind = %q, want %q", blocker.Kind, blockerSeatsExceeded)
	}
	if got := len(acActiveUsers(t, app, account)); got != 5 {
		t.Errorf("active users = %d, want 5", got)
	}

	account.GithubAccountType = models.AccountTypeUser
	if err := models.SaveAccount(context.Background(), app.DB, account); err != nil {
		t.Fatalf("save account: %v", err)
	}
	if blocker := acGetBlocker(t, app, account); blocker != nil {
		t.Errorf("expected nil blocker for user account, got %+v", blocker)
	}
}

// TestGetSubscriptionBlockerSeatsExceededNoSubOrTrialNoActivity ports
// test_get_subscription_blocker_seats_exceeded_no_sub_or_trial_no_activity.
//
// If an account has no trial or subscription, but also no active users, we
// should not raise the paywall.
func TestGetSubscriptionBlockerSeatsExceededNoSubOrTrialNoActivity(t *testing.T) {
	app, _ := newTestApp(t)
	// NOTE(port): the Python test patches the active-user query to return
	// []; the Go port simply seeds no activity rows.
	account := acCreateAccount(t, app.DB, nil)
	if blocker := acGetBlocker(t, app, account); blocker != nil {
		t.Errorf("expected nil blocker, got %+v", blocker)
	}
	if got := len(acActiveUsers(t, app, account)); got != 0 {
		t.Errorf("active users = %d, want 0", got)
	}
}

// TestGetSubscriptionBlockerSeatsExceededWithTrial ports
// test_get_subscription_blocker_seats_exceeded_with_trial.
//
// If an account has active users but is on the trial we should allow them
// full access.
func TestGetSubscriptionBlockerSeatsExceededWithTrial(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, acDate(2100, time.February, 13))
	acSeedActiveUsers(t, app.DB, account.GithubInstallationID)
	if !account.ActiveTrial() {
		t.Errorf("expected active trial")
	}
	if got := len(acActiveUsers(t, app, account)); got != 5 {
		t.Errorf("active users = %d, want 5", got)
	}
	if blocker := acGetBlocker(t, app, account); blocker != nil {
		t.Errorf("expected nil blocker, got %+v", blocker)
	}
}

// TestGetSubscriptionBlockerExpiredTrialSubscriptionOk ports
// test_get_subscription_blocker_expired_trial_subscription_ok.
//
// If an account has a trial that is expired, but their subscription is
// valid, we should not raise the paywall.
func TestGetSubscriptionBlockerExpiredTrialSubscriptionOk(t *testing.T) {
	app, _ := newTestApp(t)
	account := acCreateAccount(t, app.DB, acDate(1900, time.February, 13))
	acSeedActiveUsers(t, app.DB, account.GithubInstallationID)
	// NOTE(port): the Python test uses subscription_quantity=10, while the
	// harness factory hardcodes 3 (the test_views.py default), so bump the
	// quantity after creation.
	info := createTestStripeCustomerInfo(t, app.DB,
		"cus_H2pvQ2kt7nk0JY", "sub_Gu1xedsfo1", 0, 1987081359)
	info.SubscriptionQuantity = 10
	if err := models.SaveStripeCustomerInfo(context.Background(), app.DB, info); err != nil {
		t.Fatalf("save stripe customer info: %v", err)
	}
	if !account.TrialExpired() {
		t.Errorf("expected trial to be expired")
	}
	if blocker := acGetBlocker(t, app, account); blocker != nil {
		t.Errorf("expected nil blocker, got %+v", blocker)
	}
}

// TestCanEditSubscription ports test_can_edit_subscription. The Python
// create_user factory creates github_id=2341234, github_login="b-lowe".
func TestCanEditSubscription(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	account := acCreateAccount(t, app.DB, acDate(1900, time.February, 13))
	user := createTestUser(t, app.DB, 2341234, "b-lowe", "")

	account.LimitBillingAccessToOwners = false
	canEdit, err := app.canEditSubscription(ctx, user, account)
	if err != nil {
		t.Fatalf("can edit subscription: %v", err)
	}
	if !canEdit {
		t.Errorf("expected user to be able to edit subscription when billing access is unrestricted")
	}

	account.LimitBillingAccessToOwners = true
	canEdit, err = app.canEditSubscription(ctx, user, account)
	if err != nil {
		t.Fatalf("can edit subscription: %v", err)
	}
	if canEdit {
		t.Errorf("expected non-member to not be able to edit subscription")
	}

	account.LimitBillingAccessToOwners = true
	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, models.RoleMember); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	canEdit, err = app.canEditSubscription(ctx, user, account)
	if err != nil {
		t.Fatalf("can edit subscription: %v", err)
	}
	if canEdit {
		t.Errorf("expected member to not be able to edit subscription")
	}

	account.LimitBillingAccessToOwners = true
	if err := models.UpsertMembership(ctx, app.DB, account.ID, user.ID, models.RoleAdmin); err != nil {
		t.Fatalf("update membership: %v", err)
	}
	canEdit, err = app.canEditSubscription(ctx, user, account)
	if err != nil {
		t.Fatalf("can edit subscription: %v", err)
	}
	if !canEdit {
		t.Errorf("expected admin to be able to edit subscription")
	}
}
