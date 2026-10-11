package codearts

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/signer"
)

var optionKeys = map[string]struct{}{
	"temperature": {}, "top_p": {}, "max_tokens": {}, "reasoning_effort": {},
	"tools": {}, "tool_choice": {}, "stop": {}, "presence_penalty": {},
	"frequency_penalty": {}, "response_format": {},
}

type Client struct {
	Settings config.Settings
	Auth     *auth.Manager
	Pool     *auth.Pool
	HTTP     *http.Client
}

type Request struct {
	Model              string
	Messages           []map[string]any
	Options            map[string]any
	PreferredAccountID string
	ChatID             string
}

type Chunk struct {
	Delta        map[string]any
	FinishReason any
}

type Result struct {
	Text             string
	ReasoningContent string
	ToolCalls        []any
	FinishReason     string
	Usage            map[string]any
	ChatID           string
	AccountID        string
	Environment      string
	Attempts         int
}

type UpstreamError struct {
	StatusCode int
	Category   string
	Retryable  bool
	RetryAfter time.Duration
	Message    string
}

func (e *UpstreamError) Error() string { return e.Message }

func New(settings config.Settings, manager *auth.Manager) *Client {
	return &Client{Settings: settings, Auth: manager, Pool: auth.NewPool(settings, manager), HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) Stream(ctx context.Context, accountID string, request Request, onChunk func(Chunk) error) (Result, error) {
	return c.execute(ctx, accountID, request, onChunk, false)
}

func (c *Client) execute(ctx context.Context, accountID string, request Request, onChunk func(Chunk) error, retryAfterEmission bool) (Result, error) {
	candidates, err := c.Pool.Candidates(accountID, request.PreferredAccountID, request.Model)
	if err != nil {
		return Result{}, err
	}
	if len(candidates) == 0 {
		return Result{}, &UpstreamError{Category: "no_account", Retryable: false, Message: "No usable CodeArts account is available"}
	}
	maxAccounts := c.Settings.RoutingMaxAttempts
	if maxAccounts <= 0 || maxAccounts > len(candidates) {
		maxAccounts = len(candidates)
	}
	budgetAttempts := c.Settings.RetryBudgetAttempts
	if budgetAttempts <= 0 {
		budgetAttempts = 1
	}
	budgetSeconds := c.Settings.RetryBudgetSeconds
	if budgetSeconds < 0.1 {
		budgetSeconds = 0.1
	}
	deadline := time.Now().Add(time.Duration(budgetSeconds * float64(time.Second)))
	available := func(attempts int) bool { return attempts < budgetAttempts && time.Now().Before(deadline) }

	var lastErr error
	attempts := 0
	retryOrdinal := 0
	for _, candidate := range candidates[:maxAccounts] {
		retries := 0
		for available(attempts) {
			started, ok := c.Pool.Begin(candidate.ID)
			if !ok {
				break
			}
			attempts++
			attemptRequest := request
			if candidate.ID != request.PreferredAccountID {
				attemptRequest.ChatID = ""
			}
			emitted := false
			wrappedChunk := func(chunk Chunk) error {
				emitted = true
				return onChunk(chunk)
			}
			result, attemptErr := c.streamSingle(ctx, candidate.ID, attemptRequest, wrappedChunk)
			c.Pool.Finish(candidate.ID)
			if attemptErr == nil {
				result.Attempts = attempts
				_ = c.Pool.RecordRequest(candidate.ID, request.Model, candidate.Environment, started, true, result.Usage, "")
				_ = c.Pool.MarkSuccess(candidate.ID, request.Model)
				return result, nil
			}

			lastErr = attemptErr
			status, category, retryable, retryAfter := classifyError(attemptErr)
			_ = c.Pool.RecordRequest(candidate.ID, request.Model, candidate.Environment, started, false, nil, category)
			if category != "network" && category != "timeout" {
				_ = c.Pool.MarkFailure(candidate.ID, request.Model, category, status, retryAfter)
			}
			if emitted && !retryAfterEmission {
				return Result{}, attemptErr
			}

			retrySame := retries < maxInt(0, c.Settings.UpstreamRetriesPerAccount) && (category == "network" || category == "timeout" || category == "server")
			if retrySame && available(attempts) {
				retries++
				if !c.backoff(ctx, retryOrdinal, retryAfter, deadline) {
					break
				}
				retryOrdinal++
				continue
			}
			if accountID != "" || !retryable {
				return Result{}, attemptErr
			}
			if retryable && available(attempts) {
				if !c.backoff(ctx, retryOrdinal, retryAfter, deadline) {
					break
				}
				retryOrdinal++
			}
			break
		}
	}
	if lastErr != nil {
		return Result{}, lastErr
	}
	return Result{}, &UpstreamError{Category: "retry_budget", Retryable: false, Message: "No CodeArts account completed the request within retry budget"}
}

func (c *Client) backoff(ctx context.Context, ordinal int, retryAfter time.Duration, deadline time.Time) bool {
	baseMS := math.Max(0, c.Settings.RetryBackoffBaseMS)
	capMS := math.Max(baseMS, c.Settings.RetryBackoffCapMS)
	delayMS := 0.0
	if baseMS > 0 {
		delayMS = math.Min(capMS, baseMS*math.Pow(2, float64(maxInt(0, ordinal))))
	}
	jitter := math.Min(1, math.Max(0, c.Settings.RetryBackoffJitter))
	if delayMS > 0 && jitter > 0 {
		delayMS *= 1 + (mathrand.Float64()*2-1)*jitter
	}
	delay := time.Duration(delayMS * float64(time.Millisecond))
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay <= 0 {
		return time.Now().Before(deadline)
	}
	if time.Until(deadline) <= delay {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return time.Now().Before(deadline)
	}
}

func (c *Client) streamSingle(ctx context.Context, accountID string, request Request, onChunk func(Chunk) error) (Result, error) {
	if len(request.Messages) == 0 {
		return Result{}, fmt.Errorf("at least one chat message is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return Result{}, fmt.Errorf("model is required")
	}
	if accountID == "" {
		accounts, err := c.Auth.ListAccounts()
		if err != nil {
			return Result{}, err
		}
		for _, account := range accounts {
			if account.Authenticated {
				accountID = account.ID
				break
			}
		}
	}
	if accountID == "" {
		return Result{}, fmt.Errorf("no usable CodeArts account is available")
	}
	record, err := c.Auth.LoadAccount(accountID)
	if err != nil {
		return Result{}, err
	}
	credentials, err := c.Auth.Credentials(accountID)
	if err != nil {
		return Result{}, err
	}
	profile, err := c.Settings.Profile(record.Environment)
	if err != nil {
		return Result{}, err
	}
	chatID := strings.TrimSpace(request.ChatID)
	if chatID == "" {
		chatID, err = randomHex(16)
		if err != nil {
			return Result{}, err
		}
	}
	body := make(map[string]any)
	for key, value := range request.Options {
		if _, ok := optionKeys[key]; ok && value != nil {
			body[key] = value
		}
	}
	body["model"] = request.Model
	body["stream"] = true
	body["messages"] = request.Messages
	body["chat_id"] = chatID
	body["prompt_cache_key"] = chatID
	body["tool_stream"] = true

	headers := http.Header{
		"Content-Type":    {"application/json"},
		"Accept":          {"text/event-stream"},
		"X-Language":      {c.Settings.Language},
		"x-snap-traceid":  {mustRandomHex(16)},
		"app-id":          {"CodeAgent3.0"},
		"is_confidential": {"false"},
		"x-auth-token":    {credentials.SecurityToken},
	}
	signed, err := signer.SignRequest(http.MethodPost, profile.ChatURL(), headers, body, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SecurityToken, credentials.DomainID, time.Time{})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, profile.ChatURL(), bytes.NewReader(signed.Body))
	if err != nil {
		return Result{}, err
	}
	for key, values := range signed.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, &UpstreamError{Category: "network", Retryable: true, Message: "CodeArts transport error: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		category, retryable := classifyStatus(resp.StatusCode)
		return Result{}, &UpstreamError{
			StatusCode: resp.StatusCode,
			Category:   category,
			Retryable:  retryable,
			RetryAfter: retryAfterDuration(resp.Header.Get("Retry-After")),
			Message:    fmt.Sprintf("CodeArts upstream HTTP %d: %s", resp.StatusCode, string(raw)),
		}
	}

	result := Result{FinishReason: "stop", Usage: map[string]any{}, ChatID: chatID, AccountID: accountID, Environment: record.Environment}
	var text strings.Builder
	var reasoning strings.Builder
	toolCallParts := map[int]map[string]any{}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "[DONE]" {
			break
		}
		if !strings.HasPrefix(line, "{") && !strings.HasPrefix(line, "[") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		if eventErr := upstreamEventError(payload); eventErr != nil {
			return Result{}, eventErr
		}
		if usage := findMap(payload, "usage"); usage != nil {
			result.Usage = usage
		}
		if found := findString(payload, "chat_id"); found != "" {
			result.ChatID = found
		}
		choices, _ := payload["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if choice == nil {
			continue
		}
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			delta = map[string]any{}
		}
		if content, ok := delta["content"].(string); ok {
			text.WriteString(content)
		}
		if value, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(value)
		}
		if calls, ok := delta["tool_calls"].([]any); ok && len(calls) > 0 {
			mergeToolCallParts(toolCallParts, calls)
			result.ToolCalls = toolCallsFromParts(toolCallParts)
		}
		finishReason := choice["finish_reason"]
		if value, ok := finishReason.(string); ok && value != "" {
			result.FinishReason = value
		}
		if len(delta) > 0 || finishReason != nil {
			if err := onChunk(Chunk{Delta: delta, FinishReason: finishReason}); err != nil {
				return Result{}, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, err
	}
	result.Text = text.String()
	result.ReasoningContent = reasoning.String()
	return result, nil
}

func (c *Client) Complete(ctx context.Context, accountID string, request Request) (Result, error) {
	return c.execute(ctx, accountID, request, func(Chunk) error { return nil }, true)
}

func (c *Client) GetJSON(ctx context.Context, accountID, rawURL, agentType string, params url.Values) (map[string]any, error) {
	record, err := c.Auth.LoadAccount(accountID)
	if err != nil {
		return nil, err
	}
	credentials, err := c.Auth.Credentials(accountID)
	if err != nil {
		return nil, err
	}
	if len(params) > 0 {
		separator := "?"
		if strings.Contains(rawURL, "?") {
			separator = "&"
		}
		rawURL += separator + params.Encode()
	}
	headers := http.Header{
		"Content-Type":    {"application/json"},
		"Accept":          {"application/json"},
		"client_version":  {c.Settings.ClientVersion},
		"X-Language":      {c.Settings.Language},
		"x-snap-traceid":  {mustRandomHex(16)},
		"plugin-name":     {c.Settings.PluginName},
		"plugin-version":  {c.Settings.PluginVersion},
		"ide-name":        {c.Settings.IDEName},
		"ide-version":     {c.Settings.IDEVersion},
		"is_confidential": {"false"},
		"x-auth-token":    {""},
	}
	if agentType != "" {
		headers.Set("Agent-Type", agentType)
	}
	signed, err := signer.SignRequest(http.MethodGet, rawURL, headers, nil, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SecurityToken, record.DomainID, time.Time{})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for key, values := range signed.Headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &UpstreamError{Category: "network", Retryable: true, Message: "CodeArts transport error: " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		category, retryable := classifyStatus(resp.StatusCode)
		return nil, &UpstreamError{StatusCode: resp.StatusCode, Category: category, Retryable: retryable, RetryAfter: retryAfterDuration(resp.Header.Get("Retry-After")), Message: fmt.Sprintf("CodeArts upstream HTTP %d: %s", resp.StatusCode, string(body))}
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if object, ok := payload.(map[string]any); ok {
		return object, nil
	}
	return map[string]any{"data": payload}, nil
}

func mergeToolCallParts(parts map[int]map[string]any, calls []any) {
	for position, raw := range calls {
		call, _ := raw.(map[string]any)
		if call == nil {
			continue
		}
		index := position
		switch value := call["index"].(type) {
		case int:
			index = value
		case float64:
			index = int(value)
		}
		current := parts[index]
		if current == nil {
			current = map[string]any{"id": "", "type": "function", "function": map[string]any{"name": "", "arguments": ""}}
			parts[index] = current
		}
		if value, ok := call["id"].(string); ok && value != "" {
			current["id"] = value
		}
		if value, ok := call["type"].(string); ok && value != "" {
			current["type"] = value
		}
		incoming, _ := call["function"].(map[string]any)
		if incoming == nil {
			continue
		}
		function, _ := current["function"].(map[string]any)
		if function == nil {
			function = map[string]any{"name": "", "arguments": ""}
			current["function"] = function
		}
		if value, ok := incoming["name"].(string); ok && value != "" {
			function["name"] = stringValue(function["name"]) + value
		}
		if value, ok := incoming["arguments"].(string); ok && value != "" {
			function["arguments"] = stringValue(function["arguments"]) + value
		}
	}
}

func toolCallsFromParts(parts map[int]map[string]any) []any {
	indexes := make([]int, 0, len(parts))
	for index := range parts {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	result := make([]any, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, parts[index])
	}
	return result
}

func stringValue(value any) string { v, _ := value.(string); return v }

func upstreamEventError(payload map[string]any) error {
	if raw, ok := payload["error"].(map[string]any); ok {
		message := stringValue(raw["message"])
		if message == "" {
			message = stringValue(raw["error_msg"])
		}
		code := fmt.Sprint(raw["code"])
		if message == "" {
			message = code
		}
		if message == "" {
			message = "CodeArts upstream error"
		}
		lower := strings.ToLower(message)
		retryable := strings.Contains(code, "429") || strings.Contains(lower, "flow") || strings.Contains(lower, "busy")
		return &UpstreamError{Category: "upstream_event", Retryable: retryable, Message: message}
	}
	if value, exists := payload["error_code"]; exists && fmt.Sprint(value) != "" && fmt.Sprint(value) != "0" {
		message := stringValue(payload["error_msg"])
		if message == "" {
			message = stringValue(payload["message"])
		}
		code := fmt.Sprint(value)
		if message == "" {
			message = "CodeArts error " + code
		}
		lower := strings.ToLower(message)
		retryable := strings.Contains(code, "429") || strings.Contains(lower, "flow") || strings.Contains(lower, "busy")
		return &UpstreamError{Category: "upstream_event", Retryable: retryable, Message: message}
	}
	return nil
}

func findMap(value any, key string) map[string]any {
	switch current := value.(type) {
	case map[string]any:
		if direct, ok := current[key].(map[string]any); ok {
			return direct
		}
		for _, nested := range current {
			if found := findMap(nested, key); found != nil {
				return found
			}
		}
	case []any:
		for _, nested := range current {
			if found := findMap(nested, key); found != nil {
				return found
			}
		}
	}
	return nil
}

func findString(value any, key string) string {
	switch current := value.(type) {
	case map[string]any:
		if direct, ok := current[key].(string); ok && direct != "" {
			return direct
		}
		for _, nested := range current {
			if found := findString(nested, key); found != "" {
				return found
			}
		}
	case []any:
		for _, nested := range current {
			if found := findString(nested, key); found != "" {
				return found
			}
		}
	}
	return ""
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func mustRandomHex(n int) string {
	value, _ := randomHex(n)
	return value
}

func classifyStatus(status int) (string, bool) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth", true
	case status == http.StatusTooManyRequests:
		return "rate_limit", true
	case status >= 500:
		return "server", true
	case status >= 400:
		return "client", false
	default:
		return "upstream", false
	}
}

func retryAfterDuration(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func classifyError(err error) (status int, category string, retryable bool, retryAfter time.Duration) {
	if upstream, ok := err.(*UpstreamError); ok {
		return upstream.StatusCode, upstream.Category, upstream.Retryable, upstream.RetryAfter
	}
	if err == context.DeadlineExceeded {
		return 0, "timeout", true, 0
	}
	return 0, "network", true, 0
}
