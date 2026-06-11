// Ports the OAuth login tests from web_api/web_api/test_views.py
// (test_oauth_login through test_oauth_complete_missing_code, line 2560 to
// end of file). test_logout and test_healthcheck live in
// views_smoke_test.go.
package webapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// wv4StateToken ports the state_token pytest fixture: it is a plain literal —
// the Python view never persists state server side; the client posts the
// token back as both serverState and clientState.
const wv4StateToken = "71DDCF95-84FC-4A5F-BCBF-BEB5FCCBDEA8"

// wv4Count runs a COUNT(*) query, porting Model.objects.count().
func wv4Count(t *testing.T, db *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func wv4UserCount(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	return wv4Count(t, db, `SELECT count(*) FROM "user"`)
}

func wv4AccountCount(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	return wv4Count(t, db, `SELECT count(*) FROM account`)
}

// wv4GetOnlyUser ports User.objects.get(): exactly one user row must exist.
func wv4GetOnlyUser(t *testing.T, db *pgxpool.Pool) *models.User {
	t.Helper()
	rows, err := db.Query(context.Background(),
		`SELECT github_id, github_login, github_access_token FROM "user"`)
	if err != nil {
		t.Fatalf("query users: %v", err)
	}
	defer rows.Close()
	var users []*models.User
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.GithubID, &u.GithubLogin, &u.GithubAccessToken); err != nil {
			t.Fatalf("scan user: %v", err)
		}
		users = append(users, &u)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("User.objects.get(): found %d users, want exactly 1", len(users))
	}
	return users[0]
}

// wv4JSON decodes a JSON response body, porting res.json().
func wv4JSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal response body %q: %v", body, err)
	}
	return m
}

// wv4PostOAuthComplete posts form-encoded data to /v1/oauth_complete (the
// Django test client posts dicts as form data, which Django exposes via
// request.POST).
func wv4PostOAuthComplete(t *testing.T, app *App, form url.Values) map[string]any {
	t.Helper()
	res := doRequest(t, app, http.MethodPost, "/v1/oauth_complete", []byte(form.Encode()))
	// assert res.status_code == 200
	if res.Code != 200 {
		t.Fatalf("oauth_complete status = %d, want 200 (body=%s)", res.Code, res.Body.String())
	}
	return wv4JSON(t, res.Body.Bytes())
}

// Ported from test_oauth_login in web_api/web_api/test_views.py.
func TestOAuthLogin(t *testing.T) {
	app, _ := newTestApp(t)

	res := doRequest(t, app, http.MethodGet, "/v1/oauth_login?state="+wv4StateToken, nil)
	if res.Code != 302 {
		t.Fatalf("oauth_login status = %d, want 302", res.Code)
	}
	// Python asserts the exact string
	//   https://github.com/login/oauth/authorize?client_id=Iv1.111FAKECLIENTID111&redirect_uri=https://app.kodiakhq.com/oauth&state=<state_token>
	// NOTE(port): yarl leaves ':' and '/' unencoded in query values while
	// Go's url.Values.Encode percent-encodes them, so we compare the decoded
	// URL components instead of the raw Location string.
	location := res.Result().Header.Get("Location")
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location header %q: %v", location, err)
	}
	if base := u.Scheme + "://" + u.Host + u.Path; base != "https://github.com/login/oauth/authorize" {
		t.Errorf("Location base = %q, want %q", base, "https://github.com/login/oauth/authorize")
	}
	q := u.Query()
	if got := q.Get("client_id"); got != "Iv1.111FAKECLIENTID111" {
		t.Errorf("client_id = %q, want %q", got, "Iv1.111FAKECLIENTID111")
	}
	if got := q.Get("redirect_uri"); got != "https://app.kodiakhq.com/oauth" {
		t.Errorf("redirect_uri = %q, want %q", got, "https://app.kodiakhq.com/oauth")
	}
	if got := q.Get("state"); got != wv4StateToken {
		t.Errorf("state = %q, want %q", got, wv4StateToken)
	}
	if len(q) != 3 {
		t.Errorf("Location query has %d parameters %v, want 3", len(q), q)
	}
}

// wv4SuccessfulSyncAccountsResponse ports the
// successful_sync_accounts_response pytest fixture: GET
// https://api.github.com/user/installations returns one installation for the
// "chdsbd" user account.
func wv4SuccessfulSyncAccountsResponse(mux *http.ServeMux) {
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "total_count": 1,
  "installations": [
    {
      "id": 1066615,
      "account": {
        "login": "chdsbd",
        "id": 1929960,
        "node_id": "MDQ6VXNlcjE5Mjk5NjA=",
        "avatar_url": "https://avatars2.githubusercontent.com/u/1929960?v=4",
        "gravatar_id": "",
        "url": "https://api.github.com/users/chdsbd",
        "html_url": "https://github.com/chdsbd",
        "followers_url": "https://api.github.com/users/chdsbd/followers",
        "following_url": "https://api.github.com/users/chdsbd/following{/other_user}",
        "gists_url": "https://api.github.com/users/chdsbd/gists{/gist_id}",
        "starred_url": "https://api.github.com/users/chdsbd/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/chdsbd/subscriptions",
        "organizations_url": "https://api.github.com/users/chdsbd/orgs",
        "repos_url": "https://api.github.com/users/chdsbd/repos",
        "events_url": "https://api.github.com/users/chdsbd/events{/privacy}",
        "received_events_url": "https://api.github.com/users/chdsbd/received_events",
        "type": "User",
        "site_admin": false
      },
      "repository_selection": "selected",
      "access_tokens_url": "https://api.github.com/app/installations/1066615/access_tokens",
      "repositories_url": "https://api.github.com/installation/repositories",
      "html_url": "https://github.com/settings/installations/1066615",
      "app_id": 31500,
      "app_slug": "kodiak-local-dev",
      "target_id": 1929960,
      "target_type": "User",
      "permissions": {
        "administration": "read",
        "checks": "write",
        "contents": "write",
        "issues": "read",
        "metadata": "read",
        "pull_requests": "write",
        "statuses": "read"
      },
      "events": [
        "check_run",
        "issue_comment",
        "pull_request",
        "pull_request_review",
        "pull_request_review_comment",
        "push",
        "status"
      ],
      "created_at": "2019-05-26T23:47:57.000-04:00",
      "updated_at": "2020-02-09T18:39:43.000-05:00",
      "single_file_name": null
    }
  ]
}`)
	})
}

// wv4SuccessfulResponses ports the successful_responses pytest fixture:
//   - POST https://github.com/login/oauth/access_token returns a
//     querystring-encoded access token.
//   - GET https://api.github.com/user returns the "ghost" profile.
//
// https://developer.github.com/v3/users/#response-with-public-and-private-profile-information
func wv4SuccessfulResponses(mux *http.ServeMux) {
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-www-form-urlencoded")
		_, _ = io.WriteString(w, "access_token=D6B5A3B57D32498DB00845A99137D3E2&token_type=bearer")
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
  "login": "ghost",
  "id": 10137,
  "node_id": "MDQ6VXNlcjE=",
  "avatar_url": "https://github.com/images/error/ghost_happy.gif",
  "gravatar_id": "",
  "url": "https://api.github.com/users/ghost",
  "html_url": "https://github.com/ghost",
  "followers_url": "https://api.github.com/users/ghost/followers",
  "following_url": "https://api.github.com/users/ghost/following{/other_user}",
  "gists_url": "https://api.github.com/users/ghost/gists{/gist_id}",
  "starred_url": "https://api.github.com/users/ghost/starred{/owner}{/repo}",
  "subscriptions_url": "https://api.github.com/users/ghost/subscriptions",
  "organizations_url": "https://api.github.com/users/ghost/orgs",
  "repos_url": "https://api.github.com/users/ghost/repos",
  "events_url": "https://api.github.com/users/ghost/events{/privacy}",
  "received_events_url": "https://api.github.com/users/ghost/received_events",
  "type": "User",
  "site_admin": false,
  "name": "monalisa ghost",
  "company": "GitHub",
  "blog": "https://github.com/blog",
  "location": "San Francisco",
  "email": "ghost@github.com",
  "hireable": false,
  "bio": "There once was...",
  "public_repos": 2,
  "public_gists": 1,
  "followers": 20,
  "following": 0,
  "created_at": "2008-01-14T04:33:35Z",
  "updated_at": "2008-01-14T04:33:35Z",
  "private_gists": 81,
  "total_private_repos": 100,
  "owned_private_repos": 100,
  "disk_usage": 10000,
  "collaborators": 8,
  "two_factor_authentication": true,
  "plan": {
    "name": "Medium",
    "space": 400,
    "private_repos": 20,
    "collaborators": 0
  }
}`)
	})
}

// Ported from test_oauth_complete_success_new_account in
// web_api/web_api/test_views.py.
func TestOAuthCompleteSuccessNewAccount(t *testing.T) {
	app, _ := newTestApp(t)
	mux := http.NewServeMux()
	wv4SuccessfulResponses(mux)
	wv4SuccessfulSyncAccountsResponse(mux)
	installFakeHTTP(app, mux)

	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Fatalf("Account count = %d, want 0", n)
	}
	if n := wv4UserCount(t, app.DB); n != 0 {
		t.Fatalf("User count = %d, want 0", n)
	}

	loginResult := wv4PostOAuthComplete(t, app, url.Values{
		"serverState": {wv4StateToken},
		"clientState": {wv4StateToken},
		"code":        {"D86BE2B3F3C74ACB91D3DF7B649F40BB"},
	})

	if loginResult["ok"] != true {
		t.Errorf(`login_result["ok"] = %v, want true`, loginResult["ok"])
	}
	if n := wv4UserCount(t, app.DB); n != 1 {
		t.Errorf("User count = %d, want 1", n)
	}
	if n := wv4AccountCount(t, app.DB); n != 1 {
		t.Errorf("Account count = %d, want 1", n)
	}
	user := wv4GetOnlyUser(t, app.DB)
	if user.GithubID != 10137 {
		t.Errorf("user.github_id = %d, want 10137", user.GithubID)
	}
	if user.GithubLogin != "ghost" {
		t.Errorf("user.github_login = %q, want %q", user.GithubLogin, "ghost")
	}
	if user.GithubAccessToken != "D6B5A3B57D32498DB00845A99137D3E2" {
		t.Errorf("user.github_access_token = %q, want %q",
			user.GithubAccessToken, "D6B5A3B57D32498DB00845A99137D3E2")
	}
}

// Ported from test_oauth_complete_success_existing_account in
// web_api/web_api/test_views.py.
func TestOAuthCompleteSuccessExistingAccount(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	mux := http.NewServeMux()
	wv4SuccessfulResponses(mux)
	wv4SuccessfulSyncAccountsResponse(mux)
	installFakeHTTP(app, mux)

	if n := wv4UserCount(t, app.DB); n != 1 {
		t.Fatalf("User count = %d, want 1", n)
	}

	loginResult := wv4PostOAuthComplete(t, app, url.Values{
		"serverState": {wv4StateToken},
		"clientState": {wv4StateToken},
		"code":        {"D86BE2B3F3C74ACB91D3DF7B649F40BB"},
	})

	if loginResult["ok"] != true {
		t.Errorf(`login_result["ok"] = %v, want true`, loginResult["ok"])
	}
	if n := wv4UserCount(t, app.DB); n != 1 {
		t.Errorf("User count = %d, want 1", n)
	}
	newUser := wv4GetOnlyUser(t, app.DB)
	if newUser.GithubID != user.GithubID {
		t.Errorf("new_user.github_id = %d, want %d", newUser.GithubID, user.GithubID)
	}
	if newUser.GithubLogin != user.GithubLogin {
		t.Errorf("new_user.github_login = %q, want %q", newUser.GithubLogin, user.GithubLogin)
	}
	if newUser.GithubAccessToken != "D6B5A3B57D32498DB00845A99137D3E2" {
		t.Errorf("new_user.github_access_token = %q, want %q",
			newUser.GithubAccessToken, "D6B5A3B57D32498DB00845A99137D3E2")
	}
}

// wv4FailingSyncAccountsResponse ports the failing_sync_accounts_response
// pytest fixture: GET https://api.github.com/user/installations returns 401.
func wv4FailingSyncAccountsResponse(mux *http.ServeMux) {
	mux.HandleFunc("GET /user/installations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{
  "message": "Bad credentials",
  "documentation_url": "https://developer.github.com/v3"
}`)
	})
}

// Ported from test_oauth_complete_sync_installation_failure in
// web_api/web_api/test_views.py.
func TestOAuthCompleteSyncInstallationFailure(t *testing.T) {
	app, _ := newTestApp(t)
	mux := http.NewServeMux()
	wv4SuccessfulResponses(mux)
	wv4FailingSyncAccountsResponse(mux)
	installFakeHTTP(app, mux)

	if n := wv4UserCount(t, app.DB); n != 0 {
		t.Fatalf("User count = %d, want 0", n)
	}
	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Fatalf("Account count = %d, want 0", n)
	}

	loginResult := wv4PostOAuthComplete(t, app, url.Values{
		"serverState": {wv4StateToken},
		"clientState": {wv4StateToken},
		"code":        {"D86BE2B3F3C74ACB91D3DF7B649F40BB"},
	})

	if loginResult["ok"] != false {
		t.Errorf(`login_result["ok"] = %v, want false`, loginResult["ok"])
	}
	if loginResult["error"] != "AccountSyncFailure" {
		t.Errorf(`login_result["error"] = %v, want %q`, loginResult["error"], "AccountSyncFailure")
	}
	if loginResult["error_description"] != "Failed to sync GitHub accounts for user." {
		t.Errorf(`login_result["error_description"] = %v, want %q`,
			loginResult["error_description"], "Failed to sync GitHub accounts for user.")
	}
	if n := wv4UserCount(t, app.DB); n != 1 {
		t.Errorf("User count = %d, want 1", n)
	}
	user := wv4GetOnlyUser(t, app.DB)
	if user.GithubID != 10137 {
		t.Errorf("user.github_id = %d, want 10137", user.GithubID)
	}
	if user.GithubLogin != "ghost" {
		t.Errorf("user.github_login = %q, want %q", user.GithubLogin, "ghost")
	}
	if user.GithubAccessToken != "D6B5A3B57D32498DB00845A99137D3E2" {
		t.Errorf("user.github_access_token = %q, want %q",
			user.GithubAccessToken, "D6B5A3B57D32498DB00845A99137D3E2")
	}
	if n := wv4AccountCount(t, app.DB); n != 0 {
		t.Errorf("Account count = %d, want 0", n)
	}
}

// Ported from test_oauth_complete_cookie_session_mismatch in
// web_api/web_api/test_views.py.
func TestOAuthCompleteCookieSessionMismatch(t *testing.T) {
	t.Skip("NOTE(port): skipped in Python via @pytest.mark.skip (unimplemented; body is `raise AssertionError()`)")
}

// Ported from test_oauth_complete_fail_to_fetch_access_token in
// web_api/web_api/test_views.py.
func TestOAuthCompleteFailToFetchAccessToken(t *testing.T) {
	t.Skip("NOTE(port): skipped in Python via @pytest.mark.skip (unimplemented; body is `raise AssertionError()`)")
}

// Ported from test_oauth_complete_fetch_access_token_qs_res_error in
// web_api/web_api/test_views.py.
func TestOAuthCompleteFetchAccessTokenQsResError(t *testing.T) {
	t.Skip("NOTE(port): skipped in Python via @pytest.mark.skip (unimplemented; body is `raise AssertionError()`)")
}

// Ported from test_oauth_complete_fetch_access_token_res_error in
// web_api/web_api/test_views.py.
func TestOAuthCompleteFetchAccessTokenResError(t *testing.T) {
	t.Skip("NOTE(port): skipped in Python via @pytest.mark.skip (unimplemented; body is `raise AssertionError()`)")
}

// Ported from test_oauth_complete_fail_fetch_github_account_info in
// web_api/web_api/test_views.py.
func TestOAuthCompleteFailFetchGithubAccountInfo(t *testing.T) {
	t.Skip("NOTE(port): skipped in Python via @pytest.mark.skip (unimplemented; body is `raise AssertionError()`)")
}

// Ported from test_oauth_complete_missing_code in
// web_api/web_api/test_views.py.
func TestOAuthCompleteMissingCode(t *testing.T) {
	app, _ := newTestApp(t)

	loginResult := wv4PostOAuthComplete(t, app, url.Values{
		"serverState": {wv4StateToken},
		"clientState": {wv4StateToken},
	})

	if loginResult["ok"] != false {
		t.Errorf(`login_result["ok"] = %v, want false`, loginResult["ok"])
	}
	if loginResult["error"] != "OAuthMissingCode" {
		t.Errorf(`login_result["error"] = %v, want %q`, loginResult["error"], "OAuthMissingCode")
	}
	if loginResult["error_description"] != "Payload should have a code parameter." {
		t.Errorf(`login_result["error_description"] = %v, want %q`,
			loginResult["error_description"], "Payload should have a code parameter.")
	}
}
