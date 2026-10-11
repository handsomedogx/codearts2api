package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/securestore"
)

func TestPlaintextAPIKeyMigratesToDPAPI(t *testing.T) {
	if !securestore.Available() {
		t.Skip("DPAPI is only available on Windows")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy", "settings.json")
	current := filepath.Join(root, "local", "settings.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	const key = "sk-ca-legacy-test-key"
	if err := os.WriteFile(legacy, []byte(`{"api_key":"`+key+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	store := New(current, legacy)
	access, err := store.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	if access.APIKey != key {
		t.Fatalf("unexpected key: %q", access.APIKey)
	}
	migrated, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migrated), key) || !strings.Contains(string(migrated), "api_key_protected") {
		t.Fatalf("API key was not protected: %s", string(migrated))
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy settings should be removed, stat err=%v", err)
	}
	if !store.Verify(key) {
		t.Fatal("migrated API key should still verify")
	}
}
