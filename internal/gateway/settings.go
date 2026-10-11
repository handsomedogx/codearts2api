package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/handsomedogx/codearts2api/internal/securestore"
)

type Access struct {
	APIKey        string `json:"api_key"`
	Source        string `json:"source"`
	Regeneratable bool   `json:"regeneratable"`
}

type Store struct {
	Path       string
	LegacyPath string
	mu         sync.Mutex
}

func New(path string, legacyPath ...string) *Store {
	store := &Store{Path: path}
	if len(legacyPath) > 0 {
		store.LegacyPath = legacyPath[0]
	}
	return store
}

func (s *Store) Ensure() (Access, error) {
	if override := strings.TrimSpace(os.Getenv("CODEARTS2API_API_KEY")); override != "" {
		return Access{APIKey: override, Source: "environment", Regeneratable: false}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload, sourcePath := s.read()
	key, protected, err := decodeKey(payload)
	if err != nil {
		return Access{}, err
	}
	if key == "" {
		key, err = generatedKey()
		if err != nil {
			return Access{}, err
		}
	}
	if key != "" && (sourcePath != s.Path || (securestore.Available() && !protected)) {
		if err := s.writeKey(key); err != nil {
			return Access{}, err
		}
		if sourcePath != "" && filepath.Clean(sourcePath) != filepath.Clean(s.Path) {
			_ = os.Remove(sourcePath)
		}
	} else if sourcePath == "" {
		if err := s.writeKey(key); err != nil {
			return Access{}, err
		}
	}
	if strings.TrimSpace(s.LegacyPath) != "" && filepath.Clean(s.LegacyPath) != filepath.Clean(s.Path) {
		if _, err := os.Stat(s.Path); err == nil {
			_ = os.Remove(s.LegacyPath)
		}
	}
	return Access{APIKey: key, Source: "generated", Regeneratable: true}, nil
}

func (s *Store) Regenerate() (Access, error) {
	if strings.TrimSpace(os.Getenv("CODEARTS2API_API_KEY")) != "" {
		return Access{}, fmt.Errorf("API key is managed by CODEARTS2API_API_KEY and cannot be regenerated here")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := generatedKey()
	if err != nil {
		return Access{}, err
	}
	if err := s.writeKey(key); err != nil {
		return Access{}, err
	}
	return Access{APIKey: key, Source: "generated", Regeneratable: true}, nil
}

func (s *Store) Verify(candidate string) bool {
	access, err := s.Ensure()
	if err != nil || candidate == "" || len(candidate) != len(access.APIKey) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(access.APIKey)) == 1
}

func (s *Store) read() (map[string]any, string) {
	paths := []string{s.Path}
	if strings.TrimSpace(s.LegacyPath) != "" && filepath.Clean(s.LegacyPath) != filepath.Clean(s.Path) {
		paths = append(paths, s.LegacyPath)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		payload := map[string]any{}
		if json.Unmarshal(raw, &payload) == nil {
			return payload, path
		}
	}
	return map[string]any{}, ""
}

func decodeKey(payload map[string]any) (string, bool, error) {
	if value, _ := payload["api_key_protected"].(string); strings.TrimSpace(value) != "" {
		key, err := securestore.UnprotectString(value)
		return strings.TrimSpace(key), true, err
	}
	key, _ := payload["api_key"].(string)
	return strings.TrimSpace(key), false, nil
}

func (s *Store) writeKey(key string) error {
	payload := map[string]any{"version": 1}
	if securestore.Available() {
		protected, err := securestore.ProtectString(key)
		if err != nil {
			return err
		}
		payload["protection"] = securestore.ProtectionName()
		payload["api_key_protected"] = protected
	} else {
		payload["api_key"] = key
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
		return err
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(dir, "."+filepath.Base(s.Path)+".tmp")
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	_ = os.Remove(s.Path)
	if err := os.Rename(temp, s.Path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func generatedKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "sk-ca-" + base64.RawURLEncoding.EncodeToString(buf), nil
}
