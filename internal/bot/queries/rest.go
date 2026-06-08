// REST method implementations for the GitHub client.
//
// Ported from the request-helper portion of bot/kodiak/queries.py.

package queries

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const ghAccept = "application/vnd.github.machine-man-preview+json," +
	"application/vnd.github.antiope-preview+json," +
	"application/vnd.github.lydian-preview+json"

// tokenResponse is the shape returned by POST /app/installations/{id}/access_tokens.
type tokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func init() {
	exchangeInstallationToken = realExchangeInstallationToken
}

func realExchangeInstallationToken(ctx context.Context, h *http.Client, baseURL, installationID, jwtToken string) (string, time.Time, error) {
	endpoint := fmt.Sprintf("%s/app/installations/%s/access_tokens", baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Accept", ghAccept)
	req.Header.Set("Authorization", "Bearer "+jwtToken)

	resp, err := h.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("access_tokens: status=%d body=%s", resp.StatusCode, string(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("decode token response: %w", err)
	}
	return tr.Token, tr.ExpiresAt, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) (*http.Response, error) {
	endpoint := c.baseURL + path

	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", ghAccept)
	req.Header.Set("Authorization", "token "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, fmt.Errorf("%s %s: status=%d body=%s", method, endpoint, resp.StatusCode, string(buf))
	}
	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
	}
	resp.Body.Close()
	return resp, err
}

// GetOpenPullRequests — https://docs.github.com/en/rest/pulls/pulls#list-pull-requests
func (c *client) getOpenPullRequestsImpl(ctx context.Context, f OpenPullRequestsFilter) ([]OpenPullRequest, error) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("sort", "updated")
	if f.Head != "" {
		q.Set("head", f.Head)
	}
	if f.Base != "" {
		q.Set("base", f.Base)
	}
	var raw []struct {
		Number int `json:"number"`
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls?%s", c.owner, c.repo, q.Encode())
	if _, err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]OpenPullRequest, len(raw))
	for i, pr := range raw {
		out[i] = OpenPullRequest{Number: pr.Number}
	}
	return out, nil
}

// mergeImpl — https://docs.github.com/en/rest/pulls/pulls#merge-a-pull-request
func (c *client) mergeImpl(ctx context.Context, number int, in MergeInput) error {
	body := map[string]any{"merge_method": in.MergeMethod}
	// Match the Python source: omit title/message keys when empty so GitHub
	// generates them rather than rejecting null.
	if in.CommitTitle != "" {
		body["commit_title"] = in.CommitTitle
	}
	if in.CommitBody != "" {
		body["commit_message"] = in.CommitBody
	}
	if in.ExpectedHeadOID != "" {
		body["sha"] = in.ExpectedHeadOID
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", c.owner, c.repo, number)
	_, err := c.do(ctx, http.MethodPut, path, body, nil)
	return err
}

// updateBranchImpl — https://docs.github.com/en/rest/pulls/pulls#update-a-pull-request-branch
func (c *client) updateBranchImpl(ctx context.Context, number int) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/update-branch", c.owner, c.repo, number)
	_, err := c.do(ctx, http.MethodPut, path, nil, nil)
	return err
}

// createCommentImpl — https://docs.github.com/en/rest/issues/comments#create-an-issue-comment
func (c *client) createCommentImpl(ctx context.Context, number int, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", c.owner, c.repo, number)
	_, err := c.do(ctx, http.MethodPost, path, map[string]string{"body": body}, nil)
	return err
}

// addLabelImpl — https://docs.github.com/en/rest/issues/labels#add-labels-to-an-issue
func (c *client) addLabelImpl(ctx context.Context, number int, label string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", c.owner, c.repo, number)
	_, err := c.do(ctx, http.MethodPost, path, map[string][]string{"labels": {label}}, nil)
	return err
}

// deleteLabelImpl — https://docs.github.com/en/rest/issues/labels#remove-a-label-from-an-issue
func (c *client) deleteLabelImpl(ctx context.Context, number int, label string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", c.owner, c.repo, number, url.PathEscape(label))
	_, err := c.do(ctx, http.MethodDelete, path, nil, nil)
	return err
}

// createCheckRunImpl — https://docs.github.com/en/rest/checks/runs#create-a-check-run
func (c *client) createCheckRunImpl(ctx context.Context, in CheckRunInput) error {
	body := map[string]any{
		"name":         CheckRunName,
		"head_sha":     in.HeadSHA,
		"status":       in.Status,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if in.Conclusion != "" {
		body["conclusion"] = in.Conclusion
	}
	body["output"] = map[string]string{
		"title":   in.Title,
		"summary": in.Summary,
	}
	path := fmt.Sprintf("/repos/%s/%s/check-runs", c.owner, c.repo)
	_, err := c.do(ctx, http.MethodPost, path, body, nil)
	return err
}
