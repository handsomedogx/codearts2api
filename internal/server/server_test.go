package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
