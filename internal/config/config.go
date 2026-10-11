package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	EnvCN            = "cn"
	EnvInternational = "international"
)

var dotenvOnce sync.Once

type Profile struct {
	Environment   string
	Label         string
	BaseURL       string
	PortalBaseURL string
	STSBaseURL    string
	QuickCompFlag bool
}

func (p Profile) ChatURL() string           { return p.BaseURL + "/api/v2/chat/completions" }
func (p Profile) TokenURL() string          { return p.STSBaseURL + "/v1/oauth2/tokens" }
func (p Profile) CallerIdentityURL() string { return p.STSBaseURL + "/v5/caller-identity" }
func (p Profile) AuthorizeURL() string      { return p.PortalBaseURL + "/portal/authorize" }

type Settings struct {
	DataDir           string
	LegacyDataDir     string
	MigrateLegacy     bool
	CNBaseURL         string
	CNPortalBaseURL   string
	CNSTSBaseURL      string
	IntlBaseURL       string
	IntlPortalBaseURL string
	IntlSTSBaseURL    string

	DefaultEnvironment             string
	AuthClientID                   string
	AuthURIScheme                  string
	AuthTheme                      string
	PluginName                     string
	PluginVersion                  string
	ClientVersion                  string
	IDEName                        string
	IDEVersion                     string
	Language                       string
	DefaultModel                   string
	Models                         []string
	ModelCacheTTLSeconds           int
	SubscriptionCacheTTLSeconds    int
	UsageFile                      string
	RequestLogsFile                string
	GatewaySettingsFile            string
	PendingAuthFile                string
	AccountsDir                    string
	ConversationsDir               string
	RoutingFile                    string
	RoutingStrategy                string
	AccountMaxConcurrency          int
	RoutingMaxAttempts             int
	UpstreamRetriesPerAccount      int
	RetryBudgetAttempts            int
	RetryBudgetSeconds             float64
	RetryBackoffBaseMS             float64
	RetryBackoffCapMS              float64
	RetryBackoffJitter             float64
	HealthCooldownBaseSeconds      int
	HealthCooldownMaxSeconds       int
	HealthAuthCooldownSeconds      int
	HealthRateLimitCooldownSeconds int
	ModelHealthCooldownBaseSeconds int
	ModelHealthCooldownMaxSeconds  int
	KeepaliveEnabled               bool
	KeepaliveIntervalSeconds       int
	KeepalivePollSeconds           int
	KeepaliveAccountGapSeconds     float64
	RefreshSkewSeconds             int
	Host                           string
	Port                           int
}

func Load() Settings {
	dotenvOnce.Do(func() { loadDotEnv(".env") })
	dataDir := env("CODEARTS2API_DATA_DIR", defaultDataDir())
	legacyDataDir := filepath.Clean("data")
	migrateLegacy := storageOverridesUnset() && filepath.Clean(dataDir) != legacyDataDir
	return Settings{
		DataDir:                        dataDir,
		LegacyDataDir:                  legacyDataDir,
		MigrateLegacy:                  migrateLegacy,
		CNBaseURL:                      env("CODEARTS_CN_BASE_URL", "https://snap-access.cn-north-4.myhuaweicloud.com"),
		CNPortalBaseURL:                env("CODEARTS_CN_PORTAL_BASE_URL", "https://codearts.huaweicloud.com"),
		CNSTSBaseURL:                   env("CODEARTS_CN_STS_BASE_URL", "https://sts.cn-north-4.myhuaweicloud.com"),
		IntlBaseURL:                    env("CODEARTS_INTL_BASE_URL", "https://snap-access.ap-southeast-1.myhuaweicloud.com"),
		IntlPortalBaseURL:              env("CODEARTS_INTL_PORTAL_BASE_URL", "https://codearts.ap-southeast-1.huaweicloud.com"),
		IntlSTSBaseURL:                 env("CODEARTS_INTL_STS_BASE_URL", "https://sts.cn-north-4.myhuaweicloud.com"),
		DefaultEnvironment:             env("CODEARTS_DEFAULT_ENVIRONMENT", EnvCN),
		AuthClientID:                   env("CODEARTS_AUTH_CLIENT_ID", "codearts-snap"),
		AuthURIScheme:                  env("CODEARTS_AUTH_URI_SCHEME", "codearts-snap"),
		AuthTheme:                      env("CODEARTS_AUTH_THEME", "2"),
		PluginName:                     env("CODEARTS_PLUGIN_NAME", "codearts-snap_vscode"),
		PluginVersion:                  env("CODEARTS_PLUGIN_VERSION", "26.7.2"),
		ClientVersion:                  env("CODEARTS_CLIENT_VERSION", "26.7.2"),
		IDEName:                        env("CODEARTS_IDE_NAME", "CodeArts Agent"),
		IDEVersion:                     env("CODEARTS_IDE_VERSION", "26.7.1"),
		Language:                       env("CODEARTS_LANGUAGE", "zh-cn"),
		DefaultModel:                   env("CODEARTS_DEFAULT_MODEL", "GLM-5.2"),
		Models:                         envCSV("CODEARTS_MODELS", "GLM-5.2"),
		ModelCacheTTLSeconds:           envInt("CODEARTS_MODEL_CACHE_TTL_SECONDS", 300),
		SubscriptionCacheTTLSeconds:    envInt("CODEARTS_SUBSCRIPTION_CACHE_TTL_SECONDS", 60),
		UsageFile:                      env("CODEARTS_USAGE_FILE", filepath.Join(dataDir, "usage.jsonl")),
		RequestLogsFile:                env("CODEARTS_REQUEST_LOGS_FILE", filepath.Join(dataDir, "request_logs.jsonl")),
		GatewaySettingsFile:            env("CODEARTS2API_SETTINGS_FILE", filepath.Join(dataDir, "settings.json")),
		PendingAuthFile:                env("CODEARTS_PENDING_AUTH_FILE", filepath.Join(dataDir, "oauth_pending.json")),
		AccountsDir:                    env("CODEARTS_ACCOUNTS_DIR", filepath.Join(dataDir, "accounts")),
		ConversationsDir:               env("CODEARTS_CONVERSATIONS_DIR", filepath.Join(dataDir, "conversations")),
		RoutingFile:                    env("CODEARTS_ROUTING_FILE", filepath.Join(dataDir, "routing.json")),
		RoutingStrategy:                env("CODEARTS_ROUTING_STRATEGY", "round_robin"),
		AccountMaxConcurrency:          envInt("CODEARTS_ACCOUNT_MAX_CONCURRENCY", 4),
		RoutingMaxAttempts:             envInt("CODEARTS_ROUTING_MAX_ATTEMPTS", 3),
		UpstreamRetriesPerAccount:      envInt("CODEARTS_UPSTREAM_RETRIES_PER_ACCOUNT", 1),
		RetryBudgetAttempts:            envInt("CODEARTS_RETRY_BUDGET_ATTEMPTS", 4),
		RetryBudgetSeconds:             envFloat("CODEARTS_RETRY_BUDGET_SECONDS", 15),
		RetryBackoffBaseMS:             envFloat("CODEARTS_RETRY_BACKOFF_BASE_MS", 500),
		RetryBackoffCapMS:              envFloat("CODEARTS_RETRY_BACKOFF_CAP_MS", 8000),
		RetryBackoffJitter:             envFloat("CODEARTS_RETRY_BACKOFF_JITTER", 0.25),
		HealthCooldownBaseSeconds:      envInt("CODEARTS_HEALTH_COOLDOWN_BASE_SECONDS", 15),
		HealthCooldownMaxSeconds:       envInt("CODEARTS_HEALTH_COOLDOWN_MAX_SECONDS", 300),
		HealthAuthCooldownSeconds:      envInt("CODEARTS_HEALTH_AUTH_COOLDOWN_SECONDS", 120),
		HealthRateLimitCooldownSeconds: envInt("CODEARTS_HEALTH_RATE_LIMIT_COOLDOWN_SECONDS", 60),
		ModelHealthCooldownBaseSeconds: envInt("CODEARTS_MODEL_HEALTH_COOLDOWN_BASE_SECONDS", 15),
		ModelHealthCooldownMaxSeconds:  envInt("CODEARTS_MODEL_HEALTH_COOLDOWN_MAX_SECONDS", 300),
		KeepaliveEnabled:               envBool("CODEARTS_KEEPALIVE_ENABLED", true),
		KeepaliveIntervalSeconds:       envInt("CODEARTS_KEEPALIVE_INTERVAL_SECONDS", 86400),
		KeepalivePollSeconds:           envInt("CODEARTS_KEEPALIVE_POLL_SECONDS", 300),
		KeepaliveAccountGapSeconds:     envFloat("CODEARTS_KEEPALIVE_ACCOUNT_GAP_SECONDS", 0.8),
		RefreshSkewSeconds:             envInt("CODEARTS_REFRESH_SKEW_SECONDS", 120),
		Host:                           env("CODEARTS2API_HOST", "127.0.0.1"),
		Port:                           envInt("CODEARTS2API_PORT", 8000),
	}
}

func NormalizeEnvironment(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "cn", "china", "domestic", "zh-cn":
		return EnvCN, nil
	case "international", "intl", "global", "hk", "ap-southeast-1":
		return EnvInternational, nil
	default:
		return "", fmt.Errorf("unsupported CodeArts environment: %s", value)
	}
}

func (s Settings) Profile(environment string) (Profile, error) {
	selected := environment
	if strings.TrimSpace(selected) == "" {
		selected = s.DefaultEnvironment
	}
	normalized, err := NormalizeEnvironment(selected)
	if err != nil {
		return Profile{}, err
	}
	if normalized == EnvInternational {
		return Profile{Environment: normalized, Label: "International", BaseURL: strings.TrimRight(s.IntlBaseURL, "/"), PortalBaseURL: strings.TrimRight(s.IntlPortalBaseURL, "/"), STSBaseURL: strings.TrimRight(s.IntlSTSBaseURL, "/"), QuickCompFlag: true}, nil
	}
	return Profile{Environment: EnvCN, Label: "China", BaseURL: strings.TrimRight(s.CNBaseURL, "/"), PortalBaseURL: strings.TrimRight(s.CNPortalBaseURL, "/"), STSBaseURL: strings.TrimRight(s.CNSTSBaseURL, "/")}, nil
}

func (s Settings) CallbackURL() string {
	if v := strings.TrimSpace(os.Getenv("CODEARTS_AUTH_CALLBACK_URL")); v != "" {
		return v
	}
	return fmt.Sprintf("http://127.0.0.1:%d/oauth/callback", s.Port)
}

func defaultDataDir() string {
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		return filepath.Join(local, "codearts2api")
	}
	if configDir, err := os.UserConfigDir(); err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, "codearts2api")
	}
	return filepath.Clean("data")
}

func storageOverridesUnset() bool {
	for _, name := range []string{
		"CODEARTS2API_DATA_DIR",
		"CODEARTS_USAGE_FILE",
		"CODEARTS_REQUEST_LOGS_FILE",
		"CODEARTS2API_SETTINGS_FILE",
		"CODEARTS_PENDING_AUTH_FILE",
		"CODEARTS_ACCOUNTS_DIR",
		"CODEARTS_CONVERSATIONS_DIR",
		"CODEARTS_ROUTING_FILE",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return false
		}
	}
	return true
}

func loadDotEnv(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			if value[0] == '"' {
				if decoded, err := strconv.Unquote(value); err == nil {
					value = decoded
				}
			} else {
				value = value[1 : len(value)-1]
			}
		} else if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		_ = os.Setenv(key, value)
	}
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(name string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(name string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envCSV(name, fallback string) []string {
	value := env(name, fallback)
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items
}
