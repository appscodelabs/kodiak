// Package messages renders markdown messages posted as GitHub comments,
// ported from bot/kodiak/messages.py.
package messages

import (
	"fmt"
	"html"
	"strings"
)

const Footer = `
If you need help, you can open a GitHub issue, check the docs, or reach us privately at support@kodiakhq.com.

[docs](https://kodiakhq.com/docs/troubleshooting) | [dashboard](https://app.kodiakhq.com) | [support](https://kodiakhq.com/help)

`

func Format(msg string) string {
	return msg + "\n" + Footer
}

func GetMarkdownForConfig(err error, configStr string, gitPath string) string {
	configEscaped := html.EscapeString(configStr)
	errorEscaped := html.EscapeString(err.Error())
	lineCount := strings.Count(configStr, "\n") + 1
	return Format(fmt.Sprintf(`You have an invalid Kodiak configuration file.

## configuration file
> config_file_expression: %s
> line count: %d

<pre>
%s
</pre>

## configuration error message
<pre>
%s
</pre>

## notes
- Check the Kodiak docs for setup information at https://kodiakhq.com/docs/quickstart.
- A configuration reference is available at https://kodiakhq.com/docs/config-reference.
- Full examples are available at https://kodiakhq.com/docs/recipes
`, gitPath, lineCount, configEscaped, errorEscaped))
}

func GetMarkdownForPaywall() string {
	return Format(`You can start a 30 day trial or update your subscription on the Kodiak dashboard at https://app.kodiakhq.com.

Kodiak is free to use on public repositories, but requires a subscription to use with private repositories.

See the [Kodiak docs](https://kodiakhq.com/docs/billing) for more information about free trials and subscriptions.
`)
}

func GetMarkdownForPushAllowanceError(branchName string) string {
	return Format(fmt.Sprintf(`Your branch protection setting for `+"`%s`"+` has "Restrict who can push to matching branches" enabled. You must allow Kodiak to push to this branch for Kodiak to merge pull requests.

See the Kodiak troubleshooting docs for more information: https://kodiakhq.com/docs/troubleshooting#restricting-pushes
`, branchName))
}

// APICallRetry describes a failed GitHub API call, mirroring the
// APICallRetry protocol from the Python implementation.
type APICallRetry interface {
	APIName() string
	HTTPStatus() string
	ResponseBody() string
}

// pyRepr formats s like Python's repr(): single quoted, switching to double
// quotes when the string contains a single quote but no double quote.
//
// NOTE(port): non-printable characters are not escaped like Python repr;
// values here are HTTP statuses and JSON response bodies.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote), r == '\\':
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
	b.WriteByte(quote)
	return b.String()
}

func GetMarkdownForAPICallErrors(errors []APICallRetry) string {
	var lines []string
	for _, e := range errors {
		lines = append(lines, fmt.Sprintf("- API call %s failed with HTTP status %s and response: %s", pyRepr(e.APIName()), pyRepr(e.HTTPStatus()), pyRepr(e.ResponseBody())))
	}
	return Format(fmt.Sprintf(`Errors encountered when contacting GitHub API.

%s
`, strings.Join(lines, "\n")))
}
