package dependencies

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ported from bot/kodiak/tests/dependencies/test_dependabot.py
func TestExtractVersions(t *testing.T) {
	tests := []struct {
		title      string
		oldVersion string
		newVersion string
		ok         bool
		upgrade    MatchType
	}{
		{"Bump pip from 20.2.4 to 20.3 in /.github/workflows", "20.2.4", "20.3", true, MatchMinor},
		{"Bump lodash from 4.17.15 to 4.17.19", "4.17.15", "4.17.19", true, MatchPatch},
		{"Update tokio requirement from 0.2 to 0.3", "0.2", "0.3", true, MatchMinor},
		{"Bump jackson-databind from 2.9.10.1 to 2.10.0.pr1 in /LiveIngest/LiveEventWithDVR", "2.9.10.1", "2.10.0.pr1", true, MatchMinor},
		{"Bump commons-collections from 4.0 to 4.1 in /eosio-explorer/Quantum", "4.0", "4.1", true, MatchMinor},
		{"[Snyk] Security upgrade engine.io from 3.5.0 to 4.0.0", "3.5.0", "4.0.0", true, MatchMajor},
		{"Bump lodash", "", "", false, MatchNone},
		{"Bump lodash to 4.17.19", "", "", false, MatchNone},
		{"Bump lodash from 4.17.15 to", "", "", false, MatchNone},
	}
	for _, tt := range tests {
		old, new_, ok := extractVersions(tt.title)
		if old != tt.oldVersion || new_ != tt.newVersion || ok != tt.ok {
			t.Errorf("extractVersions(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.title, old, new_, ok, tt.oldVersion, tt.newVersion, tt.ok)
		}
		if got := DepVersionFromTitle(tt.title); got != tt.upgrade {
			t.Errorf("DepVersionFromTitle(%q) = %q, want %q", tt.title, got, tt.upgrade)
		}
		if got := DepVersionsFromPR(tt.title, ""); got != tt.upgrade {
			t.Errorf("DepVersionsFromPR(%q, \"\") = %q, want %q", tt.title, got, tt.upgrade)
		}
	}
}

// ported from bot/kodiak/tests/dependencies/test_dependabot.py
func TestCompareVersions(t *testing.T) {
	tests := []struct {
		oldVersion string
		newVersion string
		change     MatchType
	}{
		{"20.2.4", "20.3", MatchMinor},
		{"4.17.15", "4.17.19", MatchPatch},
		{"0.2", "0.3", MatchMinor},
		{"2.9.10.1", "2.10.0.pr1", MatchMinor},
		{"4.0", "4.1", MatchMinor},
		{"1.5", "2.0", MatchMajor},
		{"feb", "may", MatchNone},
	}
	for _, tt := range tests {
		if got := compareVersions(tt.oldVersion, tt.newVersion); got != tt.change {
			t.Errorf("compareVersions(%q, %q) = %q, want %q", tt.oldVersion, tt.newVersion, got, tt.change)
		}
	}
}

// ported from bot/kodiak/tests/dependencies/test_renovate.py
func TestRenovateMinorMajor(t *testing.T) {
	renovateBody := `

| Package | Change | Age | Adoption | Passing | Confidence |
|---|---|---|---|---|---|
| [@netlify/build](https://togithub.com/netlify/build) | [` + "`^16.2.1` -> `^16.3.5`" + `](https://renovatebot.com/diffs/npm/@netlify%2fbuild/16.2.1/16.3.5) | [![age](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.3.5/age-slim)](https://docs.renovatebot.com/merge-confidence/) | [![adoption](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.3.5/adoption-slim)](https://docs.renovatebot.com/merge-confidence/) | [![passing](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.3.5/compatibility-slim/16.2.1)](https://docs.renovatebot.com/merge-confidence/) | [![confidence](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.3.5/confidence-slim/16.2.1)](https://docs.renovatebot.com/merge-confidence/) |
| [@netlify/config](https://togithub.com/netlify/build) | [` + "`^13.0.0` -> `^14.0.0`" + `](https://renovatebot.com/diffs/npm/@netlify%2fconfig/13.0.0/14.0.0) | [![age](https://badges.renovateapi.com/packages/npm/@netlify%2fconfig/14.0.0/age-slim)](https://docs.renovatebot.com/merge-confidence/) | [![adoption](https://badges.renovateapi.com/packages/npm/@netlify%2fconfig/14.0.0/adoption-slim)](https://docs.renovatebot.com/merge-confidence/) | [![passing](https://badges.renovateapi.com/packages/npm/@netlify%2fconfig/14.0.0/compatibility-slim/13.0.0)](https://docs.renovatebot.com/merge-confidence/) | [![confidence](https://badges.renovateapi.com/packages/npm/@netlify%2fconfig/14.0.0/confidence-slim/13.0.0)](https://docs.renovatebot.com/merge-confidence/) |

---
`
	if got := DepVersionsFromRenovatePRBody(renovateBody); got != MatchMajor {
		t.Errorf("DepVersionsFromRenovatePRBody = %q, want %q", got, MatchMajor)
	}
}

// ported from bot/kodiak/tests/dependencies/test_renovate.py
func TestRenovateUnknown(t *testing.T) {
	renovateBody := `

| Package | Change | Age | Adoption | Passing | Confidence |
|---|---|---|---|---|---|
| [@netlify/build](https://togithub.com/netlify/build) | [` + "`^16.2.1-beta` -> `^16.2.1`" + `](https://renovatebot.com/diffs/npm/@netlify%2fbuild/16.2.1-beta/16.2.1) | [![age](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.2.1/age-slim)](https://docs.renovatebot.com/merge-confidence/) | [![adoption](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.2.1/adoption-slim)](https://docs.renovatebot.com/merge-confidence/) | [![passing](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.2.1/compatibility-slim/16.2.1-beta)](https://docs.renovatebot.com/merge-confidence/) | [![confidence](https://badges.renovateapi.com/packages/npm/@netlify%2fbuild/16.2.1/confidence-slim/16.2.1-beta)](https://docs.renovatebot.com/merge-confidence/) |

---
`
	if got := DepVersionsFromRenovatePRBody(renovateBody); got != MatchNone {
		t.Errorf("DepVersionsFromRenovatePRBody = %q, want none", got)
	}
}

// ported from bot/kodiak/tests/dependencies/test_dependencies.py
//
// Each fixture file name encodes the update type, e.g.
// update-major-single_pr.txt is a major update. The first line of the file is
// the PR title, the remainder is the body.
func TestMergeFixturePullRequests(t *testing.T) {
	updateTypeRegex := regexp.MustCompile(`^update-(.*)-.*\.txt$`)
	fixtureDir := filepath.Join("..", "..", "bot", "kodiak", "tests", "dependencies", "pull_requests")
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, entry := range entries {
		match := updateTypeRegex.FindStringSubmatch(entry.Name())
		if match == nil {
			t.Fatalf("fixture name %q does not match expected pattern", entry.Name())
		}
		updateType := MatchType(match[1])
		content, err := os.ReadFile(filepath.Join(fixtureDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		title, body := lines[0], strings.Join(lines[1:], "\n")
		if got := DepVersionsFromPR(title, body); got != updateType {
			t.Errorf("%s: DepVersionsFromPR = %q, want %q", entry.Name(), got, updateType)
		}
	}
}
