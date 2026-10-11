package auth

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestBeginLoginSession(t *testing.T) {
	settings := config.Load()
	settings.PendingAuthFile = filepath.Join(t.TempDir(), "oauth_pending.json")
	manager := NewManager(settings)
	session, err := manager.BeginLoginSession("cn")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(session.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if q.Get("client_id") != settings.AuthClientID {
		t.Fatalf("unexpected client_id: %q", q.Get("client_id"))
	}
	if q.Get("code_challenge_method") != "SHA-256" {
		t.Fatalf("unexpected challenge method: %q", q.Get("code_challenge_method"))
	}
	if q.Get("ticket_id") == "" || q.Get("code_challenge") == "" {
		t.Fatal("expected ticket_id and code_challenge")
	}
}
