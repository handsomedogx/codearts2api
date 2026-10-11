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
	pool.Finish("a1")
}
