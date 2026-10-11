package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/securestore"
)

func TestPlaintextAccountMigratesToDPAPI(t *testing.T) {
	if !securestore.Available() {
		t.Skip("DPAPI is only available on Windows")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	dataDir := filepath.Join(root, "local")
	legacyAccounts := filepath.Join(legacy, "accounts")
	if err := os.MkdirAll(legacyAccounts, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := config.Load()
	settings.DataDir = dataDir
	settings.LegacyDataDir = legacy
	settings.MigrateLegacy = true
	settings.AccountsDir = filepath.Join(dataDir, "accounts")
	settings.PendingAuthFile = filepath.Join(dataDir, "oauth_pending.json")

	legacyRecord := AccountRecord{
		ID: "a1", Environment: "cn", AccessKeyID: "plain-ak", SecretAccessKey: "plain-sk",
		SecurityToken: "plain-security-token", RefreshToken: "plain-refresh",
		Expiration: "2030-01-01T00:00:00Z", DomainID: "domain-1", UserID: "user-1",
		AuthContext: map[string]any{"pkce_pair": map[string]any{"code_verifier": "plain-verifier"}},
		Stats:       map[string]any{}, Health: map[string]any{}, ModelHealth: map[string]any{},
	}
	raw, _ := json.MarshalIndent(legacyRecord, "", "  ")
	legacyPath := filepath.Join(legacyAccounts, "a1.json")
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(settings)
	loaded, err := manager.LoadAccount("a1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SecretAccessKey != "plain-sk" || loaded.RefreshToken != "plain-refresh" {
		t.Fatalf("unexpected decrypted record: %#v", loaded)
	}
	newPath := filepath.Join(settings.AccountsDir, "a1.json")
	migrated, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"plain-ak", "plain-sk", "plain-security-token", "plain-refresh", "plain-verifier"} {
		if strings.Contains(string(migrated), secret) {
			t.Fatalf("secret %q leaked into migrated file: %s", secret, string(migrated))
		}
	}
	if !strings.Contains(string(migrated), "dpapi-current-user") || !strings.Contains(string(migrated), "dpapi:v1:") {
		t.Fatalf("migrated account is not DPAPI protected: %s", string(migrated))
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy plaintext account should be removed, stat err=%v", err)
	}
}

func TestPlaintextPendingLoginMigratesToDPAPI(t *testing.T) {
	if !securestore.Available() {
		t.Skip("DPAPI is only available on Windows")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	dataDir := filepath.Join(root, "local")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := config.Load()
	settings.DataDir = dataDir
	settings.LegacyDataDir = legacy
	settings.MigrateLegacy = true
	settings.AccountsDir = filepath.Join(dataDir, "accounts")
	settings.PendingAuthFile = filepath.Join(dataDir, "oauth_pending.json")

	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	dpop, err := GenerateDPoPKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	pending := PendingLogin{AccountID: "a1", Environment: "cn", PKCEPair: pkce, DPoPKeyPair: dpop, TicketID: "ticket"}
	raw, _ := json.MarshalIndent(pending, "", "  ")
	legacyPath := filepath.Join(legacy, "oauth_pending.json")
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(settings)
	loaded, err := manager.readPending()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PKCEPair.CodeVerifier != pkce.CodeVerifier {
		t.Fatalf("unexpected pending login: %#v", loaded)
	}
	migrated, err := os.ReadFile(settings.PendingAuthFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migrated), pkce.CodeVerifier) || strings.Contains(string(migrated), dpop.PrivateJWK.D) {
		t.Fatalf("pending OAuth secrets leaked in plaintext: %s", string(migrated))
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy pending file should be removed, stat err=%v", err)
	}
}
