package conversation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type Context struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Turns       int    `json:"turns"`
	AccountID   string `json:"account_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	HistoryHash string `json:"history_hash,omitempty"`
}

type Store struct {
	Root string
}

func New(root string) *Store { return &Store{Root: root} }

func (s *Store) Resolve(messages []map[string]any, conversationID string) (Context, error) {
	if conversationID != "" {
		conversationID, err := safeID(conversationID)
		if err != nil {
			return Context{}, err
		}
		if existing, ok := s.Get(conversationID); ok {
			return existing, nil
		}
		now := nowISO()
		return Context{ID: conversationID, CreatedAt: now, UpdatedAt: now}, nil
	}
	prefix := messages
	if len(messages) > 0 {
		if role, _ := messages[len(messages)-1]["role"].(string); role == "user" {
			prefix = messages[:len(messages)-1]
		}
	}
	if len(prefix) > 0 {
		hash, err := MessagesHash(prefix)
		if err != nil {
			return Context{}, err
		}
		if existing, ok := s.findByHistoryHash(hash); ok {
			return existing, nil
		}
	}
	id, err := randomHex(16)
	if err != nil {
		return Context{}, err
	}
	now := nowISO()
	return Context{ID: id, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *Store) Complete(context Context, accountID, environment, chatID string, messages []map[string]any, assistantText string) (Context, error) {
	history := append([]map[string]any(nil), messages...)
	history = append(history, map[string]any{"role": "assistant", "content": assistantText})
	hash, err := MessagesHash(history)
	if err != nil {
		return Context{}, err
	}
	context.AccountID = accountID
	context.Environment = environment
	context.ChatID = chatID
	context.HistoryHash = hash
	context.Turns++
	context.UpdatedAt = nowISO()
	if context.CreatedAt == "" {
		context.CreatedAt = context.UpdatedAt
	}
	if err := s.write(context); err != nil {
		return Context{}, err
	}
	return context, nil
}

func (s *Store) Get(id string) (Context, bool) {
	id, err := safeID(id)
	if err != nil {
		return Context{}, false
	}
	raw, err := os.ReadFile(filepath.Join(s.Root, id+".json"))
	if err != nil {
		return Context{}, false
	}
	var value Context
	if json.Unmarshal(raw, &value) != nil || value.ID == "" {
		return Context{}, false
	}
	return value, true
}

func (s *Store) findByHistoryHash(hash string) (Context, bool) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return Context{}, false
	}
	matches := make([]Context, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.Root, entry.Name()))
		if err != nil {
			continue
		}
		var value Context
		if json.Unmarshal(raw, &value) == nil && value.HistoryHash == hash {
			matches = append(matches, value)
		}
	}
	if len(matches) == 0 {
		return Context{}, false
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].UpdatedAt > matches[j].UpdatedAt })
	return matches[0], true
}

func (s *Store) write(value Context) error {
	id, err := safeID(value.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tempID, err := randomHex(8)
	if err != nil {
		return err
	}
	path := filepath.Join(s.Root, id+".json")
	temp := filepath.Join(s.Root, "."+id+"."+tempID+".tmp")
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func MessagesHash(messages []map[string]any) (string, error) {
	raw, err := json.Marshal(messages)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func safeID(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("invalid conversation id")
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("invalid conversation id")
	}
	return value, nil
}

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
