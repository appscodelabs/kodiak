package webapi

import (
	"net/http"
)

// Routes mirrors web_api/web_api/urls.py plus the Django
// HealthCheckMiddleware paths (/healthz and /readiness).
func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/oauth_login", handle(a.oauthLogin))
	mux.HandleFunc("/v1/oauth_complete", handle(a.oauthComplete))
	mux.HandleFunc("/v1/logout", handle(a.logout))
	mux.HandleFunc("POST /v1/sync_accounts", handle(a.loginRequired(a.syncAccountsView)))
	mux.HandleFunc("/v1/t/{team_id}/usage_billing", handle(a.loginRequired(a.usageBilling)))
	mux.HandleFunc("/v1/t/{team_id}/activity", handle(a.loginRequired(a.activity)))
	mux.HandleFunc("/v1/t/{team_id}/current_account", handle(a.loginRequired(a.currentAccount)))
	mux.HandleFunc("/v1/t/{team_id}/start_trial", handle(a.loginRequired(a.startTrialView)))
	mux.HandleFunc("/v1/t/{team_id}/update_subscription", handle(a.loginRequired(a.updateSubscription)))
	mux.HandleFunc("/v1/t/{team_id}/fetch_proration", handle(a.loginRequired(a.fetchProration)))
	mux.HandleFunc("/v1/t/{team_id}/cancel_subscription", handle(a.loginRequired(a.cancelSubscriptionView)))
	mux.HandleFunc("/v1/t/{team_id}/start_checkout", handle(a.loginRequired(a.startCheckout)))
	mux.HandleFunc("/v1/t/{team_id}/modify_payment_details", handle(a.loginRequired(a.modifyPaymentDetails)))
	mux.HandleFunc("/v1/t/{team_id}/update_stripe_customer_info", handle(a.loginRequired(a.updateStripeCustomerInfo)))
	mux.HandleFunc("GET /v1/t/{team_id}/subscription_info", handle(a.loginRequired(a.getSubscriptionInfo)))
	mux.HandleFunc("/v1/t/{team_id}/stripe_self_serve_redirect", handle(a.loginRequired(a.redirectToStripeSelfServePortal)))
	mux.HandleFunc("/v1/stripe_webhook", handle(a.stripeWebhookHandler))
	mux.HandleFunc("/v1/accounts", handle(a.loginRequired(a.accounts)))
	mux.HandleFunc("/v1/ping", handle(a.loginRequired(a.ping)))
	mux.HandleFunc("/v1/healthcheck", handle(a.healthcheck))
	mux.HandleFunc("/v1/debug_sentry", handle(a.debugSentry))

	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readiness", a.readiness)

	return mux
}

// healthz mirrors HealthCheckMiddleware.healthz.
//
// Note: we don't check the database here because if the database connection
// failed then this service would restart, not the database.
func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("OK"))
}

// readiness mirrors HealthCheckMiddleware.readiness: connect to the database
// and do a generic standard SQL query that doesn't write any data and doesn't
// depend on any tables being present.
func (a *App) readiness(w http.ResponseWriter, r *http.Request) {
	var one int
	if err := a.DB.QueryRow(r.Context(), "SELECT 1").Scan(&one); err != nil {
		http.Error(w, "PG_CANNOT_CONNECT", http.StatusInternalServerError)
		return
	}
	if one != 1 {
		http.Error(w, "PG_BAD_RESPONSE", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write([]byte("OK"))
}
