package models

import (
	"testing"
	"time"
)

const oneDaySec = int64(60 * 60 * 24)

// NOTE(port): the Python tests freeze time.time with mocker; Go's Expired()
// reads time.Now() directly, so the period end is placed relative to now
// instead. The DB row creation in the Python tests is incidental to the logic
// under test and is dropped.

// The subscription should be expired if past the period end. We give a grace
// period of two days, so to be expired we need to be three days pass the
// expiration period.
// Ported from test_expired in web_api/web_api/test_stripe_customer_info.py.
func TestStripeCustomerInformationExpired(t *testing.T) {
	s := StripeCustomerInformation{
		SubscriptionCurrentPeriodEnd: time.Now().Unix() - 3*oneDaySec,
	}
	if s.Expired() != true {
		t.Error("subscription three days past period end should be expired")
	}
}

// Inside the grace period (two days) we will not show the subscription as
// expired.
// Ported from test_expired_inside_grace_period in
// web_api/web_api/test_stripe_customer_info.py.
func TestStripeCustomerInformationExpiredInsideGracePeriod(t *testing.T) {
	s := StripeCustomerInformation{
		SubscriptionCurrentPeriodEnd: time.Now().Unix() - oneDaySec,
	}
	if s.Expired() != false {
		t.Error("subscription one day past period end is within the grace period")
	}
}

// Organization accounts that are missing exemptions can subscribe.
//
// GitHub user accounts and accounts marked as exempt cannot subscribe.
// Ported from test_account_can_subscribe in web_api/web_api/test_account.py.
func TestAccountCanSubscribe(t *testing.T) {
	if (&Account{GithubAccountType: AccountTypeUser}).CanSubscribe() != false {
		t.Error("user accounts cannot subscribe")
	}
	if (&Account{GithubAccountType: AccountTypeOrganization}).CanSubscribe() != true {
		t.Error("organization accounts can subscribe")
	}
	if (&Account{GithubAccountType: AccountTypeOrganization, SubscriptionExempt: true}).CanSubscribe() != false {
		t.Error("organization accounts with an exemption cannot subscribe")
	}
}
