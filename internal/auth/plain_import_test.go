package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/securestore"
)

func TestImportPlainCredentialsVerifiesAndProtectsNestedCredentials(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5/caller-identity":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=plain-ak") {
				t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("X-Security-Token") != "plain-security-token" {
				t.Fatalf("unexpected security token: %q", r.Header.Get("X-Security-Token"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"account_id":"domain-1","principal_id":"user-1","principal_urn":"iam::domain-1:user/hw-user"}`)
		case "/v3.0/OS-USER/users/user-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"user":{"id":"user-1","email":"person@example.com"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	manager := plainImportTestManager(t, upstream.URL)
	inputPath := filepath.Join(t.TempDir(), "account.json")
	input := `{
  "environment": "cn",
  "credentials": {
    "access_key_id": "plain-ak",
    "secret_access_key": "plain-sk",
    "security_token": "plain-security-token",
    "expiration": "2030-01-01T00:00:00Z"
  },
  "refresh_token": "plain-refresh-token",
  "auth_context": {"pkce_pair":{"code_verifier":"plain-verifier"}}
}`
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := manager.ImportPlainCredentials(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Updated != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	accounts, err := manager.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Email != "person@example.com" || accounts[0].UserID != "user-1" || accounts[0].DomainID != "domain-1" {
		t.Fatalf("unexpected public accounts: %#v", accounts)
	}
	record, err := manager.LoadAccount(accounts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccessKeyID != "plain-ak" || record.SecretAccessKey != "plain-sk" || record.RefreshToken != "plain-refresh-token" {
		t.Fatalf("unexpected imported record: %#v", record)
	}
	stored, err := os.ReadFile(filepath.Join(manager.Settings.AccountsDir, accounts[0].ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"plain-ak", "plain-sk", "plain-security-token", "plain-refresh-token", "plain-verifier", "person@example.com"} {
		if strings.Contains(string(stored), secret) {
			t.Fatalf("stored account leaked plaintext value %q: %s", secret, string(stored))
		}
	}
	if !strings.Contains(string(stored), securestore.ProtectionName()) {
		t.Fatalf("stored account is not protected: %s", string(stored))
	}
}

func TestImportPlainCredentialsUpdatesExistingIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5/caller-identity":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"account_id":"domain-1","principal_id":"user-1","principal_urn":"iam::domain-1:user/hw-user"}`)
		case "/v3.0/OS-USER/users/user-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"user":{"id":"user-1","email":"new@example.com"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	manager := plainImportTestManager(t, upstream.URL)
	existing := AccountRecord{
		ID: "existing-account", Environment: "cn", AccessKeyID: "old-ak", SecretAccessKey: "old-sk",
		SecurityToken: "old-sec", Expiration: "2029-01-01T00:00:00Z", RefreshToken: "old-refresh",
		DomainID: "domain-1", UserID: "user-1", UserName: "hw-user", Email: "old@example.com",
		PrincipalURN: "iam::domain-1:user/hw-user", AuthContext: map[string]any{"old": true},
		Stats: map[string]any{"request_count": float64(9)}, Health: map[string]any{"total_successes": float64(4)},
		ModelHealth: map[string]any{"GLM-5.2": map[string]any{"total_successes": float64(2)}}, UpdatedAt: "2026-10-01T00:00:00Z",
	}
	if err := manager.saveAccount(existing); err != nil {
		t.Fatal(err)
	}

	inputPath := filepath.Join(t.TempDir(), "accounts.json")
	input := `{"accounts":[{"id":"ignored-new-id","environment":"cn","access_key_id":"new-ak","secret_access_key":"new-sk","security_token":"new-sec","expiration":"2031-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.ImportPlainCredentials(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 0 || result.Updated != 1 {
		t.Fatalf("unexpected update result: %#v", result)
	}
	record, err := manager.LoadAccount(existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.AccessKeyID != "new-ak" || record.SecretAccessKey != "new-sk" || record.SecurityToken != "new-sec" {
		t.Fatalf("credentials were not updated: %#v", record)
	}
	if record.RefreshToken != "old-refresh" || !boolValueAny(record.AuthContext["old"]) {
		t.Fatalf("missing refresh fields should preserve existing refresh state: %#v", record)
	}
	if record.Email != "new@example.com" || numberValue(record.Stats["request_count"]) != 9 || numberValue(record.Health["total_successes"]) != 4 {
		t.Fatalf("identity metadata or runtime state was not preserved correctly: %#v", record)
	}
	if _, err := os.Stat(filepath.Join(manager.Settings.AccountsDir, "ignored-new-id.json")); !os.IsNotExist(err) {
		t.Fatalf("duplicate identity must not create a second account, stat err=%v", err)
	}
}

func TestParsePlainCredentialInputsSupportsArray(t *testing.T) {
	inputs, err := parsePlainCredentialInputs([]byte(`[{"environment":"cn","access_key_id":"a","secret_access_key":"b"},{"environment":"international","access_key_id":"c","secret_access_key":"d"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].AccessKeyID != "a" || inputs[1].Environment != "international" {
		t.Fatalf("unexpected inputs: %#v", inputs)
	}
}

func TestImportPlainStaticCredentialsAreManual(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v5/caller-identity":
			if r.Header.Get("X-Security-Token") != "" {
				t.Fatalf("static credentials should not invent a security token: %q", r.Header.Get("X-Security-Token"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"account_id":"domain-static","principal_id":"user-static","principal_urn":"iam::domain-static:user/static-user"}`)
		case "/v3.0/OS-USER/users/user-static":
			http.Error(w, "email unavailable", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	manager := plainImportTestManager(t, upstream.URL)
	inputPath := filepath.Join(t.TempDir(), "static.json")
	if err := os.WriteFile(inputPath, []byte(`{"environment":"cn","access_key_id":"static-ak","secret_access_key":"static-sk"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.ImportPlainCredentials(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Updated != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	accounts, err := manager.ListAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Source != "manual" || accounts[0].RefreshAvailable || accounts[0].ExpiresAt != "" {
		t.Fatalf("unexpected static account metadata: %#v", accounts)
	}
}

func plainImportTestManager(t *testing.T, upstreamURL string) *Manager {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	settings := config.Load()
	settings.DataDir = dataDir
	settings.LegacyDataDir = filepath.Join(dataDir, "legacy")
	settings.MigrateLegacy = false
	settings.AccountsDir = filepath.Join(dataDir, "accounts")
	settings.CNSTSBaseURL = upstreamURL
	settings.IAMBaseURL = upstreamURL
	if err := securestore.PrepareRuntime(settings.AccountsDir); err != nil {
		t.Fatal(err)
	}
	return NewManager(settings)
}
