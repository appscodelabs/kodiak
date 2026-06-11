package messages

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/appscodelabs/kodiak/internal/config"
)

type apiCallError struct {
	apiName      string
	httpStatus   string
	responseBody string
}

func (e apiCallError) APIName() string      { return e.apiName }
func (e apiCallError) HTTPStatus() string   { return e.httpStatus }
func (e apiCallError) ResponseBody() string { return e.responseBody }

// ported from bot/kodiak/tests/test_messages.py
func TestGetMarkdownForAPICallErrors(t *testing.T) {
	apiCallRetry := apiCallError{
		apiName:      "pull_request/merge",
		httpStatus:   "405",
		responseBody: `{"message":"This branch must not contain merge commits.","documentation_url":"https://docs.github.com/articles/about-protected-branches”}`,
	}
	want := `Errors encountered when contacting GitHub API.

- API call 'pull_request/merge' failed with HTTP status '405' and response: '{"message":"This branch must not contain merge commits.","documentation_url":"https://docs.github.com/articles/about-protected-branches”}'
- API call 'pull_request/merge' failed with HTTP status '405' and response: '{"message":"This branch must not contain merge commits.","documentation_url":"https://docs.github.com/articles/about-protected-branches”}'


If you need help, you can open a GitHub issue, check the docs, or reach us privately at support@kodiakhq.com.

[docs](https://kodiakhq.com/docs/troubleshooting) | [dashboard](https://app.kodiakhq.com) | [support](https://kodiakhq.com/help)

`
	got := GetMarkdownForAPICallErrors([]APICallRetry{apiCallRetry, apiCallRetry})
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetMarkdownForAPICallErrors mismatch (-want +got):\n%s", diff)
	}
}

// configMarkdown renders the expected markdown for an invalid config.
//
// NOTE(port): the Python tests (bot/kodiak/test_config_utils.py) compare
// against fixtures containing pydantic/toml library error text. The Go port
// produces different (Go library) error messages, so the expected error text
// is inlined here instead of read from the Python fixtures.
func configMarkdown(configStr, errText string) string {
	return `You have an invalid Kodiak configuration file.

## configuration file
> config_file_expression: master:.kodiak.toml
> line count: 1

<pre>
` + configStr + `
</pre>

## configuration error message
<pre>
` + errText + `
</pre>

## notes
- Check the Kodiak docs for setup information at https://kodiakhq.com/docs/quickstart.
- A configuration reference is available at https://kodiakhq.com/docs/config-reference.
- Full examples are available at https://kodiakhq.com/docs/recipes


If you need help, you can open a GitHub issue, check the docs, or reach us privately at support@kodiakhq.com.

[docs](https://kodiakhq.com/docs/troubleshooting) | [dashboard](https://app.kodiakhq.com) | [support](https://kodiakhq.com/help)

`
}

// ported from bot/kodiak/test_config_utils.py
func TestGetMarkdownForConfigValidationError(t *testing.T) {
	configStr := "version = 12"
	cfg, err := config.ParseToml(configStr)
	if cfg != nil || err == nil {
		t.Fatalf("expected validation error, got cfg=%v err=%v", cfg, err)
	}
	got := GetMarkdownForConfig(err, configStr, "master:.kodiak.toml")
	want := configMarkdown(configStr, "Version must be `1`")
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetMarkdownForConfig mismatch (-want +got):\n%s", diff)
	}
}

// ported from bot/kodiak/test_config_utils.py
func TestGetMarkdownForConfigTomlError(t *testing.T) {
	configStr := "[[[ version = 12"
	cfg, err := config.ParseToml(configStr)
	if cfg != nil || err == nil {
		t.Fatalf("expected toml error, got cfg=%v err=%v", cfg, err)
	}
	got := GetMarkdownForConfig(err, configStr, "master:.kodiak.toml")
	want := configMarkdown(
		configStr,
		"toml: line 1: expected &#39;.&#39; or &#39;]&#39; to end table name, but got &#39;[&#39; instead",
	)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetMarkdownForConfig mismatch (-want +got):\n%s", diff)
	}
}
