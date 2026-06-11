package queries

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/appscodelabs/kodiak/internal/redisclient"
)

// parseSeatsExceeded replicates SeatsExceeded.parse_raw: the kind literal
// defaults to "seats_exceeded" when missing but must match when present, and
// allowed_user_ids is required.
func parseSeatsExceeded(data string) (*SeatsExceeded, error) {
	var parsed struct {
		Kind           *string  `json:"kind"`
		AllowedUserIDs *[]int64 `json:"allowed_user_ids"`
	}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, err
	}
	if parsed.Kind != nil && *parsed.Kind != "seats_exceeded" {
		return nil, fmt.Errorf("unexpected kind: %q", *parsed.Kind)
	}
	if parsed.AllowedUserIDs == nil {
		return nil, fmt.Errorf("missing required field allowed_user_ids")
	}
	return &SeatsExceeded{AllowedUserIDs: *parsed.AllowedUserIDs}, nil
}

// GetSubscription gets subscription information for the installation.
func (c *Client) GetSubscription(ctx context.Context) (*Subscription, error) {
	db, err := redisclient.WebAPI()
	if err != nil {
		return nil, err
	}
	res, err := db.HGetAll(ctx, fmt.Sprintf("kodiak:subscription:%s", c.InstallationID)).Result()
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, nil
	}
	subscriptionBlockerKind := res["subscription_blocker"]
	var subscriptionBlocker SubscriptionBlocker
	if subscriptionBlockerKind == "seats_exceeded" {
		// to be backwards compatible we must handle the case of `data` missing.
		seatsExceeded, err := parseSeatsExceeded(res["data"])
		if err != nil {
			slog.Error("failed to parse seats_exceeded data", "error", err)
			seatsExceeded = &SeatsExceeded{AllowedUserIDs: []int64{}}
		}
		subscriptionBlocker = seatsExceeded
	}
	if subscriptionBlockerKind == "trial_expired" {
		subscriptionBlocker = TrialExpired{}
	}
	if subscriptionBlockerKind == "subscription_expired" {
		subscriptionBlocker = SubscriptionExpired{}
	}
	accountID, ok := res["account_id"]
	if !ok {
		return nil, fmt.Errorf("subscription missing account_id")
	}
	return &Subscription{
		AccountID:           accountID,
		SubscriptionBlocker: subscriptionBlocker,
	}, nil
}
