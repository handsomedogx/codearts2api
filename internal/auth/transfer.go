package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/handsomedogx/codearts2api/internal/securestore"
)

const (
	credentialBundleVersion    = 1
	credentialBundleKDF        = "pbkdf2-sha256"
	credentialBundleCipher     = "aes-256-gcm"
	credentialBundleIterations = 600_000
	credentialBundleMaxSize    = 64 << 20
	credentialBundleAAD        = "codearts2api-credential-bundle-v1"
)

type credentialBundleEnvelope struct {
	Version          int    `json:"version"`
	CreatedAt        string `json:"created_at"`
	SourceProtection string `json:"source_protection"`
	KDF              string `json:"kdf"`
	Iterations       int    `json:"iterations"`
	Salt             string `json:"salt"`
	Cipher           string `json:"cipher"`
	Nonce            string `json:"nonce"`
	Ciphertext       string `json:"ciphertext"`
}

type credentialBundlePayload struct {
	Version  int             `json:"version"`
	Accounts []AccountRecord `json:"accounts"`
}

type ImportCredentialsResult struct {
	Imported int
	Replaced int
}

func (m *Manager) ExportCredentials(path, password string) (int, error) {
	if !securestore.Available() {
		return 0, fmt.Errorf("secure credential storage is unavailable on this platform")
	}
	if strings.TrimSpace(path) == "" {
		return 0, fmt.Errorf("output path is required")
	}
	if err := validateMigrationPassword(password); err != nil {
		return 0, err
	}
	ids, err := m.credentialAccountIDs()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("no CodeArts accounts found to export")
	}
	accounts := make([]AccountRecord, 0, len(ids))
	for _, id := range ids {
		record, err := m.LoadAccount(id)
		if err != nil {
			return 0, fmt.Errorf("load account %s for export: %w", id, err)
		}
		accounts = append(accounts, record)
	}
	payload, err := json.Marshal(credentialBundlePayload{Version: credentialBundleVersion, Accounts: accounts})
	if err != nil {
		return 0, fmt.Errorf("encode credential bundle: %w", err)
	}
	envelope, err := sealCredentialBundle(payload, password, securestore.ProtectionName())
	if err != nil {
		return 0, err
	}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("encode credential envelope: %w", err)
	}
	if err := atomicWriteBytes(path, raw); err != nil {
		return 0, fmt.Errorf("write credential bundle: %w", err)
	}
	return len(accounts), nil
}

func (m *Manager) ImportCredentials(path, password string, replace bool) (ImportCredentialsResult, error) {
	if !securestore.Available() {
		return ImportCredentialsResult{}, fmt.Errorf("secure credential storage is unavailable on this platform")
	}
	if strings.TrimSpace(path) == "" {
		return ImportCredentialsResult{}, fmt.Errorf("input path is required")
	}
	if err := validateMigrationPassword(password); err != nil {
		return ImportCredentialsResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return ImportCredentialsResult{}, fmt.Errorf("open credential bundle: %w", err)
	}
	if info.Size() > credentialBundleMaxSize {
		return ImportCredentialsResult{}, fmt.Errorf("credential bundle is too large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ImportCredentialsResult{}, fmt.Errorf("read credential bundle: %w", err)
	}
	var envelope credentialBundleEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ImportCredentialsResult{}, fmt.Errorf("invalid credential bundle: %w", err)
	}
	plain, err := openCredentialBundle(envelope, password)
	if err != nil {
		return ImportCredentialsResult{}, err
	}
	var payload credentialBundlePayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return ImportCredentialsResult{}, fmt.Errorf("decode credential bundle payload: %w", err)
	}
	if payload.Version != credentialBundleVersion {
		return ImportCredentialsResult{}, fmt.Errorf("unsupported credential bundle payload version: %d", payload.Version)
	}
	if len(payload.Accounts) == 0 {
		return ImportCredentialsResult{}, fmt.Errorf("credential bundle contains no accounts")
	}
	if len(payload.Accounts) > 10_000 {
		return ImportCredentialsResult{}, fmt.Errorf("credential bundle contains too many accounts")
	}

	seen := make(map[string]struct{}, len(payload.Accounts))
	existing := make(map[string]bool, len(payload.Accounts))
	for i := range payload.Accounts {
		record := &payload.Accounts[i]
		record.ID = strings.TrimSpace(record.ID)
		if record.ID == "" || strings.ContainsAny(record.ID, `\\/:*?"<>|`) {
			return ImportCredentialsResult{}, fmt.Errorf("credential bundle contains invalid account id %q", record.ID)
		}
		if _, ok := seen[record.ID]; ok {
			return ImportCredentialsResult{}, fmt.Errorf("credential bundle contains duplicate account id %s", record.ID)
		}
		seen[record.ID] = struct{}{}
		profile, err := m.Settings.Profile(record.Environment)
		if err != nil {
			return ImportCredentialsResult{}, fmt.Errorf("account %s has invalid environment: %w", record.ID, err)
		}
		record.Environment = profile.Environment
		if strings.TrimSpace(record.AccessKeyID) == "" || strings.TrimSpace(record.SecretAccessKey) == "" {
			return ImportCredentialsResult{}, fmt.Errorf("account %s is missing credentials", record.ID)
		}
		exists, err := m.accountExists(record.ID)
		if err != nil {
			return ImportCredentialsResult{}, err
		}
		existing[record.ID] = exists
		if exists && !replace {
			return ImportCredentialsResult{}, fmt.Errorf("account %s already exists; rerun with --replace to overwrite existing account ids", record.ID)
		}
	}

	result := ImportCredentialsResult{}
	for _, record := range payload.Accounts {
		if err := m.saveAccount(record); err != nil {
			return result, fmt.Errorf("save imported account %s: %w", record.ID, err)
		}
		if m.Settings.MigrateLegacy {
			legacy := filepath.Join(m.Settings.LegacyDataDir, "accounts", record.ID+".json")
			primary := filepath.Join(m.Settings.AccountsDir, record.ID+".json")
			if filepath.Clean(legacy) != filepath.Clean(primary) {
				_ = os.Remove(legacy)
			}
		}
		result.Imported++
		if existing[record.ID] {
			result.Replaced++
		}
	}
	return result, nil
}

func (m *Manager) credentialAccountIDs() ([]string, error) {
	ids := map[string]struct{}{}
	dirs := []string{m.Settings.AccountsDir}
	if m.Settings.MigrateLegacy {
		dirs = append(dirs, filepath.Join(m.Settings.LegacyDataDir, "accounts"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				continue
			}
			ids[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))] = struct{}{}
		}
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

func (m *Manager) accountExists(accountID string) (bool, error) {
	for _, path := range m.accountPaths(accountID) {
		_, err := os.Stat(path)
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("check existing account %s: %w", accountID, err)
		}
	}
	return false, nil
}

func validateMigrationPassword(password string) error {
	if len(password) < 8 {
		return fmt.Errorf("migration password must be at least 8 characters")
	}
	return nil
}

func sealCredentialBundle(plain []byte, password, sourceProtection string) (credentialBundleEnvelope, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return credentialBundleEnvelope{}, fmt.Errorf("generate credential bundle salt: %w", err)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, credentialBundleIterations, 32)
	if err != nil {
		return credentialBundleEnvelope{}, fmt.Errorf("derive credential bundle key: %w", err)
	}
	defer zeroBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return credentialBundleEnvelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return credentialBundleEnvelope{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return credentialBundleEnvelope{}, fmt.Errorf("generate credential bundle nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plain, []byte(credentialBundleAAD))
	return credentialBundleEnvelope{
		Version: credentialBundleVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		SourceProtection: sourceProtection, KDF: credentialBundleKDF, Iterations: credentialBundleIterations,
		Salt: base64.RawStdEncoding.EncodeToString(salt), Cipher: credentialBundleCipher,
		Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}, nil
}

func openCredentialBundle(envelope credentialBundleEnvelope, password string) ([]byte, error) {
	if envelope.Version != credentialBundleVersion {
		return nil, fmt.Errorf("unsupported credential bundle version: %d", envelope.Version)
	}
	if envelope.KDF != credentialBundleKDF || envelope.Cipher != credentialBundleCipher {
		return nil, fmt.Errorf("unsupported credential bundle protection")
	}
	if envelope.Iterations < 100_000 || envelope.Iterations > 5_000_000 {
		return nil, fmt.Errorf("invalid credential bundle KDF iterations")
	}
	salt, err := base64.RawStdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) < 16 {
		return nil, fmt.Errorf("invalid credential bundle salt")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, fmt.Errorf("invalid credential bundle nonce")
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, fmt.Errorf("invalid credential bundle ciphertext")
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, envelope.Iterations, 32)
	if err != nil {
		return nil, fmt.Errorf("derive credential bundle key: %w", err)
	}
	defer zeroBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("invalid credential bundle nonce")
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, []byte(credentialBundleAAD))
	if err != nil {
		return nil, fmt.Errorf("cannot decrypt credential bundle: wrong password or corrupted file")
	}
	return plain, nil
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
