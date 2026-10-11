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

func TestStreamingNoAccountReturns503BeforeSSE(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	t.Setenv("CODEARTS_ACCOUNTS_DIR", filepath.Join(temp, "accounts"))
	t.Setenv("CODEARTS_CONVERSATIONS_DIR", filepath.Join(temp, "conversations"))
	t.Setenv("CODEARTS_USAGE_FILE", filepath.Join(temp, "usage.jsonl"))
	t.Setenv("CODEARTS_REQUEST_LOGS_FILE", filepath.Join(temp, "logs.jsonl"))
	t.Setenv("CODEARTS2API_SETTINGS_FILE", filepath.Join(temp, "settings.json"))

	body := `{"model":"GLM-5.2","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.HasPrefix(rr.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("request entered SSE before account validation")
	}
}

func TestStreamingCredentialRefreshFailureReturns503BeforeSSE(t *testing.T) {
	temp := t.TempDir()
	accountsDir := filepath.Join(temp, "accounts")
	if err := os.MkdirAll(accountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEARTS2API_API_KEY", "test-key")
	t.Setenv("CODEARTS_ACCOUNTS_DIR", accountsDir)
	t.Setenv("CODEARTS_ROUTING_FILE", filepath.Join(temp, "routing.json"))
	t.Setenv("CODEARTS_CONVERSATIONS_DIR", filepath.Join(temp, "conversations"))
	t.Setenv("CODEARTS_USAGE_FILE", filepath.Join(temp, "usage.jsonl"))
	t.Setenv("CODEARTS_REQUEST_LOGS_FILE", filepath.Join(temp, "logs.jsonl"))
	t.Setenv("CODEARTS2API_SETTINGS_FILE", filepath.Join(temp, "settings.json"))

	record := map[string]any{
		"id": "expired-account", "environment": "cn",
		"access_key_id": "ak", "secret_access_key": "sk", "security_token": "token",
		"expiration": "2020-01-01T00:00:00Z", "refresh_token": "refresh",
		"auth_context": map[string]any{}, "stats": map[string]any{}, "health": map[string]any{}, "model_health": map[string]any{},
	}
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(accountsDir, "expired-account.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	body := `{"model":"GLM-5.2","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	rr := httptest.NewRecorder()
	New().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before SSE, got %d: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") || strings.Contains(rr.Body.String(), "data:") {
		t.Fatalf("credential preflight failure must not start SSE: headers=%v body=%s", rr.Header(), rr.Body.String())
	}
}
