package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestBackfillAccountEmails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3.0/OS-USER/users/user-1" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=ak") {
			t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"user":{"id":"user-1","email":"old-account@example.com"}}`)
	}))
	defer upstream.Close()

	settings := config.Load()
	settings.IAMBaseURL = upstream.URL
	settings.AccountsDir = filepath.Join(t.TempDir(), "accounts")
	settings.MigrateLegacy = false
	manager := NewManager(settings)

	record := AccountRecord{
		ID: "account-1", Environment: "cn", AccessKeyID: "ak", SecretAccessKey: "sk",
		SecurityToken: "sec", Expiration: "2035-01-01T00:00:00Z", RefreshToken: "refresh",
		DomainID: "domain-1", UserID: "user-1", UserName: "hw-user",
		AuthContext: map[string]any{}, Stats: map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{},
		UpdatedAt: "2026-10-01T00:00:00Z",
	}
	if err := manager.saveAccount(record); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.BackfillAccountEmails()
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("expected one email backfill, got %d", updated)
	}
	stored, err := manager.LoadAccount(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Email != "old-account@example.com" {
		t.Fatalf("unexpected backfilled email: %#v", stored)
	}
}
