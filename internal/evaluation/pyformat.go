package evaluation

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// pyStrRepr formats a string like Python's repr() so status check messages
// match the Python implementation (e.g. f"{value!r}").
func pyStrRepr(s string) string {
	quote := '\''
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == quote:
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune(quote)
	return b.String()
}

// pyListRepr formats a list of strings like Python's repr() of a list of str.
func pyListRepr(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, pyStrRepr(item))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// pySetRepr formats a set of strings like Python's repr() of a set of str.
//
// NOTE(port): elements are sorted for determinism; Python set iteration order
// is arbitrary.
func pySetRepr(set map[string]bool) string {
	if len(set) == 0 {
		return "set()"
	}
	quoted := make([]string, 0, len(set))
	for _, item := range sortedKeys(set) {
		quoted = append(quoted, pyStrRepr(item))
	}
	return "{" + strings.Join(quoted, ", ") + "}"
}

// ordinalize ports inflection.ordinalize: 1 -> "1st", 2 -> "2nd", ...
func ordinalize(n int) string {
	abs := n
	if abs < 0 {
		abs = -abs
	}
	suffix := "th"
	if abs%100 < 11 || abs%100 > 13 {
		switch abs % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

func newStringSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

func setIntersection(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for item := range a {
		if b[item] {
			out[item] = true
		}
	}
	return out
}

func setDifference(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for item := range a {
		if !b[item] {
			out[item] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}
