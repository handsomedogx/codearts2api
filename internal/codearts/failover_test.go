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

func TestFailoverToSecondAccount(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			http.Error(w, "temporary failure", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNBaseURL = upstream.URL
	settings.AccountsDir = filepath.Join(temp, "accounts")
	settings.RoutingFile = filepath.Join(temp, "routing.json")
	settings.RoutingStrategy = "round_robin"
	settings.RoutingMaxAttempts = 2
	settings.RetryBudgetAttempts = 2
	settings.UpstreamRetriesPerAccount = 0
	manager := auth.NewManager(settings)
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, record := range []auth.AccountRecord{
		{ID: "a1", Environment: "cn", AccessKeyID: "ak1", SecretAccessKey: "sk1", SecurityToken: "sec1", Expiration: "2030-01-01T00:00:00Z", DomainID: "d1", UpdatedAt: "2026-10-11T00:00:02Z"},
		{ID: "a2", Environment: "cn", AccessKeyID: "ak2", SecretAccessKey: "sk2", SecurityToken: "sec2", Expiration: "2030-01-01T00:00:00Z", DomainID: "d2", UpdatedAt: "2026-10-11T00:00:01Z"},
	} {
		payload, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(settings.AccountsDir, record.ID+".json"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	client := New(settings, manager)
	result, err := client.Complete(context.Background(), "", Request{Model: "GLM-5.2", Messages: []map[string]any{{"role": "user", "content": "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.AccountID != "a2" || result.Text != "ok" {
		t.Fatalf("unexpected failover result: %#v", result)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", calls.Load())
	}
}
