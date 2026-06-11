package queue

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
)

// WebhookEvent is the message stored in the Redis webhook and merge queues.
type WebhookEvent struct {
	RepoOwner         string `json:"repo_owner"`
	RepoName          string `json:"repo_name"`
	PullRequestNumber int    `json:"pull_request_number"`
	InstallationID    string `json:"installation_id"`
	TargetName        string `json:"target_name"`
}

// JSON serializes the event exactly like pydantic's BaseModel.json() (field
// declaration order, ", "/": " separators, ensure_ascii escapes) so the bytes
// match what a Python deployment stored in Redis. Sorted-set members are
// matched by byte equality (ZREM, merge-target comparison), so this must not
// change.
func (e WebhookEvent) JSON() string {
	var b strings.Builder
	b.WriteString(`{"repo_owner": `)
	writePyJSONString(&b, e.RepoOwner)
	b.WriteString(`, "repo_name": `)
	writePyJSONString(&b, e.RepoName)
	b.WriteString(`, "pull_request_number": `)
	b.WriteString(strconv.Itoa(e.PullRequestNumber))
	b.WriteString(`, "installation_id": `)
	writePyJSONString(&b, e.InstallationID)
	b.WriteString(`, "target_name": `)
	writePyJSONString(&b, e.TargetName)
	b.WriteString("}")
	return b.String()
}

// writePyJSONString encodes s like Python's json.dumps with the default
// ensure_ascii=True.
func writePyJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= 0x20 && r < 0x7f:
				b.WriteRune(r)
			case r > 0xffff:
				r1, r2 := utf16.EncodeRune(r)
				fmt.Fprintf(b, `\u%04x\u%04x`, r1, r2)
			default:
				fmt.Fprintf(b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
}

func (e WebhookEvent) GetMergeQueueName() string {
	return GetMergeQueueName(e)
}

func (e WebhookEvent) GetMergeTargetQueueName() string {
	return e.GetMergeQueueName() + ":target"
}

func (e WebhookEvent) GetWebhookQueueName() string {
	return GetWebhookQueueName(e)
}

func GetMergeQueueName(event WebhookEvent) string {
	escapedTarget := pyQuote(event.TargetName)
	return fmt.Sprintf("merge_queue:%s.%s/%s/%s",
		event.InstallationID, event.RepoOwner, event.RepoName, escapedTarget)
}

func GetWebhookQueueName(event WebhookEvent) string {
	return "webhook:" + event.InstallationID
}

// pyQuote matches urllib.parse.quote with the default safe="/".
func pyQuote(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '_' || c == '.' || c == '-' || c == '~' || c == '/' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
