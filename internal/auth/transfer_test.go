package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/securestore"
)

func TestCredentialBundleRoundTrip(t *testing.T) {
	if !securestore.Available() {
		t.Skip("platform secure storage is unavailable")
	}
	password := "correct horse battery staple"
	source := transferTestManager(t, filepath.Join(t.TempDir(), "source"))
	record := transferTestAccount("account-a", "source-secret-sk")
	if err := source.saveAccount(record); err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "accounts.enc")
	count, err := source.ExportCredentials(bundle, password)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 exported account, got %d", count)
	}
	raw, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"source-secret-sk", "refresh-secret", "security-secret", "private-dpop-secret", "plain-ak"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("migration bundle leaked secret %q: %s", secret, raw)
		}
	}
	if !strings.Contains(string(raw), credentialBundleCipher) || !strings.Contains(string(raw), credentialBundleKDF) {
		t.Fatalf("migration bundle does not describe expected protection: %s", raw)
	}

	destination := transferTestManager(t, filepath.Join(t.TempDir(), "destination"))
	result, err := destination.ImportCredentials(bundle, password, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Replaced != 0 {
		t.Fatalf("unexpected import result: %#v", result)
	}
	loaded, err := destination.LoadAccount("account-a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SecretAccessKey != "source-secret-sk" || loaded.RefreshToken != "refresh-secret" {
		t.Fatalf("imported secrets did not round trip: %#v", loaded)
	}
	stored, err := os.ReadFile(filepath.Join(destination.Settings.AccountsDir, "account-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "source-secret-sk") || !strings.Contains(string(stored), securestore.ProtectionName()) {
		t.Fatalf("imported account was not re-protected for destination platform: %s", stored)
	}
}

func TestCredentialBundleBytesRoundTrip(t *testing.T) {
	if !securestore.Available() {
		t.Skip("platform secure storage is unavailable")
	}
	password := "correct horse battery staple"
	source := transferTestManager(t, filepath.Join(t.TempDir(), "source"))
	if err := source.saveAccount(transferTestAccount("account-memory", "memory-secret-sk")); err != nil {
		t.Fatal(err)
	}

	raw, count, err := source.ExportCredentialsBytes(password)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || len(raw) == 0 {
		t.Fatalf("unexpected in-memory export: count=%d bytes=%d", count, len(raw))
	}
	if strings.Contains(string(raw), "memory-secret-sk") {
		t.Fatal("in-memory migration bundle leaked plaintext secret")
	}

	destination := transferTestManager(t, filepath.Join(t.TempDir(), "destination"))
	result, err := destination.ImportCredentialsBytes(raw, password, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Replaced != 0 {
		t.Fatalf("unexpected in-memory import result: %#v", result)
	}
	loaded, err := destination.LoadAccount("account-memory")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SecretAccessKey != "memory-secret-sk" {
		t.Fatalf("in-memory import did not preserve credentials: %#v", loaded)
	}
}

func TestCredentialBundleRejectsWrongPasswordAndTampering(t *testing.T) {
	if !securestore.Available() {
		t.Skip("platform secure storage is unavailable")
	}
	password := "correct horse battery staple"
	source := transferTestManager(t, filepath.Join(t.TempDir(), "source"))
	if err := source.saveAccount(transferTestAccount("account-a", "source-secret-sk")); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "accounts.enc")
	if _, err := source.ExportCredentials(bundle, password); err != nil {
		t.Fatal(err)
	}
	destination := transferTestManager(t, filepath.Join(t.TempDir(), "destination"))
	if _, err := destination.ImportCredentials(bundle, "definitely-wrong-password", false); err == nil || !strings.Contains(err.Error(), "wrong password or corrupted file") {
		t.Fatalf("wrong password should fail authentication, got %v", err)
	}

	raw, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var envelope credentialBundleEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(ciphertext) == 0 {
		t.Fatalf("decode ciphertext for tamper test: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0x01
	envelope.Ciphertext = base64.RawStdEncoding.EncodeToString(ciphertext)
	tampered, _ := json.Marshal(envelope)
	if err := os.WriteFile(bundle, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.ImportCredentials(bundle, password, false); err == nil {
		t.Fatal("tampered credential bundle should be rejected")
	}
}

func TestCredentialImportRequiresExplicitReplace(t *testing.T) {
	if !securestore.Available() {
		t.Skip("platform secure storage is unavailable")
	}
	password := "correct horse battery staple"
	source := transferTestManager(t, filepath.Join(t.TempDir(), "source"))
	if err := source.saveAccount(transferTestAccount("account-a", "source-secret-sk")); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "accounts.enc")
	if _, err := source.ExportCredentials(bundle, password); err != nil {
		t.Fatal(err)
	}

	destination := transferTestManager(t, filepath.Join(t.TempDir(), "destination"))
	if err := destination.saveAccount(transferTestAccount("account-a", "destination-secret-sk")); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.ImportCredentials(bundle, password, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing account should require --replace, got %v", err)
	}
	unchanged, err := destination.LoadAccount("account-a")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.SecretAccessKey != "destination-secret-sk" {
		t.Fatalf("conflicting account was overwritten without replace: %#v", unchanged)
	}

	result, err := destination.ImportCredentials(bundle, password, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 || result.Replaced != 1 {
		t.Fatalf("unexpected replace result: %#v", result)
	}
	replaced, err := destination.LoadAccount("account-a")
	if err != nil {
		t.Fatal(err)
	}
	if replaced.SecretAccessKey != "source-secret-sk" {
		t.Fatalf("replace did not import source credentials: %#v", replaced)
	}
}

func transferTestManager(t *testing.T, dataDir string) *Manager {
	t.Helper()
	settings := config.Load()
	settings.DataDir = dataDir
	settings.LegacyDataDir = filepath.Join(dataDir, "legacy")
	settings.MigrateLegacy = false
	settings.AccountsDir = filepath.Join(dataDir, "accounts")
	if err := securestore.PrepareRuntime(settings.AccountsDir); err != nil {
		t.Fatal(err)
	}
	return NewManager(settings)
}

func transferTestAccount(id, secret string) AccountRecord {
	return AccountRecord{
		ID: id, Environment: "cn", AccessKeyID: "plain-ak", SecretAccessKey: secret,
		SecurityToken: "security-secret", Expiration: "2030-01-01T00:00:00Z", RefreshToken: "refresh-secret",
		DomainID: "domain-1", UserID: "user-1", UserName: "tester", PrincipalURN: "urn:test:user:tester",
		AuthContext: map[string]any{
			"pkce_pair":     map[string]any{"code_verifier": "verifier-secret"},
			"dpop_key_pair": map[string]any{"private_jwk": map[string]any{"d": "private-dpop-secret"}},
		},
		Stats: map[string]any{"request_count": float64(3)}, Health: map[string]any{}, ModelHealth: map[string]any{},
		UpdatedAt: "2026-10-15T00:00:00Z",
	}
}
