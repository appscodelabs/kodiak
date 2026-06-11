package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	stripesdk "github.com/stripe/stripe-go/v78"
	billingportalsession "github.com/stripe/stripe-go/v78/billingportal/session"
	checkoutsession "github.com/stripe/stripe-go/v78/checkout/session"
	"github.com/stripe/stripe-go/v78/customer"
	"github.com/stripe/stripe-go/v78/invoice"
	"github.com/stripe/stripe-go/v78/paymentmethod"
	"github.com/stripe/stripe-go/v78/subscription"
	"github.com/stripe/stripe-go/v78/webhook"

	"github.com/appscodelabs/kodiak/internal/webapi/mergequeue"
	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

const defaultCurrency = "usd"

func (a *App) ping(w http.ResponseWriter, r *http.Request, user *models.User) error {
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) healthcheck(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

type actorResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ProfileImgURL string `json:"profileImgUrl"`
}

type trialResponse struct {
	StartDate string        `json:"startDate"`
	EndDate   string        `json:"endDate"`
	Expired   bool          `json:"expired"`
	StartedBy actorResponse `json:"startedBy"`
}

type customerAddressResponse struct {
	Line1      *string `json:"line1"`
	City       *string `json:"city"`
	Country    *string `json:"country"`
	Line2      *string `json:"line2"`
	PostalCode *string `json:"postalCode"`
	State      *string `json:"state"`
}

type subscriptionCostResponse struct {
	TotalCents   int64  `json:"totalCents"`
	PerSeatCents int64  `json:"perSeatCents"`
	Currency     string `json:"currency"`
	PlanInterval string `json:"planInterval"`
}

type subscriptionResponse struct {
	Seats                      int64                    `json:"seats"`
	NextBillingDate            string                   `json:"nextBillingDate"`
	Expired                    bool                     `json:"expired"`
	Cost                       subscriptionCostResponse `json:"cost"`
	BillingEmail               string                   `json:"billingEmail"`
	ContactEmails              string                   `json:"contactEmails"`
	CustomerName               *string                  `json:"customerName"`
	CustomerAddress            *customerAddressResponse `json:"customerAddress"`
	CardInfo                   string                   `json:"cardInfo"`
	ViewerIsOrgOwner           bool                     `json:"viewerIsOrgOwner"`
	ViewerCanModify            bool                     `json:"viewerCanModify"`
	LimitBillingAccessToOwners bool                     `json:"limitBillingAccessToOwners"`
}

type activeUserResponse struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	ProfileImgURL   string `json:"profileImgUrl"`
	Interactions    int    `json:"interactions"`
	LastActiveDate  string `json:"lastActiveDate"`
	FirstActiveDate string `json:"firstActiveDate"`
	HasSeatLicense  bool   `json:"hasSeatLicense"`
}

type subscriptionExemptionResponse struct {
	Message *string `json:"message"`
}

// pythonStr mirrors Python's str() of an Optional value used in f-strings.
func pythonStr(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

// pythonTitle mirrors str.title() for single-word card brands.
func pythonTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}

func (a *App) usageBilling(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	activeUsers, err := models.GetActiveUsersInLast30Days(ctx, a.DB, account.GithubInstallationID)
	if err != nil {
		return err
	}

	var trial *trialResponse
	if account.TrialStart != nil && account.TrialExpiration != nil && account.TrialStartedByID != nil {
		startedBy, err := models.GetUserByID(ctx, a.DB, *account.TrialStartedByID)
		if err != nil {
			return err
		}
		if startedBy != nil {
			trial = &trialResponse{
				StartDate: djangoDateTime(*account.TrialStart),
				EndDate:   djangoDateTime(*account.TrialExpiration),
				Expired:   account.TrialExpired(),
				StartedBy: actorResponse{
					ID:            startedBy.ID.String(),
					Name:          startedBy.GithubLogin,
					ProfileImgURL: startedBy.ProfileImage(),
				},
			}
		}
	}

	stripeCustomerInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return err
	}
	var subscriptionResp *subscriptionResponse
	if stripeCustomerInfo != nil {
		var customerAddress *customerAddressResponse
		if stripeCustomerInfo.CustomerAddressLine1 != nil {
			customerAddress = &customerAddressResponse{
				Line1:      stripeCustomerInfo.CustomerAddressLine1,
				City:       stripeCustomerInfo.CustomerAddressCity,
				Country:    stripeCustomerInfo.CustomerAddressCountry,
				Line2:      stripeCustomerInfo.CustomerAddressLine2,
				PostalCode: stripeCustomerInfo.CustomerAddressPostalCode,
				State:      stripeCustomerInfo.CustomerAddressState,
			}
		}
		planInterval := "month"
		if stripeCustomerInfo.PlanInterval != nil && *stripeCustomerInfo.PlanInterval == "year" {
			planInterval = "year"
		}
		brandTitle := "None"
		if stripeCustomerInfo.PaymentMethodCardBrand != nil {
			brandTitle = pythonTitle(*stripeCustomerInfo.PaymentMethodCardBrand)
		}
		isAdmin, err := models.IsAdmin(ctx, a.DB, account.ID, user.ID)
		if err != nil {
			return err
		}
		subscriptionResp = &subscriptionResponse{
			Seats:           stripeCustomerInfo.SubscriptionQuantity,
			NextBillingDate: naiveDateTime(stripeCustomerInfo.SubscriptionCurrentPeriodEnd),
			Expired:         stripeCustomerInfo.Expired(),
			Cost: subscriptionCostResponse{
				TotalCents:   stripeCustomerInfo.PlanAmount * stripeCustomerInfo.SubscriptionQuantity,
				PerSeatCents: stripeCustomerInfo.PlanAmount,
				Currency: func() string {
					if stripeCustomerInfo.CustomerCurrency != nil {
						return *stripeCustomerInfo.CustomerCurrency
					}
					return defaultCurrency
				}(),
				PlanInterval: planInterval,
			},
			BillingEmail:               stripeCustomerInfo.CustomerEmail,
			ContactEmails:              account.ContactEmails,
			CustomerName:               stripeCustomerInfo.CustomerName,
			CustomerAddress:            customerAddress,
			CardInfo:                   fmt.Sprintf("%s (%s)", brandTitle, pythonStr(stripeCustomerInfo.PaymentMethodCardLast4)),
			ViewerIsOrgOwner:           isAdmin,
			ViewerCanModify:            !account.LimitBillingAccessToOwners || isAdmin,
			LimitBillingAccessToOwners: account.LimitBillingAccessToOwners,
		}
	}

	var subscriptionQuantity int
	if stripeCustomerInfo != nil {
		subscriptionQuantity = int(stripeCustomerInfo.SubscriptionQuantity)
	}
	// we assign seats to users in order of first active for the last 30 days.
	sortedUsers := make([]models.ActiveUser, len(activeUsers))
	copy(sortedUsers, activeUsers)
	sort.SliceStable(sortedUsers, func(i, j int) bool {
		return sortedUsers[i].FirstActiveAt.Before(sortedUsers[j].FirstActiveAt)
	})
	allowedUserIDs := map[int64]bool{}
	for i, u := range sortedUsers {
		if i >= subscriptionQuantity {
			break
		}
		allowedUserIDs[u.GithubID] = true
	}
	activeUsersResp := []activeUserResponse{}
	for _, u := range activeUsers {
		activeUsersResp = append(activeUsersResp, activeUserResponse{
			ID:              u.GithubID,
			Name:            u.GithubLogin,
			ProfileImgURL:   u.ProfileImage(),
			Interactions:    u.DaysActive,
			LastActiveDate:  djangoDate(u.LastActiveAt),
			FirstActiveDate: djangoDate(u.FirstActiveAt),
			HasSeatLicense:  allowedUserIDs[u.GithubID],
		})
	}

	var subscriptionExemption *subscriptionExemptionResponse
	if account.SubscriptionExempt {
		subscriptionExemption = &subscriptionExemptionResponse{Message: account.SubscriptionExemptMessage}
	}

	writeJSON(w, 200, map[string]any{
		"accountCanSubscribe":   account.CanSubscribe(),
		"subscription":          subscriptionResp,
		"trial":                 trial,
		"activeUsers":           activeUsersResp,
		"subscriptionExemption": subscriptionExemption,
	})
	return nil
}

type activeMergeQueueResponse struct {
	Owner  string             `json:"owner"`
	Repo   string             `json:"repo"`
	Queues []mergequeue.Queue `json:"queues"`
}

func (a *App) activity(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	activities, err := models.ListPullRequestActivity(ctx, a.DB, account.GithubInstallationID)
	if err != nil {
		return err
	}
	kodiakLabels := []string{}
	kodiakApproved := []int{}
	kodiakMerged := []int{}
	kodiakUpdated := []int{}
	totalLabels := []string{}
	totalOpened := []int{}
	totalMerged := []int{}
	totalClosed := []int{}
	for _, day := range activities {
		kodiakLabels = append(kodiakLabels, djangoDate(day.Date))
		kodiakApproved = append(kodiakApproved, day.KodiakApproved)
		kodiakMerged = append(kodiakMerged, day.KodiakMerged)
		kodiakUpdated = append(kodiakUpdated, day.KodiakUpdated)
		totalLabels = append(totalLabels, djangoDate(day.Date))
		totalOpened = append(totalOpened, day.TotalOpened)
		totalMerged = append(totalMerged, day.TotalMerged)
		totalClosed = append(totalClosed, day.TotalClosed)
	}

	repos, err := mergequeue.GetActiveMergeQueues(ctx, a.Redis, strconv.FormatInt(account.GithubInstallationID, 10))
	if err != nil {
		return err
	}
	activeMergeQueues := []activeMergeQueueResponse{}
	for _, repo := range repos {
		activeMergeQueues = append(activeMergeQueues, activeMergeQueueResponse{
			Owner:  repo.Owner,
			Repo:   repo.Repo,
			Queues: repo.Queues,
		})
	}

	writeJSON(w, 200, map[string]any{
		"kodiakActivity": map[string]any{
			"labels": kodiakLabels,
			"datasets": map[string]any{
				"approved": kodiakApproved,
				"merged":   kodiakMerged,
				"updated":  kodiakUpdated,
			},
		},
		"pullRequestActivity": map[string]any{
			"labels": totalLabels,
			"datasets": map[string]any{
				"opened": totalOpened,
				"merged": totalMerged,
				"closed": totalClosed,
			},
		},
		"activeMergeQueues": activeMergeQueues,
	})
	return nil
}

func accountResponse(a *models.Account) actorResponse {
	return actorResponse{
		ID:            a.ID.String(),
		Name:          a.GithubAccountLogin,
		ProfileImgURL: a.ProfileImage(),
	}
}

func (a *App) currentAccount(w http.ResponseWriter, r *http.Request, user *models.User) error {
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	allAccounts, err := models.ListAccountsForUser(r.Context(), a.DB, user.ID, false)
	if err != nil {
		return err
	}
	accountsResp := []actorResponse{}
	for _, x := range allAccounts {
		accountsResp = append(accountsResp, accountResponse(x))
	}
	writeJSON(w, 200, map[string]any{
		"user": actorResponse{
			ID:            user.ID.String(),
			Name:          user.GithubLogin,
			ProfileImgURL: user.ProfileImage(),
		},
		"org":      accountResponse(account),
		"accounts": accountsResp,
	})
	return nil
}

func (a *App) startTrialView(w http.ResponseWriter, r *http.Request, user *models.User) error {
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return errBadRequest()
	}
	billingEmail := r.PostFormValue("billingEmail")
	if !r.PostForm.Has("billingEmail") {
		return errBadRequest()
	}
	if err := a.startTrial(r.Context(), account, user, billingEmail); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (a *App) updateSubscription(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		return errBadRequest()
	}
	seats, err := strconv.ParseInt(r.PostFormValue("seats"), 10, 64)
	if err != nil {
		return &apiError{code: 400, message: "invalid seats parameter"}
	}
	prorationTimestamp, err := strconv.ParseInt(r.PostFormValue("prorationTimestamp"), 10, 64)
	if err != nil {
		return &apiError{code: 400, message: "invalid prorationTimestamp parameter"}
	}
	planPeriod := r.PostFormValue("planPeriod")
	if planPeriod == "" {
		planPeriod = "month"
	}
	if planPeriod != "month" && planPeriod != "year" {
		return &apiError{code: 400, message: "invalid planPeriod parameter"}
	}

	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	canEdit, err := a.canEditSubscription(ctx, user, account)
	if err != nil {
		return err
	}
	if !canEdit {
		return errPermissionDenied()
	}
	stripeCustomerInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return err
	}
	if stripeCustomerInfo == nil {
		return errUnprocessable("Subscription does not exist to modify.")
	}

	sub, err := subscription.Get(stripeCustomerInfo.SubscriptionID, nil)
	if err != nil {
		return err
	}
	currentPlan, _ := subscriptionPlanQuantity(sub)
	if currentPlan == nil {
		return fmt.Errorf("expected subscription %s to have a plan", sub.ID)
	}
	updatedSub, err := subscription.Update(sub.ID, &stripesdk.SubscriptionParams{
		Items: []*stripesdk.SubscriptionItemsParams{{
			ID:       stripesdk.String(sub.Items.Data[0].ID),
			Plan:     stripesdk.String(a.planIDFromPeriod(planPeriod)),
			Quantity: stripesdk.Int64(seats),
		}},
		ProrationDate: stripesdk.Int64(prorationTimestamp),
	})
	if err != nil {
		return err
	}
	// we only need to manually create invoices when a user modifies their
	// subscription within the same billing period.
	if planPeriod == string(currentPlan.Interval) {
		// when we upgrade a user's plan Stripe will charge them on their next
		// billing cycle. To make Stripe charge for the upgrade immediately we
		// must create an invoice and pay it. If we don't pay the invoice
		// Stripe will wait 1 hour before attempting to charge the user.
		inv, err := invoice.New(&stripesdk.InvoiceParams{
			Customer:    stripesdk.String(stripeCustomerInfo.CustomerID),
			AutoAdvance: stripesdk.Bool(true),
		})
		if err != nil {
			return err
		}
		// we must specify the payment method because our Stripe customers
		// don't have a default payment method, so the default invoicing
		// would fail.
		pmIter := paymentmethod.List(&stripesdk.PaymentMethodListParams{
			Customer: stripesdk.String(stripeCustomerInfo.CustomerID),
			Type:     stripesdk.String("card"),
		})
		var paymentMethodID string
		for pmIter.Next() {
			paymentMethodID = pmIter.PaymentMethod().ID
			break
		}
		if err := pmIter.Err(); err != nil {
			return err
		}
		if paymentMethodID == "" {
			return fmt.Errorf("expected customer %s to have a payment method", stripeCustomerInfo.CustomerID)
		}
		if _, err := invoice.Pay(inv.ID, &stripesdk.InvoicePayParams{
			PaymentMethod: stripesdk.String(paymentMethodID),
		}); err != nil {
			return err
		}
	}
	updatedPlan, updatedQuantity := subscriptionPlanQuantity(updatedSub)
	if updatedPlan == nil {
		return fmt.Errorf("expected subscription %s to have a plan", updatedSub.ID)
	}
	stripeCustomerInfo.PlanAmount = updatedPlan.Amount
	stripeCustomerInfo.PlanInterval = optionalString(string(updatedPlan.Interval))
	stripeCustomerInfo.SubscriptionQuantity = updatedQuantity
	stripeCustomerInfo.SubscriptionCurrentPeriodEnd = updatedSub.CurrentPeriodEnd
	stripeCustomerInfo.SubscriptionCurrentPeriodStart = updatedSub.CurrentPeriodStart
	if err := models.SaveStripeCustomerInfo(ctx, a.DB, stripeCustomerInfo); err != nil {
		return err
	}
	if err := a.updateBot(ctx, account); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

// canEditSubscription ports User.can_edit_subscription.
func (a *App) canEditSubscription(ctx context.Context, user *models.User, account *models.Account) (bool, error) {
	if !account.LimitBillingAccessToOwners {
		return true, nil
	}
	return models.IsAdmin(ctx, a.DB, account.ID, user.ID)
}

type updateBillingInfoPayload struct {
	Email                      *string                   `json:"email"`
	Name                       *string                   `json:"name"`
	Address                    *updateBillingInfoAddress `json:"address"`
	ContactEmails              *string                   `json:"contactEmails"`
	LimitBillingAccessToOwners *bool                     `json:"limitBillingAccessToOwners"`
}

type updateBillingInfoAddress struct {
	Line1      *string `json:"line1"`
	City       *string `json:"city"`
	Country    *string `json:"country"`
	Line2      *string `json:"line2"`
	PostalCode *string `json:"postalCode"`
	State      *string `json:"state"`
}

// updateStripeCustomerInfo allows users to update Stripe customer info.
func (a *App) updateStripeCustomerInfo(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return errBadRequest()
	}
	var payload updateBillingInfoPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return &apiError{code: 400, message: "invalid request body"}
	}
	if payload.Email != nil {
		if _, err := mail.ParseAddress(*payload.Email); err != nil {
			return &apiError{code: 400, message: "invalid email"}
		}
	}
	if payload.LimitBillingAccessToOwners != nil {
		isAdmin, err := models.IsAdmin(ctx, a.DB, account.ID, user.ID)
		if err != nil {
			return err
		}
		if !isAdmin {
			return errPermissionDenied()
		}
		account.LimitBillingAccessToOwners = *payload.LimitBillingAccessToOwners
		if err := models.SaveAccount(ctx, a.DB, account); err != nil {
			return err
		}
	}
	if payload.ContactEmails != nil {
		canEdit, err := a.canEditSubscription(ctx, user, account)
		if err != nil {
			return err
		}
		if !canEdit {
			return errPermissionDenied()
		}
		account.ContactEmails = *payload.ContactEmails
		if err := models.SaveAccount(ctx, a.DB, account); err != nil {
			return err
		}
	}
	if payload.Email != nil || payload.Name != nil || payload.Address != nil {
		canEdit, err := a.canEditSubscription(ctx, user, account)
		if err != nil {
			return err
		}
		if !canEdit {
			return errPermissionDenied()
		}
		var addr *address
		if payload.Address != nil {
			addr = &address{
				Line1:      payload.Address.Line1,
				City:       payload.Address.City,
				Country:    payload.Address.Country,
				Line2:      payload.Address.Line2,
				PostalCode: payload.Address.PostalCode,
				State:      payload.Address.State,
			}
		}
		if err := a.updateBillingInfo(ctx, account, payload.Email, payload.Name, addr); err != nil {
			return err
		}
	}
	w.WriteHeader(204)
	return nil
}

func (a *App) startCheckout(w http.ResponseWriter, r *http.Request, user *models.User) error {
	if err := r.ParseForm(); err != nil {
		return errBadRequest()
	}
	seatCount := int64(1)
	if v := r.PostFormValue("seatCount"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return &apiError{code: 400, message: "invalid seatCount parameter"}
		}
		seatCount = parsed
	}
	planPeriod := r.PostFormValue("planPeriod")
	if planPeriod == "" {
		planPeriod = "month"
	}
	if planPeriod != "month" && planPeriod != "year" {
		return &apiError{code: 400, message: "invalid planPeriod parameter"}
	}
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	params := &stripesdk.CheckoutSessionParams{
		ClientReferenceID: stripesdk.String(account.ID.String()),
		// cards only work with subscriptions and StripeCustomerInformation
		// depends on credit cards
		// (payment_method_card_{brand,exp_month,exp_year,last4}).
		PaymentMethodTypes: stripesdk.StringSlice([]string{"card"}),
		// NOTE(port): newer Stripe API versions replaced
		// subscription_data[items] with mode=subscription + line_items.
		Mode: stripesdk.String(string(stripesdk.CheckoutSessionModeSubscription)),
		LineItems: []*stripesdk.CheckoutSessionLineItemParams{{
			Price:    stripesdk.String(a.planIDFromPeriod(planPeriod)),
			Quantity: stripesdk.Int64(seatCount),
		}},
		SuccessURL: stripesdk.String(fmt.Sprintf("%s/t/%s/usage?install_complete=1", a.Cfg.KodiakWebAppURL, account.ID)),
		CancelURL:  stripesdk.String(fmt.Sprintf("%s/t/%s/usage?start_subscription=1", a.Cfg.KodiakWebAppURL, account.ID)),
	}
	// if available, using the existing customer_id allows us to pre-fill the
	// checkout form with their email.
	if account.StripeCustomerID != "" {
		params.Customer = stripesdk.String(account.StripeCustomerID)
	}
	sess, err := checkoutsession.New(params)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{
		"stripeCheckoutSessionId": sess.ID,
		"stripePublishableApiKey": a.Cfg.StripePublishableAPIKey,
	})
	return nil
}

// redirectToStripeSelfServePortal redirects the user to the temporary URL for
// the Stripe self-serve portal.
func (a *App) redirectToStripeSelfServePortal(w http.ResponseWriter, r *http.Request, user *models.User) error {
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	sess, err := billingportalsession.New(&stripesdk.BillingPortalSessionParams{
		Customer:  stripesdk.String(account.StripeCustomerID),
		ReturnURL: stripesdk.String(fmt.Sprintf("%s/t/%s/usage", a.Cfg.KodiakWebAppURL, account.ID)),
	})
	if err != nil {
		return err
	}
	http.Redirect(w, r, sess.URL, http.StatusFound)
	return nil
}

func (a *App) modifyPaymentDetails(w http.ResponseWriter, r *http.Request, user *models.User) error {
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	canEdit, err := a.canEditSubscription(r.Context(), user, account)
	if err != nil {
		return err
	}
	if !canEdit {
		return errPermissionDenied()
	}
	params := &stripesdk.CheckoutSessionParams{
		ClientReferenceID:  stripesdk.String(account.ID.String()),
		Mode:               stripesdk.String(string(stripesdk.CheckoutSessionModeSetup)),
		PaymentMethodTypes: stripesdk.StringSlice([]string{"card"}),
		SuccessURL:         stripesdk.String(fmt.Sprintf("%s/t/%s/usage?install_complete=1", a.Cfg.KodiakWebAppURL, account.ID)),
		CancelURL:          stripesdk.String(fmt.Sprintf("%s/t/%s/usage?modify_subscription=1", a.Cfg.KodiakWebAppURL, account.ID)),
	}
	if account.StripeCustomerID != "" {
		params.Customer = stripesdk.String(account.StripeCustomerID)
	}
	sess, err := checkoutsession.New(params)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{
		"stripeCheckoutSessionId": sess.ID,
		"stripePublishableApiKey": a.Cfg.StripePublishableAPIKey,
	})
	return nil
}

func (a *App) cancelSubscriptionView(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	canEdit, err := a.canEditSubscription(ctx, user, account)
	if err != nil {
		return err
	}
	if !canEdit {
		return errPermissionDenied()
	}
	customerInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return err
	}
	if customerInfo != nil {
		if err := a.cancelSubscription(ctx, customerInfo); err != nil {
			return err
		}
	}
	w.WriteHeader(204)
	return nil
}

func (a *App) getSubscriptionInfo(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	subscriptionStatus, err := a.getSubscriptionBlocker(ctx, account)
	if err != nil {
		return err
	}
	if subscriptionStatus != nil {
		switch subscriptionStatus.Kind {
		case blockerTrialExpired:
			writeJSON(w, 200, map[string]string{"type": "TRIAL_EXPIRED"})
			return nil
		case blockerSeatsExceeded:
			stripeInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
			if err != nil {
				return err
			}
			var licenseCount int64
			if stripeInfo != nil && stripeInfo.SubscriptionQuantity > 0 {
				licenseCount = stripeInfo.SubscriptionQuantity
			}
			activeUsers, err := models.GetActiveUsersInLast30Days(ctx, a.DB, account.GithubInstallationID)
			if err != nil {
				return err
			}
			writeJSON(w, 200, map[string]any{
				"type":            "SUBSCRIPTION_OVERAGE",
				"activeUserCount": len(activeUsers),
				"licenseCount":    licenseCount,
			})
			return nil
		case blockerSubscriptionExpired:
			writeJSON(w, 200, map[string]string{"type": "SUBSCRIPTION_EXPIRED"})
			return nil
		}
	}
	writeJSON(w, 200, map[string]string{"type": "VALID_SUBSCRIPTION"})
	return nil
}

func (a *App) fetchProration(w http.ResponseWriter, r *http.Request, user *models.User) error {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		return errBadRequest()
	}
	subscriptionQuantity, err := strconv.ParseInt(r.PostFormValue("subscriptionQuantity"), 10, 64)
	if err != nil {
		return &apiError{code: 400, message: "invalid subscriptionQuantity parameter"}
	}
	subscriptionPeriod := r.PostFormValue("subscriptionPeriod")
	if subscriptionPeriod == "" {
		subscriptionPeriod = "month"
	}
	if subscriptionPeriod != "month" && subscriptionPeriod != "year" {
		return &apiError{code: 400, message: "invalid subscriptionPeriod parameter"}
	}
	account, err := a.teamAccount(r, user)
	if err != nil {
		return err
	}
	customerInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return err
	}
	if customerInfo == nil {
		w.WriteHeader(500)
		return nil
	}
	prorationDate := time.Now().Unix()
	proratedCost, err := a.previewProration(customerInfo, prorationDate, subscriptionQuantity, a.planIDFromPeriod(subscriptionPeriod))
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]int64{
		"proratedCost":  proratedCost,
		"prorationTime": prorationDate,
	})
	return nil
}

// stripeWebhookHandler processes Stripe webhook events.
//
// After checkout, Stripe sends a checkout.session.completed event. Stripe
// will wait up to 10 seconds for this webhook to return before redirecting a
// user back to the return URL.
func (a *App) stripeWebhookHandler(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return errBadRequest()
	}
	event, err := webhook.ConstructEvent(body, r.Header.Get("Stripe-Signature"), a.Cfg.StripeWebhookSecret)
	if err != nil {
		slog.Warn("problem parsing stripe payload", "error", err)
		return errBadRequest()
	}
	slog.Info("stripe webhook", "stripe_event_type", event.Type)

	switch string(event.Type) {
	// triggered when a customer completes the Stripe Checkout form.
	case "checkout.session.completed":
		var checkoutSession stripesdk.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &checkoutSession); err != nil {
			return errBadRequest()
		}
		switch checkoutSession.Mode {
		case stripesdk.CheckoutSessionModeSetup, stripesdk.CheckoutSessionModeSubscription:
			// setup: a user updated their payment method or billing email.
			// subscription: a user created a subscription through checkout.
			accountID, err := uuid.Parse(checkoutSession.ClientReferenceID)
			if err != nil {
				return errBadRequest()
			}
			account, err := models.GetAccountByID(ctx, a.DB, accountID)
			if err != nil {
				return err
			}
			if account == nil {
				return fmt.Errorf("account %s not found for checkout session", checkoutSession.ClientReferenceID)
			}
			customerID := ""
			if checkoutSession.Customer != nil {
				customerID = checkoutSession.Customer.ID
			}
			account.StripeCustomerID = customerID
			if err := models.SaveAccount(ctx, a.DB, account); err != nil {
				return err
			}
			if checkoutSession.Mode == stripesdk.CheckoutSessionModeSetup {
				info, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
				if err != nil {
					return err
				}
				if info == nil {
					slog.Warn("expected account to have customer info", "account_id", account.ID)
					w.WriteHeader(200)
					return nil
				}
			}
			if err := a.updateCustomerInfoFromStripe(ctx, account.StripeCustomerID); err != nil {
				return err
			}
		default:
			return errBadRequest()
		}
	case "invoice.payment_succeeded":
		// triggered whenever a subscription is paid. We need to update the
		// subscription to have the correct period information.
		var inv stripesdk.Invoice
		if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
			return errBadRequest()
		}
		if !inv.Paid {
			slog.Warn("invoice not paid", "event_id", event.ID)
			w.WriteHeader(200)
			return nil
		}
		customerID := ""
		if inv.Customer != nil {
			customerID = inv.Customer.ID
		}
		info, err := models.GetStripeCustomerInfo(ctx, a.DB, customerID)
		if err != nil {
			return err
		}
		if info == nil {
			slog.Warn("expected invoice to have corresponding StripeCustomerInformation")
			return errBadRequest()
		}
		if err := a.updateCustomerInfoFromStripe(ctx, info.CustomerID); err != nil {
			return err
		}
	case "customer.subscription.deleted":
		var sub stripesdk.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			return errBadRequest()
		}
		if sub.Customer != nil {
			if err := models.DeleteStripeCustomerInfo(ctx, a.DB, sub.Customer.ID); err != nil {
				return err
			}
		}
	case "invoice.payment_action_required":
		slog.Warn("more action required for payment", "event_id", event.ID)
	case "invoice.payment_failed":
		slog.Warn("invoice.payment_failed", "event_id", event.ID)
	case "customer.updated":
		var cust stripesdk.Customer
		if err := json.Unmarshal(event.Data.Raw, &cust); err != nil {
			return errBadRequest()
		}
		info, err := models.GetStripeCustomerInfo(ctx, a.DB, cust.ID)
		if err != nil {
			return err
		}
		if info == nil {
			slog.Warn("customer.updated event for unknown customer", "event_id", event.ID)
			return errBadRequest()
		}
		if err := a.updateCustomerInfoFromStripe(ctx, info.CustomerID); err != nil {
			return err
		}
	case "payment_method.attached":
		// a customer should only have one payment method.
		//
		// When a customer attaches a new payment method, remove the old ones
		// to ensure subscriptions use the newly added method.
		var newPaymentMethod stripesdk.PaymentMethod
		if err := json.Unmarshal(event.Data.Raw, &newPaymentMethod); err != nil {
			return errBadRequest()
		}
		customerID := ""
		if newPaymentMethod.Customer != nil {
			customerID = newPaymentMethod.Customer.ID
		}
		pmIter := paymentmethod.List(&stripesdk.PaymentMethodListParams{
			Customer: stripesdk.String(customerID),
			Type:     stripesdk.String("card"),
		})
		for pmIter.Next() {
			pm := pmIter.PaymentMethod()
			if pm.ID != newPaymentMethod.ID {
				if _, err := paymentmethod.Detach(pm.ID, nil); err != nil {
					return err
				}
			}
		}
		if err := pmIter.Err(); err != nil {
			return err
		}
		if _, err := customer.Update(customerID, &stripesdk.CustomerParams{
			InvoiceSettings: &stripesdk.CustomerInvoiceSettingsParams{
				DefaultPaymentMethod: stripesdk.String(newPaymentMethod.ID),
			},
		}); err != nil {
			return err
		}
		info, err := models.GetStripeCustomerInfo(ctx, a.DB, customerID)
		if err != nil {
			return err
		}
		if info == nil {
			slog.Warn("payment_method.attached event for unknown customer", "event_id", event.ID)
			return errBadRequest()
		}
		if err := a.updateCustomerInfoFromStripe(ctx, info.CustomerID); err != nil {
			return err
		}
	default:
		slog.Warn("unexpected event type", "stripe_event_type", event.Type)
		return errBadRequest()
	}
	w.WriteHeader(200)
	return nil
}

func (a *App) accounts(w http.ResponseWriter, r *http.Request, user *models.User) error {
	allAccounts, err := models.ListAccountsForUser(r.Context(), a.DB, user.ID, true)
	if err != nil {
		return err
	}
	resp := []actorResponse{}
	for _, x := range allAccounts {
		resp = append(resp, accountResponse(x))
	}
	writeJSON(w, 200, resp)
	return nil
}

// oauthLogin is the entry point to the OAuth flow.
//
// We keep this as a simple endpoint on the API to redirect users to from the
// frontend. This way we keep complexity within the API.
func (a *App) oauthLogin(w http.ResponseWriter, r *http.Request) error {
	state := r.URL.Query().Get("state")
	if state == "" {
		return &plainError{code: 400, body: "Missing required state parameter"}
	}
	q := url.Values{
		"client_id":    {a.Cfg.KodiakAPIGithubClientID},
		"redirect_uri": {a.Cfg.KodiakWebAuthedLandingPath},
		"state":        {state},
	}
	http.Redirect(w, r, "https://github.com/login/oauth/authorize?"+q.Encode(), http.StatusFound)
	return nil
}

type loginErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Ok               bool   `json:"ok"`
}

type loginSuccessResponse struct {
	Ok bool `json:"ok"`
}

// processLoginRequest ports views.process_login_request.
func (a *App) processLoginRequest(w http.ResponseWriter, r *http.Request) any {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		return loginErrorResponse{
			Error:            "OAuthStateMismatch",
			ErrorDescription: "State parameters must match.",
		}
	}
	sessionOAuthState := r.PostFormValue("serverState")
	requestOAuthState := r.PostFormValue("clientState")
	if sessionOAuthState == "" || requestOAuthState == "" || sessionOAuthState != requestOAuthState {
		return loginErrorResponse{
			Error:            "OAuthStateMismatch",
			ErrorDescription: "State parameters must match.",
		}
	}
	if oauthError := r.PostFormValue("error"); oauthError != "" {
		return loginErrorResponse{
			Error:            oauthError,
			ErrorDescription: r.PostFormValue("error_description"),
		}
	}
	code := r.PostFormValue("code")
	if code == "" {
		return loginErrorResponse{
			Error:            "OAuthMissingCode",
			ErrorDescription: "Payload should have a code parameter.",
		}
	}

	accessRes, err := a.exchangeOAuthCode(ctx, code)
	if err != nil {
		return loginErrorResponse{
			Error:            "OAuthServerError",
			ErrorDescription: "Failed to fetch access token.",
		}
	}
	if accessTokenError := accessRes.Get("error"); accessTokenError != "" {
		return loginErrorResponse{
			Error:            accessTokenError,
			ErrorDescription: accessRes.Get("error_description"),
		}
	}
	accessToken := accessRes.Get("access_token")
	if accessToken == "" {
		return loginErrorResponse{
			Error:            "OAuthMissingAccessToken",
			ErrorDescription: "OAuth missing access token.",
		}
	}

	// fetch information about the user using their oauth access token.
	userData, err := a.fetchGithubUser(ctx, accessToken)
	if err != nil {
		return loginErrorResponse{
			Error:            "OAuthServerError",
			ErrorDescription: "Failed to fetch account information from GitHub.",
		}
	}

	existingUser, err := models.GetUserByGithubID(ctx, a.DB, userData.ID)
	if err != nil {
		slog.Error("failed to fetch user", "error", err)
		return loginErrorResponse{
			Error:            "OAuthServerError",
			ErrorDescription: "Failed to fetch account information from GitHub.",
		}
	}
	var user *models.User
	if existingUser != nil {
		existingUser.GithubLogin = userData.Login
		existingUser.GithubAccessToken = accessToken
		if err := models.SaveUser(ctx, a.DB, existingUser); err != nil {
			slog.Error("failed to save user", "error", err)
			return loginErrorResponse{
				Error:            "AccountSyncFailure",
				ErrorDescription: "Failed to sync GitHub accounts for user.",
			}
		}
		user = existingUser
	} else {
		user, err = models.CreateUser(ctx, a.DB, userData.ID, userData.Login, accessToken)
		if err != nil {
			slog.Error("failed to create user", "error", err)
			return loginErrorResponse{
				Error:            "AccountSyncFailure",
				ErrorDescription: "Failed to sync GitHub accounts for user.",
			}
		}
	}
	// TODO(chdsbd): Run this in as a background job if the user is an
	// existing user.
	if err := a.syncAccounts(ctx, user); err != nil {
		slog.Warn("sync_accounts failed", "error", err)
		// ignore the errors if we were an existing user as we can use old
		// data.
		if existingUser == nil {
			return loginErrorResponse{
				Error:            "AccountSyncFailure",
				ErrorDescription: "Failed to sync GitHub accounts for user.",
			}
		}
	}

	a.Sessions.Login(w, user.ID.String())
	return loginSuccessResponse{Ok: true}
}

// oauthComplete is the OAuth callback handler from GitHub.
//
// We get a code from GitHub that we can use with our client secret to get an
// OAuth token for a GitHub user. The GitHub OAuth token only expires when the
// user uninstalls the app.
func (a *App) oauthComplete(w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case http.MethodPost:
		writeJSON(w, 200, a.processLoginRequest(w, r))
		return nil
	case http.MethodOptions:
		w.WriteHeader(200)
		return nil
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return nil
	}
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	a.Sessions.Logout(w)
	w.WriteHeader(201)
	return nil
}

func (a *App) syncAccountsView(w http.ResponseWriter, r *http.Request, user *models.User) error {
	if err := a.syncAccounts(r.Context(), user); err != nil {
		writeJSON(w, 200, map[string]bool{"ok": false})
		return nil
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (a *App) debugSentry(w http.ResponseWriter, r *http.Request) error {
	// the Django view raises ZeroDivisionError to test error reporting.
	return fmt.Errorf("debug_sentry: intentional error")
}
