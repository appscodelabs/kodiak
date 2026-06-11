// Ported from web_api/web_api/test_views.py — the Stripe webhook handler
// tests, from generate_header/test_generate_header through
// test_stripe_webhook_handler_customer_updated_no_matching_customer.
package webapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	stripesdk "github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/webhook"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// wv3WebhookSecret mirrors Cfg.StripeWebhookSecret from newTestApp
// (settings.STRIPE_WEBHOOK_SECRET in the pytest environment).
const wv3WebhookSecret = "whsec_someWebhookSecret"

// wv3GenerateHeader ports generate_header from test_views.py.
//
// https://github.com/stripe/stripe-python/blob/a16bdc5123bcc25e20b309419f547a150e83e44d/tests/test_webhook.py#L19
func wv3GenerateHeader(payload string) string {
	now := time.Now()
	signature := webhook.ComputeSignature(now, []byte(payload), wv3WebhookSecret)
	return fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(signature))
}

// wv3PostWebhook ports post_webhook from test_views.py:
// send a signed payload to our stripe webhook endpoint.
func wv3PostWebhook(t *testing.T, app *App, event string) *httptest.ResponseRecorder {
	t.Helper()
	// NOTE(port): stripe-go's webhook.ConstructEvent (used by
	// stripeWebhookHandler) rejects events whose api_version differs from the
	// SDK's pinned version; stripe-python performs no such check. Swap the
	// canned 2020-03-02 api_version of the Python payloads for the SDK's so
	// the handler logic under test is reached.
	payload := strings.ReplaceAll(event,
		`"api_version": "2020-03-02"`,
		fmt.Sprintf(`"api_version": %q`, stripesdk.APIVersion))
	req := httptest.NewRequest(http.MethodPost, "/v1/stripe_webhook", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", wv3GenerateHeader(payload))
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	return rec
}

// wv3StripeCustomerJSON ports create_stripe_customer from test_views.py.
// withAddress=false mirrors create_stripe_customer(address=None); the id is
// supplied by the caller (the fake server echoes the requested id).
func wv3StripeCustomerJSON(id string, withAddress bool) map[string]any {
	var addr any
	if withAddress {
		addr = map[string]any{
			"line1":       "123 Main St",
			"line2":       "Apt 2B",
			"city":        "Cambridge",
			"state":       "Massachusetts",
			"postal_code": "02139",
			"country":     "United States",
		}
	}
	return map[string]any{
		"object":        "customer",
		"id":            id,
		"address":       addr,
		"balance":       0,
		"created":       1592096376,
		"currency":      "usd",
		"email":         "accounting@acme.corp",
		"name":          "Acme Corp Inc",
		"subscriptions": map[string]any{"data": []any{map[string]any{"id": "sub_Gu1xedsfo1"}}},
	}
}

// wv3StripeSubscriptionJSON ports create_stripe_subscription from
// test_views.py (default interval="month").
func wv3StripeSubscriptionJSON(planID string) map[string]any {
	return map[string]any{
		"object":               "subscription",
		"id":                   "sub_Gu1xedsfo1",
		"current_period_end":   1690982549,
		"current_period_start": 1688304149,
		"customer":             "cus_f8935e914afa",
		// NOTE(port): stripe-go v78 removed Subscription.plan/quantity; the
		// Go production code reads both from items.data[0] (like real Stripe
		// API responses), so the fixture carries them there instead of at the
		// top level as in the Python fixture.
		"items": map[string]any{"data": []any{map[string]any{
			"object": "subscription_item",
			"id":     "si_Gx234091sd2",
			"plan": map[string]any{
				"id":             planID,
				"object":         "plan",
				"amount":         499,
				"interval":       "month",
				"interval_count": 1,
			},
			"quantity": 4,
		}}},
		"start_date":             1443556775,
		"default_payment_method": "pm_22dldxf3",
	}
}

// wv3StripePaymentMethodJSON ports create_stripe_payment_method from
// test_views.py.
func wv3StripePaymentMethodJSON() map[string]any {
	return map[string]any{
		"object": "payment_method",
		"id":     "pm_55yfgbc6",
		"card": map[string]any{
			"brand": "mastercard",
			// NOTE(port): stripe-go parses card exp_month/exp_year as
			// integers; the Python fixture uses the strings "04"/"22".
			"exp_month": 4,
			"exp_year":  22,
			"last4":     "4040",
		},
	}
}

// wv3StripeStub counts requests served by the fake Stripe API, porting the
// mocker.patch call_count assertions for stripe.Customer.retrieve,
// stripe.Subscription.retrieve, stripe.PaymentMethod.retrieve and
// stripe.PaymentMethod.list.
type wv3StripeStub struct {
	customerRetrieve      atomic.Int64
	subscriptionRetrieve  atomic.Int64
	paymentMethodRetrieve atomic.Int64
	paymentMethodList     atomic.Int64
}

func (s *wv3StripeStub) check(t *testing.T, customerRetrieve, subscriptionRetrieve, paymentMethodRetrieve, paymentMethodList int64) {
	t.Helper()
	if got := s.customerRetrieve.Load(); got != customerRetrieve {
		t.Errorf("stripe.Customer.retrieve call_count = %d, want %d", got, customerRetrieve)
	}
	if got := s.subscriptionRetrieve.Load(); got != subscriptionRetrieve {
		t.Errorf("stripe.Subscription.retrieve call_count = %d, want %d", got, subscriptionRetrieve)
	}
	if got := s.paymentMethodRetrieve.Load(); got != paymentMethodRetrieve {
		t.Errorf("stripe.PaymentMethod.retrieve call_count = %d, want %d", got, paymentMethodRetrieve)
	}
	if got := s.paymentMethodList.Load(); got != paymentMethodList {
		t.Errorf("stripe.PaymentMethod.list call_count = %d, want %d", got, paymentMethodList)
	}
}

// wv3InstallStripeStub wires the fake Stripe API used by the webhook tests.
//
// NOTE(port): the customer endpoint echoes the requested id where the Python
// mock always returns a fixed object. The observable results are identical
// because Python's StripeCustomerInformation.update_from_stripe keeps the
// row's existing customer_id regardless of the id on the retrieved customer.
func wv3InstallStripeStub(t *testing.T, app *App, customerWithAddress bool) *wv3StripeStub {
	t.Helper()
	stub := &wv3StripeStub{}
	respond := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Errorf("encode fake stripe response: %v", err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		stub.customerRetrieve.Add(1)
		respond(w, wv3StripeCustomerJSON(r.PathValue("id"), customerWithAddress))
	})
	mux.HandleFunc("GET /v1/subscriptions/{id}", func(w http.ResponseWriter, r *http.Request) {
		stub.subscriptionRetrieve.Add(1)
		respond(w, wv3StripeSubscriptionJSON(app.Cfg.StripeAnnualPlanID))
	})
	mux.HandleFunc("GET /v1/payment_methods/{id}", func(w http.ResponseWriter, r *http.Request) {
		stub.paymentMethodRetrieve.Add(1)
		respond(w, wv3StripePaymentMethodJSON())
	})
	mux.HandleFunc("GET /v1/payment_methods", func(w http.ResponseWriter, r *http.Request) {
		stub.paymentMethodList.Add(1)
		respond(w, map[string]any{
			"object":   "list",
			"data":     []any{wv3StripePaymentMethodJSON()},
			"has_more": false,
			"url":      "/v1/payment_methods",
		})
	})
	installFakeStripe(t, mux)
	return stub
}

// wv3StripeCustomerInfoCount ports StripeCustomerInformation.objects.count().
func wv3StripeCustomerInfoCount(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM stripe_customer_information`).Scan(&n); err != nil {
		t.Fatalf("count stripe_customer_information: %v", err)
	}
	return n
}

// wv3UpdateBotCalls counts Account.update_bot invocations. The Python tests
// mock the method; in Go updateBot runs for real and pushes exactly one
// message per call onto the refresh queue in (mini)redis.
func wv3UpdateBotCalls(t *testing.T, app *App) int64 {
	t.Helper()
	n, err := app.Redis.LLen(context.Background(), "kodiak:refresh_pull_requests_for_installation").Result()
	if err != nil {
		t.Fatalf("llen kodiak:refresh_pull_requests_for_installation: %v", err)
	}
	return n
}

// wv3GetStripeCustomerInfo fetches a row that must exist (objects.get()).
func wv3GetStripeCustomerInfo(t *testing.T, db *pgxpool.Pool, customerID string) *models.StripeCustomerInformation {
	t.Helper()
	info, err := models.GetStripeCustomerInfo(context.Background(), db, customerID)
	if err != nil {
		t.Fatalf("get stripe customer info %q: %v", customerID, err)
	}
	if info == nil {
		t.Fatalf("stripe customer info %q not found", customerID)
	}
	return info
}

// wv3WantStr asserts a nullable text column holds want.
func wv3WantStr(t *testing.T, field string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = <nil>, want %q", field, want)
	} else if *got != want {
		t.Errorf("%s = %q, want %q", field, *got, want)
	}
}

// wv3WantNil asserts a nullable text column is NULL.
func wv3WantNil(t *testing.T, field string, got *string) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %q, want nil", field, *got)
	}
}

// wv3CheckUpdatedSubscriptionFields ports the shared block of assertions on
// stripe_customer_info_updated in both checkout.session.completed tests:
// every value comes from the fake subscription / payment method / customer
// fixtures above.
func wv3CheckUpdatedSubscriptionFields(t *testing.T, app *App, updated *models.StripeCustomerInformation) {
	t.Helper()
	if updated.SubscriptionID != "sub_Gu1xedsfo1" {
		t.Errorf("subscription_id = %q, want %q", updated.SubscriptionID, "sub_Gu1xedsfo1")
	}
	if updated.PlanID != app.Cfg.StripeAnnualPlanID {
		t.Errorf("plan_id = %q, want %q", updated.PlanID, app.Cfg.StripeAnnualPlanID)
	}
	if updated.PaymentMethodID != "pm_55yfgbc6" {
		t.Errorf("payment_method_id = %q, want %q", updated.PaymentMethodID, "pm_55yfgbc6")
	}
	if updated.CustomerEmail != "accounting@acme.corp" {
		t.Errorf("customer_email = %q, want %q", updated.CustomerEmail, "accounting@acme.corp")
	}
	if updated.CustomerBalance != 0 {
		t.Errorf("customer_balance = %d, want 0", updated.CustomerBalance)
	}
	if updated.CustomerCreated != 1592096376 {
		t.Errorf("customer_created = %d, want 1592096376", updated.CustomerCreated)
	}
	wv3WantStr(t, "payment_method_card_brand", updated.PaymentMethodCardBrand, "mastercard")
	// NOTE(port): the Python fixture's exp_month/exp_year are the strings
	// "04"/"22"; stripe-go parses them as integers so the stored values are
	// "4"/"22".
	wv3WantStr(t, "payment_method_card_exp_month", updated.PaymentMethodCardExpMonth, "4")
	wv3WantStr(t, "payment_method_card_exp_year", updated.PaymentMethodCardExpYear, "22")
	wv3WantStr(t, "payment_method_card_last4", updated.PaymentMethodCardLast4, "4040")
	if updated.PlanAmount != 499 {
		t.Errorf("plan_amount = %d, want 499", updated.PlanAmount)
	}
	if updated.PlanIntervalCount == nil || *updated.PlanIntervalCount != 1 {
		t.Errorf("plan_interval_count = %v, want 1", updated.PlanIntervalCount)
	}
	wv3WantStr(t, "plan_interval", updated.PlanInterval, "month")
	if updated.SubscriptionQuantity != 4 {
		t.Errorf("subscription_quantity = %d, want 4", updated.SubscriptionQuantity)
	}
	if updated.SubscriptionStartDate != 1443556775 {
		t.Errorf("subscription_start_date = %d, want 1443556775", updated.SubscriptionStartDate)
	}
	if updated.SubscriptionCurrentPeriodEnd != 1690982549 {
		t.Errorf("subscription_current_period_end = %d, want 1690982549", updated.SubscriptionCurrentPeriodEnd)
	}
	if updated.SubscriptionCurrentPeriodStart != 1688304149 {
		t.Errorf("subscription_current_period_start = %d, want 1688304149", updated.SubscriptionCurrentPeriodStart)
	}
}

// https://github.com/stripe/stripe-python/blob/a16bdc5123bcc25e20b309419f547a150e83e44d/tests/test_webhook.py#L19
// Ported from test_generate_header.
func TestGenerateHeader(t *testing.T) {
	payloadV2, err := json.Marshal(map[string]any{"blah": 123})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	header := wv3GenerateHeader(string(payloadV2))
	// NOTE(port): stripe-go's ConstructEvent additionally pins the event
	// api_version (stripe-python does not), so that check is ignored here;
	// the signature and timestamp tolerance are still fully verified, and a
	// successfully returned event is the isinstance(event, stripe.Event)
	// assertion of the Python test.
	if _, err := webhook.ConstructEventWithOptions(payloadV2, header, wv3WebhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true}); err != nil {
		t.Fatalf("construct_event rejected generated header: %v", err)
	}
}

// Verify our webhook handler updates our subscription on this event.
// Ported from test_stripe_webhook_handler_checkout_session_complete_setup.
func TestStripeWebhookHandlerCheckoutSessionCompleteSetup(t *testing.T) {
	app, _ := newTestApp(t)
	account := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", "User")
	// fake customer with id=account.stripe_customer_id and address=None.
	stub := wv3InstallStripeStub(t, app, false)

	stripeCustomerInfo := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID,
		"sub_Gu1xedsfo1", 1650581784, 1658357784)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	// assert account.stripe_customer_info() == stripe_customer_info
	// (Django model equality is primary-key equality).
	if got := wv3GetStripeCustomerInfo(t, app.DB, account.StripeCustomerID); got.CustomerID != stripeCustomerInfo.CustomerID {
		t.Fatalf("account.stripe_customer_info().customer_id = %q, want %q",
			got.CustomerID, stripeCustomerInfo.CustomerID)
	}
	res := wv3PostWebhook(t, app, fmt.Sprintf(`
{
  "id": "evt_1GTJLPCoyKa1V9Y6dIfvIuva",
  "object": "event",
  "api_version": "2020-03-02",
  "created": 1585796631,
  "data": {
    "object": {
      "id": "cs_test_Enc3u2mzp4vowbDcwrSneOhGOLwoYHX4UJGmUh6f4b3fnS88LTZgsJZT",
      "object": "checkout.session",
      "billing_address_collection": null,
      "cancel_url": "http://app.localhost.kodiakhq.com:3000/t/8f5b095f-9a11-4183-aa00-2152e2001a34/usage?start_subscription=1",
      "client_reference_id": "%s",
      "customer": "%s",
      "customer_email": null,
      "display_items": [
      ],
      "livemode": false,
      "locale": null,
      "metadata": {
      },
      "mode": "setup",
      "payment_intent": null,
      "payment_method_types": [
        "card"
      ],
      "setup_intent": "seti_1GTJKRCoyKa1V9Y6M41eERR5",
      "shipping": null,
      "shipping_address_collection": null,
      "submit_type": null,
      "subscription": null,
      "success_url": "http://app.localhost.kodiakhq.com:3000/t/8f5b095f-9a11-4183-aa00-2152e2001a34/usage?install_complete=1"
    }
  },
  "livemode": false,
  "pending_webhooks": 1,
  "request": {
    "id": null,
    "idempotency_key": null
  },
  "type": "checkout.session.completed"
}
`, account.ID, account.StripeCustomerID))
	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	stub.check(t, 1, 1, 1, 1)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Errorf("StripeCustomerInformation count = %d, want 1", got)
	}

	if got := wv3UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call_count = %d, want 1", got)
	}

	updated := wv3GetStripeCustomerInfo(t, app.DB, account.StripeCustomerID)
	wv3WantStr(t, "customer_currency", updated.CustomerCurrency, "usd")
	wv3CheckUpdatedSubscriptionFields(t, app, updated)
}

// wv3EqualSubscriptions ports equal_subscriptions from test_views.py:
// compare every model field of the two rows.
func wv3EqualSubscriptions(a, b *models.StripeCustomerInformation) bool {
	return reflect.DeepEqual(a, b)
}

// Verify our webhook handler creates a subscription on this event.
// Ported from test_stripe_webhook_handler_checkout_session_complete_subscription.
func TestStripeWebhookHandlerCheckoutSessionCompleteSubscription(t *testing.T) {
	app, _ := newTestApp(t)
	// fake customer built with the default (present) address.
	stub := wv3InstallStripeStub(t, app, true)

	account := createTestAccount(t, app.DB, "", "acme-corp", "User")
	otherAccount := createTestAccount(t, app.DB, "cus_354HjLriodop21", "delos-engineering", "Organization")
	otherSubscription := createTestStripeCustomerInfo(t, app.DB, otherAccount.StripeCustomerID,
		"sub_L43DyAEVGwzt32", 1650581784, 1658357784)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	if got := wv3UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call_count = %d, want 0", got)
	}
	res := wv3PostWebhook(t, app, fmt.Sprintf(`
{
  "id": "evt_1GQQCUCoyKa1V9Y6EvAMbLgM",
  "object": "event",
  "api_version": "2020-03-02",
  "created": 1585108002,
  "data": {
    "object": {
      "id": "cs_test_3UbqT95rC08iaEzKbGT5tkYgLwuoEeUFfd93uiFaQoo9ZeslGZeV27fP",
      "object": "checkout.session",
      "billing_address_collection": null,
      "cancel_url": "http://app.localhost.kodiakhq.com:3000/t/8f5b095f-9a11-4183-aa00-2152e2001a34/usage?cancel=1",
      "client_reference_id": "%s",
      "customer": "cus_Gz7jQFKdh4KirU",
      "customer_email": null,
      "display_items": [
        {
          "amount": 499,
          "currency": "usd",
          "plan": {
            "id": "plan_GuzDf41AmGOJzQ",
            "object": "plan",
            "active": true,
            "aggregate_usage": null,
            "amount": 499,
            "amount_decimal": "499",
            "billing_scheme": "per_unit",
            "created": 1584327811,
            "currency": "usd",
            "interval": "month",
            "interval_count": 1,
            "livemode": false,
            "metadata": {
            },
            "nickname": "Monthly",
            "product": "prod_GuzCMVKonQIp2l",
            "tiers": null,
            "tiers_mode": null,
            "transform_usage": null,
            "trial_period_days": null,
            "usage_type": "licensed"
          },
          "quantity": 2,
          "type": "plan"
        }
      ],
      "livemode": false,
      "locale": null,
      "metadata": {
      },
      "mode": "subscription",
      "payment_intent": null,
      "payment_method_types": [
        "card"
      ],
      "setup_intent": null,
      "shipping": null,
      "shipping_address_collection": null,
      "submit_type": null,
      "subscription": "sub_GyMw1lyNB2LXmn",
      "success_url": "http://app.localhost.kodiakhq.com:3000/t/8f5b095f-9a11-4183-aa00-2152e2001a34/usage?success=1"
    }
  },
  "livemode": false,
  "pending_webhooks": 1,
  "request": {
    "id": "req_nIsiFwPHQZauDS",
    "idempotency_key": null
  },
  "type": "checkout.session.completed"
}`, account.ID))

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if got := wv3UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call_count = %d, want 1", got)
	}
	stub.check(t, 1, 1, 1, 1)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 2 {
		t.Errorf("StripeCustomerInformation count = %d, want 2", got)
	}

	// verify `other_subscription` hasn't been modified
	otherSubscriptionRefreshed := wv3GetStripeCustomerInfo(t, app.DB, otherAccount.StripeCustomerID)
	if otherSubscriptionRefreshed.SubscriptionID != otherSubscription.SubscriptionID {
		t.Fatalf("other subscription_id = %q, want %q",
			otherSubscriptionRefreshed.SubscriptionID, otherSubscription.SubscriptionID)
	}
	if !wv3EqualSubscriptions(otherSubscriptionRefreshed, otherSubscription) {
		t.Errorf("other_subscription was modified:\n got %+v\nwant %+v",
			otherSubscriptionRefreshed, otherSubscription)
	}

	updated := wv3GetStripeCustomerInfo(t, app.DB, "cus_Gz7jQFKdh4KirU")
	wv3CheckUpdatedSubscriptionFields(t, app, updated)
}

// Verify our webhook handler updates our subscription on this event.
//
// This event will get sent when a subscription is updated.
// Ported from test_stripe_webhook_handler_invoice_payment_succeeded.
func TestStripeWebhookHandlerInvoicePaymentSucceeded(t *testing.T) {
	app, _ := newTestApp(t)
	account := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", "User")

	stripeCustomerInfo := createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID,
		"sub_Gu1xedsfo1", 1650581784, 1658357784)
	// retrieve mocks return create_stripe_customer() (default, with address),
	// create_stripe_subscription() and create_stripe_payment_method().
	stub := wv3InstallStripeStub(t, app, true)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	if got := wv3UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call_count = %d, want 0", got)
	}
	stub.check(t, 0, 0, 0, 0)
	res := wv3PostWebhook(t, app, fmt.Sprintf(`
{
  "created": 1326853478,
  "livemode": false,
  "id": "evt_00000000000000",
  "type": "invoice.payment_succeeded",
  "object": "event",
  "request": null,
  "pending_webhooks": 1,
  "api_version": "2020-03-02",
  "data": {
    "object": {
      "id": "in_00000000000000",
      "object": "invoice",
      "account_country": "US",
      "account_name": "Kodiakhq",
      "amount_due": 9980,
      "amount_paid": 9980,
      "amount_remaining": 0,
      "application_fee_amount": null,
      "attempt_count": 1,
      "attempted": true,
      "auto_advance": false,
      "billing_reason": "subscription_create",
      "charge": "_00000000000000",
      "collection_method": "charge_automatically",
      "created": 1584328318,
      "currency": "usd",
      "custom_fields": null,
      "customer": "%s",
      "customer_address": null,
      "customer_email": "j.doe@example.com",
      "customer_name": null,
      "customer_phone": null,
      "customer_shipping": null,
      "customer_tax_exempt": "none",
      "customer_tax_ids": [
      ],
      "default_payment_method": null,
      "default_source": null,
      "default_tax_rates": [
      ],
      "description": null,
      "discount": null,
      "due_date": null,
      "ending_balance": 0,
      "footer": null,
      "hosted_invoice_url": "https://pay.stripe.com/invoice/acct_1GN6r2CoyKa1V9Y6/invst_GuzL5pfiHFXemcitpnDzhVLbrgrHk3T",
      "invoice_pdf": "https://pay.stripe.com/invoice/acct_1GN6r2CoyKa1V9Y6/invst_GuzL5pfiHFXemcitpnDzhVLbrgrHk3T/pdf",
      "lines": {
        "data": [
          {
            "id": "il_00000000000000",
            "object": "line_item",
            "amount": 4990,
            "currency": "usd",
            "description": "10 seat × Kodiak Seat License (at $4.99 / month)",
            "discountable": true,
            "livemode": false,
            "metadata": {
            },
            "period": {
              "end": 1660949784,
              "start": 1653173784
            },
            "plan": {
              "id": "plan_00000000000000",
              "object": "plan",
              "active": true,
              "aggregate_usage": null,
              "amount": 499,
              "amount_decimal": "499",
              "billing_scheme": "per_unit",
              "created": 1584327811,
              "currency": "usd",
              "interval": "month",
              "interval_count": 1,
              "livemode": false,
              "metadata": {
              },
              "nickname": "Monthly",
              "product": "prod_00000000000000",
              "tiers": null,
              "tiers_mode": null,
              "transform_usage": null,
              "trial_period_days": null,
              "usage_type": "licensed"
            },
            "proration": false,
            "quantity": 10,
            "subscription": "sub_00000000000000",
            "subscription_item": "si_00000000000000",
            "tax_amounts": [
            ],
            "tax_rates": [
            ],
            "type": "subscription"
          }
        ],
        "has_more": false,
        "object": "list",
        "url": "/v1/invoices/in_1GN9MwCoyKa1V9Y6Ox2tehjN/lines"
      },
      "livemode": false,
      "metadata": {
      },
      "next_payment_attempt": null,
      "number": "E5700931-0001",
      "paid": true,
      "payment_intent": "pi_00000000000000",
      "period_end": 1660949784,
      "period_start": 1653173784,
      "post_payment_credit_notes_amount": 0,
      "pre_payment_credit_notes_amount": 0,
      "receipt_number": "2421-2035",
      "starting_balance": 0,
      "statement_descriptor": null,
      "status": "paid",
      "status_transitions": {
        "finalized_at": 1584328319,
        "marked_uncollectible_at": null,
        "paid_at": 1584328320,
        "voided_at": null
      },
      "subscription": "sub_00000000000000",
      "subtotal": 9980,
      "tax": null,
      "tax_percent": null,
      "total": 9980,
      "total_tax_amounts": [
      ],
      "webhooks_delivered_at": 1584328320,
      "closed": true
    }
  }
}`, account.StripeCustomerID))

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if got := wv3UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call_count = %d, want 1", got)
	}
	stub.check(t, 1, 1, 1, 1)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Errorf("StripeCustomerInformation count = %d, want 1", got)
	}
	updated := wv3GetStripeCustomerInfo(t, app.DB, account.StripeCustomerID)
	if updated.SubscriptionCurrentPeriodStart <= stripeCustomerInfo.SubscriptionCurrentPeriodStart {
		t.Errorf("subscription_current_period_start = %d, want > %d",
			updated.SubscriptionCurrentPeriodStart, stripeCustomerInfo.SubscriptionCurrentPeriodStart)
	}
	if updated.SubscriptionCurrentPeriodEnd <= stripeCustomerInfo.SubscriptionCurrentPeriodEnd {
		t.Errorf("subscription_current_period_end = %d, want > %d",
			updated.SubscriptionCurrentPeriodEnd, stripeCustomerInfo.SubscriptionCurrentPeriodEnd)
	}
	if updated.SubscriptionCurrentPeriodEnd != 1690982549 {
		t.Errorf("subscription_current_period_end = %d, want 1690982549", updated.SubscriptionCurrentPeriodEnd)
	}
	if updated.SubscriptionCurrentPeriodStart != 1688304149 {
		t.Errorf("subscription_current_period_start = %d, want 1688304149", updated.SubscriptionCurrentPeriodStart)
	}
}

// wv3MakeCustomerUpdatedEvent ports make_customer_updated_event from
// test_views.py.
func wv3MakeCustomerUpdatedEvent(customerID string) string {
	return fmt.Sprintf(`
{
  "created": 1326853478,
  "livemode": false,
  "id": "evt_00000000000000",
  "type": "customer.updated",
  "object": "event",
  "request": null,
  "pending_webhooks": 1,
  "api_version": "2020-03-02",
  "data": {
    "object": {
      "id": "%s",
      "object": "customer",
      "address": null,
      "balance": 0,
      "created": 1592097133,
      "currency": "usd",
      "default_source": null,
      "delinquent": false,
      "description": null,
      "discount": null,
      "email": null,
      "invoice_prefix": "62BA0D0",
      "invoice_settings": {
        "custom_fields": null,
        "default_payment_method": null,
        "footer": null
      },
      "livemode": false,
      "metadata": {
      },
      "name": null,
      "next_invoice_sequence": 1,
      "phone": null,
      "preferred_locales": [
      ],
      "shipping": null,
      "sources": {
        "object": "list",
        "data": [
        ],
        "has_more": false,
        "url": "/v1/customers/cus_HSfjRanfIZeNYE/sources"
      },
      "subscriptions": {
        "object": "list",
        "data": [
        ],
        "has_more": false,
        "url": "/v1/customers/cus_HSfjRanfIZeNYE/subscriptions"
      },
      "tax_exempt": "none",
      "tax_ids": {
        "object": "list",
        "data": [
        ],
        "has_more": false,
        "url": "/v1/customers/cus_HSfjRanfIZeNYE/tax_ids"
      }
    },
    "previous_attributes": {
      "description": "Old description"
    }
  }
}`, customerID)
}

// Verify our webhook handler updates our customer when we get a
// customer.updated event
// Ported from test_stripe_webhook_handler_customer_updated.
func TestStripeWebhookHandlerCustomerUpdated(t *testing.T) {
	app, _ := newTestApp(t)
	account := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", "User")

	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID,
		"sub_Gu1xedsfo1", 1650581784, 1658357784)
	// fake customer with id=account.stripe_customer_id and address=None.
	stub := wv3InstallStripeStub(t, app, false)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	if got := wv3UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call_count = %d, want 0", got)
	}
	stub.check(t, 0, 0, 0, 0)
	res := wv3PostWebhook(t, app, wv3MakeCustomerUpdatedEvent(account.StripeCustomerID))

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if got := wv3UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call_count = %d, want 1", got)
	}
	stub.check(t, 1, 1, 1, 1)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Errorf("StripeCustomerInformation count = %d, want 1", got)
	}
	updated := wv3GetStripeCustomerInfo(t, app.DB, account.StripeCustomerID)
	if updated.CustomerEmail != "accounting@acme.corp" {
		t.Errorf("customer_email = %q, want %q", updated.CustomerEmail, "accounting@acme.corp")
	}
	if updated.CustomerBalance != 0 {
		t.Errorf("customer_balance = %d, want 0", updated.CustomerBalance)
	}
	if updated.CustomerCreated != 1592096376 {
		t.Errorf("customer_created = %d, want 1592096376", updated.CustomerCreated)
	}
	wv3WantStr(t, "customer_currency", updated.CustomerCurrency, "usd")
	wv3WantStr(t, "customer_name", updated.CustomerName, "Acme Corp Inc")

	// fake_customer.address is None
	wv3WantNil(t, "customer_address_line1", updated.CustomerAddressLine1)
	wv3WantNil(t, "customer_address_city", updated.CustomerAddressCity)
	wv3WantNil(t, "customer_address_country", updated.CustomerAddressCountry)
	wv3WantNil(t, "customer_address_line2", updated.CustomerAddressLine2)
	wv3WantNil(t, "customer_address_postal_code", updated.CustomerAddressPostalCode)
	wv3WantNil(t, "customer_address_state", updated.CustomerAddressState)
}

// Verify our webhook handler updates our customer when we get a
// customer.updated event
//
// This is basically the same as the previous test but with the address
// provided.
// Ported from test_stripe_webhook_handler_customer_updated_with_address.
func TestStripeWebhookHandlerCustomerUpdatedWithAddress(t *testing.T) {
	app, _ := newTestApp(t)
	account := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", "User")

	createTestStripeCustomerInfo(t, app.DB, account.StripeCustomerID,
		"sub_Gu1xedsfo1", 1650581784, 1658357784)
	// fake customer with id=account.stripe_customer_id and the default
	// address.
	stub := wv3InstallStripeStub(t, app, true)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Fatalf("StripeCustomerInformation count = %d, want 1", got)
	}
	if got := wv3UpdateBotCalls(t, app); got != 0 {
		t.Fatalf("update_bot call_count = %d, want 0", got)
	}
	stub.check(t, 0, 0, 0, 0)
	res := wv3PostWebhook(t, app, wv3MakeCustomerUpdatedEvent(account.StripeCustomerID))

	if res.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	if got := wv3UpdateBotCalls(t, app); got != 1 {
		t.Errorf("update_bot call_count = %d, want 1", got)
	}
	stub.check(t, 1, 1, 1, 1)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 1 {
		t.Errorf("StripeCustomerInformation count = %d, want 1", got)
	}
	updated := wv3GetStripeCustomerInfo(t, app.DB, account.StripeCustomerID)
	if updated.CustomerEmail != "accounting@acme.corp" {
		t.Errorf("customer_email = %q, want %q", updated.CustomerEmail, "accounting@acme.corp")
	}
	if updated.CustomerBalance != 0 {
		t.Errorf("customer_balance = %d, want 0", updated.CustomerBalance)
	}
	if updated.CustomerCreated != 1592096376 {
		t.Errorf("customer_created = %d, want 1592096376", updated.CustomerCreated)
	}
	wv3WantStr(t, "customer_currency", updated.CustomerCurrency, "usd")
	wv3WantStr(t, "customer_name", updated.CustomerName, "Acme Corp Inc")

	// fake_customer.address is not None
	wv3WantStr(t, "customer_address_line1", updated.CustomerAddressLine1, "123 Main St")
	wv3WantStr(t, "customer_address_city", updated.CustomerAddressCity, "Cambridge")
	wv3WantStr(t, "customer_address_country", updated.CustomerAddressCountry, "United States")
	wv3WantStr(t, "customer_address_line2", updated.CustomerAddressLine2, "Apt 2B")
	wv3WantStr(t, "customer_address_postal_code", updated.CustomerAddressPostalCode, "02139")
	wv3WantStr(t, "customer_address_state", updated.CustomerAddressState, "Massachusetts")
}

// If we get a customer.updated event for a non existent customer we should
// error.
// Ported from test_stripe_webhook_handler_customer_updated_no_matching_customer.
func TestStripeWebhookHandlerCustomerUpdatedNoMatchingCustomer(t *testing.T) {
	app, _ := newTestApp(t)
	account := createTestAccount(t, app.DB, "cus_523405923045", "acme-corp", "User")

	// The Python test patches StripeCustomerInformation.update_from_stripe
	// and Account.update_bot and asserts neither is called; here the stub
	// counts every Stripe API request and updateBot pushes onto the redis
	// refresh queue, so zero requests + zero pushes is the equivalent check.
	stub := wv3InstallStripeStub(t, app, true)
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Fatalf("StripeCustomerInformation count = %d, want 0", got)
	}
	stub.check(t, 0, 0, 0, 0)
	res := wv3PostWebhook(t, app, wv3MakeCustomerUpdatedEvent(account.StripeCustomerID))

	if res.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
	if got := wv3StripeCustomerInfoCount(t, app.DB); got != 0 {
		t.Errorf("StripeCustomerInformation count = %d, want 0", got)
	}
	stub.check(t, 0, 0, 0, 0)
	if got := wv3UpdateBotCalls(t, app); got != 0 {
		t.Errorf("update_bot call_count = %d, want 0", got)
	}
}
