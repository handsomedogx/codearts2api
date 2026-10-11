package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultDataDirUsesLocalAppData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	got := defaultDataDir()
	want := filepath.Join(root, "codearts2api")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("default data dir = %q, want %q", got, want)
	}
}

func TestDataDirEnvironmentOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "custom-data")
	t.Setenv("CODEARTS2API_DATA_DIR", root)
	settings := Load()
	if filepath.Clean(settings.DataDir) != filepath.Clean(root) {
		t.Fatalf("data dir = %q, want %q", settings.DataDir, root)
	}
	if filepath.Clean(settings.AccountsDir) != filepath.Clean(filepath.Join(root, "accounts")) {
		t.Fatalf("accounts dir did not follow data dir: %q", settings.AccountsDir)
	}
	if settings.MigrateLegacy {
		t.Fatal("explicit data directory should disable implicit legacy migration")
	}
}
