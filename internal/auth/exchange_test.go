package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/handsomedogx/codearts2api/internal/config"
)

func TestExchangeCode(t *testing.T) {
	var tokenCalled bool
	var identityCalled bool
	var emailCalled bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth2/tokens":
			tokenCalled = true
			if r.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", r.Method)
			}
			if r.Header.Get("DPoP") == "" {
				t.Fatal("expected DPoP header")
			}
			body, _ := io.ReadAll(r.Body)
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Fatal(err)
			}
			if form.Get("code") != "oauth-code" || form.Get("grant_type") != "authorization_code" || form.Get("code_verifier") == "" {
				t.Fatalf("unexpected token form: %#v", form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"credentials":{"access_key_id":"ak","secret_access_key":"sk","security_token":"sec","expiration":"2030-01-01T00:00:00Z"},"refresh_token":"refresh"}`)
		case "/v5/caller-identity":
			identityCalled = true
			if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=ak") {
				t.Fatalf("unexpected authorization: %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"account_id":"domain-1","principal_id":"user-1","principal_urn":"iam::domain-1:user/test-user"}`)
		case "/v3.0/OS-USER/users/user-1":
			emailCalled = true
			if !strings.HasPrefix(r.Header.Get("Authorization"), "SDK-HMAC-SHA256 Access=ak") {
				t.Fatalf("unexpected IAM authorization: %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"user":{"id":"user-1","email":"tester@example.com"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNSTSBaseURL = upstream.URL
	settings.IAMBaseURL = upstream.URL
	settings.PendingAuthFile = filepath.Join(temp, "oauth_pending.json")
	settings.AccountsDir = filepath.Join(temp, "accounts")
	manager := NewManager(settings)

	session, err := manager.BeginLoginSession("cn")
	if err != nil {
		t.Fatal(err)
	}
	account, err := manager.ExchangeCode("oauth-code")
	if err != nil {
		t.Fatal(err)
	}
	if !tokenCalled || !identityCalled || !emailCalled {
		t.Fatalf("expected token, identity and email calls: token=%v identity=%v email=%v", tokenCalled, identityCalled, emailCalled)
	}
	if account.ID != session.AccountID || !account.Authenticated || account.UserID != "user-1" || account.DomainID != "domain-1" || account.Email != "tester@example.com" {
		t.Fatalf("unexpected public account: %#v", account)
	}
	if _, err := os.Stat(settings.PendingAuthFile); !os.IsNotExist(err) {
		t.Fatalf("pending login should be removed, stat err=%v", err)
	}
	accountPath := filepath.Join(settings.AccountsDir, session.AccountID+".json")
	payload, err := os.ReadFile(accountPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"secret_access_key":"sk"`) || strings.Contains(string(payload), `"refresh_token":"refresh"`) || strings.Contains(string(payload), `"access_key_id":"ak"`) || strings.Contains(string(payload), "tester@example.com") {
		t.Fatalf("account secrets must not be stored in plaintext: %s", string(payload))
	}
	stored, err := manager.LoadAccount(session.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessKeyID != "ak" || stored.SecretAccessKey != "sk" || stored.RefreshToken != "refresh" || stored.UserName != "user/test-user" || stored.Email != "tester@example.com" {
		t.Fatalf("unexpected decrypted account: %#v", stored)
	}
}

func TestExchangeCodeReusesExistingIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth2/tokens":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"credentials":{"access_key_id":"new-ak","secret_access_key":"new-sk","security_token":"new-sec","expiration":"2031-01-01T00:00:00Z"},"refresh_token":"new-refresh"}`)
		case "/v5/caller-identity":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"account_id":"domain-1","principal_id":"user-1","principal_urn":"iam::domain-1:user/test-user"}`)
		case "/v3.0/OS-USER/users/user-1":
			http.Error(w, "temporary IAM failure", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNSTSBaseURL = upstream.URL
	settings.IAMBaseURL = upstream.URL
	settings.PendingAuthFile = filepath.Join(temp, "oauth_pending.json")
	settings.AccountsDir = filepath.Join(temp, "accounts")
	settings.MigrateLegacy = false
	manager := NewManager(settings)

	existing := AccountRecord{
		ID: "existing-account", Environment: "cn", AccessKeyID: "old-ak", SecretAccessKey: "old-sk",
		SecurityToken: "old-sec", Expiration: "2030-01-01T00:00:00Z", RefreshToken: "old-refresh",
		DomainID: "domain-1", UserID: "user-1", UserName: "test-user", Email: "existing@example.com", PrincipalURN: "iam::domain-1:user/test-user",
		AuthContext: map[string]any{"old": true}, Stats: map[string]any{"request_count": float64(7)},
		Health: map[string]any{"total_successes": float64(5)}, ModelHealth: map[string]any{"GLM-5.2": map[string]any{"total_successes": float64(3)}},
		UpdatedAt: "2026-10-01T00:00:00Z",
	}
	if err := manager.saveAccount(existing); err != nil {
		t.Fatal(err)
	}

	session, err := manager.BeginLoginSession("cn")
	if err != nil {
		t.Fatal(err)
	}
	if session.AccountID == existing.ID {
		t.Fatal("test requires a distinct temporary OAuth account id")
	}
	account, err := manager.ExchangeCode("oauth-code")
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != existing.ID {
		t.Fatalf("expected existing account id %q, got %q", existing.ID, account.ID)
	}
	if _, err := os.Stat(filepath.Join(settings.AccountsDir, session.AccountID+".json")); !os.IsNotExist(err) {
		t.Fatalf("temporary OAuth account must not be persisted, stat err=%v", err)
	}
	stored, err := manager.LoadAccount(existing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessKeyID != "new-ak" || stored.SecretAccessKey != "new-sk" || stored.RefreshToken != "new-refresh" {
		t.Fatalf("existing account credentials were not replaced: %#v", stored)
	}
	if stored.Email != "existing@example.com" {
		t.Fatalf("existing email should survive a best-effort IAM lookup failure: %#v", stored)
	}
	if numberValue(stored.Stats["request_count"]) != 7 || numberValue(stored.Health["total_successes"]) != 5 {
		t.Fatalf("existing account runtime stats were not preserved: stats=%#v health=%#v", stored.Stats, stored.Health)
	}
	if _, err := os.Stat(settings.PendingAuthFile); !os.IsNotExist(err) {
		t.Fatalf("pending login should be removed, stat err=%v", err)
	}
}
