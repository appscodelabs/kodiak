// Package server wires the chi router for the web_api service.
//
// Routes mirror web_api/core/urls.py one-for-one.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/appscodelabs/kodiak/internal/webapi/auth"
	"github.com/appscodelabs/kodiak/internal/webapi/config"
	"github.com/appscodelabs/kodiak/internal/webapi/store"
)

// Server holds the web_api service dependencies.
type Server struct {
	cfg   *config.Config
	db    *pgxpool.Pool
	store *store.Store
	auth  *auth.Manager
	log   zerolog.Logger
}

// New constructs a Server.
func New(cfg *config.Config, db *pgxpool.Pool, log zerolog.Logger) *Server {
	return &Server{
		cfg:   cfg,
		db:    db,
		store: store.New(db),
		auth:  auth.New(cfg.SecretKey),
		log:   log,
	}
}

// Router returns the configured chi router mounted under /v1/.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Route("/v1", func(r chi.Router) {
		r.Get("/ping", s.ping)
		r.Get("/debug_sentry", s.debugSentry)

		r.Get("/oauth_login", s.oauthLogin)
		r.Get("/oauth_complete", s.oauthComplete)
		r.Post("/logout", s.logout)
		r.Post("/sync_accounts", s.auth.LoginRequired(s.syncAccounts))
		r.Get("/accounts", s.auth.LoginRequired(s.accounts))

		r.Route("/t/{teamID}", func(r chi.Router) {
			r.Get("/usage_billing", s.auth.LoginRequired(s.usageBilling))
			r.Get("/activity", s.auth.LoginRequired(s.activity))
			r.Get("/current_account", s.auth.LoginRequired(s.currentAccount))
			r.Post("/start_trial", s.auth.LoginRequired(s.startTrial))
			r.Post("/update_subscription", s.auth.LoginRequired(s.updateSubscription))
			r.Post("/fetch_proration", s.auth.LoginRequired(s.fetchProration))
			r.Post("/cancel_subscription", s.auth.LoginRequired(s.cancelSubscription))
			r.Post("/start_checkout", s.auth.LoginRequired(s.startCheckout))
			r.Post("/modify_payment_details", s.auth.LoginRequired(s.modifyPaymentDetails))
			r.Post("/update_stripe_customer_info", s.auth.LoginRequired(s.updateStripeCustomerInfo))
			r.Get("/subscription_info", s.auth.LoginRequired(s.subscriptionInfo))
			r.Get("/stripe_self_serve_redirect", s.auth.LoginRequired(s.stripeSelfServeRedirect))
		})

		r.Post("/stripe_webhook", s.stripeWebhook)
	})
	return r
}

func (s *Server) ping(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) debugSentry(_ http.ResponseWriter, _ *http.Request) {
	panic("sentry test panic")
}

// oauthLogin redirects to GitHub. Mirrors core/views.oauth_login.
func (s *Server) oauthLogin(w http.ResponseWriter, r *http.Request) {
	redirect := r.URL.Query().Get("redirect")
	if redirect == "" {
		redirect = "/"
	}
	state := uuid.NewString()
	http.SetCookie(w, &http.Cookie{
		Name: "oauth_state", Value: state + "|" + redirect, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	target := "https://github.com/login/oauth/authorize?client_id=" + s.cfg.GitHubClientID +
		"&state=" + state +
		"&scope=read:org,read:user,user:email"
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

// oauthComplete is the OAuth callback. The full token exchange + user upsert
// is intentionally left as a stub so the handler returns a clear 501 instead
// of failing in subtle ways — the data path is ready (store.UpsertUserByGitHubID).
func (s *Server) oauthComplete(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, "oauth_complete: TODO call POST github.com/login/oauth/access_token, GET api.github.com/user, then store.UpsertUserByGitHubID + auth.Login")
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.auth.Logout(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	accounts, err := s.store.AccountsForUser(r.Context(), sess.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, map[string]any{
			"id":             a.ID.String(),
			"name":           a.GitHubAccountLogin,
			"profileImgUrl":  "https://avatars.githubusercontent.com/u/" + uuidFmtAccount(a.GitHubAccountID),
			"installationId": a.GitHubInstallationID,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) syncAccounts(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "sync_accounts: call GitHub installations API and reconcile core.Account / AccountMembership")
}

func (s *Server) currentAccount(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	teamID, err := uuid.Parse(chi.URLParam(r, "teamID"))
	if err != nil {
		http.Error(w, "bad teamID", http.StatusBadRequest)
		return
	}
	account, err := s.store.AccountForTeam(r.Context(), sess.UserID, teamID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                  account.ID.String(),
		"name":                account.GitHubAccountLogin,
		"installationId":      account.GitHubInstallationID,
		"githubAccountType":   string(account.GitHubAccountType),
		"subscriptionExempt":  account.SubscriptionExempt,
	})
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	teamID, _ := uuid.Parse(chi.URLParam(r, "teamID"))
	account, err := s.store.AccountForTeam(r.Context(), sess.UserID, teamID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	rows, err := s.store.PullRequestActivityForInstallation(r.Context(), account.GitHubInstallationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	labels := make([]string, len(rows))
	opened := make([]int, len(rows))
	merged := make([]int, len(rows))
	closed := make([]int, len(rows))
	approvals := make([]int, len(rows))
	updates := make([]int, len(rows))
	for i, r := range rows {
		labels[i] = r.Date.Format("2006-01-02")
		opened[i] = r.OpenedPullRequests
		merged[i] = r.MergedPullRequests
		closed[i] = r.ClosedPullRequests
		approvals[i] = r.KodiakApprovals
		updates[i] = r.KodiakUpdates
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kodiakActivity": map[string]any{
			"labels": labels, "approved": approvals, "merged": merged, "updated": updates,
		},
		"pullRequestActivity": map[string]any{
			"labels": labels, "opened": opened, "merged": merged, "closed": closed,
		},
	})
}

func (s *Server) usageBilling(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "usage_billing: aggregate UserPullRequestActivity + StripeCustomerInformation")
}
func (s *Server) startTrial(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "start_trial: set Account.trial_start/trial_expiration")
}
func (s *Server) updateSubscription(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "update_subscription: call stripe subscriptions API")
}
func (s *Server) fetchProration(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "fetch_proration: stripe.InvoiceUpcoming")
}
func (s *Server) cancelSubscription(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "cancel_subscription: stripe.Subscription delete")
}
func (s *Server) startCheckout(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "start_checkout: stripe.CheckoutSession.Create")
}
func (s *Server) modifyPaymentDetails(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "modify_payment_details: stripe.CheckoutSession update mode")
}
func (s *Server) updateStripeCustomerInfo(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "update_stripe_customer_info: stripe.Customer.Update + sync local cache")
}
func (s *Server) subscriptionInfo(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "subscription_info: read StripeCustomerInformation row")
}
func (s *Server) stripeSelfServeRedirect(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "stripe_self_serve_redirect: stripe.BillingPortalSession.Create")
}
func (s *Server) stripeWebhook(w http.ResponseWriter, _ *http.Request) {
	notImplemented(w, "stripe_webhook: verify signature with cfg.StripeWebhookSecret, dispatch by event.Type")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func notImplemented(w http.ResponseWriter, view string) {
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error": "not implemented",
		"view":  view,
	})
}

// uuidFmtAccount formats a github account id for use in an avatar URL path.
func uuidFmtAccount(id int64) string {
	// avatars.githubusercontent.com/u/<id>?v=4 is the canonical URL
	return jitoa(id)
}

func jitoa(id int64) string {
	if id == 0 {
		return "0"
	}
	neg := id < 0
	if neg {
		id = -id
	}
	var buf [20]byte
	i := len(buf)
	for id > 0 {
		i--
		buf[i] = byte('0' + id%10)
		id /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
