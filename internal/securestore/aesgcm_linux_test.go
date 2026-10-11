//go:build linux

package securestore

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func resetLinuxMasterKeyForTest() {
	masterKeyOnce = sync.Once{}
	masterKeyData = nil
	masterKeyErr = nil
}

func TestLinuxDefaultMasterKeyFile(t *testing.T) {
	resetLinuxMasterKeyForTest()
	t.Cleanup(resetLinuxMasterKeyForTest)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("CODEARTS2API_MASTER_KEY", "")
	t.Setenv("CODEARTS2API_MASTER_KEY_FILE", "")

	protected, err := ProtectString("linux-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !IsProtected(protected) || Prefix() != "aesgcm:v1:" {
		t.Fatalf("unexpected Linux protected value: %q", protected)
	}
	path := filepath.Join(stateHome, "codearts2api", "master.key")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("master key mode = %#o, want 0600", got)
	}
	decoded, err := UnprotectString(protected)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "linux-secret" {
		t.Fatalf("unexpected plaintext: %q", decoded)
	}
}

func TestLinuxMasterKeyEnvironmentOverride(t *testing.T) {
	resetLinuxMasterKeyForTest()
	t.Cleanup(resetLinuxMasterKeyForTest)
	key := bytes.Repeat([]byte{0x5a}, 32)
	t.Setenv("CODEARTS2API_MASTER_KEY", base64.RawStdEncoding.EncodeToString(key))
	t.Setenv("CODEARTS2API_MASTER_KEY_FILE", filepath.Join(t.TempDir(), "must-not-exist.key"))

	protected, err := ProtectString("env-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := UnprotectString(protected)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "env-secret" {
		t.Fatalf("unexpected plaintext: %q", plain)
	}
	if _, err := os.Stat(os.Getenv("CODEARTS2API_MASTER_KEY_FILE")); !os.IsNotExist(err) {
		t.Fatalf("master key file should not be created when env key is set, stat err=%v", err)
	}
}

func TestLinuxMasterKeyFileOverrideRestrictsPermissions(t *testing.T) {
	resetLinuxMasterKeyForTest()
	t.Cleanup(resetLinuxMasterKeyForTest)
	keyPath := filepath.Join(t.TempDir(), "master.key")
	key := bytes.Repeat([]byte{0x33}, 32)
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEARTS2API_MASTER_KEY", "")
	t.Setenv("CODEARTS2API_MASTER_KEY_FILE", keyPath)

	if _, err := ProtectString("file-secret"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("master key mode = %#o, want 0600", got)
	}
}

func TestLinuxPrepareRejectsMissingKeyWithProtectedData(t *testing.T) {
	resetLinuxMasterKeyForTest()
	t.Cleanup(resetLinuxMasterKeyForTest)
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("CODEARTS2API_MASTER_KEY", "")
	t.Setenv("CODEARTS2API_MASTER_KEY_FILE", "")

	dataDir := t.TempDir()
	settingsPath := filepath.Join(dataDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"protection":"aes-256-gcm-master-key","api_key_protected":"aesgcm:v1:deadbeef"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := PrepareRuntime(settingsPath)
	if err == nil || !strings.Contains(err.Error(), "master key is missing") {
		t.Fatalf("expected missing master key error, got %v", err)
	}
	keyPath := filepath.Join(stateHome, "codearts2api", "master.key")
	if _, statErr := os.Stat(keyPath); !os.IsNotExist(statErr) {
		t.Fatalf("missing-key guard must not create a replacement key, stat err=%v", statErr)
	}
}
