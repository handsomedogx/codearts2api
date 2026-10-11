package codearts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestCompleteFromSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=ak") {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "GLM-5.2" || payload["stream"] != true || payload["tool_stream"] != true {
			t.Fatalf("unexpected request body: %#v", payload)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello \"},\"finish_reason\":null}],\"chat_id\":\"chat-upstream\"}\n\n")
		_, _ = io.WriteString(w, "{\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNBaseURL = upstream.URL
	settings.AccountsDir = filepath.Join(temp, "accounts")
	manager := auth.NewManager(settings)
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := auth.AccountRecord{
		ID:              "account-1",
		Environment:     "cn",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
		SecurityToken:   "sec",
		Expiration:      "2030-01-01T00:00:00Z",
		DomainID:        "domain-1",
		UpdatedAt:       "2026-10-11T00:00:00Z",
	}
	encoded, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(settings.AccountsDir, "account-1.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	client := New(settings, manager)
	result, err := client.Complete(context.Background(), "account-1", Request{
		Model:    "GLM-5.2",
		Messages: []map[string]any{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Hello world" || result.FinishReason != "stop" || result.ChatID != "chat-upstream" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Usage["total_tokens"] != float64(5) {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}
