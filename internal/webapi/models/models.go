// Package models holds the database row types used by the dashboard backend.
//
// Ported from web_api/core/models.py. Django's ORM is replaced by plain Go
// structs annotated for pgx — the Django Meta unique constraints and indexes
// are reproduced as comments next to each struct so the migration SQL can be
// generated from one place.
package models

import (
	"time"

	"github.com/google/uuid"
)

// AccountType matches core.models.AccountType.
type AccountType string

const (
	AccountUser         AccountType = "User"
	AccountOrganization AccountType = "Organization"
)

// AccountRole matches core.models.AccountRole.
type AccountRole string

const (
	RoleMember AccountRole = "member"
	RoleAdmin  AccountRole = "admin"
)

// User is the GitHub user that authenticated via OAuth.
//
// Source: core.models.User. Unique on github_id.
type User struct {
	ID                  uuid.UUID `db:"id"`
	CreatedAt           time.Time `db:"created_at"`
	ModifiedAt          time.Time `db:"modified_at"`
	GitHubID            int64     `db:"github_id"`
	GitHubLogin         string    `db:"github_login"`
	GitHubAccessToken   string    `db:"github_access_token"`
}

// Account is a GitHub user or organization that has the Kodiak app installed.
//
// Source: core.models.Account. Unique on github_account_id.
type Account struct {
	ID                  uuid.UUID   `db:"id"`
	CreatedAt           time.Time   `db:"created_at"`
	ModifiedAt          time.Time   `db:"modified_at"`
	GitHubInstallationID int64      `db:"github_installation_id"`
	GitHubAccountID     int64       `db:"github_account_id"`
	GitHubAccountLogin  string      `db:"github_account_login"`
	GitHubAccountType   AccountType `db:"github_account_type"`
	StripeCustomerID    string      `db:"stripe_customer_id"`
	TrialExpiration     *time.Time  `db:"trial_expiration"`
	TrialStart          *time.Time  `db:"trial_start"`
	TrialStartedBy      *uuid.UUID  `db:"trial_started_by_id"`
	SubscriptionExempt  bool        `db:"subscription_exempt"`
	SubscriptionExemptMessage string `db:"subscription_exempt_message"`
}

// AccountMembership joins users to accounts.
//
// Source: core.models.AccountMembership. Unique on (account_id, user_id).
type AccountMembership struct {
	ID         uuid.UUID   `db:"id"`
	CreatedAt  time.Time   `db:"created_at"`
	ModifiedAt time.Time   `db:"modified_at"`
	AccountID  uuid.UUID   `db:"account_id"`
	UserID     uuid.UUID   `db:"user_id"`
	Role       AccountRole `db:"role"`
}

// PullRequestActivity is the per-day, per-repo aggregate consumed by the
// dashboard charts.
//
// Source: core.models.PullRequestActivity. Unique on (date, github_repository_id).
type PullRequestActivity struct {
	ID                       uuid.UUID `db:"id"`
	CreatedAt                time.Time `db:"created_at"`
	ModifiedAt               time.Time `db:"modified_at"`
	Date                     time.Time `db:"date"`
	GitHubRepositoryID       int64     `db:"github_repository_id"`
	GitHubInstallationID     int64     `db:"github_installation_id"`
	OpenedPullRequests       int       `db:"opened_pull_requests"`
	MergedPullRequests       int       `db:"merged_pull_requests"`
	ClosedPullRequests       int       `db:"closed_pull_requests"`
	KodiakApprovals          int       `db:"kodiak_approvals"`
	KodiakMerges             int       `db:"kodiak_merges"`
	KodiakUpdates            int       `db:"kodiak_updates"`
	TotalOpenedPullRequests  int       `db:"total_opened_pull_requests"`
	TotalMergedPullRequests  int       `db:"total_merged_pull_requests"`
	TotalClosedPullRequests  int       `db:"total_closed_pull_requests"`
}

// UserPullRequestActivity tracks per-user PR activity for seat accounting.
//
// Source: core.models.UserPullRequestActivity.
type UserPullRequestActivity struct {
	ID                 uuid.UUID `db:"id"`
	CreatedAt          time.Time `db:"created_at"`
	ModifiedAt         time.Time `db:"modified_at"`
	GitHubLogin        string    `db:"github_login"`
	GitHubUserID       int64     `db:"github_user_id"`
	GitHubRepositoryID int64     `db:"github_repository_id"`
	GitHubInstallationID int64   `db:"github_installation_id"`
	OpenedPRs          int       `db:"opened_prs"`
	MergedPRs          int       `db:"merged_prs"`
	ClosedPRs          int       `db:"closed_prs"`
	Date               time.Time `db:"date"`
}

// StripeCustomerInformation mirrors the local Stripe customer/subscription
// cache. Source: core.models.StripeCustomerInformation. Unique on customer_id.
type StripeCustomerInformation struct {
	CustomerID                 string    `db:"customer_id"`
	CustomerEmail              string    `db:"customer_email"`
	CustomerBalance            int64     `db:"customer_balance"`
	CustomerCurrency           string    `db:"customer_currency"`
	CustomerName               string    `db:"customer_name"`
	CustomerAddressLine1       string    `db:"customer_address_line1"`
	CustomerAddressCity        string    `db:"customer_address_city"`
	CustomerAddressCountry     string    `db:"customer_address_country"`
	CustomerAddressLine2       string    `db:"customer_address_line2"`
	CustomerAddressPostalCode  string    `db:"customer_address_postal_code"`
	CustomerAddressState       string    `db:"customer_address_state"`
	PaymentMethodID            string    `db:"payment_method_id"`
	PaymentMethodCardBrand     string    `db:"payment_method_card_brand"`
	PaymentMethodCardExpMonth  string    `db:"payment_method_card_exp_month"`
	PaymentMethodCardExpYear   string    `db:"payment_method_card_exp_year"`
	PaymentMethodCardLast4     string    `db:"payment_method_card_last4"`
	PlanID                     string    `db:"plan_id"`
	PlanAmount                 int64     `db:"plan_amount"`
	PlanInterval               string    `db:"plan_interval"`
	PlanIntervalCount          int       `db:"plan_interval_count"`
	SubscriptionID             string    `db:"subscription_id"`
	SubscriptionQuantity       int       `db:"subscription_quantity"`
	SubscriptionStartDate      time.Time `db:"subscription_start_date"`
	SubscriptionCurrentPeriodEnd   time.Time `db:"subscription_current_period_end"`
	SubscriptionCurrentPeriodStart time.Time `db:"subscription_current_period_start"`
}
