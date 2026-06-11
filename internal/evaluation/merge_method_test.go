// Ported from bot/kodiak/tests/evaluation/test_merge_method.py.
package evaluation

import (
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
)

// Happy case.
func TestRebaseMergeFastForward(t *testing.T) {
	api := createAPI()
	cfg := createConfig()
	method := config.MergeMethodRebaseFastForward
	cfg.Merge.Method = &method
	params := createMergeableParams()
	params.Config = cfg
	params.Merging = true
	runMergeable(t, api, params)

	assertEqual(t, len(api.UpdateRefCalls), 1, "update_ref call count")
	assertEqual(t, len(api.MergeCalls), 0, "merge call count")
	if len(api.SetStatusCalls) != 2 {
		t.Fatalf("set_status call count: got %d, want 2", len(api.SetStatusCalls))
	}
	assertContains(t, api.SetStatusCalls[0].Msg, "(merging)", "first set_status message")
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}
