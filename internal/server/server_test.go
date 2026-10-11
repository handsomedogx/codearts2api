package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["detail"] == "" || body["error"] == nil {
		t.Fatalf("error response must support FastAPI and OpenAI clients: %#v", body)
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

func TestAccessRegenerateEnvironmentOverrideReturnsConflict(t *testing.T) {
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/ui/access/regenerate", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected %d, got %d: %s", http.StatusConflict, rr.Code, rr.Body.String())
	}
}

func TestDeleteAccountKeepsPythonContract(t *testing.T) {
	temp := t.TempDir()
	accountsDir := filepath.Join(temp, "accounts")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEARTS_ACCOUNTS_DIR", accountsDir)
	t.Setenv("CODEARTS_ROUTING_FILE", filepath.Join(temp, "routing.json"))

	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodDelete, "/api/ui/accounts/missing", nil)
	missingReq.RemoteAddr = "127.0.0.1:12345"
	New().ServeHTTP(missing, missingReq)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing account expected 404, got %d: %s", missing.Code, missing.Body.String())
	}

	if err := os.WriteFile(filepath.Join(accountsDir, "a1.json"), []byte(`{"id":"a1","environment":"cn","stats":{},"health":{},"model_health":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/ui/accounts/a1", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["deleted"] != true || body["account_id"] != "a1" {
		t.Fatalf("unexpected delete response: %#v", body)
	}
}

func TestChatDefaultsModelAndCorrelatesRequestID(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	t.Setenv("CODEARTS_ACCOUNTS_DIR", filepath.Join(temp, "accounts"))
	t.Setenv("CODEARTS_ROUTING_FILE", filepath.Join(temp, "routing.json"))
	t.Setenv("CODEARTS_CONVERSATIONS_DIR", filepath.Join(temp, "conversations"))
	t.Setenv("CODEARTS_USAGE_FILE", filepath.Join(temp, "usage.jsonl"))
	t.Setenv("CODEARTS_REQUEST_LOGS_FILE", filepath.Join(temp, "logs.jsonl"))
	t.Setenv("CODEARTS2API_SETTINGS_FILE", filepath.Join(temp, "settings.json"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing model should use default model and reach account selection; got %d: %s", rr.Code, rr.Body.String())
	}
	requestID := rr.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("missing X-Request-ID")
	}

	logsRR := httptest.NewRecorder()
	logsReq := httptest.NewRequest(http.MethodGet, "/api/ui/logs?time_range=all", nil)
	logsReq.RemoteAddr = "127.0.0.1:12345"
	New().ServeHTTP(logsRR, logsReq)
	var logsBody map[string]any
	if err := json.Unmarshal(logsRR.Body.Bytes(), &logsBody); err != nil {
		t.Fatal(err)
	}
	items, _ := logsBody["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one request log, got %#v", logsBody["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["request_id"] != requestID {
		t.Fatalf("log request_id %q != response X-Request-ID %q", item["request_id"], requestID)
	}
	if item["model"] != "GLM-5.2" {
		t.Fatalf("default model was not applied: %#v", item["model"])
	}
}
