package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestMigrateNonSensitiveMovesLegacyFiles(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	current := filepath.Join(root, "local")
	if err := os.MkdirAll(filepath.Join(legacy, "conversations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "routing.json"), []byte(`{"strategy":"weighted"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "usage.jsonl"), []byte("usage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "request_logs.jsonl"), []byte("log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "conversations", "c1.json"), []byte(`{"id":"c1"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	settings := config.Settings{
		DataDir: current, LegacyDataDir: legacy, MigrateLegacy: true,
		RoutingFile:      filepath.Join(current, "routing.json"),
		UsageFile:        filepath.Join(current, "usage.jsonl"),
		RequestLogsFile:  filepath.Join(current, "request_logs.jsonl"),
		ConversationsDir: filepath.Join(current, "conversations"),
	}
	if err := MigrateNonSensitive(settings); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{settings.RoutingFile, settings.UsageFile, settings.RequestLogsFile, filepath.Join(settings.ConversationsDir, "c1.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing migrated path %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(legacy, "routing.json"), filepath.Join(legacy, "usage.jsonl"), filepath.Join(legacy, "request_logs.jsonl"), filepath.Join(legacy, "conversations")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("legacy path should be removed: %s (%v)", path, err)
		}
	}
}
