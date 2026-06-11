package queue

import "testing"

// TestWebhookEventJSONMatchesPydantic locks the serialization to the exact
// bytes pydantic's BaseModel.json() produces, since Redis sorted-set members
// are matched by byte equality across the Python and Go bots.
func TestWebhookEventJSONMatchesPydantic(t *testing.T) {
	event := WebhookEvent{
		RepoOwner:         "chdsbd",
		RepoName:          "kodiak",
		PullRequestNumber: 123,
		InstallationID:    "848733",
		TargetName:        "maín/π branch",
	}
	// output of WebhookEvent(...).json() under the Python bot
	// (json.dumps with ensure_ascii and ", "/": " separators).
	want := "{\"repo_owner\": \"chdsbd\", \"repo_name\": \"kodiak\", " +
		"\"pull_request_number\": 123, \"installation_id\": \"848733\", " +
		"\"target_name\": \"ma\\u00edn/\\u03c0 branch\"}"
	if got := event.JSON(); got != want {
		t.Errorf("JSON() = %s, want %s", got, want)
	}
	// output of get_merge_queue_name(...) under the Python bot
	// (urllib.parse.quote of the target name).
	wantQueue := "merge_queue:848733.chdsbd/kodiak/ma%C3%ADn/%CF%80%20branch"
	if got := event.GetMergeQueueName(); got != wantQueue {
		t.Errorf("GetMergeQueueName() = %s, want %s", got, wantQueue)
	}
	if got, want := event.GetWebhookQueueName(), "webhook:848733"; got != want {
		t.Errorf("GetWebhookQueueName() = %s, want %s", got, want)
	}
	if got, want := InstallationIDFromQueue("merge_queue:848733.chdsbd/kodiak/master"), "848733"; got != want {
		t.Errorf("InstallationIDFromQueue() = %s, want %s", got, want)
	}
}
