// Port of web_api/web_api/test_user.py: User.sync_accounts installation
// membership syncing against a mocked GitHub API. Helpers are prefixed "us".
package webapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appscodelabs/kodiak/internal/webapi/models"
)

// usUser ports the `user` pytest fixture.
func usUser(t *testing.T, app *App) *models.User {
	t.Helper()
	return createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
}

// usMockedResponses ports the `mocked_responses` fixture
// (responses.RequestsMock): app HTTP traffic is routed to the registered
// handlers; hitting an unregistered URL fails the test (RequestsMock raises
// ConnectionError) and every registered response must be requested at least
// once (assert_all_requests_are_fired=True, checked on cleanup).
func usMockedResponses(t *testing.T, app *App, routes map[string]http.HandlerFunc) {
	t.Helper()
	mux := http.NewServeMux()
	called := make(map[string]bool, len(routes))
	for path, handler := range routes {
		p, h := path, handler
		called[p] = false
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			called[p] = true
			h(w, r)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request to unregistered URL: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusNotImplemented)
	})
	installFakeHTTP(app, mux)
	t.Cleanup(func() {
		for p, ok := range called {
			if !ok {
				t.Errorf("registered response %s was never requested", p)
			}
		}
	})
}

// usJSON responds with the given status code and JSON body.
func usJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// usCount runs a count query and returns the result.
func usCount(t *testing.T, db *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

const usGhostInstallationJSON = `{
    "id": 1066615,
    "account": {
        "login": "ghost",
        "id": 10137,
        "node_id": "MDQ6VXNlcjE5Mjk5NjA=",
        "avatar_url": "https://avatars2.githubusercontent.com/u/10137?v=4",
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
        "site_admin": false
    },
    "repository_selection": "selected",
    "access_tokens_url": "https://api.github.com/app/installations/136746/access_tokens",
    "repositories_url": "https://api.github.com/installation/repositories",
    "html_url": "https://github.com/settings/installations/136746",
    "app_id": 31500,
    "app_slug": "kodiak-local-dev",
    "target_id": 10137,
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
}`

const usChdsbdInstallationJSON = `{
    "id": 136746,
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
}`

const usRecipeyakInstallationJSON = `{
    "id": 81843,
    "account": {
        "login": "recipeyak",
        "id": 57954,
        "node_id": "MDQ6T3JnYW5pemF0aW9uNTc5NTQ=",
        "avatar_url": "https://avatars2.githubusercontent.com/u/57954?v=4",
        "gravatar_id": "",
        "url": "https://api.github.com/users/recipeyak",
        "html_url": "https://github.com/recipeyak",
        "followers_url": "https://api.github.com/users/recipeyak/followers",
        "following_url": "https://api.github.com/users/recipeyak/following{/other_user}",
        "gists_url": "https://api.github.com/users/recipeyak/gists{/gist_id}",
        "starred_url": "https://api.github.com/users/recipeyak/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/recipeyak/subscriptions",
        "organizations_url": "https://api.github.com/users/recipeyak/orgs",
        "repos_url": "https://api.github.com/users/recipeyak/repos",
        "events_url": "https://api.github.com/users/recipeyak/events{/privacy}",
        "received_events_url": "https://api.github.com/users/recipeyak/received_events",
        "type": "Organization",
        "site_admin": false
    },
    "repository_selection": "selected",
    "access_tokens_url": "https://api.github.com/app/installations/81843/access_tokens",
    "repositories_url": "https://api.github.com/installation/repositories",
    "html_url": "https://github.com/settings/installations/81843",
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
    "created_at": "2019-06-12T16:21:22.000-04:00",
    "updated_at": "2020-03-11T13:11:25.000-05:00",
    "single_file_name": null
}`

const usAcmeCorpInstallationJSON = `{
    "id": 65490234,
    "account": {
        "login": "acme-corp",
        "id": 23452345,
        "node_id": "MDQ6T3JnYW5pemF0aW9uNjU0OTAyMzQK",
        "avatar_url": "https://avatars2.githubusercontent.com/u/57954?v=4",
        "gravatar_id": "",
        "url": "https://api.github.com/users/acme-corp",
        "html_url": "https://github.com/acme-corp",
        "followers_url": "https://api.github.com/users/acme-corp/followers",
        "following_url": "https://api.github.com/users/acme-corp/following{/other_user}",
        "gists_url": "https://api.github.com/users/acme-corp/gists{/gist_id}",
        "starred_url": "https://api.github.com/users/acme-corp/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/acme-corp/subscriptions",
        "organizations_url": "https://api.github.com/users/acme-corp/orgs",
        "repos_url": "https://api.github.com/users/acme-corp/repos",
        "events_url": "https://api.github.com/users/acme-corp/events{/privacy}",
        "received_events_url": "https://api.github.com/users/acme-corp/received_events",
        "type": "Organization",
        "site_admin": false
    },
    "repository_selection": "selected",
    "access_tokens_url": "https://api.github.com/app/installations/65490234/access_tokens",
    "repositories_url": "https://api.github.com/installation/repositories",
    "html_url": "https://github.com/settings/installations/65490234",
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
    "created_at": "2019-06-12T16:21:22.000-04:00",
    "updated_at": "2020-03-11T13:11:25.000-05:00",
    "single_file_name": null
}`

const usRecipeyakMembershipJSON = `{
    "organization": {
        "avatar_url": "https://avatars2.githubusercontent.com/u/57954?v=4",
        "description": null,
        "events_url": "https://api.github.com/orgs/recipeyak/events",
        "hooks_url": "https://api.github.com/orgs/recipeyak/hooks",
        "id": 57954,
        "issues_url": "https://api.github.com/orgs/recipeyak/issues",
        "login": "recipeyak",
        "members_url": "https://api.github.com/orgs/recipeyak/members{/member}",
        "node_id": "MDQ6T3JnYW5pemF0aW9uNTc5NTQ=",
        "public_members_url": "https://api.github.com/orgs/recipeyak/public_members{/member}",
        "repos_url": "https://api.github.com/orgs/recipeyak/repos",
        "url": "https://api.github.com/orgs/recipeyak"
    },
    "organization_url": "https://api.github.com/orgs/recipeyak",
    "role": "admin",
    "state": "active",
    "url": "https://api.github.com/orgs/recipeyak/memberships/ghost",
    "user": {
        "avatar_url": "https://avatars2.githubusercontent.com/u/1929960?v=4",
        "events_url": "https://api.github.com/users/ghost/events{/privacy}",
        "followers_url": "https://api.github.com/users/ghost/followers",
        "following_url": "https://api.github.com/users/ghost/following{/other_user}",
        "gists_url": "https://api.github.com/users/ghost/gists{/gist_id}",
        "gravatar_id": "",
        "html_url": "https://github.com/ghost",
        "id": 10137,
        "login": "ghost",
        "node_id": "MDQ6VXNlcjEwMTM3Cg=",
        "organizations_url": "https://api.github.com/users/ghost/orgs",
        "received_events_url": "https://api.github.com/users/ghost/received_events",
        "repos_url": "https://api.github.com/users/ghost/repos",
        "site_admin": false,
        "starred_url": "https://api.github.com/users/ghost/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/ghost/subscriptions",
        "type": "User",
        "url": "https://api.github.com/users/ghost"
    }
}`

// usFailingInstallationResponse ports the failing_installation_response
// fixture.
func usFailingInstallationResponse(t *testing.T, app *App) {
	t.Helper()
	usMockedResponses(t, app, map[string]http.HandlerFunc{
		"/user/installations": usJSON(401, `{
    "message": "Bad credentials",
    "documentation_url": "https://developer.github.com/v3"
}`),
	})
}

// usSuccessfulInstallationResponse ports the
// successful_installation_response fixture.
func usSuccessfulInstallationResponse(t *testing.T, app *App) {
	t.Helper()
	usMockedResponses(t, app, map[string]http.HandlerFunc{
		"/user/installations": usJSON(200, `{"total_count": 1, "installations": [`+
			usGhostInstallationJSON+`, `+usChdsbdInstallationJSON+`, `+usRecipeyakInstallationJSON+`]}`),
		"/orgs/recipeyak/memberships/ghost": usJSON(200, usRecipeyakMembershipJSON),
	})
}

// usFailingInstallationResponseMembershipCheck ports the
// failing_installation_response_membership_check fixture.
func usFailingInstallationResponseMembershipCheck(t *testing.T, app *App) {
	t.Helper()
	usMockedResponses(t, app, map[string]http.HandlerFunc{
		"/user/installations": usJSON(200, `{"total_count": 1, "installations": [`+
			usGhostInstallationJSON+`, `+usChdsbdInstallationJSON+`, `+usRecipeyakInstallationJSON+`, `+
			usAcmeCorpInstallationJSON+`]}`),
		"/orgs/recipeyak/memberships/ghost": usJSON(200, usRecipeyakMembershipJSON),
		"/orgs/acme-corp/memberships/ghost": usJSON(403, `{
    "message": "You must be a member of acme-corp to see membership information for ghost.",
    "documentation_url": "https://developer.github.com/v3/orgs/members/#get-organization-membership"
}`),
	})
}

// TestSyncAccountsFailingApiRequest ports
// test_sync_accounts_failing_api_request.
func TestSyncAccountsFailingApiRequest(t *testing.T) {
	app, _ := newTestApp(t)
	user := usUser(t, app)
	usFailingInstallationResponse(t, app)

	err := app.syncAccounts(context.Background(), user)
	if !errors.Is(err, errSyncAccounts) {
		t.Fatalf("sync_accounts error = %v, want SyncAccountsError (errSyncAccounts)", err)
	}
}

// TestSyncAccountsFailingApiRequestCollaborator ports
// test_sync_accounts_failing_api_request_collaborator.
//
// If the user is a collaborator of an organization they will get an API error
// when testing membership. We should ignore the error and not add them to
// that organization.
func TestSyncAccountsFailingApiRequestCollaborator(t *testing.T) {
	app, _ := newTestApp(t)
	user := usUser(t, app)
	usFailingInstallationResponseMembershipCheck(t, app)

	if got := usCount(t, app.DB, `SELECT count(*) FROM account`); got != 0 {
		t.Fatalf("Account.objects.count() = %d, want 0", got)
	}
	if got := usCount(t, app.DB, `SELECT count(*) FROM account_membership`); got != 0 {
		t.Fatalf("AccountMembership.objects.count() = %d, want 0", got)
	}
	if got := usCount(t, app.DB, `SELECT count(*) FROM "user"`); got != 1 {
		t.Fatalf("User.objects.count() = %d, want 1", got)
	}
	if err := app.syncAccounts(context.Background(), user); err != nil {
		t.Fatalf("sync_accounts: %v", err)
	}

	matching := usCount(t, app.DB,
		`SELECT count(*) FROM account WHERE github_account_login = ANY($1)`,
		[]string{"ghost", "chdsbd", "recipeyak"})
	total := usCount(t, app.DB, `SELECT count(*) FROM account`)
	if matching != total || total != 3 {
		t.Errorf("accounts with login in [ghost chdsbd recipeyak] = %d, Account.objects.count() = %d, want both 3", matching, total)
	}
	if got := usCount(t, app.DB, `SELECT count(*) FROM account_membership`); got != 3 {
		t.Errorf("AccountMembership.objects.count() = %d, want 3", got)
	}
	if got := usCount(t, app.DB, `SELECT count(*) FROM "user"`); got != 1 {
		t.Errorf("User.objects.count() = %d, want 1", got)
	}
}

// TestSyncAccountsNewAndExistingAccounts ports
// test_sync_accounts_new_and_existing_accounts.
func TestSyncAccountsNewAndExistingAccounts(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	user := usUser(t, app)
	usSuccessfulInstallationResponse(t, app)

	userAccount, err := models.CreateAccount(ctx, app.DB, 1066615, user.GithubID,
		user.GithubLogin, models.AccountTypeUser)
	if err != nil {
		t.Fatalf("create user account: %v", err)
	}
	if err := models.UpsertMembership(ctx, app.DB, userAccount.ID, user.ID, models.RoleMember); err != nil {
		t.Fatalf("create membership: %v", err)
	}

	// the user should get removed from this account when we sync. This tests
	// that our membership removal of installations a user no longer has access
	// to works.
	acmeCorpAccount, err := models.CreateAccount(ctx, app.DB, 79233, 33803,
		"acme-corp", models.AccountTypeOrganization)
	if err != nil {
		t.Fatalf("create acme-corp account: %v", err)
	}
	if err := models.UpsertMembership(ctx, app.DB, acmeCorpAccount.ID, user.ID, models.RoleMember); err != nil {
		t.Fatalf("create acme-corp membership: %v", err)
	}

	if got := usCount(t, app.DB, `SELECT count(*) FROM account`); got != 2 {
		t.Fatalf("Account.objects.count() = %d, want 2", got)
	}
	if got := usCount(t, app.DB,
		`SELECT count(*) FROM account_membership WHERE user_id = $1`, user.ID); got != 2 {
		t.Fatalf("AccountMembership.objects.filter(user=user).count() = %d, want 2", got)
	}
	if err := app.syncAccounts(ctx, user); err != nil {
		t.Fatalf("sync_accounts: %v", err)
	}

	if got := usCount(t, app.DB,
		`SELECT count(*) FROM account WHERE github_account_login = ANY($1)`,
		[]string{"recipeyak", "chdsbd", "ghost", "acme-corp"}); got == 0 {
		t.Errorf("expected accounts with login in [recipeyak chdsbd ghost acme-corp], found none")
	}
	if got := usCount(t, app.DB, `SELECT count(*) FROM account`); got != 4 {
		t.Errorf("Account.objects.count() = %d, want 4: we should have a new account for recipeyak and chdsbd.", got)
	}
	if got := usCount(t, app.DB, `
SELECT count(*) FROM account_membership m
JOIN account a ON a.id = m.account_id
WHERE m.user_id = $1 AND NOT (a.github_account_login = ANY($2))`,
		user.ID, []string{"recipeyak", "chdsbd", "ghost"}); got != 0 {
		t.Errorf("memberships excluding [recipeyak chdsbd ghost] = %d, want 0: we should have removed acme-corp.", got)
	}
	if got := usCount(t, app.DB, `
SELECT count(*) FROM account_membership m
JOIN account a ON a.id = m.account_id
WHERE m.user_id = $1 AND m.role = 'member' AND a.github_account_login = 'chdsbd'`,
		user.ID); got != 1 {
		t.Errorf("member memberships for chdsbd = %d, want 1", got)
	}
	if got := usCount(t, app.DB, `
SELECT count(*) FROM account_membership m
JOIN account a ON a.id = m.account_id
WHERE m.user_id = $1 AND m.role = 'admin' AND a.github_account_login = 'recipeyak'`,
		user.ID); got != 1 {
		t.Errorf("admin memberships for recipeyak = %d, want 1", got)
	}

	if got := usCount(t, app.DB,
		`SELECT count(*) FROM account_membership WHERE user_id = $1 AND account_id = $2`,
		user.ID, acmeCorpAccount.ID); got != 0 {
		t.Errorf("acme-corp membership still exists: the user should no longer be a member of the organization if is no longer returned from /user/installations endpoint.")
	}
	if got := usCount(t, app.DB,
		`SELECT count(*) FROM account WHERE id = $1`, acmeCorpAccount.ID); got != 1 {
		t.Errorf("acme-corp account missing: account that we are no longer a member of should not be deleted.")
	}
}
