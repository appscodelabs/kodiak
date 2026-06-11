// Package models ports the Django ORM models from web_api/web_api/models.py
// to Go structs with hand-written SQL. Table names match the Django
// db_table settings.
package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// User is a GitHub user that can login to Kodiak (table "user").
type User struct {
	ID                uuid.UUID
	CreatedAt         time.Time
	ModifiedAt        time.Time
	GithubID          int64
	GithubLogin       string
	GithubAccessToken string
}

func (u *User) ProfileImage() string {
	return fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", u.GithubID)
}

// Account is a GitHub Kodiak App installation for a GitHub organization or
// user (table "account").
type Account struct {
	ID                         uuid.UUID
	CreatedAt                  time.Time
	ModifiedAt                 time.Time
	GithubInstallationID       int64
	GithubAccountID            int64
	GithubAccountLogin         string
	GithubAccountType          string
	TrialExpiration            *time.Time
	TrialStart                 *time.Time
	TrialStartedByID           *uuid.UUID
	TrialEmail                 string
	StripeCustomerID           string
	LimitBillingAccessToOwners bool
	ContactEmails              string
	SubscriptionExempt         bool
	SubscriptionExemptMessage  *string
}

const (
	AccountTypeUser         = "User"
	AccountTypeOrganization = "Organization"
)

func (a *Account) ProfileImage() string {
	return fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", a.GithubAccountID)
}

func (a *Account) TrialExpired() bool {
	return a.TrialExpiration != nil && time.Until(*a.TrialExpiration) < 0
}

func (a *Account) ActiveTrial() bool {
	return a.TrialExpiration != nil && time.Until(*a.TrialExpiration) > 0
}

func (a *Account) CanSubscribe() bool {
	return a.GithubAccountType != AccountTypeUser && !a.SubscriptionExempt
}

// AccountMembership associates a User with an Account
// (table "account_membership").
type AccountMembership struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	UserID    uuid.UUID
	Role      string
}

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// PullRequestActivity stores a per-day aggregate of pull request activity
// (table "pull_request_activity").
type PullRequestActivity struct {
	Date                 time.Time
	TotalOpened          int
	TotalMerged          int
	TotalClosed          int
	KodiakApproved       int
	KodiakMerged         int
	KodiakUpdated        int
	GithubInstallationID int64
}

// ActiveUser is a row from the active-user usage query.
type ActiveUser struct {
	GithubLogin   string
	GithubID      int64
	DaysActive    int
	FirstActiveAt time.Time
	LastActiveAt  time.Time
}

func (u *ActiveUser) ProfileImage() string {
	return fmt.Sprintf("https://avatars.githubusercontent.com/u/%d", u.GithubID)
}

// StripeCustomerInformation mirrors the Stripe Customer, PaymentMethod, Plan
// and Subscription objects (table "stripe_customer_information").
type StripeCustomerInformation struct {
	CustomerID      string
	SubscriptionID  string
	PlanID          string
	PaymentMethodID string

	CustomerEmail             string
	CustomerBalance           int64
	CustomerCreated           int64
	CustomerCurrency          *string
	CustomerName              *string
	CustomerAddressLine1      *string
	CustomerAddressCity       *string
	CustomerAddressCountry    *string
	CustomerAddressLine2      *string
	CustomerAddressPostalCode *string
	CustomerAddressState      *string

	PaymentMethodCardBrand    *string
	PaymentMethodCardExpMonth *string
	PaymentMethodCardExpYear  *string
	PaymentMethodCardLast4    *string

	PlanAmount        int64
	PlanInterval      *string
	PlanIntervalCount *int64

	SubscriptionQuantity           int64
	SubscriptionStartDate          int64
	SubscriptionCurrentPeriodEnd   int64
	SubscriptionCurrentPeriodStart int64
}

// Expired reports whether the subscription period has lapsed, with two days
// of leeway for good measure.
func (s *StripeCustomerInformation) Expired() bool {
	twoDays := int64(60 * 60 * 24 * 2)
	return time.Now().Unix() > s.SubscriptionCurrentPeriodEnd+twoDays
}

const userColumns = `id, created_at, modified_at, github_id, github_login, github_access_token`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.CreatedAt, &u.ModifiedAt, &u.GithubID, &u.GithubLogin, &u.GithubAccessToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func GetUserByID(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*User, error) {
	row := db.QueryRow(ctx, `SELECT `+userColumns+` FROM "user" WHERE id = $1`, id)
	return scanUser(row)
}

func GetUserByGithubID(ctx context.Context, db *pgxpool.Pool, githubID int64) (*User, error) {
	row := db.QueryRow(ctx, `SELECT `+userColumns+` FROM "user" WHERE github_id = $1`, githubID)
	return scanUser(row)
}

func CreateUser(ctx context.Context, db *pgxpool.Pool, githubID int64, githubLogin, accessToken string) (*User, error) {
	row := db.QueryRow(ctx, `
INSERT INTO "user" (id, created_at, modified_at, github_id, github_login, github_access_token)
VALUES ($1, NOW(), NOW(), $2, $3, $4)
RETURNING `+userColumns, uuid.New(), githubID, githubLogin, accessToken)
	return scanUser(row)
}

func SaveUser(ctx context.Context, db *pgxpool.Pool, u *User) error {
	_, err := db.Exec(ctx, `
UPDATE "user"
SET modified_at = NOW(), github_id = $2, github_login = $3, github_access_token = $4
WHERE id = $1`, u.ID, u.GithubID, u.GithubLogin, u.GithubAccessToken)
	return err
}

const accountColumns = `id, created_at, modified_at, github_installation_id, github_account_id,
github_account_login, github_account_type, trial_expiration, trial_start, trial_started_by_id,
trial_email, stripe_customer_id, limit_billing_access_to_owners, contact_emails,
subscription_exempt, subscription_exempt_message`

func scanAccount(row pgx.Row) (*Account, error) {
	var a Account
	err := row.Scan(
		&a.ID, &a.CreatedAt, &a.ModifiedAt, &a.GithubInstallationID, &a.GithubAccountID,
		&a.GithubAccountLogin, &a.GithubAccountType, &a.TrialExpiration, &a.TrialStart,
		&a.TrialStartedByID, &a.TrialEmail, &a.StripeCustomerID, &a.LimitBillingAccessToOwners,
		&a.ContactEmails, &a.SubscriptionExempt, &a.SubscriptionExemptMessage)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func collectAccounts(rows pgx.Rows) ([]*Account, error) {
	defer rows.Close()
	var accounts []*Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func prefixColumns(prefix string) string {
	return prefix + ".id, " + prefix + `.created_at, ` + prefix + `.modified_at, ` +
		prefix + `.github_installation_id, ` + prefix + `.github_account_id, ` +
		prefix + `.github_account_login, ` + prefix + `.github_account_type, ` +
		prefix + `.trial_expiration, ` + prefix + `.trial_start, ` + prefix + `.trial_started_by_id, ` +
		prefix + `.trial_email, ` + prefix + `.stripe_customer_id, ` + prefix + `.limit_billing_access_to_owners, ` +
		prefix + `.contact_emails, ` + prefix + `.subscription_exempt, ` + prefix + `.subscription_exempt_message`
}

// GetAccountForUser mirrors get_account_or_404: the account must have a
// membership for the given user.
func GetAccountForUser(ctx context.Context, db *pgxpool.Pool, accountID, userID uuid.UUID) (*Account, error) {
	row := db.QueryRow(ctx, `
SELECT `+prefixColumns("a")+`
FROM account a
JOIN account_membership m ON m.account_id = a.id
WHERE a.id = $1 AND m.user_id = $2
LIMIT 1`, accountID, userID)
	return scanAccount(row)
}

func ListAccountsForUser(ctx context.Context, db *pgxpool.Pool, userID uuid.UUID, orderByLogin bool) ([]*Account, error) {
	q := `
SELECT ` + prefixColumns("a") + `
FROM account a
JOIN account_membership m ON m.account_id = a.id
WHERE m.user_id = $1`
	if orderByLogin {
		q += ` ORDER BY a.github_account_login`
	}
	rows, err := db.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	return collectAccounts(rows)
}

func GetAccountByID(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*Account, error) {
	row := db.QueryRow(ctx, `SELECT `+accountColumns+` FROM account WHERE id = $1`, id)
	return scanAccount(row)
}

func GetAccountByGithubAccountID(ctx context.Context, db *pgxpool.Pool, githubAccountID int64) (*Account, error) {
	row := db.QueryRow(ctx, `SELECT `+accountColumns+` FROM account WHERE github_account_id = $1`, githubAccountID)
	return scanAccount(row)
}

func GetAccountByStripeCustomerID(ctx context.Context, db *pgxpool.Pool, customerID string) (*Account, error) {
	row := db.QueryRow(ctx, `SELECT `+accountColumns+` FROM account WHERE stripe_customer_id = $1 LIMIT 1`, customerID)
	return scanAccount(row)
}

func CreateAccount(ctx context.Context, db *pgxpool.Pool, installationID, githubAccountID int64, login, accountType string) (*Account, error) {
	row := db.QueryRow(ctx, `
INSERT INTO account (
    id, created_at, modified_at, github_installation_id, github_account_id,
    github_account_login, github_account_type, trial_email, stripe_customer_id,
    limit_billing_access_to_owners, contact_emails, subscription_exempt)
VALUES ($1, NOW(), NOW(), $2, $3, $4, $5, '', '', FALSE, '', FALSE)
RETURNING `+accountColumns, uuid.New(), installationID, githubAccountID, login, accountType)
	return scanAccount(row)
}

func SaveAccount(ctx context.Context, db *pgxpool.Pool, a *Account) error {
	_, err := db.Exec(ctx, `
UPDATE account
SET modified_at = NOW(),
    github_installation_id = $2,
    github_account_id = $3,
    github_account_login = $4,
    github_account_type = $5,
    trial_expiration = $6,
    trial_start = $7,
    trial_started_by_id = $8,
    trial_email = $9,
    stripe_customer_id = $10,
    limit_billing_access_to_owners = $11,
    contact_emails = $12,
    subscription_exempt = $13,
    subscription_exempt_message = $14
WHERE id = $1`,
		a.ID, a.GithubInstallationID, a.GithubAccountID, a.GithubAccountLogin,
		a.GithubAccountType, a.TrialExpiration, a.TrialStart, a.TrialStartedByID,
		a.TrialEmail, a.StripeCustomerID, a.LimitBillingAccessToOwners,
		a.ContactEmails, a.SubscriptionExempt, a.SubscriptionExemptMessage)
	return err
}

// IsAdmin reports whether the user has an admin membership for the account.
func IsAdmin(ctx context.Context, db *pgxpool.Pool, accountID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM account_membership WHERE account_id = $1 AND user_id = $2 AND role = 'admin'
)`, accountID, userID).Scan(&exists)
	return exists, err
}

// UpsertMembership updates the role of an existing membership or creates one.
func UpsertMembership(ctx context.Context, db *pgxpool.Pool, accountID, userID uuid.UUID, role string) error {
	tag, err := db.Exec(ctx, `
UPDATE account_membership SET modified_at = NOW(), role = $3
WHERE account_id = $1 AND user_id = $2`, accountID, userID, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = db.Exec(ctx, `
INSERT INTO account_membership (id, created_at, modified_at, account_id, user_id, role)
VALUES ($1, NOW(), NOW(), $2, $3, $4)`, uuid.New(), accountID, userID, role)
	return err
}

// DeleteMembershipsExcluding removes the user's memberships for accounts not
// in keepAccountIDs.
func DeleteMembershipsExcluding(ctx context.Context, db *pgxpool.Pool, userID uuid.UUID, keepAccountIDs []uuid.UUID) error {
	keep := make([]string, 0, len(keepAccountIDs))
	for _, id := range keepAccountIDs {
		keep = append(keep, id.String())
	}
	_, err := db.Exec(ctx, `
DELETE FROM account_membership
WHERE user_id = $1 AND account_id <> ALL ($2::uuid[])`, userID, keep)
	return err
}

func ListPullRequestActivity(ctx context.Context, db *pgxpool.Pool, installationID int64) ([]PullRequestActivity, error) {
	rows, err := db.Query(ctx, `
SELECT date, total_opened, total_merged, total_closed, kodiak_approved, kodiak_merged,
       kodiak_updated, github_installation_id
FROM pull_request_activity
WHERE github_installation_id = $1
ORDER BY date`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var activities []PullRequestActivity
	for rows.Next() {
		var a PullRequestActivity
		if err := rows.Scan(&a.Date, &a.TotalOpened, &a.TotalMerged, &a.TotalClosed,
			&a.KodiakApproved, &a.KodiakMerged, &a.KodiakUpdated, &a.GithubInstallationID); err != nil {
			return nil, err
		}
		activities = append(activities, a)
	}
	return activities, rows.Err()
}

// GetActiveUsersInLast30Days returns users active on private repository pull
// requests that Kodiak operated on, ordered by first activity.
func GetActiveUsersInLast30Days(ctx context.Context, db *pgxpool.Pool, installationID int64) ([]ActiveUser, error) {
	rows, err := db.Query(ctx, `
SELECT
    max(a.github_user_login) github_user_login,
    a.github_user_id,
    count(distinct a.activity_date) days_active,
    min(a.activity_date) first_active_at,
    max(a.activity_date) last_active_at
FROM
    user_pull_request_activity a
    JOIN user_pull_request_activity b ON a.github_installation_id = b.github_installation_id
        AND a.github_repository_name = b.github_repository_name
        AND a.github_pull_request_number = b.github_pull_request_number
WHERE
    b.github_user_login LIKE 'kodiak%[bot]'
    AND a.github_user_login NOT LIKE '%[bot]'
    -- We only consider users that open pull requests.
    -- For table b we look at all pull request events for Kodiak to
    -- find all the PRs Kodiak has touched.
    AND a.opened_pull_request = TRUE
    AND a.activity_date > now() - '30 days'::interval
    AND b.activity_date > now() - '30 days'::interval
    AND a.is_private_repository = TRUE
    AND b.is_private_repository = TRUE
    AND a.github_installation_id = $1
GROUP BY
    a.github_user_id
ORDER BY
    min(a.activity_date) ASC`, installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []ActiveUser
	for rows.Next() {
		var u ActiveUser
		if err := rows.Scan(&u.GithubLogin, &u.GithubID, &u.DaysActive, &u.FirstActiveAt, &u.LastActiveAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func CreateGitHubEvent(ctx context.Context, db *pgxpool.Pool, eventName string, payload []byte) error {
	_, err := db.Exec(ctx, `
INSERT INTO github_event (id, created_at, modified_at, event_name, payload)
VALUES ($1, NOW(), NOW(), $2, $3::jsonb)`, uuid.New(), eventName, string(payload))
	return err
}

const stripeCustomerInfoColumns = `customer_id, subscription_id, plan_id, payment_method_id,
customer_email, customer_balance, customer_created, customer_currency, customer_name,
customer_address_line1, customer_address_city, customer_address_country, customer_address_line2,
customer_address_postal_code, customer_address_state, payment_method_card_brand,
payment_method_card_exp_month, payment_method_card_exp_year, payment_method_card_last4,
plan_amount, plan_interval, plan_interval_count, subscription_quantity, subscription_start_date,
subscription_current_period_end, subscription_current_period_start`

func GetStripeCustomerInfo(ctx context.Context, db *pgxpool.Pool, customerID string) (*StripeCustomerInformation, error) {
	if customerID == "" {
		return nil, nil
	}
	row := db.QueryRow(ctx, `SELECT `+stripeCustomerInfoColumns+` FROM stripe_customer_information WHERE customer_id = $1`, customerID)
	var s StripeCustomerInformation
	err := row.Scan(
		&s.CustomerID, &s.SubscriptionID, &s.PlanID, &s.PaymentMethodID,
		&s.CustomerEmail, &s.CustomerBalance, &s.CustomerCreated, &s.CustomerCurrency, &s.CustomerName,
		&s.CustomerAddressLine1, &s.CustomerAddressCity, &s.CustomerAddressCountry, &s.CustomerAddressLine2,
		&s.CustomerAddressPostalCode, &s.CustomerAddressState, &s.PaymentMethodCardBrand,
		&s.PaymentMethodCardExpMonth, &s.PaymentMethodCardExpYear, &s.PaymentMethodCardLast4,
		&s.PlanAmount, &s.PlanInterval, &s.PlanIntervalCount, &s.SubscriptionQuantity, &s.SubscriptionStartDate,
		&s.SubscriptionCurrentPeriodEnd, &s.SubscriptionCurrentPeriodStart)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SaveStripeCustomerInfo upserts the row, mirroring Django Model.save() which
// inserts or updates based on the primary key.
func SaveStripeCustomerInfo(ctx context.Context, db *pgxpool.Pool, s *StripeCustomerInformation) error {
	_, err := db.Exec(ctx, `
INSERT INTO stripe_customer_information (`+stripeCustomerInfoColumns+`)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19,
        $20, $21, $22, $23, $24, $25, $26)
ON CONFLICT (customer_id) DO UPDATE SET
    subscription_id = excluded.subscription_id,
    plan_id = excluded.plan_id,
    payment_method_id = excluded.payment_method_id,
    customer_email = excluded.customer_email,
    customer_balance = excluded.customer_balance,
    customer_created = excluded.customer_created,
    customer_currency = excluded.customer_currency,
    customer_name = excluded.customer_name,
    customer_address_line1 = excluded.customer_address_line1,
    customer_address_city = excluded.customer_address_city,
    customer_address_country = excluded.customer_address_country,
    customer_address_line2 = excluded.customer_address_line2,
    customer_address_postal_code = excluded.customer_address_postal_code,
    customer_address_state = excluded.customer_address_state,
    payment_method_card_brand = excluded.payment_method_card_brand,
    payment_method_card_exp_month = excluded.payment_method_card_exp_month,
    payment_method_card_exp_year = excluded.payment_method_card_exp_year,
    payment_method_card_last4 = excluded.payment_method_card_last4,
    plan_amount = excluded.plan_amount,
    plan_interval = excluded.plan_interval,
    plan_interval_count = excluded.plan_interval_count,
    subscription_quantity = excluded.subscription_quantity,
    subscription_start_date = excluded.subscription_start_date,
    subscription_current_period_end = excluded.subscription_current_period_end,
    subscription_current_period_start = excluded.subscription_current_period_start`,
		s.CustomerID, s.SubscriptionID, s.PlanID, s.PaymentMethodID,
		s.CustomerEmail, s.CustomerBalance, s.CustomerCreated, s.CustomerCurrency, s.CustomerName,
		s.CustomerAddressLine1, s.CustomerAddressCity, s.CustomerAddressCountry, s.CustomerAddressLine2,
		s.CustomerAddressPostalCode, s.CustomerAddressState, s.PaymentMethodCardBrand,
		s.PaymentMethodCardExpMonth, s.PaymentMethodCardExpYear, s.PaymentMethodCardLast4,
		s.PlanAmount, s.PlanInterval, s.PlanIntervalCount, s.SubscriptionQuantity, s.SubscriptionStartDate,
		s.SubscriptionCurrentPeriodEnd, s.SubscriptionCurrentPeriodStart)
	return err
}

func DeleteStripeCustomerInfo(ctx context.Context, db *pgxpool.Pool, customerID string) error {
	_, err := db.Exec(ctx, `DELETE FROM stripe_customer_information WHERE customer_id = $1`, customerID)
	return err
}
