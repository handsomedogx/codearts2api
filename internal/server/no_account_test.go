package server

import (
	"net/http"
	"net/http/httptest"
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
