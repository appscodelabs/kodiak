// Test harness for the web-api view tests, porting the helpers from
// web_api/web_api/testutils.py and the factories at the top of
// web_api/web_api/test_views.py. DB-backed tests skip unless
// TEST_DATABASE_URL is set (see internal/webapi/dbtest).
package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	stripesdk "github.com/stripe/stripe-go/v78"

	"github.com/appscodelabs/kodiak/internal/webapi/dbtest"
	"github.com/appscodelabs/kodiak/internal/webapi/models"
	"github.com/appscodelabs/kodiak/internal/webapi/session"
	"github.com/appscodelabs/kodiak/internal/webapi/settings"
)

// testPK ports the PRIMARY_KEYS iterator from test_views.py.
var testPK int64 = 1000

func nextPK() int64 { return atomic.AddInt64(&testPK, 1) }

func tstrPtr(s string) *string { return &s }

// newTestApp builds an App wired to the test database, a miniredis instance,
// and a Django-test-like settings configuration.
func newTestApp(t *testing.T) (*App, *miniredis.Miniredis) {
	t.Helper()
	db := dbtest.New(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	// values mirror the pytest env in web_api/pyproject.toml plus the
	// outputs of settings.Load (debug secret key, authed landing path).
	cfg := &settings.Config{
		Debug:                       true,
		SecretKey:                   "gjzyca8n8$gg^d9u-ts6x5_+=)x*h!=ae&y$%m*ecsjly&*8j3",
		KodiakAPIGithubClientID:     "Iv1.111FAKECLIENTID111",
		KodiakAPIGithubClientSecret: "888INVALIDSECRET8888",
		KodiakWebAppURL:             "https://app.kodiakhq.com/",
		KodiakWebAuthedLandingPath:  "https://app.kodiakhq.com/oauth",
		StripeSecretKey:             "sk_someStripeSecretKey",
		StripePlanID:                "plan_somePlanId",
		StripeAnnualPlanID:          "price_annualPlanId",
		StripeWebhookSecret:         "whsec_someWebhookSecret",
		StripePublishableAPIKey:     "pk_test_someExampleStripeApiKey",
	}
	app := &App{
		Cfg:        cfg,
		DB:         db,
		Redis:      rdb,
		Sessions:   session.New(cfg.SecretKey, !cfg.Debug),
		HTTPClient: &http.Client{},
	}
	return app, mr
}

// createTestUser ports the `user` / `other_user` pytest fixtures.
func createTestUser(t *testing.T, db *pgxpool.Pool, githubID int64, login, accessToken string) *models.User {
	t.Helper()
	u, err := models.CreateUser(context.Background(), db, githubID, login, accessToken)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

// createTestAccount ports create_account from test_views.py. The Python
// defaults are stripeCustomerID="cus_523405923045", login="acme-corp",
// accountType="User"; callers pass them explicitly.
func createTestAccount(t *testing.T, db *pgxpool.Pool, stripeCustomerID, githubAccountLogin, githubAccountType string) *models.Account {
	t.Helper()
	ctx := context.Background()
	a, err := models.CreateAccount(ctx, db, nextPK(), nextPK(), githubAccountLogin, githubAccountType)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	a.StripeCustomerID = stripeCustomerID
	if err := models.SaveAccount(ctx, db, a); err != nil {
		t.Fatalf("save account: %v", err)
	}
	return a
}

// createOrgAccountForUser ports create_org_account from test_views.py.
func createOrgAccountForUser(t *testing.T, db *pgxpool.Pool, user *models.User, role string, limitBillingAccessToOwners bool) *models.Account {
	t.Helper()
	ctx := context.Background()
	accountID := nextPK()
	a, err := models.CreateAccount(ctx, db, nextPK(), accountID,
		fmt.Sprintf("Acme-corp-%d", accountID), models.AccountTypeOrganization)
	if err != nil {
		t.Fatalf("create org account: %v", err)
	}
	a.StripeCustomerID = fmt.Sprintf("cus_Ged32s2xnx12-%d", accountID)
	a.LimitBillingAccessToOwners = limitBillingAccessToOwners
	if err := models.SaveAccount(ctx, db, a); err != nil {
		t.Fatalf("save org account: %v", err)
	}
	if err := models.UpsertMembership(ctx, db, a.ID, user.ID, role); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	return a
}

// createTestStripeCustomerInfo ports create_stripe_customer_info from
// test_views.py. Python defaults: customerID="cus_eG4134df",
// subscriptionID="sub_Gu1xedsfo1", periodStart=1650581784,
// periodEnd=1658357784.
func createTestStripeCustomerInfo(t *testing.T, db *pgxpool.Pool, customerID, subscriptionID string, periodStart, periodEnd int64) *models.StripeCustomerInformation {
	t.Helper()
	info := &models.StripeCustomerInformation{
		CustomerID:                     customerID,
		SubscriptionID:                 subscriptionID,
		PlanID:                         "plan_G2df31A4G5JzQ",
		PaymentMethodID:                "pm_22dldxf3",
		CustomerEmail:                  "accounting@acme-corp.com",
		CustomerBalance:                0,
		CustomerCreated:                1585781308,
		PaymentMethodCardBrand:         tstrPtr("mastercard"),
		PaymentMethodCardExpMonth:      tstrPtr("03"),
		PaymentMethodCardExpYear:       tstrPtr("32"),
		PaymentMethodCardLast4:         tstrPtr("4242"),
		PlanAmount:                     499,
		PlanInterval:                   tstrPtr("month"),
		SubscriptionQuantity:           3,
		SubscriptionStartDate:          1585781784,
		SubscriptionCurrentPeriodStart: periodStart,
		SubscriptionCurrentPeriodEnd:   periodEnd,
	}
	if err := models.SaveStripeCustomerInfo(context.Background(), db, info); err != nil {
		t.Fatalf("save stripe customer info: %v", err)
	}
	return info
}

// loginCookie ports TestClient.login from testutils.py: it produces the
// signed session cookie identifying the user.
func loginCookie(app *App, user *models.User) *http.Cookie {
	rec := httptest.NewRecorder()
	app.Sessions.Login(rec, user.ID.String())
	return rec.Result().Cookies()[0]
}

// doRequest performs a request against the app's router. body may be nil,
// []byte, or any JSON-marshalable value (sent as application/json, like the
// Django test client's content_type="application/json").
func doRequest(t *testing.T, app *App, method, target string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case []byte:
		reader = bytes.NewReader(b)
		contentType = "application/x-www-form-urlencoded"
	default:
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
		contentType = "application/json"
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec
}

// installFakeStripe redirects the global stripe-go backend at an in-process
// HTTP server for the duration of the test (the Go analogue of mocking
// stripe.* calls in Python). Handlers register stripe paths such as
// "/v1/customers/{id}". Restores the real backend on cleanup.
func installFakeStripe(t *testing.T, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := stripesdk.GetBackend(stripesdk.APIBackend)
	stripesdk.SetBackend(stripesdk.APIBackend, stripesdk.GetBackendWithConfig(
		stripesdk.APIBackend,
		&stripesdk.BackendConfig{URL: stripesdk.String(srv.URL)},
	))
	t.Cleanup(func() { stripesdk.SetBackend(stripesdk.APIBackend, old) })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// installFakeHTTP routes ALL of app.HTTPClient's outbound traffic (GitHub
// API, github.com OAuth, ...) to handler regardless of target host — the Go
// analogue of the `responses` library used by the Python tests. Handlers
// match on path only, e.g. "/user/installations".
func installFakeHTTP(app *App, handler http.Handler) {
	app.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		res := rec.Result()
		res.Request = req
		return res, nil
	})}
}
