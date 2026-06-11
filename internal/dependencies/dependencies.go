// Package dependencies determines dependency upgrade types (major, minor,
// patch) from pull request titles and bodies created by Dependabot and
// Renovate. Ported from bot/kodiak/dependencies.py.
package dependencies

import (
	"regexp"
	"strconv"
	"strings"
)

type MatchType string

const (
	MatchNone  MatchType = ""
	MatchMajor MatchType = "major"
	MatchMinor MatchType = "minor"
	MatchPatch MatchType = "patch"
)

var titleRegex = regexp.MustCompile(`from (\S+) to (\S+)`)

// extractVersions finds old and new version from a PR title.
//
// Example: "Bump jackson-databind from 2.9.10.1 to 2.10.0.pr1 in /LiveIngest"
// gives "2.9.10.1", "2.10.0.pr1".
func extractVersions(s string) (old string, new_ string, ok bool) {
	m := titleRegex.FindStringSubmatch(s)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// versions aren't necessarily semver; split on "." and "+" like dependabot.
var versionRegex = regexp.MustCompile(`[.+]`)

func parseVersionSimple(s string) []string {
	return versionRegex.Split(s, -1)
}

func getOrEmpty(arr []string, index int) string {
	if index < len(arr) {
		return arr[index]
	}
	return ""
}

// compareVersions determines the upgrade type, like Dependabot does.
func compareVersions(oldVersion, newVersion string) MatchType {
	oldParts := parseVersionSimple(oldVersion)
	newParts := parseVersionSimple(newVersion)

	for _, parts := range [][]string{newParts, oldParts} {
		for _, part := range parts[:min(len(parts), 3)] {
			if _, err := strconv.Atoi(part); err != nil {
				return MatchNone
			}
		}
	}

	if getOrEmpty(newParts, 0) != getOrEmpty(oldParts, 0) {
		return MatchMajor
	}
	if getOrEmpty(newParts, 1) != getOrEmpty(oldParts, 1) {
		return MatchMinor
	}
	return MatchPatch
}

// DepVersionFromTitle tries to determine the semver upgrade type from a title.
//
// For example, "Bump lodash from 4.17.15 to 4.17.19" is "patch".
func DepVersionFromTitle(title string) MatchType {
	old, new_, ok := extractVersions(title)
	if !ok {
		return MatchNone
	}
	return compareVersions(old, new_)
}

// `^4.14.0` -> `v4.15.0`
var renovateBodyRegex = regexp.MustCompile("`\\^?v?(.*)` -> `\\^?v?(.*)`")

var matchRank = map[MatchType]int{
	MatchMajor: 3,
	MatchMinor: 2,
	MatchPatch: 1,
	MatchNone:  0,
}

func CompareMatchType(a, b MatchType) bool {
	return matchRank[a] > matchRank[b]
}

// DepVersionsFromRenovatePRBody parses the update type from a Renovate PR body.
//
// Renovate can batch updates, so we report the largest update type of the
// batch: if the batch contained a "minor" and "major" update, we return "major".
func DepVersionsFromRenovatePRBody(body string) MatchType {
	largest := MatchNone
	if strings.Contains(body, "| lockFileMaintenance |") {
		largest = MatchPatch
	}
	for _, m := range renovateBodyRegex.FindAllStringSubmatch(body, -1) {
		matchType := compareVersions(m[1], m[2])
		if matchType == MatchNone {
			continue
		}
		if CompareMatchType(matchType, largest) {
			largest = matchType
		}
	}
	return largest
}

// Updates jest from 29.1.1 to 29.1.2
var dependabotBodyRegex = regexp.MustCompile(`from (.*) to (.*)`)

func DepVersionsFromDependabotPRBody(body string) MatchType {
	largest := MatchNone
	for _, m := range dependabotBodyRegex.FindAllStringSubmatch(body, -1) {
		matchType := compareVersions(m[1], m[2])
		if matchType == MatchNone {
			continue
		}
		if CompareMatchType(matchType, largest) {
			largest = matchType
		}
	}
	return largest
}

// DepVersionsFromPR determines the upgrade type for a pull request, checking
// the Renovate body format, then the Dependabot body format, then the title.
func DepVersionsFromPR(title, body string) MatchType {
	if matchType := DepVersionsFromRenovatePRBody(body); matchType != MatchNone {
		return matchType
	}
	if matchType := DepVersionsFromDependabotPRBody(body); matchType != MatchNone {
		return matchType
	}
	return DepVersionFromTitle(title)
}
