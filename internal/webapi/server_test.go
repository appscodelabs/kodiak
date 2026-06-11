package webapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// smoke test for the health check endpoints
// Ported from test_health_check_middleware in web_api/web_api/test_middleware.py.
// NOTE(port): only /healthz is exercised; /readiness performs a live
// `SELECT 1` against Postgres, which the hermetic test suite cannot provide.
func TestHealthCheckEndpoints(t *testing.T) {
	app := &App{}
	srv := httptest.NewServer(app.Routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("GET /healthz status = %d, want 200", res.StatusCode)
	}
}
