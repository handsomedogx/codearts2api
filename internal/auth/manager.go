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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/securestore"
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
	Email           string         `json:"email,omitempty"`
	PrincipalURN    string         `json:"principal_urn"`
	AuthContext     map[string]any `json:"auth_context"`
	Stats           map[string]any `json:"stats"`
	Health          map[string]any `json:"health"`
	ModelHealth     map[string]any `json:"model_health"`
	UpdatedAt       string         `json:"updated_at"`
}

type accountSecrets struct {
	AccessKeyID     string         `json:"access_key_id"`
	SecretAccessKey string         `json:"secret_access_key"`
	SecurityToken   string         `json:"security_token"`
	RefreshToken    string         `json:"refresh_token"`
	Email           string         `json:"email,omitempty"`
	AuthContext     map[string]any `json:"auth_context"`
}

type storedAccount struct {
	Version      int            `json:"version"`
	Protection   string         `json:"protection,omitempty"`
	Protected    string         `json:"protected,omitempty"`
	ID           string         `json:"id"`
	Environment  string         `json:"environment"`
	Expiration   string         `json:"expiration"`
	DomainID     string         `json:"domain_id"`
	UserID       string         `json:"user_id"`
	UserName     string         `json:"user_name"`
	PrincipalURN string         `json:"principal_urn"`
	Stats        map[string]any `json:"stats"`
	Health       map[string]any `json:"health"`
	ModelHealth  map[string]any `json:"model_health"`
	UpdatedAt    string         `json:"updated_at"`
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
	Email            string         `json:"email,omitempty"`
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
	if err := m.writePending(pending); err != nil {
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
	if email, err := m.queryUserEmail(payload.Credentials, identity); err == nil {
		record.Email = email
	}
	if existing, found, err := m.findAccountByIdentity(record.Environment, record.DomainID, record.UserID); err != nil {
		return PublicAccount{}, err
	} else if found {
		record.ID = existing.ID
		if record.Email == "" {
			record.Email = existing.Email
		}
		record.Stats = existing.Stats
		record.Health = existing.Health
		record.ModelHealth = existing.ModelHealth
	}
	if err := m.saveAccount(record); err != nil {
		return PublicAccount{}, err
	}
	_ = os.Remove(m.Settings.PendingAuthFile)
	return m.publicAccount(record), nil
}

func (m *Manager) readPending() (PendingLogin, error) {
	paths := []string{m.Settings.PendingAuthFile}
	if m.Settings.MigrateLegacy {
		paths = append(paths, filepath.Join(m.Settings.LegacyDataDir, "oauth_pending.json"))
	}
	var lastErr error
	for _, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			lastErr = err
			continue
		}
		var context PendingLogin
		protected, err := securestore.UnmarshalProtectedJSON(payload, &context)
		if err != nil {
			return PendingLogin{}, fmt.Errorf("invalid pending CodeArts login: %w", err)
		}
		if securestore.Available() && (!protected || filepath.Clean(path) != filepath.Clean(m.Settings.PendingAuthFile)) {
			if err := m.writePending(context); err != nil {
				return PendingLogin{}, err
			}
			if filepath.Clean(path) != filepath.Clean(m.Settings.PendingAuthFile) {
				_ = os.Remove(path)
			}
		}
		return context, nil
	}
	if lastErr != nil {
		return PendingLogin{}, lastErr
	}
	return PendingLogin{}, fmt.Errorf("No pending CodeArts login. Open /auth/login first.")
}

func (m *Manager) writePending(context PendingLogin) error {
	payload, err := securestore.MarshalProtectedJSON(context)
	if err != nil {
		return err
	}
	return atomicWriteBytes(m.Settings.PendingAuthFile, payload)
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

func (m *Manager) queryUserEmail(credentials Credentials, identity Identity) (string, error) {
	if strings.TrimSpace(identity.PrincipalID) == "" {
		return "", fmt.Errorf("IAM user id is empty")
	}
	base := strings.TrimRight(strings.TrimSpace(m.Settings.IAMBaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("IAM base URL is empty")
	}
	rawURL := base + "/v3.0/OS-USER/users/" + url.PathEscape(identity.PrincipalID)
	signed, err := signer.SignRequest(
		http.MethodGet,
		rawURL,
		http.Header{"Content-Type": {"application/json"}},
		nil,
		credentials.AccessKeyID,
		credentials.SecretAccessKey,
		credentials.SecurityToken,
		identity.AccountID,
		time.Time{},
	)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	for key, values := range signed.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := m.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Huawei IAM user detail HTTP %d", resp.StatusCode)
	}
	var payload struct {
		User struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode Huawei IAM user detail: %w", err)
	}
	return strings.TrimSpace(payload.User.Email), nil
}

func (m *Manager) saveAccount(record AccountRecord) error {
	if record.ID == "" {
		return fmt.Errorf("account id is required")
	}
	path := filepath.Join(m.Settings.AccountsDir, record.ID+".json")
	if !securestore.Available() {
		return atomicWriteJSON(path, record)
	}
	secretPayload, err := json.Marshal(accountSecrets{
		AccessKeyID: record.AccessKeyID, SecretAccessKey: record.SecretAccessKey,
		SecurityToken: record.SecurityToken, RefreshToken: record.RefreshToken,
		Email: record.Email, AuthContext: record.AuthContext,
	})
	if err != nil {
		return err
	}
	protected, err := securestore.ProtectString(string(secretPayload))
	if err != nil {
		return err
	}
	stored := storedAccount{
		Version: 1, Protection: securestore.ProtectionName(), Protected: protected,
		ID: record.ID, Environment: record.Environment, Expiration: record.Expiration,
		DomainID: record.DomainID, UserID: record.UserID, UserName: record.UserName,
		PrincipalURN: record.PrincipalURN, Stats: record.Stats, Health: record.Health,
		ModelHealth: record.ModelHealth, UpdatedAt: record.UpdatedAt,
	}
	return atomicWriteJSON(path, stored)
}

func (m *Manager) publicAccount(record AccountRecord) PublicAccount {
	profile, _ := m.Settings.Profile(record.Environment)
	rawHealth := cloneMap(record.Health)
	rawStats := cloneMap(record.Stats)
	cooldownUntilTS := numberValue(rawHealth["cooldown_until_ts"])
	status := "healthy"
	cooldownUntil := ""
	if cooldownUntilTS > float64(time.Now().Unix()) {
		status = "cooldown"
		cooldownUntil = time.Unix(int64(cooldownUntilTS), 0).UTC().Format(time.RFC3339)
	} else if boolValueAny(rawHealth["needs_relogin"]) || int(numberValue(rawHealth["consecutive_failures"])) > 0 {
		status = "degraded"
	}
	health := map[string]any{
		"status":                       status,
		"consecutive_failures":         int(numberValue(rawHealth["consecutive_failures"])),
		"total_failures":               int(numberValue(rawHealth["total_failures"])),
		"total_successes":              int(numberValue(rawHealth["total_successes"])),
		"last_failure_at":              stringValueAny(rawHealth["last_failure_at"]),
		"last_success_at":              stringValueAny(rawHealth["last_success_at"]),
		"last_error_category":          stringValueAny(rawHealth["last_error_category"]),
		"last_status_code":             rawHealth["last_status_code"],
		"cooldown_until":               cooldownUntil,
		"last_probe_at":                stringValueAny(rawHealth["last_probe_at"]),
		"probe_status":                 stringValueAny(rawHealth["probe_status"]),
		"probe_error_category":         stringValueAny(rawHealth["probe_error_category"]),
		"needs_relogin":                boolValueAny(rawHealth["needs_relogin"]),
		"refresh_probe_status":         stringValueAny(rawHealth["refresh_probe_status"]),
		"refresh_probe_error_category": stringValueAny(rawHealth["refresh_probe_error_category"]),
	}
	requestCount := int(numberValue(rawStats["request_count"]))
	totalLatency := numberValue(rawStats["total_latency_ms"])
	promptTokens := int(numberValue(rawStats["prompt_tokens"]))
	completionTokens := int(numberValue(rawStats["completion_tokens"]))
	totalTokens := int(numberValue(rawStats["total_tokens"]))
	if totalTokens == 0 {
		totalTokens = promptTokens + completionTokens
	}
	successCount := int(numberValue(rawStats["success_count"]))
	failureCount := int(numberValue(rawStats["failure_count"]))
	var avgLatency any
	if requestCount > 0 {
		avgLatency = math.Round(totalLatency/float64(requestCount)*10) / 10
	}
	var avgOutputTPS any
	successLatency := numberValue(rawStats["success_latency_ms"])
	if successLatency > 0 {
		avgOutputTPS = math.Round(float64(completionTokens)/(successLatency/1000)*100) / 100
	}
	var lastLatency any
	if requestCount > 0 {
		lastLatency = math.Round(numberValue(rawStats["last_latency_ms"])*10) / 10
	}
	stats := map[string]any{
		"in_flight":             0,
		"concurrency":           0,
		"max_concurrency":       m.Settings.AccountMaxConcurrency,
		"available_concurrency": nil,
		"request_count":         requestCount,
		"success_count":         successCount,
		"failure_count":         failureCount,
		"prompt_tokens":         promptTokens,
		"completion_tokens":     completionTokens,
		"total_tokens":          totalTokens,
		"avg_output_tps":        avgOutputTPS,
		"avg_latency_ms":        avgLatency,
		"last_latency_ms":       lastLatency,
		"last_request_at":       stringValueAny(rawStats["last_request_at"]),
	}
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
		Email:            record.Email,
		DomainID:         record.DomainID,
		UpdatedAt:        record.UpdatedAt,
		Health:           health,
		ModelHealth:      normalizeModelHealth(record.ModelHealth),
		Stats:            stats,
	}
}

func (m *Manager) findAccountByIdentity(environment, domainID, userID string) (AccountRecord, bool, error) {
	environment = strings.TrimSpace(environment)
	domainID = strings.TrimSpace(domainID)
	userID = strings.TrimSpace(userID)
	if environment == "" || domainID == "" || userID == "" {
		return AccountRecord{}, false, nil
	}
	accounts, err := m.ListAccounts()
	if err != nil {
		return AccountRecord{}, false, err
	}
	for _, account := range accounts {
		if account.Environment != environment || account.DomainID != domainID || account.UserID != userID {
			continue
		}
		record, err := m.LoadAccount(account.ID)
		if err != nil {
			return AccountRecord{}, false, err
		}
		return record, true, nil
	}
	return AccountRecord{}, false, nil
}

func (m *Manager) BackfillAccountEmails() (int, error) {
	accounts, err := m.ListAccounts()
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, account := range accounts {
		if strings.TrimSpace(account.Email) != "" || strings.TrimSpace(account.UserID) == "" || strings.TrimSpace(account.DomainID) == "" {
			continue
		}
		credentials, err := m.Credentials(account.ID)
		if err != nil {
			continue
		}
		email, err := m.queryUserEmail(credentials, Identity{AccountID: account.DomainID, PrincipalID: account.UserID})
		if err != nil || strings.TrimSpace(email) == "" {
			continue
		}
		record, err := m.LoadAccount(account.ID)
		if err != nil {
			continue
		}
		record.Email = strings.TrimSpace(email)
		if err := m.saveAccount(record); err != nil {
			continue
		}
		updated++
	}
	return updated, nil
}

func normalizeModelHealth(value map[string]any) map[string]any {
	result := map[string]any{}
	now := float64(time.Now().Unix())
	for model, raw := range value {
		item, ok := raw.(map[string]any)
		if !ok || item == nil {
			continue
		}
		untilTS := numberValue(item["cooldown_until_ts"])
		status := "healthy"
		cooldownUntil := ""
		if untilTS > now {
			status = "cooldown"
			cooldownUntil = time.Unix(int64(untilTS), 0).UTC().Format(time.RFC3339)
		} else if int(numberValue(item["consecutive_failures"])) > 0 {
			status = "degraded"
		}
		result[model] = map[string]any{
			"status":               status,
			"consecutive_failures": int(numberValue(item["consecutive_failures"])),
			"total_failures":       int(numberValue(item["total_failures"])),
			"total_successes":      int(numberValue(item["total_successes"])),
			"last_error_category":  stringValueAny(item["last_error_category"]),
			"last_status_code":     item["last_status_code"],
			"cooldown_until":       cooldownUntil,
		}
	}
	return result
}

func stringValueAny(value any) string {
	result, _ := value.(string)
	return result
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

func (m *Manager) MigrateSensitiveStorage() error {
	if !securestore.Available() {
		return nil
	}
	ids := map[string]bool{}
	for _, dir := range []string{m.Settings.AccountsDir, filepath.Join(m.Settings.LegacyDataDir, "accounts")} {
		if !m.Settings.MigrateLegacy && filepath.Clean(dir) != filepath.Clean(m.Settings.AccountsDir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
				ids[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))] = true
			}
		}
	}
	for id := range ids {
		if _, err := m.LoadAccount(id); err != nil {
			return fmt.Errorf("migrate account %s: %w", id, err)
		}
		if m.Settings.MigrateLegacy {
			primary := filepath.Join(m.Settings.AccountsDir, id+".json")
			legacy := filepath.Join(m.Settings.LegacyDataDir, "accounts", id+".json")
			if _, err := os.Stat(primary); err == nil && filepath.Clean(primary) != filepath.Clean(legacy) {
				_ = os.Remove(legacy)
			}
		}
	}
	legacyPending := filepath.Join(m.Settings.LegacyDataDir, "oauth_pending.json")
	for _, path := range []string{m.Settings.PendingAuthFile, legacyPending} {
		if !m.Settings.MigrateLegacy && filepath.Clean(path) != filepath.Clean(m.Settings.PendingAuthFile) {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			if _, err := m.readPending(); err != nil {
				return err
			}
			if m.Settings.MigrateLegacy && filepath.Clean(legacyPending) != filepath.Clean(m.Settings.PendingAuthFile) {
				if _, err := os.Stat(m.Settings.PendingAuthFile); err == nil {
					_ = os.Remove(legacyPending)
				}
			}
			break
		}
	}
	return nil
}

func (m *Manager) DeleteAccount(accountID string) (bool, error) {
	if accountID == "" || strings.ContainsAny(accountID, `\\/:*?\"<>|`) {
		return false, fmt.Errorf("invalid account id")
	}
	deleted := false
	for _, path := range m.accountPaths(accountID) {
		if err := os.Remove(path); err == nil {
			deleted = true
		} else if !os.IsNotExist(err) {
			return deleted, err
		}
	}
	return deleted, nil
}

func (m *Manager) LoadAccount(accountID string) (AccountRecord, error) {
	if accountID == "" || strings.ContainsAny(accountID, `\\/:*?\"<>|`) {
		return AccountRecord{}, fmt.Errorf("invalid account id")
	}
	var lastErr error
	for _, path := range m.accountPaths(accountID) {
		payload, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			lastErr = err
			continue
		}
		record, protected, err := decodeAccount(payload)
		if err != nil {
			return AccountRecord{}, err
		}
		if record.ID == "" {
			record.ID = accountID
		}
		isPrimary := filepath.Clean(path) == filepath.Clean(filepath.Join(m.Settings.AccountsDir, accountID+".json"))
		if securestore.Available() && (!protected || !isPrimary) {
			if err := m.saveAccount(record); err != nil {
				return AccountRecord{}, err
			}
			if !isPrimary {
				_ = os.Remove(path)
			}
		}
		return record, nil
	}
	if lastErr != nil {
		return AccountRecord{}, lastErr
	}
	return AccountRecord{}, os.ErrNotExist
}

func (m *Manager) accountPaths(accountID string) []string {
	paths := []string{filepath.Join(m.Settings.AccountsDir, accountID+".json")}
	if m.Settings.MigrateLegacy {
		legacy := filepath.Join(m.Settings.LegacyDataDir, "accounts", accountID+".json")
		if filepath.Clean(legacy) != filepath.Clean(paths[0]) {
			paths = append(paths, legacy)
		}
	}
	return paths
}

func decodeAccount(payload []byte) (AccountRecord, bool, error) {
	var stored storedAccount
	if err := json.Unmarshal(payload, &stored); err == nil && stored.Protected != "" {
		plain, err := securestore.UnprotectString(stored.Protected)
		if err != nil {
			return AccountRecord{}, true, err
		}
		var secrets accountSecrets
		if err := json.Unmarshal([]byte(plain), &secrets); err != nil {
			return AccountRecord{}, true, err
		}
		return AccountRecord{
			ID: stored.ID, Environment: stored.Environment,
			AccessKeyID: secrets.AccessKeyID, SecretAccessKey: secrets.SecretAccessKey,
			SecurityToken: secrets.SecurityToken, Expiration: stored.Expiration,
			RefreshToken: secrets.RefreshToken, DomainID: stored.DomainID,
			UserID: stored.UserID, UserName: stored.UserName, Email: secrets.Email, PrincipalURN: stored.PrincipalURN,
			AuthContext: secrets.AuthContext, Stats: stored.Stats, Health: stored.Health,
			ModelHealth: stored.ModelHealth, UpdatedAt: stored.UpdatedAt,
		}, true, nil
	}
	var record AccountRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		return AccountRecord{}, false, err
	}
	return record, false, nil
}

func (m *Manager) ListAccounts() ([]PublicAccount, error) {
	ids := map[string]bool{}
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
			ids[strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))] = true
		}
	}
	accounts := make([]PublicAccount, 0, len(ids))
	for accountID := range ids {
		record, err := m.LoadAccount(accountID)
		if err != nil {
			continue
		}
		profile, err := m.Settings.Profile(record.Environment)
		if err != nil {
			continue
		}
		record.Environment = profile.Environment
		accounts = append(accounts, m.publicAccount(record))
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].UpdatedAt == accounts[j].UpdatedAt {
			return accounts[i].ID < accounts[j].ID
		}
		return accounts[i].UpdatedAt > accounts[j].UpdatedAt
	})
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
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteBytes(path, payload)
}

func atomicWriteBytes(path string, payload []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
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
