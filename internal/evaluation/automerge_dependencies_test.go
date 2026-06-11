// Ported from bot/kodiak/tests/evaluation/test_automerge_dependencies.py.
package evaluation

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/appscodelabs/kodiak/internal/config"
	"github.com/appscodelabs/kodiak/internal/dependencies"
)

// crFakePR ports kodiak.tests.dependencies.test_dependencies.FakePR.
type crFakePR struct {
	title string
	body  string
}

type crRenovateTestCase struct {
	pr         crFakePR
	updateType dependencies.MatchType
}

// crGenerateTestCases ports
// kodiak.tests.dependencies.test_dependencies.generate_test_cases.
//
// Generate test cases from the renovate_pull_requests/ directory.
//
// Each example file name has the update type. For example,
// (update-major-single_pr.txt) would be a major update.
//
// The first line of the file is the PR title. The remainder is the body.
func crGenerateTestCases(t *testing.T) []crRenovateTestCase {
	t.Helper()
	updateTypeRegex := regexp.MustCompile(`^update-(.*)-.*\.txt$`)
	renovateExamples := filepath.Join("..", "..", "bot", "kodiak", "tests", "dependencies", "pull_requests")

	entries, err := os.ReadDir(renovateExamples)
	if err != nil {
		t.Fatal(err)
	}
	var cases []crRenovateTestCase
	for _, entry := range entries {
		match := updateTypeRegex.FindStringSubmatch(entry.Name())
		if match == nil {
			t.Fatalf("fixture name %q does not match expected pattern", entry.Name())
		}
		updateType := match[1]
		content, err := os.ReadFile(filepath.Join(renovateExamples, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		title, body := lines[0], strings.Join(lines[1:], "\n")
		cases = append(cases, crRenovateTestCase{
			pr:         crFakePR{title: title, body: body},
			updateType: dependencies.MatchType(updateType),
		})
	}
	if len(cases) == 0 {
		t.Fatal("no fixtures found")
	}
	return cases
}

// Happy case.
//
// The upgrade type (patch) is specified in "versions" and the PR author
// "my-custom-dependabot" is specified in "usernames".
func TestMergeOkay(t *testing.T) {
	cfg := createConfig()
	cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{"minor", "patch"}
	cfg.Merge.AutomergeDependencies.Usernames = []string{"my-custom-dependabot"}
	pullRequest := createPullRequest()
	pullRequest.Labels = []string{}
	if pullRequest.Author == nil {
		t.Fatal("pull_request.author is nil")
	}
	pullRequest.Author.Login = "my-custom-dependabot"
	pullRequest.Title = "Bump lodash from 4.17.15 to 4.17.19"
	api := createAPI()
	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.Config = cfg
	runMergeable(t, api, params)
	assertEqual(t, len(api.SetStatusCalls), 1, "set_status call count")
	assertContains(t, api.SetStatusCalls[0].Msg, "enqueued", "set_status msg")
	assertEqual(t, len(api.QueueForMergeCalls), 1, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 0, "dequeue call count")
}

// We should only merge the pull request if the userrname is specified within
// "usernames" and the version type is in the "versions" field.
func TestMergeMismatchUsername(t *testing.T) {
	cfg := createConfig()
	cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{"minor", "patch"}
	cfg.Merge.AutomergeDependencies.Usernames = []string{"dependabot"}
	pullRequest := createPullRequest()
	pullRequest.Labels = []string{}
	if pullRequest.Author == nil {
		t.Fatal("pull_request.author is nil")
	}
	pullRequest.Author.Login = "my-custom-dependabot"
	pullRequest.Title = "Bump lodash from 4.17.15 to 4.17.19"
	api := createAPI()
	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.Config = cfg
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
}

// If we can't find a version from the PR title, we shouldn't merge.
//
// Packages don't necessarily use semver.
func TestMergeNoVersionFound(t *testing.T) {
	cfg := createConfig()
	cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{"minor", "patch"}
	cfg.Merge.AutomergeDependencies.Usernames = []string{"my-custom-dependabot"}
	pullRequest := createPullRequest()
	pullRequest.Labels = []string{}
	if pullRequest.Author == nil {
		t.Fatal("pull_request.author is nil")
	}
	pullRequest.Author.Login = "my-custom-dependabot"

	for _, title := range []string{"Bump lodash from 4.17.15 to", "Bump lodash from griffin to phoenix"} {
		pullRequest.Title = title
		api := createAPI()
		params := createMergeableParams()
		params.PullRequest = pullRequest
		params.Config = cfg
		runMergeable(t, api, params)
		assertEqual(t, len(api.QueueForMergeCalls), 0,
			fmt.Sprintf("title=%q: queue_for_merge call count", title))
		assertEqual(t, len(api.DequeueCalls), 1,
			fmt.Sprintf("title=%q: dequeue call count", title))
	}
}

// We should only auto merge if the upgrade type is specified in "versions".
//
// So if a PR is a major upgrade, we should only auto merge if "major" is in
// the "versions" configuration.
func TestMergeDisallowedVersion(t *testing.T) {
	cfg := createConfig()
	cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{"minor", "patch"}
	cfg.Merge.AutomergeDependencies.Usernames = []string{"my-custom-dependabot"}
	pullRequest := createPullRequest()
	pullRequest.Labels = []string{}
	if pullRequest.Author == nil {
		t.Fatal("pull_request.author is nil")
	}
	pullRequest.Author.Login = "my-custom-dependabot"
	pullRequest.Title = "Bump lodash from 4.17.15 to 5.0.1"
	api := createAPI()
	params := createMergeableParams()
	params.PullRequest = pullRequest
	params.Config = cfg
	runMergeable(t, api, params)
	assertEqual(t, len(api.QueueForMergeCalls), 0, "queue_for_merge call count")
	assertEqual(t, len(api.DequeueCalls), 1, "dequeue call count")
}

// NOTE(port): the @pytest.mark.parametrize("pr,update_type",
// generate_test_cases()) parameterization is ported as a loop over the fixture
// pull request files.
func TestMergeRenovate(t *testing.T) {
	for _, tc := range crGenerateTestCases(t) {
		cfg := createConfig()
		cfg.Merge.AutomergeDependencies.Usernames = []string{"my-custom-renovate"}
		pullRequest := createPullRequest()
		pullRequest.Labels = []string{}
		if pullRequest.Author == nil {
			t.Fatal("pull_request.author is nil")
		}
		pullRequest.Author.Login = "my-custom-renovate"
		pullRequest.Title = tc.pr.title
		pullRequest.Body = tc.pr.body

		for _, version := range []dependencies.MatchType{tc.updateType, dependencies.MatchNone} {
			if version != dependencies.MatchNone {
				cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{config.UpgradeVersion(version)}
			} else {
				cfg.Merge.AutomergeDependencies.Versions = []config.UpgradeVersion{}
			}
			api := createAPI()
			params := createMergeableParams()
			params.PullRequest = pullRequest
			params.Config = cfg
			runMergeable(t, api, params)
			if version != dependencies.MatchNone {
				assertEqual(t, len(api.QueueForMergeCalls), 1,
					fmt.Sprintf("title=%q version=%q: queue_for_merge call count", tc.pr.title, version))
				assertEqual(t, len(api.DequeueCalls), 0,
					fmt.Sprintf("title=%q version=%q: dequeue call count", tc.pr.title, version))
			} else {
				assertEqual(t, len(api.QueueForMergeCalls), 0,
					fmt.Sprintf("title=%q version=%q: queue_for_merge call count", tc.pr.title, version))
				assertEqual(t, len(api.DequeueCalls), 1,
					fmt.Sprintf("title=%q version=%q: dequeue call count", tc.pr.title, version))
			}
		}
	}
}
