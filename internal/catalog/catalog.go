package catalog

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
)

type Model struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	OwnedBy       string   `json:"owned_by"`
	Sources       []string `json:"sources"`
	Environments  []string `json:"environments"`
	ContextWindow *int     `json:"context_window"`
	InputLength   *int     `json:"input_length"`
	MaxTokens     *int     `json:"max_tokens"`
	ThinkingType  any      `json:"thinking_type"`
}

type rawModel struct {
	ID            string
	DisplayName   string
	OwnedBy       string
	Source        string
	Environment   string
	ContextWindow *int
	InputLength   *int
	MaxTokens     *int
	ThinkingType  any
}

type cacheEntry struct {
	Expires time.Time
	Items   []rawModel
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

func (c *Catalog) List(ctx context.Context, force bool) ([]Model, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if force {
		c.cache = map[string]cacheEntry{}
	}
	accounts, err := c.Client.Pool.Candidates("", "", "")
	if err != nil {
		return nil, err
	}
	gathered := make([]rawModel, 0)
	for _, account := range accounts {
		items, err := c.fetchAccount(ctx, account.ID, account.Environment)
		if err != nil {
			continue
		}
		gathered = append(gathered, items...)
	}
	merged := map[string]Model{}
	for _, item := range gathered {
		current, ok := merged[item.ID]
		if !ok {
			merged[item.ID] = Model{ID: item.ID, DisplayName: item.DisplayName, OwnedBy: item.OwnedBy, Sources: []string{item.Source}, Environments: []string{item.Environment}, ContextWindow: item.ContextWindow, InputLength: item.InputLength, MaxTokens: item.MaxTokens, ThinkingType: item.ThinkingType}
			continue
		}
		current.Sources = appendUnique(current.Sources, item.Source)
		current.Environments = appendUnique(current.Environments, item.Environment)
		if current.ContextWindow == nil && item.ContextWindow != nil {
			current.ContextWindow = item.ContextWindow
		}
		if current.InputLength == nil && item.InputLength != nil {
			current.InputLength = item.InputLength
		}
		if current.MaxTokens == nil && item.MaxTokens != nil {
			current.MaxTokens = item.MaxTokens
		}
		if current.ThinkingType == nil && item.ThinkingType != nil {
			current.ThinkingType = item.ThinkingType
		}
		merged[item.ID] = current
	}
	if len(merged) == 0 {
		fallback := append([]string{c.Settings.DefaultModel}, c.Settings.Models...)
		seen := map[string]bool{}
		models := make([]Model, 0, len(fallback))
		for _, id := range fallback {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			models = append(models, Model{ID: id, DisplayName: id, OwnedBy: "codearts", Sources: []string{"fallback"}, Environments: []string{}})
		}
		return models, nil
	}
	models := make([]Model, 0, len(merged))
	for _, item := range merged {
		models = append(models, item)
	}
	sort.Slice(models, func(i, j int) bool {
		a, b := strings.ToLower(models[i].DisplayName), strings.ToLower(models[j].DisplayName)
		if a == b {
			return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID)
		}
		return a < b
	})
	return models, nil
}

func (c *Catalog) fetchAccount(ctx context.Context, accountID, environment string) ([]rawModel, error) {
	if cached, ok := c.cache[accountID]; ok && cached.Expires.After(time.Now()) {
		return append([]rawModel(nil), cached.Items...), nil
	}
	profile, err := c.Settings.Profile(environment)
	if err != nil {
		return nil, err
	}
	items := make([]rawModel, 0)
	builtin, err := c.Client.GetJSON(ctx, accountID, profile.BaseURL+"/v1/model/builtin", "PromptCenter", nil)
	if err != nil {
		return nil, err
	}
	items = append(items, extract(builtin, "builtin", environment)...)

	if agents, err := c.Client.GetJSON(ctx, accountID, profile.BaseURL+"/v1/agent-center/agents/useragents", "AgentCenter", nil); err == nil {
		ids := collectAgentIDs(agents)
		if len(ids) > 6 {
			ids = ids[:6]
		}
		for _, id := range ids {
			detail, err := c.Client.GetJSON(ctx, accountID, profile.BaseURL+"/v1/agent-center/agents/detail", "AgentCenter", url.Values{"agent_id": {id}})
			if err != nil {
				continue
			}
			found := extract(detail, "agent_detail", environment)
			items = append(items, found...)
			if hasContextWindow(found) {
				break
			}
		}
	}
	if third, err := c.Client.GetJSON(ctx, accountID, profile.BaseURL+"/api/v1/third-party/model/list", "PromptCenter", url.Values{"page_num": {"1"}, "page_size": {"50"}}); err == nil {
		items = append(items, extract(third, "third_party", environment)...)
	}
	c.cache[accountID] = cacheEntry{Expires: time.Now().Add(time.Duration(max(1, c.Settings.ModelCacheTTLSeconds)) * time.Second), Items: append([]rawModel(nil), items...)}
	return items, nil
}

func extract(value any, source, environment string) []rawModel {
	items := make([]rawModel, 0)
	seen := map[string]bool{}
	var visit func(any)
	visit = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			id := modelID(v)
			if id != "" && modelish(v) {
				key := id + "\x00" + source
				if !seen[key] {
					seen[key] = true
					params := v
					if p, ok := v["model_parameters"].(map[string]any); ok {
						params = p
					}
					displayName := firstString(v, "display_name", "model_name", "name")
					if displayName == "" {
						displayName = id
					}
					owner := firstString(v, "provider_name", "provider", "vendor_name", "vendor")
					if owner == "" {
						owner = "codearts"
					}
					items = append(items, rawModel{ID: id, DisplayName: displayName, OwnedBy: owner, Source: source, Environment: environment, ContextWindow: intPtr(params["context_window"]), InputLength: firstIntPtr(params, "input_length", "input_context_window"), MaxTokens: firstIntPtr(params, "max_tokens", "output_context_window"), ThinkingType: params["thinking_type"]})
				}
			}
			for _, nested := range v {
				visit(nested)
			}
		case []any:
			for _, nested := range v {
				visit(nested)
			}
		}
	}
	visit(value)
	return items
}

func modelID(m map[string]any) string {
	return firstString(m, "model_id", "modelId", "id", "model_alias", "modelAlias", "name", "model_name")
}
func modelish(m map[string]any) bool {
	for _, key := range []string{"model_id", "modelId", "model_alias", "modelAlias", "model_name", "provider", "provider_name", "vendor", "vendor_name", "model_parameters"} {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}
func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			switch v := value.(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			case float64:
				return strconv.FormatFloat(v, 'f', -1, 64)
			}
		}
	}
	return ""
}
func intPtr(value any) *int {
	switch v := value.(type) {
	case float64:
		n := int(v)
		return &n
	case int:
		n := v
		return &n
	case jsonNumber:
		n, err := strconv.Atoi(string(v))
		if err == nil {
			return &n
		}
	}
	return nil
}

type jsonNumber string

func firstIntPtr(m map[string]any, keys ...string) *int {
	for _, key := range keys {
		if p := intPtr(m[key]); p != nil {
			return p
		}
	}
	return nil
}
func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}
func collectAgentIDs(value any) []string {
	ids := make([]string, 0)
	seen := map[string]bool{}
	var visit func(any)
	visit = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if id := firstString(v, "agent_id"); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
			for _, nested := range v {
				visit(nested)
			}
		case []any:
			for _, nested := range v {
				visit(nested)
			}
		}
	}
	visit(value)
	return ids
}
func hasContextWindow(items []rawModel) bool {
	for _, item := range items {
		if item.ContextWindow != nil {
			return true
		}
	}
	return false
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
