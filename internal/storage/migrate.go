package storage

import (
	"io"
	"os"
	"path/filepath"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func MigrateNonSensitive(settings config.Settings) error {
	if !settings.MigrateLegacy {
		return nil
	}
	if err := os.MkdirAll(settings.DataDir, 0o755); err != nil {
		return err
	}
	pairs := [][2]string{
		{filepath.Join(settings.LegacyDataDir, "routing.json"), settings.RoutingFile},
		{filepath.Join(settings.LegacyDataDir, "usage.jsonl"), settings.UsageFile},
		{filepath.Join(settings.LegacyDataDir, "request_logs.jsonl"), settings.RequestLogsFile},
		{filepath.Join(settings.LegacyDataDir, "conversations"), settings.ConversationsDir},
	}
	for _, pair := range pairs {
		if err := moveIfDestinationMissing(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

func moveIfDestinationMissing(source, destination string) error {
	if _, err := os.Stat(source); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if _, err := os.Stat(destination); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyDir(source, destination); err != nil {
			return err
		}
		return os.RemoveAll(source)
	}
	if err := copyFile(source, destination, info.Mode()); err != nil {
		return err
	}
	return os.Remove(source)
}

func copyDir(source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		src := filepath.Join(source, entry.Name())
		dst := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			if err := copyDir(src, dst); err != nil {
				return err
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := copyFile(src, dst, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
