package codearts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestExplicitAccountRetriesSameAccount(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNBaseURL = upstream.URL
	settings.AccountsDir = filepath.Join(temp, "accounts")
	settings.RoutingFile = filepath.Join(temp, "routing.json")
	settings.UpstreamRetriesPerAccount = 1
	settings.RetryBudgetAttempts = 2
	settings.RetryBudgetSeconds = 5
	settings.RetryBackoffBaseMS = 0
	settings.RetryBackoffCapMS = 0
	settings.RetryBackoffJitter = 0
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := auth.AccountRecord{ID: "a1", Environment: "cn", AccessKeyID: "ak", SecretAccessKey: "sk", SecurityToken: "sec", Expiration: "2030-01-01T00:00:00Z", DomainID: "d1", Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{}}
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(settings.AccountsDir, "a1.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	client := New(settings, auth.NewManager(settings))
	result, err := client.Complete(context.Background(), "a1", Request{Model: "GLM-5.2", Messages: []map[string]any{{"role": "user", "content": "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" || result.Attempts != 2 || calls.Load() != 2 {
		t.Fatalf("unexpected retry result: %#v calls=%d", result, calls.Load())
	}
}
