//go:build linux

package securestore

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	masterKeyOnce sync.Once
	masterKeyData []byte
	masterKeyErr  error
)

func available() bool                { return true }
func platformPrefix() string         { return "aesgcm:v1:" }
func platformProtectionName() string { return "aes-256-gcm-master-key" }

func prepareRuntime(paths ...string) error {
	if strings.TrimSpace(os.Getenv("CODEARTS2API_MASTER_KEY")) != "" {
		_, err := masterKey()
		return err
	}
	path := masterKeyFilePath()
	if path == "" {
		return fmt.Errorf("cannot determine Linux master key path; set CODEARTS2API_MASTER_KEY or CODEARTS2API_MASTER_KEY_FILE")
	}
	if _, err := os.Lstat(path); err == nil {
		_, err := masterKey()
		return err
	} else if !os.IsNotExist(err) {
		return err
	}
	protected, err := hasProtectedRuntimeData(paths...)
	if err != nil {
		return err
	}
	if protected {
		return fmt.Errorf("Linux master key is missing at %s but AES-protected credentials already exist; restore the original key or set CODEARTS2API_MASTER_KEY", path)
	}
	_, err = masterKey()
	return err
}

func protectBytes(plain []byte) ([]byte, error) {
	key, err := masterKey()
	if err != nil {
		return nil, err
	}
	return sealAESGCM(key, plain)
}

func unprotectBytes(cipher []byte) ([]byte, error) {
	key, err := masterKey()
	if err != nil {
		return nil, err
	}
	return openAESGCM(key, cipher)
}

func masterKey() ([]byte, error) {
	masterKeyOnce.Do(func() {
		masterKeyData, masterKeyErr = loadOrCreateMasterKey()
	})
	if masterKeyErr != nil {
		return nil, masterKeyErr
	}
	key := make([]byte, len(masterKeyData))
	copy(key, masterKeyData)
	return key, nil
}

func loadOrCreateMasterKey() ([]byte, error) {
	if raw := strings.TrimSpace(os.Getenv("CODEARTS2API_MASTER_KEY")); raw != "" {
		key, err := decodeMasterKey(raw)
		if err != nil {
			return nil, fmt.Errorf("CODEARTS2API_MASTER_KEY: %w", err)
		}
		return key, nil
	}

	path := masterKeyFilePath()
	if path == "" {
		return nil, fmt.Errorf("cannot determine Linux master key path; set CODEARTS2API_MASTER_KEY or CODEARTS2API_MASTER_KEY_FILE")
	}
	return loadOrCreateMasterKeyFile(path)
}

func masterKeyFilePath() string {
	if path := strings.TrimSpace(os.Getenv("CODEARTS2API_MASTER_KEY_FILE")); path != "" {
		return path
	}
	return defaultMasterKeyPath()
}

func defaultMasterKeyPath() string {
	if stateHome := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); stateHome != "" {
		return filepath.Join(stateHome, "codearts2api", "master.key")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "codearts2api", "master.key")
}

func hasProtectedRuntimeData(paths ...string) (bool, error) {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}
		if !info.IsDir() {
			match, err := fileHasProtectedMarker(path)
			if err != nil {
				return false, err
			}
			if match {
				return true, nil
			}
			continue
		}
		found := false
		err = filepath.WalkDir(path, func(candidate string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				return nil
			}
			match, err := fileHasProtectedMarker(candidate)
			if err != nil {
				return err
			}
			if match {
				found = true
			}
			return nil
		})
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}

func fileHasProtectedMarker(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return bytes.Contains(raw, []byte("aesgcm:v1:")) || bytes.Contains(raw, []byte("aes-256-gcm-master-key")), nil
}

func loadOrCreateMasterKeyFile(path string) ([]byte, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("master key path must be a regular file: %s", path)
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(path, 0o600); err != nil {
				return nil, fmt.Errorf("restrict master key permissions: %w", err)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return decodeMasterKey(strings.TrimSpace(string(raw)))
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create master key directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key) + "\n"
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return loadOrCreateMasterKeyFile(path)
		}
		return nil, fmt.Errorf("create master key file: %w", err)
	}
	if _, err := file.WriteString(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write master key file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync master key file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return key, nil
}

func decodeMasterKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	for _, prefix := range []string{"base64:", "b64:"} {
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			value = strings.TrimSpace(value[len(prefix):])
			break
		}
	}
	if strings.HasPrefix(strings.ToLower(value), "hex:") {
		decoded, err := hex.DecodeString(strings.TrimSpace(value[4:]))
		return validateMasterKey(decoded, err)
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) == 32 {
		return decoded, nil
	}
	return nil, fmt.Errorf("master key must decode to exactly 32 bytes (base64 or hex)")
}

func validateMasterKey(key []byte, err error) ([]byte, error) {
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must decode to exactly 32 bytes, got %d", len(key))
	}
	return key, nil
}
