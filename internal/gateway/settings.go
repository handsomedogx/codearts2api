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
)

type Access struct {
	APIKey        string `json:"api_key"`
	Source        string `json:"source"`
	Regeneratable bool   `json:"regeneratable"`
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func New(path string) *Store { return &Store{Path: path} }

func (s *Store) Ensure() (Access, error) {
	if override := strings.TrimSpace(os.Getenv("CODEARTS2API_API_KEY")); override != "" {
		return Access{APIKey: override, Source: "environment", Regeneratable: false}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	payload := s.read()
	key, _ := payload["api_key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		var err error
		key, err = generatedKey()
		if err != nil {
			return Access{}, err
		}
		payload["api_key"] = key
		if err := s.write(payload); err != nil {
			return Access{}, err
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
	payload := s.read()
	payload["api_key"] = key
	if err := s.write(payload); err != nil {
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

func (s *Store) read() map[string]any {
	payload := map[string]any{}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return payload
	}
	_ = json.Unmarshal(raw, &payload)
	return payload
}

func (s *Store) write(payload map[string]any) error {
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
