package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	stripesdk "github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/customer"
	"github.com/stripe/stripe-go/v78/invoice"
	"github.com/stripe/stripe-go/v78/paymentmethod"
	"github.com/stripe/stripe-go/v78/subscription"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// subscriptionBlocker ports the SubscriptionExpired / TrialExpired /
// SeatsExceeded pydantic models.
type subscriptionBlocker struct {
	Kind           string  `json:"kind"`
	AllowedUserIDs []int64 `json:"allowed_user_ids,omitempty"`
}

const (
	blockerSubscriptionExpired = "subscription_expired"
	blockerTrialExpired        = "trial_expired"
	blockerSeatsExceeded       = "seats_exceeded"
)

// address ports the Address dataclass from models.py.
type address struct {
	Line1      *string
	City       *string
	Country    *string
	Line2      *string
	PostalCode *string
	State      *string
}

// getSubscriptionBlocker ports Account.get_subscription_blocker. Returns nil
// if there is a valid trial or subscription.
func (a *App) getSubscriptionBlocker(ctx context.Context, account *models.Account) (*subscriptionBlocker, error) {
	if !account.CanSubscribe() {
		return nil, nil
	}
	if account.ActiveTrial() {
		return nil, nil
	}
	customerInfo, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return nil, err
	}
	var subscriptionQuantity int
	if customerInfo != nil {
		subscriptionQuantity = int(customerInfo.SubscriptionQuantity)
	}
	activeUsers, err := models.GetActiveUsersInLast30Days(ctx, a.DB, account.GithubInstallationID)
	if err != nil {
		return nil, err
	}
	if subscriptionQuantity < len(activeUsers) {
		allowedUserIDs := make([]int64, 0, subscriptionQuantity)
		for _, u := range activeUsers[:subscriptionQuantity] {
			allowedUserIDs = append(allowedUserIDs, u.GithubID)
		}
		return &subscriptionBlocker{Kind: blockerSeatsExceeded, AllowedUserIDs: allowedUserIDs}, nil
	}
	if customerInfo != nil {
		if customerInfo.Expired() {
			return &subscriptionBlocker{Kind: blockerSubscriptionExpired}, nil
		}
		// active subscription within active user limits.
		return nil, nil
	}
	if account.TrialExpired() {
		return &subscriptionBlocker{Kind: blockerTrialExpired}, nil
	}
	// the user has never signed up for a trial or subscription and has 0
	// active users.
	return nil, nil
}

// updateBot ports Account.update_bot: refresh subscription information in
// Redis for the GitHub bot to load.
func (a *App) updateBot(ctx context.Context, account *models.Account) error {
	key := fmt.Sprintf("kodiak:subscription:%d", account.GithubInstallationID)
	if err := a.Redis.HSet(ctx, key, "account_id", account.ID.String()).Err(); err != nil {
		return err
	}
	blocker, err := a.getSubscriptionBlocker(ctx, account)
	if err != nil {
		return err
	}
	if blocker != nil {
		data, err := json.Marshal(blocker)
		if err != nil {
			return err
		}
		if err := a.Redis.HSet(ctx, key, "subscription_blocker", blocker.Kind, "data", string(data)).Err(); err != nil {
			return err
		}
	} else if err := a.Redis.HSet(ctx, key, "subscription_blocker", "", "data", "").Err(); err != nil {
		return err
	}

	// Trigger bot to reevaluate pull request mergeability.
	// We can use this to trigger the bot to remove the paywall status message
	// on upgrades.
	msg, err := json.Marshal(map[string]string{
		"installation_id": strconv.FormatInt(account.GithubInstallationID, 10),
	})
	if err != nil {
		return err
	}
	return a.Redis.RPush(ctx, "kodiak:refresh_pull_requests_for_installation", string(msg)).Err()
}

// startTrial ports Account.start_trial: start the timer for the trial and
// create a Stripe customer.
func (a *App) startTrial(ctx context.Context, account *models.Account, actor *models.User, billingEmail string) error {
	const trialLengthDays = 30
	account.TrialEmail = billingEmail
	if account.TrialExpiration == nil {
		expiration := time.Now().Add(trialLengthDays * 24 * time.Hour)
		start := time.Now()
		account.TrialExpiration = &expiration
		account.TrialStart = &start
		actorID := actor.ID
		account.TrialStartedByID = &actorID
	} else {
		slog.Warn("attempted to start trial when one already exists")
	}
	if account.StripeCustomerID == "" {
		cust, err := customer.New(&stripesdk.CustomerParams{Email: stripesdk.String(billingEmail)})
		if err != nil {
			return err
		}
		account.StripeCustomerID = cust.ID
	} else if _, err := customer.Update(account.StripeCustomerID, &stripesdk.CustomerParams{
		Email: stripesdk.String(billingEmail),
	}); err != nil {
		return err
	}
	if err := models.SaveAccount(ctx, a.DB, account); err != nil {
		return err
	}
	return a.updateBot(ctx, account)
}

// updateBillingInfo ports Account.update_billing_info.
func (a *App) updateBillingInfo(ctx context.Context, account *models.Account, email, name *string, addr *address) error {
	if account.StripeCustomerID == "" {
		return nil
	}
	params := &stripesdk.CustomerParams{Email: email, Name: name}
	if addr != nil {
		params.Address = &stripesdk.AddressParams{
			Line1:      addr.Line1,
			City:       addr.City,
			Country:    addr.Country,
			Line2:      addr.Line2,
			PostalCode: addr.PostalCode,
			State:      addr.State,
		}
	}
	if _, err := customer.Update(account.StripeCustomerID, params); err != nil {
		return err
	}
	info, err := models.GetStripeCustomerInfo(ctx, a.DB, account.StripeCustomerID)
	if err != nil {
		return err
	}
	if info == nil {
		return nil
	}
	if email != nil {
		info.CustomerEmail = *email
	}
	if name != nil {
		info.CustomerName = name
	}
	if addr != nil {
		info.CustomerAddressLine1 = addr.Line1
		info.CustomerAddressCity = addr.City
		info.CustomerAddressCountry = addr.Country
		info.CustomerAddressLine2 = addr.Line2
		info.CustomerAddressPostalCode = addr.PostalCode
		info.CustomerAddressState = addr.State
	}
	return models.SaveStripeCustomerInfo(ctx, a.DB, info)
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// updateCustomerInfoFromStripe ports StripeCustomerInformation.update_from_stripe:
// fetch the customer, subscription, and payment method from Stripe, upsert the
// local copy, then refresh the bot's view of the account.
func (a *App) updateCustomerInfoFromStripe(ctx context.Context, customerID string) error {
	custParams := &stripesdk.CustomerParams{}
	custParams.AddExpand("subscriptions")
	cust, err := customer.Get(customerID, custParams)
	if err != nil {
		return err
	}
	if cust.Subscriptions == nil || len(cust.Subscriptions.Data) == 0 {
		return fmt.Errorf("expected customer %s to have a subscription", customerID)
	}
	sub, err := subscription.Get(cust.Subscriptions.Data[0].ID, nil)
	if err != nil {
		return err
	}
	pmIter := paymentmethod.List(&stripesdk.PaymentMethodListParams{
		Customer: stripesdk.String(cust.ID),
		Type:     stripesdk.String("card"),
	})
	var firstPaymentMethodID string
	for pmIter.Next() {
		firstPaymentMethodID = pmIter.PaymentMethod().ID
		break
	}
	if err := pmIter.Err(); err != nil {
		return err
	}
	if firstPaymentMethodID == "" {
		return fmt.Errorf("expected customer %s to have a payment method", customerID)
	}
	pm, err := paymentmethod.Get(firstPaymentMethodID, nil)
	if err != nil {
		return err
	}

	info := &models.StripeCustomerInformation{CustomerID: cust.ID}
	info.CustomerEmail = cust.Email
	info.CustomerBalance = cust.Balance
	info.CustomerCreated = cust.Created
	info.CustomerCurrency = optionalString(string(cust.Currency))
	info.CustomerName = optionalString(cust.Name)
	if cust.Address != nil {
		info.CustomerAddressLine1 = optionalString(cust.Address.Line1)
		info.CustomerAddressCity = optionalString(cust.Address.City)
		info.CustomerAddressCountry = optionalString(cust.Address.Country)
		info.CustomerAddressLine2 = optionalString(cust.Address.Line2)
		info.CustomerAddressPostalCode = optionalString(cust.Address.PostalCode)
		info.CustomerAddressState = optionalString(cust.Address.State)
	}

	info.PaymentMethodID = pm.ID
	if pm.Card != nil {
		info.PaymentMethodCardBrand = optionalString(string(pm.Card.Brand))
		info.PaymentMethodCardExpMonth = optionalString(strconv.FormatInt(pm.Card.ExpMonth, 10))
		info.PaymentMethodCardExpYear = optionalString(strconv.FormatInt(pm.Card.ExpYear, 10))
		info.PaymentMethodCardLast4 = optionalString(pm.Card.Last4)
	}

	// NOTE(port): stripe-go v78 removed Subscription.plan/quantity; the
	// equivalent data lives on the subscription's first item.
	plan, quantity := subscriptionPlanQuantity(sub)
	if plan == nil {
		return fmt.Errorf("expected subscription %s to have a plan", sub.ID)
	}
	info.PlanID = plan.ID
	info.PlanAmount = plan.Amount
	info.PlanInterval = optionalString(string(plan.Interval))
	intervalCount := plan.IntervalCount
	info.PlanIntervalCount = &intervalCount

	info.SubscriptionID = sub.ID
	info.SubscriptionQuantity = quantity
	info.SubscriptionStartDate = sub.StartDate
	info.SubscriptionCurrentPeriodEnd = sub.CurrentPeriodEnd
	info.SubscriptionCurrentPeriodStart = sub.CurrentPeriodStart

	if err := models.SaveStripeCustomerInfo(ctx, a.DB, info); err != nil {
		return err
	}

	account, err := models.GetAccountByStripeCustomerID(ctx, a.DB, info.CustomerID)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("expected account for stripe customer %s", info.CustomerID)
	}
	return a.updateBot(ctx, account)
}

func subscriptionPlanQuantity(sub *stripesdk.Subscription) (*stripesdk.Plan, int64) {
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return nil, 0
	}
	return sub.Items.Data[0].Plan, sub.Items.Data[0].Quantity
}

// cancelSubscription ports StripeCustomerInformation.cancel_subscription:
// cancel the subscription in Stripe and remove the local subscription info,
// keeping the Stripe Customer for future checkout flows.
func (a *App) cancelSubscription(ctx context.Context, info *models.StripeCustomerInformation) error {
	if _, err := subscription.Cancel(info.SubscriptionID, nil); err != nil {
		return err
	}
	account, err := models.GetAccountByStripeCustomerID(ctx, a.DB, info.CustomerID)
	if err != nil {
		return err
	}
	if err := models.DeleteStripeCustomerInfo(ctx, a.DB, info.CustomerID); err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("expected account for stripe customer %s", info.CustomerID)
	}
	return a.updateBot(ctx, account)
}

// previewProration ports StripeCustomerInformation.preview_proration: see
// what the next invoice would look like with a plan switch and proration set.
func (a *App) previewProration(info *models.StripeCustomerInformation, timestamp int64, subscriptionQuantity int64, planID string) (int64, error) {
	sub, err := subscription.Get(info.SubscriptionID, nil)
	if err != nil {
		return 0, err
	}
	if sub.Items == nil || len(sub.Items.Data) == 0 {
		return 0, fmt.Errorf("expected subscription %s to have items", sub.ID)
	}
	inv, err := invoice.Upcoming(&stripesdk.InvoiceUpcomingParams{
		Customer:     stripesdk.String(info.CustomerID),
		Subscription: stripesdk.String(info.SubscriptionID),
		SubscriptionItems: []*stripesdk.SubscriptionItemsParams{{
			ID:       stripesdk.String(sub.Items.Data[0].ID),
			Plan:     stripesdk.String(planID),
			Quantity: stripesdk.Int64(subscriptionQuantity),
		}},
		SubscriptionProrationDate: stripesdk.Int64(timestamp),
	})
	if err != nil {
		return 0, err
	}
	// Calculate the proration cost.
	var total int64
	if inv.Lines != nil {
		for _, line := range inv.Lines.Data {
			if line.Period != nil && line.Period.Start-timestamp <= 1 {
				total += line.Amount
			}
		}
	}
	return total, nil
}

// planIDFromPeriod ports views.plan_id_from_period.
func (a *App) planIDFromPeriod(period string) string {
	if period == "year" {
		return a.Cfg.StripeAnnualPlanID
	}
	return a.Cfg.StripePlanID
}
