package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealth(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)

	New().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rr.Code)
	}
}

func TestChatCompletionsRejectsInvalidBody(t *testing.T) {
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer test-key")

	New().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestAuthLoginURL(t *testing.T) {
	t.Setenv("CODEARTS_PENDING_AUTH_FILE", filepath.Join(t.TempDir(), "oauth_pending.json"))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/login-url?environment=cn", nil)
	req.RemoteAddr = "127.0.0.1:12345"

	New().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["authorize_url"] == "" || body["callback_url"] == "" {
		t.Fatalf("unexpected response: %#v", body)
	}
}

func TestAccountsResponseKeepsWebUIContract(t *testing.T) {
	temp := t.TempDir()
	accountsDir := filepath.Join(temp, "accounts")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEARTS_ACCOUNTS_DIR", accountsDir)
	t.Setenv("CODEARTS_ROUTING_FILE", filepath.Join(temp, "routing.json"))
	t.Setenv("CODEARTS2API_SETTINGS_FILE", filepath.Join(temp, "settings.json"))
	account := []byte(`{"id":"account-1","environment":"cn","access_key_id":"ak","secret_access_key":"sk","expiration":"2030-01-01T00:00:00Z","stats":{},"health":{},"model_health":{}}`)
	if err := os.WriteFile(filepath.Join(accountsDir, "account-1.json"), account, 0o600); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ui/accounts", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	New().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d: %s", http.StatusOK, rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one item in legacy Web UI contract, got %#v", body["items"])
	}
	accounts, ok := body["accounts"].([]any)
	if !ok || len(accounts) != 1 {
		t.Fatalf("expected one account in compatibility field, got %#v", body["accounts"])
	}
	routing, ok := body["routing"].(map[string]any)
	if !ok || routing["strategy"] == "" || routing["retry_budget_seconds"] == nil {
		t.Fatalf("unexpected routing payload: %#v", body["routing"])
	}
}

func TestEmbeddedWebAssetsDisableBrowserCache(t *testing.T) {
	for _, path := range []string{"/", "/static/app.js"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		New().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s expected %d, got %d", path, http.StatusOK, rr.Code)
		}
		if got := rr.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
	}
}
