package keepalive

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
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestRunOnceDueOnly(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[]}`)
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNBaseURL = upstream.URL
	settings.AccountsDir = filepath.Join(temp, "accounts")
	settings.RoutingFile = filepath.Join(temp, "routing.json")
	settings.KeepaliveEnabled = true
	settings.KeepaliveIntervalSeconds = 86400
	settings.KeepaliveAccountGapSeconds = 0
	if err := os.MkdirAll(settings.AccountsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	record := auth.AccountRecord{ID: "a1", Environment: "cn", AccessKeyID: "ak", SecretAccessKey: "sk", SecurityToken: "sec", Expiration: "2030-01-01T00:00:00Z", DomainID: "d1", Health: map[string]any{}, Stats: map[string]any{}, ModelHealth: map[string]any{}, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	raw, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(settings.AccountsDir, "a1.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := auth.NewManager(settings)
	client := codearts.New(settings, manager)
	service := New(settings, manager, client)
	results, err := service.RunOnce(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || calls.Load() != 1 {
		t.Fatalf("unexpected first probe: results=%d calls=%d", len(results), calls.Load())
	}
	if results[0].Health["probe_status"] != "ok" {
		t.Fatalf("unexpected health: %#v", results[0].Health)
	}

	results, err = service.RunOnce(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || calls.Load() != 1 {
		t.Fatalf("successful probe should not be due yet: results=%d calls=%d", len(results), calls.Load())
	}
}

func TestStartStopWithoutImmediateProbe(t *testing.T) {
	settings := config.Load()
	settings.AccountsDir = filepath.Join(t.TempDir(), "accounts")
	settings.KeepaliveEnabled = true
	settings.KeepalivePollSeconds = 300
	manager := auth.NewManager(settings)
	service := New(settings, manager, codearts.New(settings, manager))
	service.Start()
	service.Stop()
}
