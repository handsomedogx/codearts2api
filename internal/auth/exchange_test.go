package auth

import (
	"encoding/json"
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	temp := t.TempDir()
	settings := config.Load()
	settings.CNSTSBaseURL = upstream.URL
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
	if !tokenCalled || !identityCalled {
		t.Fatalf("expected both upstream calls: token=%v identity=%v", tokenCalled, identityCalled)
	}
	if account.ID != session.AccountID || !account.Authenticated || account.UserID != "user-1" || account.DomainID != "domain-1" {
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
	var stored AccountRecord
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.AccessKeyID != "ak" || stored.SecretAccessKey != "sk" || stored.RefreshToken != "refresh" || stored.UserName != "user/test-user" {
		t.Fatalf("unexpected stored account: %#v", stored)
	}
}
