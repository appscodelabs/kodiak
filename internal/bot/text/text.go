// Package text contains text-rendering helpers used to build merge commit
// messages and bot comments. Ported from bot/kodiak/text.py.
package text

import (
	"regexp"
	"strings"
)

var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// StripHTMLComments removes <!-- ... --> blocks, mirroring the Python helper.
func StripHTMLComments(body string) string {
	return htmlCommentRE.ReplaceAllString(body, "")
}

// Strip trims surrounding whitespace and collapses any internal CR/LF runs.
func Strip(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.TrimSpace(body)
}
