package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardRequiresBasicAuthBehindLoopbackProxy(t *testing.T) {
	t.Setenv("CODEARTS2API_API_KEY", "test-key")

	unauthorized := httptest.NewRecorder()
	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	unauthorizedReq.RemoteAddr = "127.0.0.1:43122"
	New().ServeHTTP(unauthorized, unauthorizedReq)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected loopback-proxied dashboard request without Basic auth to return 401, got %d", unauthorized.Code)
	}
	if got := unauthorized.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("missing Basic auth challenge: %q", got)
	}

	wrongUser := httptest.NewRecorder()
	wrongUserReq := httptest.NewRequest(http.MethodGet, "/", nil)
	wrongUserReq.RemoteAddr = "127.0.0.1:43122"
	wrongUserReq.SetBasicAuth("root", "test-key")
	New().ServeHTTP(wrongUser, wrongUserReq)
	if wrongUser.Code != http.StatusUnauthorized {
		t.Fatalf("wrong Dashboard username should return 401, got %d", wrongUser.Code)
	}

	authorized := httptest.NewRecorder()
	authorizedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	authorizedReq.RemoteAddr = "203.0.113.55:43122"
	authorizedReq.SetBasicAuth("admin", "test-key")
	New().ServeHTTP(authorized, authorizedReq)
	if authorized.Code != http.StatusOK {
		t.Fatalf("correct Dashboard Basic auth should return 200, got %d: %s", authorized.Code, authorized.Body.String())
	}
}

func TestDashboardAndAPIAuthorizationSchemesDoNotConflict(t *testing.T) {
	t.Setenv("CODEARTS2API_API_KEY", "test-key")

	basic := httptest.NewRecorder()
	basicReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	basicReq.SetBasicAuth("admin", "test-key")
	New().ServeHTTP(basic, basicReq)
	if basic.Code != http.StatusUnauthorized {
		t.Fatalf("/v1 must reject Basic auth, got %d", basic.Code)
	}

	bearer := httptest.NewRecorder()
	bearerReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	bearerReq.Header.Set("Authorization", "Bearer test-key")
	New().ServeHTTP(bearer, bearerReq)
	if bearer.Code == http.StatusUnauthorized {
		t.Fatalf("/v1 must continue accepting Bearer API key: %s", bearer.Body.String())
	}
}

func TestOAuthCallbackRemainsPublic(t *testing.T) {
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("public callback without code should reach callback handler and return 400, got %d", rr.Code)
	}
	if rr.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("oauth callback must not require Dashboard Basic auth")
	}
}
