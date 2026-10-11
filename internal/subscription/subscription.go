package subscription

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
)

type cacheEntry struct {
	Expires time.Time
	Value   map[string]any
}

type Catalog struct {
	Settings config.Settings
	Auth     *auth.Manager
	Client   *codearts.Client
	mu       sync.Mutex
	cache    map[string]cacheEntry
}

func New(settings config.Settings, manager *auth.Manager, client *codearts.Client) *Catalog {
	return &Catalog{Settings: settings, Auth: manager, Client: client, cache: map[string]cacheEntry{}}
}

func (c *Catalog) Aggregate(ctx context.Context, force bool) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	accounts, err := c.Auth.ListAccounts()
	if err != nil {
		return unavailable()
	}
	items := make([]map[string]any, 0)
	for _, account := range accounts {
		value := c.fetch(ctx, account.ID, account.Environment, force)
		if available, _ := value["available"].(bool); available {
			items = append(items, value)
		}
	}
	used, quota := 0, 0
	packageSet := map[string]bool{}
	for _, item := range items {
		used += number(item["used_tokens"])
		quota += number(item["quota_tokens"])
		if name, _ := item["package_name"].(string); name != "" {
			packageSet[name] = true
		}
	}
	packageNames := make([]string, 0, len(packageSet))
	for name := range packageSet {
		packageNames = append(packageNames, name)
	}
	sort.Strings(packageNames)
	var usagePercent, progressPercent any
	if quota > 0 {
		ratio := float64(used) / float64(quota)
		usagePercent = round2(ratio * 100)
		progressPercent = round2(math.Min(1, ratio) * 100)
	}
	return map[string]any{
		"available":        len(items) > 0,
		"account_count":    len(items),
		"used_tokens":      used,
		"quota_tokens":     quota,
		"usage_percent":    usagePercent,
		"progress_percent": progressPercent,
		"over_limit":       quota > 0 && used > quota,
		"overage_tokens":   maxInt(0, used-quota),
		"package_names":    packageNames,
		"items":            items,
	}
}

func (c *Catalog) fetch(ctx context.Context, accountID, environment string, force bool) map[string]any {
	if !force {
		if cached, ok := c.cache[accountID]; ok && cached.Expires.After(time.Now()) {
			return clone(cached.Value)
		}
	}
	profile, err := c.Settings.Profile(environment)
	if err != nil {
		return map[string]any{"available": false, "account_id": accountID, "environment": environment}
	}
	payload, err := c.Client.GetJSON(ctx, accountID, profile.BaseURL+"/snap-manager/v1/statistics/plugin", "PromptCenter", nil)
	value := map[string]any{"available": false, "account_id": accountID, "environment": environment}
	if err == nil {
		value = parse(payload, accountID, environment)
	}
	ttl := c.Settings.SubscriptionCacheTTLSeconds
	if ttl < 5 {
		ttl = 5
	}
	c.cache[accountID] = cacheEntry{Expires: time.Now().Add(time.Duration(ttl) * time.Second), Value: clone(value)}
	return value
}

func parse(payload map[string]any, accountID, environment string) map[string]any {
	packageInfo, _ := payload["package"].(map[string]any)
	metrics, _ := payload["metrics"].([]any)
	usageMetric := map[string]any{}
	for _, raw := range metrics {
		item, _ := raw.(map[string]any)
		if item != nil && item["name"] == "usageTokenChatMessages" {
			usageMetric = item
			break
		}
	}
	used := number(usageMetric["usage_token_num"])
	quota := number(usageMetric["package_token_amount"])
	var usagePercent, progressPercent any
	if quota > 0 {
		ratio := float64(used) / float64(quota)
		usagePercent = round2(ratio * 100)
		progressPercent = round2(math.Min(1, ratio) * 100)
	}
	modelMetrics := make([]map[string]any, 0)
	if rawItems, ok := payload["model_metrics"].([]any); ok {
		for _, raw := range rawItems {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			modelMetrics = append(modelMetrics, map[string]any{
				"model_id":        stringValue(item["model_id"]),
				"model_type":      stringValue(item["model_type"]),
				"usage_token":     item["usage_token"],
				"usage_token_num": number(item["usage_token_num"]),
				"model_quota_num": number(item["model_quota_num"]),
				"enable":          boolValue(item["enable"]),
				"status":          stringValue(item["status"]),
			})
		}
	}
	packageName := stringValue(packageInfo["package_name_en"])
	if packageName == "" {
		packageName = stringValue(packageInfo["package_name_zh_cn"])
	}
	return map[string]any{
		"available":        len(usageMetric) > 0,
		"account_id":       accountID,
		"environment":      environment,
		"package_name":     packageName,
		"package_status":   stringValue(packageInfo["status"]),
		"spec_code":        stringValue(packageInfo["spec_code"]),
		"used_tokens":      used,
		"quota_tokens":     quota,
		"usage_percent":    usagePercent,
		"progress_percent": progressPercent,
		"over_limit":       quota > 0 && used > quota,
		"overage_tokens":   maxInt(0, used-quota),
		"start_date":       stringValue(payload["start_date"]),
		"end_date":         stringValue(payload["end_date"]),
		"model_metrics":    modelMetrics,
	}
}

func unavailable() map[string]any {
	return map[string]any{"available": false, "account_count": 0, "used_tokens": 0, "quota_tokens": 0, "items": []any{}, "package_names": []string{}}
}
func number(value any) int {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return 0
}
func stringValue(value any) string {
	if v, ok := value.(string); ok {
		return v
	}
	return ""
}
func boolValue(value any) bool { v, _ := value.(bool); return v }
func clone(value map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range value {
		out[k] = v
	}
	return out
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
