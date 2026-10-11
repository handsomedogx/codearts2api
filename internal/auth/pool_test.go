package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestPoolStatsAndPublicShape(t *testing.T) {
	temp := t.TempDir()
	settings := config.Load()
	settings.AccountsDir = filepath.Join(temp, "accounts")
	settings.RoutingFile = filepath.Join(temp, "routing.json")
	settings.AccountMaxConcurrency = 4
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := AccountRecord{ID: "a1", Environment: "cn", AccessKeyID: "ak", SecretAccessKey: "sk", Expiration: "2030-01-01T00:00:00Z", Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{}, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	payload, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(settings.AccountsDir, "a1.json"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(settings)
	pool := NewPool(settings, manager)
	started, ok := pool.Begin("a1")
	if !ok {
		t.Fatal("expected request slot")
	}
	if err := pool.RecordRequest("a1", "GLM-5.2", "cn", started, true, map[string]any{"prompt_tokens": 3.0, "completion_tokens": 2.0, "total_tokens": 5.0}, ""); err != nil {
		t.Fatal(err)
	}
	accounts, err := pool.ListPublic()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("unexpected accounts: %#v", accounts)
	}
	if accounts[0].Stats["request_count"] != float64(1) && accounts[0].Stats["request_count"] != 1 {
		t.Fatalf("unexpected stats: %#v", accounts[0].Stats)
	}
	if accounts[0].Stats["in_flight"] != 1 {
		t.Fatalf("expected in_flight 1: %#v", accounts[0].Stats)
	}
	for _, key := range []string{"request_count", "success_count", "failure_count", "prompt_tokens", "completion_tokens", "total_tokens", "avg_output_tps", "avg_latency_ms", "last_latency_ms", "last_request_at"} {
		if _, ok := accounts[0].Stats[key]; !ok {
			t.Fatalf("public stats missing stable key %q: %#v", key, accounts[0].Stats)
		}
	}
	for _, key := range []string{"status", "consecutive_failures", "total_failures", "total_successes", "last_failure_at", "last_success_at", "last_error_category", "last_status_code", "cooldown_until", "last_probe_at", "probe_status", "probe_error_category", "needs_relogin", "refresh_probe_status", "refresh_probe_error_category"} {
		if _, ok := accounts[0].Health[key]; !ok {
			t.Fatalf("public health missing stable key %q: %#v", key, accounts[0].Health)
		}
	}
	pool.Finish("a1")
}

func TestListAccountsUsesPythonCompatibleOrdering(t *testing.T) {
	temp := t.TempDir()
	settings := config.Load()
	settings.AccountsDir = filepath.Join(temp, "accounts")
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	records := []AccountRecord{
		{ID: "older", Environment: "cn", Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{}, UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: "newer", Environment: "international", Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{}, UpdatedAt: "2026-02-01T00:00:00Z"},
		{ID: "invalid", Environment: "not-real", Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{}, UpdatedAt: "2026-03-01T00:00:00Z"},
	}
	for _, record := range records {
		payload, _ := json.Marshal(record)
		if err := os.WriteFile(filepath.Join(settings.AccountsDir, record.ID+".json"), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := NewManager(settings).ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].ID != "newer" || accounts[1].ID != "older" {
		t.Fatalf("unexpected account ordering/filtering: %#v", accounts)
	}
}
