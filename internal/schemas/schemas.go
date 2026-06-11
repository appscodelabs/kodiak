// Package schemas contains shared event schemas, ported from bot/kodiak/schemas.py.
package schemas

import "encoding/json"

type RawWebhookEvent struct {
	EventName string          `json:"event_name"`
	Payload   json.RawMessage `json:"payload"`
}
