package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/signer"
)

type PendingLogin struct {
	AccountID   string      `json:"account_id"`
	Environment string      `json:"environment"`
	PKCEPair    PKCEPair    `json:"pkce_pair"`
	DPoPKeyPair DPoPKeyPair `json:"dpop_key_pair"`
	TicketID    string      `json:"ticket_id"`
	CreatedAt   string      `json:"created_at"`
}

type LoginSession struct {
	AccountID    string `json:"account_id"`
	Environment  string `json:"environment"`
	AuthorizeURL string `json:"authorize_url"`
	CallbackURL  string `json:"callback_url"`
}

type Credentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SecurityToken   string `json:"security_token"`
	Expiration      string `json:"expiration"`
	DomainID        string `json:"-"`
}

type TokenPayload struct {
	Credentials  Credentials `json:"credentials"`
	RefreshToken string      `json:"refresh_token"`
}

type Identity struct {
	AccountID    string `json:"account_id"`
	PrincipalID  string `json:"principal_id"`
	PrincipalURN string `json:"principal_urn"`
}

type AccountRecord struct {
	ID              string         `json:"id"`
	Environment     string         `json:"environment"`
	AccessKeyID     string         `json:"access_key_id"`
	SecretAccessKey string         `json:"secret_access_key"`
	SecurityToken   string         `json:"security_token"`
	Expiration      string         `json:"expiration"`
	RefreshToken    string         `json:"refresh_token"`
	DomainID        string         `json:"domain_id"`
	UserID          string         `json:"user_id"`
	UserName        string         `json:"user_name"`
	PrincipalURN    string         `json:"principal_urn"`
	AuthContext     map[string]any `json:"auth_context"`
	Stats           map[string]any `json:"stats"`
	Health          map[string]any `json:"health"`
	ModelHealth     map[string]any `json:"model_health"`
	UpdatedAt       string         `json:"updated_at"`
}

type PublicAccount struct {
	ID               string         `json:"id"`
	Environment      string         `json:"environment"`
	EnvironmentLabel string         `json:"environment_label"`
	Authenticated    bool           `json:"authenticated"`
	Source           string         `json:"source"`
	ExpiresAt        string         `json:"expires_at"`
	RefreshAvailable bool           `json:"refresh_available"`
	UserID           string         `json:"user_id"`
	UserName         string         `json:"user_name"`
	DomainID         string         `json:"domain_id"`
	UpdatedAt        string         `json:"updated_at"`
	Health           map[string]any `json:"health"`
	ModelHealth      map[string]any `json:"model_health"`
	Stats            map[string]any `json:"stats"`
}

type Manager struct {
	Settings config.Settings
	Client   *http.Client
}

func NewManager(settings config.Settings) *Manager {
	return &Manager{Settings: settings, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (m *Manager) BeginLoginSession(environment string) (LoginSession, error) {
	profile, err := m.Settings.Profile(environment)
	if err != nil {
		return LoginSession{}, err
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		return LoginSession{}, err
	}
	dpop, err := GenerateDPoPKeyPair()
	if err != nil {
		return LoginSession{}, err
	}
	accountID, err := randomHex(16)
	if err != nil {
		return LoginSession{}, err
	}
	ticketID, err := randomUUIDv4()
	if err != nil {
		return LoginSession{}, err
	}
	pending := PendingLogin{
		AccountID:   accountID,
		Environment: profile.Environment,
		PKCEPair:    pkce,
		DPoPKeyPair: dpop,
		TicketID:    ticketID,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := atomicWriteJSON(m.Settings.PendingAuthFile, pending); err != nil {
		return LoginSession{}, err
	}

	callback := m.Settings.CallbackURL()
	port := m.Settings.Port
	if parsed, err := url.Parse(callback); err == nil && parsed.Port() != "" {
		if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
			port = parsedPort
		}
	}
	params := url.Values{
		"theme":                 {m.Settings.AuthTheme},
		"locale":                {m.Settings.Language},
		"uri_scheme":            {m.Settings.AuthURIScheme},
		"client_id":             {m.Settings.AuthClientID},
		"port":                  {strconv.Itoa(port)},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {pkce.CodeChallengeMethod},
		"ticket_id":             {ticketID},
		"auth_callback_url":     {callback},
		"plugin-name":           {m.Settings.PluginName},
		"plugin-version":        {m.Settings.PluginVersion},
	}
	if profile.QuickCompFlag {
		params.Set("quickcompfalg", "1")
	}
	return LoginSession{
		AccountID:    accountID,
		Environment:  profile.Environment,
		AuthorizeURL: profile.AuthorizeURL() + "?" + params.Encode(),
		CallbackURL:  callback,
	}, nil
}

func (m *Manager) ExchangeCode(code string) (PublicAccount, error) {
	if strings.TrimSpace(code) == "" {
		return PublicAccount{}, fmt.Errorf("OAuth callback is missing code")
	}
	context, err := m.readPending()
	if err != nil {
		return PublicAccount{}, err
	}
	profile, err := m.Settings.Profile(context.Environment)
	if err != nil {
		return PublicAccount{}, err
	}
	payload, err := m.tokenRequest(context, profile, url.Values{
		"client_id":     {m.Settings.AuthClientID},
		"code":          {code},
		"code_verifier": {context.PKCEPair.CodeVerifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {m.Settings.CallbackURL()},
	})
	if err != nil {
		return PublicAccount{}, err
	}
	identity, err := m.queryIdentity(profile, payload.Credentials)
	if err != nil {
		return PublicAccount{}, err
	}
	record := AccountRecord{
		ID:              context.AccountID,
		Environment:     profile.Environment,
		AccessKeyID:     payload.Credentials.AccessKeyID,
		SecretAccessKey: payload.Credentials.SecretAccessKey,
		SecurityToken:   payload.Credentials.SecurityToken,
		Expiration:      payload.Credentials.Expiration,
		RefreshToken:    payload.RefreshToken,
		DomainID:        identity.AccountID,
		UserID:          identity.PrincipalID,
		PrincipalURN:    identity.PrincipalURN,
		AuthContext: map[string]any{
			"pkce_pair":     context.PKCEPair,
			"dpop_key_pair": context.DPoPKeyPair,
		},
		Stats:       map[string]any{},
		Health:      map[string]any{},
		ModelHealth: map[string]any{},
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if idx := strings.LastIndex(identity.PrincipalURN, ":"); idx >= 0 {
		record.UserName = identity.PrincipalURN[idx+1:]
	}
	if err := m.saveAccount(record); err != nil {
		return PublicAccount{}, err
	}
	_ = os.Remove(m.Settings.PendingAuthFile)
	return m.publicAccount(record), nil
}

func (m *Manager) readPending() (PendingLogin, error) {
	payload, err := os.ReadFile(m.Settings.PendingAuthFile)
	if err != nil {
		if os.IsNotExist(err) {
			return PendingLogin{}, fmt.Errorf("No pending CodeArts login. Open /auth/login first.")
		}
		return PendingLogin{}, err
	}
	var context PendingLogin
	if err := json.Unmarshal(payload, &context); err != nil {
		return PendingLogin{}, fmt.Errorf("invalid pending CodeArts login: %w", err)
	}
	return context, nil
}

func (m *Manager) tokenRequest(context PendingLogin, profile config.Profile, form url.Values) (TokenPayload, error) {
	dpop, err := SignDPoP(context.DPoPKeyPair, http.MethodPost, profile.TokenURL(), time.Time{}, "")
	if err != nil {
		return TokenPayload{}, err
	}
	req, err := http.NewRequest(http.MethodPost, profile.TokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return TokenPayload{}, err
	}
	req.Header.Set("DPoP", dpop)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.Client.Do(req)
	if err != nil {
		return TokenPayload{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return TokenPayload{}, fmt.Errorf("CodeArts OAuth token HTTP %d: %s", resp.StatusCode, truncate(string(body), 1200))
	}
	var payload TokenPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return TokenPayload{}, fmt.Errorf("decode CodeArts OAuth response: %w", err)
	}
	if payload.Credentials.AccessKeyID == "" || payload.Credentials.SecretAccessKey == "" {
		return TokenPayload{}, fmt.Errorf("CodeArts OAuth response is missing credentials")
	}
	return payload, nil
}

func (m *Manager) queryIdentity(profile config.Profile, credentials Credentials) (Identity, error) {
	signed, err := signer.SignRequest(
		http.MethodGet,
		profile.CallerIdentityURL(),
		http.Header{"Content-Type": {"application/json"}},
		nil,
		credentials.AccessKeyID,
		credentials.SecretAccessKey,
		credentials.SecurityToken,
		"",
		time.Time{},
	)
	if err != nil {
		return Identity{}, err
	}
	req, err := http.NewRequest(http.MethodGet, profile.CallerIdentityURL(), nil)
	if err != nil {
		return Identity{}, err
	}
	for key, values := range signed.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return Identity{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return Identity{}, fmt.Errorf("CodeArts caller identity HTTP %d: %s", resp.StatusCode, truncate(string(body), 1200))
	}
	var identity Identity
	if err := json.Unmarshal(body, &identity); err != nil {
		return Identity{}, fmt.Errorf("decode CodeArts caller identity: %w", err)
	}
	return identity, nil
}

func (m *Manager) saveAccount(record AccountRecord) error {
	if record.ID == "" {
		return fmt.Errorf("account id is required")
	}
	path := filepath.Join(m.Settings.AccountsDir, record.ID+".json")
	return atomicWriteJSON(path, record)
}

func (m *Manager) publicAccount(record AccountRecord) PublicAccount {
	profile, _ := m.Settings.Profile(record.Environment)
	health := cloneMap(record.Health)
	stats := cloneMap(record.Stats)
	modelHealth := cloneMap(record.ModelHealth)
	cooldownUntilTS := numberValue(health["cooldown_until_ts"])
	status := "healthy"
	if cooldownUntilTS > float64(time.Now().Unix()) {
		status = "cooldown"
		health["cooldown_until"] = time.Unix(int64(cooldownUntilTS), 0).UTC().Format(time.RFC3339)
	} else if boolValueAny(health["needs_relogin"]) || int(numberValue(health["consecutive_failures"])) > 0 {
		status = "degraded"
	}
	health["status"] = status
	requestCount := int(numberValue(stats["request_count"]))
	totalLatency := numberValue(stats["total_latency_ms"])
	if requestCount > 0 {
		stats["avg_latency_ms"] = math.Round(totalLatency/float64(requestCount)*10) / 10
	}
	completion := numberValue(stats["completion_tokens"])
	successLatency := numberValue(stats["success_latency_ms"])
	if successLatency > 0 {
		stats["avg_output_tps"] = math.Round(completion/(successLatency/1000)*100) / 100
	}
	stats["max_concurrency"] = m.Settings.AccountMaxConcurrency
	return PublicAccount{
		ID:               record.ID,
		Environment:      record.Environment,
		EnvironmentLabel: profile.Label,
		Authenticated:    record.AccessKeyID != "" && record.SecretAccessKey != "",
		Source:           "oauth",
		ExpiresAt:        record.Expiration,
		RefreshAvailable: record.RefreshToken != "" && len(record.AuthContext) > 0,
		UserID:           record.UserID,
		UserName:         record.UserName,
		DomainID:         record.DomainID,
		UpdatedAt:        record.UpdatedAt,
		Health:           health,
		ModelHealth:      modelHealth,
		Stats:            stats,
	}
}

func cloneMap(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		result[key] = item
	}
	return result
}

func numberValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return 0
}

func boolValueAny(value any) bool { v, _ := value.(bool); return v }

func (m *Manager) DeleteAccount(accountID string) (bool, error) {
	if accountID == "" || strings.ContainsAny(accountID, `\\/:*?\"<>|`) {
		return false, fmt.Errorf("invalid account id")
	}
	path := filepath.Join(m.Settings.AccountsDir, accountID+".json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) LoadAccount(accountID string) (AccountRecord, error) {
	if accountID == "" || strings.ContainsAny(accountID, `\\/:*?\"<>|`) {
		return AccountRecord{}, fmt.Errorf("invalid account id")
	}
	payload, err := os.ReadFile(filepath.Join(m.Settings.AccountsDir, accountID+".json"))
	if err != nil {
		return AccountRecord{}, err
	}
	var record AccountRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return AccountRecord{}, err
	}
	if record.ID == "" {
		record.ID = accountID
	}
	return record, nil
}

func (m *Manager) ListAccounts() ([]PublicAccount, error) {
	entries, err := os.ReadDir(m.Settings.AccountsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []PublicAccount{}, nil
		}
		return nil, err
	}
	accounts := make([]PublicAccount, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		accountID := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		record, err := m.LoadAccount(accountID)
		if err != nil {
			continue
		}
		accounts = append(accounts, m.publicAccount(record))
	}
	return accounts, nil
}

func (m *Manager) RefreshAccount(accountID string) (PublicAccount, error) {
	record, err := m.LoadAccount(accountID)
	if err != nil {
		return PublicAccount{}, err
	}
	if record.RefreshToken == "" || len(record.AuthContext) == 0 {
		return PublicAccount{}, fmt.Errorf("Persisted credentials cannot be refreshed; login again")
	}
	contextBytes, err := json.Marshal(record.AuthContext)
	if err != nil {
		return PublicAccount{}, err
	}
	var context struct {
		PKCEPair    PKCEPair    `json:"pkce_pair"`
		DPoPKeyPair DPoPKeyPair `json:"dpop_key_pair"`
	}
	if err := json.Unmarshal(contextBytes, &context); err != nil {
		return PublicAccount{}, err
	}
	pending := PendingLogin{AccountID: record.ID, Environment: record.Environment, PKCEPair: context.PKCEPair, DPoPKeyPair: context.DPoPKeyPair}
	profile, err := m.Settings.Profile(record.Environment)
	if err != nil {
		return PublicAccount{}, err
	}
	payload, err := m.tokenRequest(pending, profile, url.Values{
		"client_id":     {m.Settings.AuthClientID},
		"code_verifier": {context.PKCEPair.CodeVerifier},
		"grant_type":    {"refresh_token"},
		"refresh_token": {record.RefreshToken},
	})
	if err != nil {
		return PublicAccount{}, err
	}
	record.AccessKeyID = payload.Credentials.AccessKeyID
	record.SecretAccessKey = payload.Credentials.SecretAccessKey
	record.SecurityToken = payload.Credentials.SecurityToken
	record.Expiration = payload.Credentials.Expiration
	if payload.RefreshToken != "" {
		record.RefreshToken = payload.RefreshToken
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.saveAccount(record); err != nil {
		return PublicAccount{}, err
	}
	return m.publicAccount(record), nil
}

func (m *Manager) Credentials(accountID string) (Credentials, error) {
	record, err := m.LoadAccount(accountID)
	if err != nil {
		return Credentials{}, err
	}
	if needsRefresh(record.Expiration, m.Settings.RefreshSkewSeconds) && record.RefreshToken != "" {
		if _, err := m.RefreshAccount(accountID); err != nil {
			return Credentials{}, err
		}
		record, err = m.LoadAccount(accountID)
		if err != nil {
			return Credentials{}, err
		}
	}
	if record.AccessKeyID == "" || record.SecretAccessKey == "" {
		return Credentials{}, fmt.Errorf("credentials unavailable")
	}
	return Credentials{AccessKeyID: record.AccessKeyID, SecretAccessKey: record.SecretAccessKey, SecurityToken: record.SecurityToken, Expiration: record.Expiration, DomainID: record.DomainID}, nil
}

func needsRefresh(expiration string, skewSeconds int) bool {
	if strings.TrimSpace(expiration) == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiration)
	if err != nil {
		return false
	}
	return parsed.Before(time.Now().UTC().Add(time.Duration(skewSeconds) * time.Second))
}

func atomicWriteJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
		return err
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	suffix, err := randomHex(8)
	if err != nil {
		return err
	}
	temp := filepath.Join(dir, "."+filepath.Base(path)+"."+suffix+".tmp")
	if err := os.WriteFile(temp, payload, 0o600); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("replace json file: %w", err)
	}
	return nil
}

func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomUUIDv4() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}
