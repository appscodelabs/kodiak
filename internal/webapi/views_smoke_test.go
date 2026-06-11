package webapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Ensure we delete the cookie on logout.
// The user should no longer be able to access authed routes.
// Ported from test_logout in web_api/web_api/test_views.py.
func TestLogout(t *testing.T) {
	app, _ := newTestApp(t)
	user := createTestUser(t, app.DB, 10137, "ghost", "33149942-C986-42F8-9A45-AD83D5077776")
	cookie := loginCookie(app, user)

	res := doRequest(t, app, http.MethodGet, "/v1/ping", nil, cookie)
	if res.Code != 200 {
		t.Fatalf("ping status = %d, want 200", res.Code)
	}

	res = doRequest(t, app, http.MethodGet, "/v1/logout", nil, cookie)
	if res.Code != 201 {
		t.Fatalf("logout status = %d, want 201", res.Code)
	}
	// the Django test client carries the (now cleared) cookie jar forward;
	// mimic that by using the cleared cookie from the logout response.
	cleared := res.Result().Cookies()[0]
	if cleared.MaxAge >= 0 {
		t.Errorf("logout should expire the session cookie, got MaxAge=%d", cleared.MaxAge)
	}

	res = doRequest(t, app, http.MethodGet, "/v1/ping", nil, cleared)
	if res.Code != 401 {
		t.Errorf("ping after logout status = %d, want 401", res.Code)
	}
}

// We should return 200 even when logged out.
// Ported from test_healthcheck in web_api/web_api/test_views.py.
func TestHealthcheck(t *testing.T) {
	app, _ := newTestApp(t)

	res := doRequest(t, app, http.MethodGet, "/v1/healthcheck", nil)
	if res.Code != 200 {
		t.Fatalf("healthcheck status = %d, want 200", res.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal healthcheck body: %v", err)
	}
	if body["ok"] != true {
		t.Errorf(`healthcheck body["ok"] = %v, want true`, body["ok"])
	}
}
