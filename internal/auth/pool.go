package auth

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/handsomedogx/codearts2api/internal/config"
)

type Pool struct {
	Settings config.Settings
	Manager  *Manager

	mu       sync.Mutex
	rrIndex  int
	inFlight map[string]int
}

func NewPool(settings config.Settings, manager *Manager) *Pool {
	return &Pool{Settings: settings, Manager: manager, inFlight: map[string]int{}}
}

func (p *Pool) ListPublic() ([]PublicAccount, error) {
	accounts, err := p.Manager.ListAccounts()
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		if accounts[i].Stats == nil {
			accounts[i].Stats = map[string]any{}
		}
		inFlight := p.InFlight(accounts[i].ID)
		accounts[i].Stats["in_flight"] = inFlight
		accounts[i].Stats["concurrency"] = inFlight
		accounts[i].Stats["max_concurrency"] = p.Settings.AccountMaxConcurrency
		if p.Settings.AccountMaxConcurrency > 0 {
			available := p.Settings.AccountMaxConcurrency - inFlight
			if available < 0 {
				available = 0
			}
			accounts[i].Stats["available_concurrency"] = available
		}
	}
	return accounts, nil
}

func (p *Pool) RoutingStrategy() string {
	payload, err := os.ReadFile(p.Settings.RoutingFile)
	if err == nil {
		var value map[string]any
		if json.Unmarshal(payload, &value) == nil {
			if strategy, ok := value["strategy"].(string); ok {
				strategy = strings.ToLower(strings.TrimSpace(strategy))
				if strategy == "round_robin" || strategy == "random" || strategy == "weighted" {
					return strategy
				}
			}
		}
	}
	strategy := strings.ToLower(strings.TrimSpace(p.Settings.RoutingStrategy))
	if strategy != "random" && strategy != "weighted" {
		return "round_robin"
	}
	return strategy
}

func (p *Pool) SetRoutingStrategy(strategy string) error {
	strategy = strings.ToLower(strings.TrimSpace(strategy))
	if strategy != "round_robin" && strategy != "random" && strategy != "weighted" {
		return fmt.Errorf("unsupported routing strategy")
	}
	return atomicWriteJSON(p.Settings.RoutingFile, map[string]any{"strategy": strategy})
}

func (p *Pool) Begin(accountID string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.inFlight[accountID]
	if p.Settings.AccountMaxConcurrency > 0 && current >= p.Settings.AccountMaxConcurrency {
		return time.Time{}, false
	}
	p.inFlight[accountID] = current + 1
	return time.Now(), true
}

func (p *Pool) Finish(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.inFlight[accountID]
	if current <= 1 {
		delete(p.inFlight, accountID)
	} else {
		p.inFlight[accountID] = current - 1
	}
}

func (p *Pool) InFlight(accountID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inFlight[accountID]
}

func (p *Pool) HasUsable(explicitAccountID, model string) bool {
	if explicitAccountID != "" {
		record, err := p.Manager.LoadAccount(explicitAccountID)
		return err == nil && p.usable(record, model)
	}
	accounts, err := p.Manager.ListAccounts()
	if err != nil {
		return false
	}
	for _, account := range accounts {
		record, err := p.Manager.LoadAccount(account.ID)
		if err == nil && p.usable(record, model) {
			return true
		}
	}
	return false
}

func (p *Pool) Candidates(explicitAccountID, preferredAccountID, model string) ([]AccountRecord, error) {
	if explicitAccountID != "" {
		record, err := p.Manager.LoadAccount(explicitAccountID)
		if err != nil {
			return nil, err
		}
		if p.usable(record, model) {
			return []AccountRecord{record}, nil
		}
		return []AccountRecord{}, nil
	}
	public, err := p.Manager.ListAccounts()
	if err != nil {
		return nil, err
	}
	records := make([]AccountRecord, 0, len(public))
	for _, item := range public {
		record, err := p.Manager.LoadAccount(item.ID)
		if err == nil && p.usable(record, model) {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return records, nil
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].UpdatedAt > records[j].UpdatedAt })
	var preferred []AccountRecord
	if preferredAccountID != "" {
		remaining := records[:0]
		for _, record := range records {
			if record.ID == preferredAccountID {
				preferred = append(preferred, record)
			} else {
				remaining = append(remaining, record)
			}
		}
		records = remaining
	}
	switch p.RoutingStrategy() {
	case "random":
		for i := len(records) - 1; i > 0; i-- {
			j := rand.IntN(i + 1)
			records[i], records[j] = records[j], records[i]
		}
	case "weighted":
		records = p.weightedOrder(records, model)
	default:
		p.mu.Lock()
		if len(records) > 0 {
			start := p.rrIndex % len(records)
			records = append(records[start:], records[:start]...)
			p.rrIndex = (p.rrIndex + 1) % len(records)
		}
		p.mu.Unlock()
	}
	return append(preferred, records...), nil
}

func (p *Pool) usable(record AccountRecord, model string) bool {
	if record.AccessKeyID == "" || record.SecretAccessKey == "" {
		return false
	}
	if p.Settings.AccountMaxConcurrency > 0 && p.InFlight(record.ID) >= p.Settings.AccountMaxConcurrency {
		return false
	}
	if cooldown(record.Health) || boolValue(record.Health, "needs_relogin") {
		return false
	}
	if model != "" {
		if raw, ok := record.ModelHealth[model].(map[string]any); ok && cooldown(raw) {
			return false
		}
	}
	return true
}

func cooldown(health map[string]any) bool {
	return floatValue(health, "cooldown_until_ts") > float64(time.Now().Unix())
}

func (p *Pool) RecordRequest(accountID, model, environment string, started time.Time, success bool, usage map[string]any, errorCategory string) error {
	record, err := p.Manager.LoadAccount(accountID)
	if err != nil {
		return err
	}
	if record.Stats == nil {
		record.Stats = map[string]any{}
	}
	elapsedMS := math.Max(0, float64(time.Since(started).Microseconds())/1000)
	prompt := intValue(usage, "prompt_tokens")
	completion := intValue(usage, "completion_tokens")
	total := intValue(usage, "total_tokens")
	if total <= 0 {
		total = prompt + completion
	}
	record.Stats["request_count"] = intValue(record.Stats, "request_count") + 1
	if success {
		record.Stats["success_count"] = intValue(record.Stats, "success_count") + 1
		record.Stats["success_latency_ms"] = floatValue(record.Stats, "success_latency_ms") + elapsedMS
	} else {
		record.Stats["failure_count"] = intValue(record.Stats, "failure_count") + 1
	}
	record.Stats["prompt_tokens"] = intValue(record.Stats, "prompt_tokens") + maxInt(0, prompt)
	record.Stats["completion_tokens"] = intValue(record.Stats, "completion_tokens") + maxInt(0, completion)
	record.Stats["total_tokens"] = intValue(record.Stats, "total_tokens") + maxInt(0, total)
	record.Stats["total_latency_ms"] = floatValue(record.Stats, "total_latency_ms") + elapsedMS
	record.Stats["last_latency_ms"] = elapsedMS
	record.Stats["last_request_at"] = time.Now().UTC().Format(time.RFC3339)
	record.Stats["last_model"] = model
	record.Stats["last_environment"] = environment
	record.Stats["last_error_category"] = errorCategory
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return p.Manager.saveAccount(record)
}

func (p *Pool) RecordProbe(accountID string, ok bool, category string, statusCode int, needsRelogin bool) error {
	record, err := p.Manager.LoadAccount(accountID)
	if err != nil {
		return err
	}
	if record.Health == nil {
		record.Health = map[string]any{}
	}
	health := record.Health
	health["last_probe_at"] = time.Now().UTC().Format(time.RFC3339)
	if ok {
		health["probe_status"] = "ok"
		health["probe_error_category"] = ""
		health["probe_status_code"] = nil
		health["probe_failures"] = 0
		health["needs_relogin"] = false
		if stringValue(health["last_error_category"]) == "auth_probe" {
			health["consecutive_failures"] = 0
			health["last_error_category"] = ""
			health["last_status_code"] = nil
			health["cooldown_until_ts"] = 0
		}
	} else {
		health["probe_status"] = "failed"
		health["probe_error_category"] = category
		if statusCode != 0 {
			health["probe_status_code"] = statusCode
		} else {
			health["probe_status_code"] = nil
		}
		health["probe_failures"] = intValue(health, "probe_failures") + 1
		health["needs_relogin"] = needsRelogin || boolValue(health, "needs_relogin")
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return p.Manager.saveAccount(record)
}

func (p *Pool) RecordRefreshProbe(accountID string, ok bool, category string) error {
	record, err := p.Manager.LoadAccount(accountID)
	if err != nil {
		return err
	}
	if record.Health == nil {
		record.Health = map[string]any{}
	}
	record.Health["last_refresh_probe_at"] = time.Now().UTC().Format(time.RFC3339)
	if ok {
		record.Health["refresh_probe_status"] = "ok"
		record.Health["refresh_probe_error_category"] = ""
	} else {
		record.Health["refresh_probe_status"] = "failed"
		record.Health["refresh_probe_error_category"] = category
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return p.Manager.saveAccount(record)
}

func stringValue(value any) string { v, _ := value.(string); return v }

func (p *Pool) MarkSuccess(accountID, model string) error {
	record, err := p.Manager.LoadAccount(accountID)
	if err != nil {
		return err
	}
	if record.Health == nil {
		record.Health = map[string]any{}
	}
	record.Health["consecutive_failures"] = 0
	record.Health["total_successes"] = intValue(record.Health, "total_successes") + 1
	record.Health["last_success_at"] = time.Now().UTC().Format(time.RFC3339)
	record.Health["last_error_category"] = ""
	record.Health["last_status_code"] = nil
	record.Health["cooldown_until_ts"] = 0
	record.Health["needs_relogin"] = false
	if model != "" {
		if record.ModelHealth == nil {
			record.ModelHealth = map[string]any{}
		}
		health, _ := record.ModelHealth[model].(map[string]any)
		if health == nil {
			health = map[string]any{}
		}
		health["consecutive_failures"] = 0
		health["total_successes"] = intValue(health, "total_successes") + 1
		health["last_success_at"] = time.Now().UTC().Format(time.RFC3339)
		health["last_error_category"] = ""
		health["last_status_code"] = nil
		health["cooldown_until_ts"] = 0
		record.ModelHealth[model] = health
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return p.Manager.saveAccount(record)
}

func (p *Pool) MarkFailure(accountID, model, category string, statusCode int, retryAfter time.Duration) error {
	record, err := p.Manager.LoadAccount(accountID)
	if err != nil {
		return err
	}
	accountWide := statusCode == 401 || statusCode == 403 || category == "auth" || category == "credential_refresh" || category == "credentials_unavailable"
	if accountWide || model == "" {
		if record.Health == nil {
			record.Health = map[string]any{}
		}
		applyFailure(record.Health, category, statusCode, retryAfter, p.Settings.HealthCooldownBaseSeconds, p.Settings.HealthCooldownMaxSeconds, p.Settings.HealthAuthCooldownSeconds, p.Settings.HealthRateLimitCooldownSeconds)
		if statusCode == 401 || statusCode == 403 {
			record.Health["needs_relogin"] = true
		}
	} else {
		if record.ModelHealth == nil {
			record.ModelHealth = map[string]any{}
		}
		health, _ := record.ModelHealth[model].(map[string]any)
		if health == nil {
			health = map[string]any{}
		}
		applyFailure(health, category, statusCode, retryAfter, p.Settings.ModelHealthCooldownBaseSeconds, p.Settings.ModelHealthCooldownMaxSeconds, 0, p.Settings.HealthRateLimitCooldownSeconds)
		record.ModelHealth[model] = health
	}
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return p.Manager.saveAccount(record)
}

func applyFailure(health map[string]any, category string, statusCode int, retryAfter time.Duration, base, capSeconds, authSeconds, rateSeconds int) {
	failures := intValue(health, "consecutive_failures") + 1
	if base <= 0 {
		base = 1
	}
	if capSeconds <= 0 {
		capSeconds = 300
	}
	cooldownSeconds := math.Min(float64(capSeconds), float64(base)*math.Pow(2, float64(failures-1)))
	if statusCode == 429 && rateSeconds > 0 {
		cooldownSeconds = math.Max(cooldownSeconds, float64(rateSeconds))
	}
	if (statusCode == 401 || statusCode == 403) && authSeconds > 0 {
		cooldownSeconds = math.Max(cooldownSeconds, float64(authSeconds))
	}
	if retryAfter > 0 {
		cooldownSeconds = math.Max(cooldownSeconds, retryAfter.Seconds())
	}
	health["consecutive_failures"] = failures
	health["total_failures"] = intValue(health, "total_failures") + 1
	health["last_failure_at"] = time.Now().UTC().Format(time.RFC3339)
	health["last_error_category"] = category
	if statusCode != 0 {
		health["last_status_code"] = statusCode
	} else {
		health["last_status_code"] = nil
	}
	health["cooldown_until_ts"] = float64(time.Now().Unix()) + cooldownSeconds
}

func (p *Pool) weightedOrder(records []AccountRecord, model string) []AccountRecord {
	remaining := append([]AccountRecord(nil), records...)
	ordered := make([]AccountRecord, 0, len(records))
	for len(remaining) > 0 {
		weights := make([]float64, len(remaining))
		total := 0.0
		for i, record := range remaining {
			weights[i] = p.weight(record, model)
			total += weights[i]
		}
		pick := rand.Float64() * total
		index := 0
		for i, weight := range weights {
			pick -= weight
			if pick <= 0 {
				index = i
				break
			}
		}
		ordered = append(ordered, remaining[index])
		remaining = append(remaining[:index], remaining[index+1:]...)
	}
	return ordered
}

func (p *Pool) weight(record AccountRecord, model string) float64 {
	stats := record.Stats
	health := record.Health
	successes := intValue(stats, "success_count")
	failures := intValue(stats, "failure_count")
	total := successes + failures
	failureRate := 0.0
	if total > 0 {
		failureRate = float64(failures) / float64(total)
	}
	requestCount := intValue(stats, "request_count")
	avgLatency := 0.0
	if requestCount > 0 {
		avgLatency = floatValue(stats, "total_latency_ms") / float64(requestCount)
	}
	consecutive := intValue(health, "consecutive_failures")
	modelConsecutive := 0
	if raw, ok := record.ModelHealth[model].(map[string]any); ok {
		modelConsecutive = intValue(raw, "consecutive_failures")
	}
	weight := 1.0
	weight *= 1 / (1 + float64(p.InFlight(record.ID))*1.5)
	weight *= 1 / (1 + float64(consecutive)*2 + float64(modelConsecutive)*2)
	weight *= 1 / (1 + failureRate*4)
	weight *= 1 / (1 + avgLatency/15000)
	idleHours := 6.0
	if lastRequest, _ := stats["last_request_at"].(string); lastRequest != "" {
		if parsed, err := time.Parse(time.RFC3339, lastRequest); err == nil {
			idleHours = math.Max(0, time.Since(parsed).Hours())
		}
	}
	weight *= 1 + math.Min(idleHours/6, 1)*0.5
	return math.Max(0.001, weight)
}

func intValue(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func floatValue(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return v
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

func boolValue(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, _ := m[key].(bool)
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = filepath.Separator
