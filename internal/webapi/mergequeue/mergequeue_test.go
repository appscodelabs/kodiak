package mergequeue

import "testing"

// Ported from test_queue_info_from_name in web_api/web_api/test_merge_queues.py.
func TestQueueInfoFromName(t *testing.T) {
	cases := []struct {
		name   string
		org    string
		repo   string
		branch string
	}{
		{"merge_queue:11256551.sbdchd/squawk/main", "sbdchd", "squawk", "main"},
		{"merge_queue:11256551.sbdchd/squawk/main.test.foo", "sbdchd", "squawk", "main.test.foo"},
		{"merge_queue:11256551.sbdchd/squawk/chris/main.test.foo", "sbdchd", "squawk", "chris/main.test.foo"},
	}
	for _, tc := range cases {
		org, repo, branch, err := queueInfoFromName(tc.name)
		if err != nil {
			t.Fatalf("queueInfoFromName(%q) returned error: %v", tc.name, err)
		}
		if org != tc.org || repo != tc.repo || branch != tc.branch {
			t.Errorf("queueInfoFromName(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tc.name, org, repo, branch, tc.org, tc.repo, tc.branch)
		}
	}
}
