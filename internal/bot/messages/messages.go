// Package messages renders bot comment and status check bodies.
//
// Ported from bot/kodiak/messages.py.
package messages

import (
	"fmt"
	"html"
	"strings"
)

const footer = `
If you need help, you can open a GitHub issue, check the docs, or reach us privately at support@kodiakhq.com.

[docs](https://kodiakhq.com/docs/troubleshooting) | [dashboard](https://app.kodiakhq.com) | [support](https://kodiakhq.com/help)

`

// Format appends Kodiak's standard footer.
func Format(msg string) string {
	return msg + "\n" + footer
}

// ForConfigError renders the markdown shown when .kodiak.toml fails to parse.
func ForConfigError(err error, configStr, gitPath string) string {
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
`, gitPath, lineCount, html.EscapeString(configStr), html.EscapeString(err.Error())))
}

// ForPaywall is shown when a private-repo subscription is missing.
func ForPaywall() string {
	return Format(`You can start a 30 day trial or update your subscription on the Kodiak dashboard at https://app.kodiakhq.com.

Kodiak is free to use on public repositories, but requires a subscription to use with private repositories.

See the [Kodiak docs](https://kodiakhq.com/docs/billing) for more information about free trials and subscriptions.
`)
}

// ForPushAllowanceError is shown when branch protection's push allowance list
// doesn't include the Kodiak GitHub App.
func ForPushAllowanceError(branchName string) string {
	return Format(fmt.Sprintf(`Your branch protection setting for `+"`%s`"+` has "Restrict who can push to matching branches" enabled. You must allow Kodiak to push to this branch for Kodiak to merge pull requests.

See the Kodiak troubleshooting docs for more information: https://kodiakhq.com/docs/troubleshooting#restricting-pushes
`, branchName))
}
