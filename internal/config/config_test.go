package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func loadConfigFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "bot", "kodiak", "test", "fixtures", "config", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(data)
}

func appIDPtr(s string) *AppIDStr { v := AppIDStr(s); return &v }

func mergeMethodPtr(m MergeMethod) *MergeMethod { return &m }

func TestConfigDefault(t *testing.T) {
	content := loadConfigFixture(t, "v1-default.toml")
	actual, err := ParseToml(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := DefaultV1()
	if diff := cmp.Diff(expected, *actual, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("config mismatch (-expected +actual):\n%s", diff)
	}
}

// parse config with all opposite settings so we can ensure the config is
// correctly formatted.
func TestConfigParsingOpposite(t *testing.T) {
	expectedFor := func(body MergeBodyStyle) V1 {
		expected := DefaultV1()
		expected.AppID = appIDPtr("12345")
		expected.Merge = Merge{
			AutomergeLabel:                   Labels{"mergeit!"},
			RequireAutomergeLabel:            false,
			ShowMissingAutomergeLabelMessage: true,
			BlacklistTitleRegex:              "",
			BlockingTitleRegex:               UnsetTitleRegex,
			BlacklistLabels:                  []string{"wip", "block-merge"},
			Method:                           mergeMethodPtr(MergeMethodSquash),
			DeleteBranchOnMerge:              true,
			BlockOnReviewsRequested:          true,
			NotifyOnConflict:                 false,
			OptimisticUpdates:                false,
			DontWaitOnStatusChecks:           []string{"ci/circleci: deploy"},
			UpdateBranchImmediately:          true,
			PrioritizeReadyToMerge:           true,
			DoNotMerge:                       true,
			Message: MergeMessage{
				Title:             MergeTitleStylePullRequestTitle,
				Body:              body,
				IncludePRNumber:   false,
				BodyType:          BodyTextPlainText,
				StripHTMLComments: true,
			},
		}
		expected.Update = Update{Always: true, RequireAutomergeLabel: false}
		expected.Approve = Approve{
			AutoApproveUsernames: []string{"dependabot"},
			AutoApproveLabels:    []string{"autoapprove"},
		}
		return expected
	}

	cases := []struct {
		fixture  string
		expected V1
	}{
		{"v1-opposite.1.toml", expectedFor(MergeBodyStylePullRequestBody)},
		{"v1-opposite.2.toml", expectedFor(MergeBodyStyleEmpty)},
	}
	for _, c := range cases {
		content := loadConfigFixture(t, c.fixture)
		actual, err := ParseToml(content)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.fixture, err)
		}
		if diff := cmp.Diff(c.expected, *actual, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("%s: config mismatch (-expected +actual):\n%s", c.fixture, diff)
		}
	}
}

func TestBadFile(t *testing.T) {
	if _, err := ParseToml("something[invalid["); err == nil {
		t.Error("expected toml decode error")
	}

	// we should return an error when we try to parse a different version
	if _, err := ParseToml("version = 20"); err == nil {
		t.Error("expected invalid version error")
	}

	// we should always require that the version is specified, even if we
	// provide defaults for everything else
	if _, err := ParseToml(""); err == nil {
		t.Error("expected missing version error")
	}

	if _, err := ParseToml("version = 1\nmerge.automerge_label = 123"); err == nil {
		t.Error("expected automerge_label type error")
	}
}
