package conversation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConversationResolveAndHistoryRouting(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "conversations"))
	messages := []map[string]any{{"role": "user", "content": "hello"}}
	ctx, err := store.Resolve(messages, "")
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ID == "" || ctx.Turns != 0 {
		t.Fatalf("unexpected context: %#v", ctx)
	}
	updated, err := store.Complete(ctx, "account-1", "cn", "chat-1", messages, "world")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Turns != 1 || updated.AccountID != "account-1" || updated.ChatID != "chat-1" {
		t.Fatalf("unexpected updated context: %#v", updated)
	}
	if _, err := os.Stat(filepath.Join(store.Root, updated.ID+".json")); err != nil {
		t.Fatal(err)
	}

	history := []map[string]any{{"role": "user", "content": "hello"}, {"role": "assistant", "content": "world"}, {"role": "user", "content": "again"}}
	resolved, err := store.Resolve(history, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != updated.ID || resolved.AccountID != "account-1" || resolved.ChatID != "chat-1" {
		t.Fatalf("history hash did not recover routing context: %#v", resolved)
	}
}

func TestConversationExplicitIDValidation(t *testing.T) {
	store := New(t.TempDir())
	ctx, err := store.Resolve(nil, "my-conversation_1")
	if err != nil || ctx.ID != "my-conversation_1" {
		t.Fatalf("unexpected explicit id: %#v %v", ctx, err)
	}
	if _, err := store.Resolve(nil, "../escape"); err == nil {
		t.Fatal("expected invalid conversation id")
	}
}
