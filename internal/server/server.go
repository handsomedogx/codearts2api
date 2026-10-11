package server

import (
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	webui "github.com/handsomedogx/codearts2api/app/web"
	"github.com/handsomedogx/codearts2api/internal/auth"
	"github.com/handsomedogx/codearts2api/internal/catalog"
	"github.com/handsomedogx/codearts2api/internal/codearts"
	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/conversation"
	"github.com/handsomedogx/codearts2api/internal/gateway"
	"github.com/handsomedogx/codearts2api/internal/keepalive"
	"github.com/handsomedogx/codearts2api/internal/observe"
	"github.com/handsomedogx/codearts2api/internal/securestore"
	"github.com/handsomedogx/codearts2api/internal/storage"
	"github.com/handsomedogx/codearts2api/internal/subscription"
)

const appVersion = "0.9.0-go"

type API struct {
	mux           *http.ServeMux
	settings      config.Settings
	auth          *auth.Manager
	codearts      *codearts.Client
	catalog       *catalog.Catalog
	observe       *observe.Store
	gateway       *gateway.Store
	subscription  *subscription.Catalog
	conversations *conversation.Store
}

func New() http.Handler { return newAPI(config.Load()) }

func NewRuntime() (http.Handler, func(), error) {
	settings := config.Load()
	if err := storage.MigrateNonSensitive(settings); err != nil {
		return nil, nil, fmt.Errorf("migrate legacy data: %w", err)
	}
	securePaths := []string{settings.GatewaySettingsFile, settings.PendingAuthFile, settings.AccountsDir}
	if settings.MigrateLegacy {
		securePaths = append(securePaths,
			filepath.Join(settings.LegacyDataDir, "settings.json"),
			filepath.Join(settings.LegacyDataDir, "oauth_pending.json"),
			filepath.Join(settings.LegacyDataDir, "accounts"),
		)
	}
	if err := securestore.PrepareRuntime(securePaths...); err != nil {
		return nil, nil, fmt.Errorf("initialize secure credential storage: %w", err)
	}
	a := newAPI(settings)
	if err := a.auth.MigrateSensitiveStorage(); err != nil {
		return nil, nil, fmt.Errorf("migrate protected credentials: %w", err)
	}
	if _, err := a.gateway.Ensure(); err != nil {
		return nil, nil, fmt.Errorf("initialize gateway API key: %w", err)
	}
	service := keepalive.New(a.settings, a.auth, a.codearts)
	service.Start()
	return a, service.Stop, nil
}

func newAPI(settings config.Settings) *API {
	manager := auth.NewManager(settings)
	client := codearts.New(settings, manager)
	legacySettings := ""
	if settings.MigrateLegacy {
		legacySettings = filepath.Join(settings.LegacyDataDir, "settings.json")
	}
	gatewayStore := gateway.New(settings.GatewaySettingsFile, legacySettings)
	a := &API{
		mux:           http.NewServeMux(),
		settings:      settings,
		auth:          manager,
		codearts:      client,
		catalog:       catalog.New(settings, manager, client),
		observe:       observe.New(settings.UsageFile, settings.RequestLogsFile),
		gateway:       gatewayStore,
		subscription:  subscription.New(settings, manager, client),
		conversations: conversation.New(settings.ConversationsDir),
	}
	a.routes()
	return a
}

func (a *API) routes() {
	a.mux.HandleFunc("GET /", a.webUI)
	staticFiles := http.StripPrefix("/static/", http.FileServer(http.FS(webui.FS())))
	a.mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		staticFiles.ServeHTTP(w, r)
	}))
	a.mux.HandleFunc("GET /health", a.health)
	a.mux.HandleFunc("GET /api/ui/meta", a.meta)
	a.mux.HandleFunc("GET /api/ui/access", a.access)
	a.mux.HandleFunc("POST /api/ui/access/regenerate", a.accessRegenerate)
	a.mux.HandleFunc("GET /auth/login", a.authLogin)
	a.mux.HandleFunc("GET /auth/login-url", a.authLoginURL)
	a.mux.HandleFunc("GET /oauth/callback", a.oauthCallback)
	a.mux.HandleFunc("GET /api/ui/accounts", a.accounts)
	a.mux.HandleFunc("POST /api/ui/accounts/{account_id}/refresh", a.refreshAccount)
	a.mux.HandleFunc("POST /api/ui/accounts/{account_id}/probe", a.probeAccount)
	a.mux.HandleFunc("POST /api/ui/accounts/probe", a.probeAccounts)
	a.mux.HandleFunc("DELETE /api/ui/accounts/{account_id}", a.deleteAccount)
	a.mux.HandleFunc("GET /api/ui/routing", a.routing)
	a.mux.HandleFunc("POST /api/ui/routing", a.routingUpdate)
	a.mux.HandleFunc("GET /api/ui/usage", a.usage)
	a.mux.HandleFunc("GET /api/ui/logs", a.logs)
	a.mux.HandleFunc("GET /v1/models", a.models)
	a.mux.HandleFunc("POST /v1/chat/completions", a.chatCompletions)
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "codearts2api-go")
	w.Header().Set("X-Request-ID", fmt.Sprintf("req-%x", time.Now().UnixNano()))
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		candidate := ""
		if authorization := strings.TrimSpace(r.Header.Get("Authorization")); len(authorization) >= 7 && strings.EqualFold(authorization[:7], "Bearer ") {
			candidate = strings.TrimSpace(authorization[7:])
		}
		if !a.gateway.Verify(candidate) {
			writeError(w, http.StatusUnauthorized, "Invalid API key", "invalid_api_key")
			return
		}
	}
	if isManagementPath(r.URL.Path) && !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "Dashboard management APIs are available only from this machine", "forbidden")
		return
	}
	a.mux.ServeHTTP(w, r)
}

func (a *API) webUI(w http.ResponseWriter, _ *http.Request) {
	index, err := webui.Index()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "web_ui_error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}

func (a *API) meta(w http.ResponseWriter, _ *http.Request) {
	cn, _ := a.settings.Profile(config.EnvCN)
	intl, _ := a.settings.Profile(config.EnvInternational)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "codearts2api",
		"version":         appVersion,
		"api_base":        "/v1",
		"api_key_enabled": true,
		"environments": []any{
			map[string]any{"id": cn.Environment, "label": cn.Label, "upstream": cn.BaseURL, "portal": cn.PortalBaseURL, "quickcompfalg": cn.QuickCompFlag},
			map[string]any{"id": intl.Environment, "label": intl.Label, "upstream": intl.BaseURL, "portal": intl.PortalBaseURL, "quickcompfalg": intl.QuickCompFlag},
		},
		"callback_url": a.settings.CallbackURL(),
	})
}

func (a *API) access(w http.ResponseWriter, r *http.Request) {
	access, err := a.gateway.Ensure()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "access_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":       access.APIKey,
		"source":        access.Source,
		"regeneratable": access.Regeneratable,
		"base_url":      requestBaseURL(r) + "/v1",
	})
}

func (a *API) accessRegenerate(w http.ResponseWriter, r *http.Request) {
	access, err := a.gateway.Regenerate()
	if err != nil {
		status := http.StatusBadRequest
		if strings.TrimSpace(os.Getenv("CODEARTS2API_API_KEY")) != "" {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error(), "access_regenerate_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"api_key":       access.APIKey,
		"source":        access.Source,
		"regeneratable": access.Regeneratable,
		"base_url":      requestBaseURL(r) + "/v1",
	})
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	accounts, _ := a.codearts.Pool.ListPublic()
	authenticated := false
	for _, account := range accounts {
		if account.Authenticated {
			authenticated = true
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":                 "ok",
		"credentials_configured": authenticated,
		"account_count":          len(accounts),
		"routing_strategy":       a.codearts.Pool.RoutingStrategy(),
		"runtime":                "go",
	})
}

func (a *API) authLogin(w http.ResponseWriter, r *http.Request) {
	session, err := a.beginLogin(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request")
		return
	}
	http.Redirect(w, r, session.AuthorizeURL, http.StatusTemporaryRedirect)
}

func (a *API) authLoginURL(w http.ResponseWriter, r *http.Request) {
	session, err := a.beginLogin(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (a *API) oauthCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeOAuthPage(w, http.StatusBadRequest, false, "", "", "", "OAuth callback is missing code")
		return
	}
	account, err := a.auth.ExchangeCode(code)
	if err != nil {
		writeOAuthPage(w, http.StatusBadGateway, false, "", "", "", err.Error())
		return
	}
	writeOAuthPage(w, http.StatusOK, true, account.Environment, account.EnvironmentLabel, account.ID, "")
}

func writeOAuthPage(w http.ResponseWriter, status int, success bool, environment, environmentLabel, accountID, errorMessage string) {
	title := "登录失败 / Login failed"
	message := "CodeArts 登录未完成。 / CodeArts login did not complete."
	statusClass := "error"
	if success {
		title = "登录成功 / Login successful"
		message = "CodeArts 登录已完成，正在返回 codearts2api。 / CodeArts login completed. Returning to codearts2api."
		statusClass = "success"
	}
	payload := map[string]any{"type": "codearts2api:oauth-complete", "success": success, "environment": environment, "environment_label": environmentLabel, "account_id": accountID}
	if !success {
		payload["error"] = "OAuth login failed"
	}
	encoded, _ := json.Marshal(payload)
	payloadJSON := strings.ReplaceAll(string(encoded), "</", "<\\/")
	detail := ""
	if errorMessage != "" {
		detail = `<pre class="detail">` + html.EscapeString(truncateString(errorMessage, 800)) + `</pre>`
	}
	page := `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>:root{color-scheme:dark;font-family:Inter,ui-sans-serif,system-ui,sans-serif}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b0d12;color:#e8e9ee}.card{width:min(520px,calc(100vw - 40px));border:1px solid #252a35;background:#11141b;border-radius:18px;padding:28px;box-shadow:0 22px 70px #0008}.dot{width:11px;height:11px;border-radius:50%;display:inline-block;margin-right:8px}.success .dot{background:#49d49d}.error .dot{background:#ff6b79}h1{font-size:20px;margin:0 0 10px}p{color:#9298a6;line-height:1.65}.detail{margin:16px 0 0;padding:12px;white-space:pre-wrap;word-break:break-word;background:#0b0d12;border-radius:10px;border:1px solid #242936;font-size:11px}button{margin-top:20px;border:1px solid #353b49;background:#191d26;color:#e8e9ee;border-radius:9px;padding:9px 13px;cursor:pointer}</style></head><body><main class="card ` + statusClass + `"><h1><span class="dot"></span>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(message) + `</p>` + detail + `<button type="button" onclick="window.close()">关闭窗口 / Close window</button></main><script>const payload=` + payloadJSON + `;try{localStorage.setItem("codearts2api.oauthEvent",JSON.stringify({...payload,at:Date.now()}))}catch(_){}if(window.opener&&!window.opener.closed){window.opener.postMessage(payload,window.location.origin)}if(payload.success)setTimeout(()=>window.close(),550);</script></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(page))
}

func (a *API) beginLogin(r *http.Request) (auth.LoginSession, error) {
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if environment == "" {
		environment = a.settings.DefaultEnvironment
	}
	return a.auth.BeginLoginSession(environment)
}

func (a *API) accounts(w http.ResponseWriter, _ *http.Request) {
	accounts, err := a.codearts.Pool.ListPublic()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "internal_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    accounts,
		"accounts": accounts,
		"routing":  a.routingPayload(),
	})
}

func (a *API) refreshAccount(w http.ResponseWriter, r *http.Request) {
	account, err := a.auth.RefreshAccount(r.PathValue("account_id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "refresh_failed")
		return
	}
	writeJSON(w, http.StatusOK, account)
}

func (a *API) probeAccount(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("account_id")
	record, err := a.auth.LoadAccount(accountID)
	if err != nil {
		writeError(w, http.StatusNotFound, "CodeArts account not found", "not_found")
		return
	}
	profile, err := a.settings.Profile(record.Environment)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_environment")
		return
	}
	probe := func() error {
		_, err := a.codearts.GetJSON(r.Context(), accountID, profile.BaseURL+"/v1/model/builtin", "PromptCenter", nil)
		return err
	}
	err = probe()
	if upstream, ok := err.(*codearts.UpstreamError); ok && (upstream.StatusCode == http.StatusUnauthorized || upstream.StatusCode == http.StatusForbidden) && record.RefreshToken != "" {
		if _, refreshErr := a.auth.RefreshAccount(accountID); refreshErr == nil {
			err = probe()
		}
	}
	if err != nil {
		status, category := 0, errorCategory(err)
		if upstream, ok := err.(*codearts.UpstreamError); ok {
			status = upstream.StatusCode
			category = upstream.Category
		}
		_ = a.codearts.Pool.MarkFailure(accountID, "", category, status, 0)
	} else {
		_ = a.codearts.Pool.MarkSuccess(accountID, "")
	}
	accounts, _ := a.codearts.Pool.ListPublic()
	for _, account := range accounts {
		if account.ID == accountID {
			writeJSON(w, http.StatusOK, account)
			return
		}
	}
	writeError(w, http.StatusNotFound, "CodeArts account not found", "not_found")
}

func (a *API) probeAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.codearts.Pool.ListPublic()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "probe_failed")
		return
	}
	results := make([]auth.PublicAccount, 0, len(accounts))
	for _, account := range accounts {
		record, err := a.auth.LoadAccount(account.ID)
		if err != nil {
			continue
		}
		profile, err := a.settings.Profile(record.Environment)
		if err != nil {
			continue
		}
		_, probeErr := a.codearts.GetJSON(r.Context(), account.ID, profile.BaseURL+"/v1/model/builtin", "PromptCenter", nil)
		if probeErr != nil {
			status, category := 0, errorCategory(probeErr)
			if upstream, ok := probeErr.(*codearts.UpstreamError); ok {
				status = upstream.StatusCode
				category = upstream.Category
			}
			_ = a.codearts.Pool.MarkFailure(account.ID, "", category, status, 0)
		} else {
			_ = a.codearts.Pool.MarkSuccess(account.ID, "")
		}
		updated, _ := a.codearts.Pool.ListPublic()
		for _, item := range updated {
			if item.ID == account.ID {
				results = append(results, item)
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    results,
		"accounts": results,
	})
}

func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("account_id")
	deleted, err := a.auth.DeleteAccount(accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "delete_failed")
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "CodeArts account not found", "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "account_id": accountID})
}

func (a *API) routing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.routingPayload())
}

func (a *API) routingPayload() map[string]any {
	return map[string]any{
		"strategy":              a.codearts.Pool.RoutingStrategy(),
		"max_concurrency":       a.settings.AccountMaxConcurrency,
		"retry_budget_attempts": a.settings.RetryBudgetAttempts,
		"retry_budget_seconds":  a.settings.RetryBudgetSeconds,
	}
}

func (a *API) routingUpdate(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body", "invalid_request")
		return
	}
	strategy, _ := payload["strategy"].(string)
	if err := a.codearts.Pool.SetRoutingStrategy(strategy); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request")
		return
	}
	a.routing(w, r)
}

func (a *API) usage(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	points, _ := strconv.Atoi(r.URL.Query().Get("points"))
	if days == 0 {
		days = 3
	}
	if points == 0 {
		points = 30
	}
	summary := a.observe.UsageSummary(days, points)
	forceQuota := strings.EqualFold(r.URL.Query().Get("refresh_quota"), "true") || r.URL.Query().Get("refresh_quota") == "1"
	summary["official_quota"] = a.subscription.Aggregate(r.Context(), forceQuota)
	writeJSON(w, http.StatusOK, summary)
}

func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}
	writeJSON(w, http.StatusOK, a.observe.QueryLogs(
		r.URL.Query().Get("q"),
		r.URL.Query().Get("result"),
		r.URL.Query().Get("time_range"),
		limit,
	))
}

func (a *API) models(w http.ResponseWriter, r *http.Request) {
	force := strings.EqualFold(r.URL.Query().Get("refresh"), "true") || r.URL.Query().Get("refresh") == "1"
	items, err := a.catalog.List(r.Context(), force)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "model_catalog_error")
		return
	}
	now := time.Now().Unix()
	data := make([]map[string]any, 0, len(items))
	for _, item := range items {
		data = append(data, map[string]any{
			"id":                    item.ID,
			"object":                "model",
			"created":               now,
			"owned_by":              item.OwnedBy,
			"display_name":          item.DisplayName,
			"codearts_sources":      item.Sources,
			"codearts_environments": item.Environments,
			"context_window":        item.ContextWindow,
			"input_length":          item.InputLength,
			"max_tokens":            item.MaxTokens,
			"thinking_type":         item.ThinkingType,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (a *API) chatCompletions(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body", "invalid_request")
		return
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		model = a.settings.DefaultModel
	}
	rawMessages, _ := payload["messages"].([]any)
	messages := make([]map[string]any, 0, len(rawMessages))
	for _, raw := range rawMessages {
		if message, ok := raw.(map[string]any); ok {
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages are required", "invalid_request")
		return
	}
	requestedConversationID := strings.TrimSpace(r.Header.Get("X-CodeArts-Conversation"))
	if requestedConversationID == "" {
		requestedConversationID, _ = payload["conversation_id"].(string)
		requestedConversationID = strings.TrimSpace(requestedConversationID)
	}
	conversationContext, err := a.conversations.Resolve(messages, requestedConversationID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_conversation")
		return
	}
	request := codearts.Request{
		Model: model, Messages: messages, Options: payload,
		PreferredAccountID: conversationContext.AccountID,
		ChatID:             conversationContext.ChatID,
	}
	accountID := strings.TrimSpace(r.Header.Get("X-CodeArts-Account"))
	completionID := fmt.Sprintf("chatcmpl-%x", time.Now().UnixNano())
	requestID := strings.TrimSpace(w.Header().Get("X-Request-ID"))
	if requestID == "" {
		requestID = fmt.Sprintf("req-%x", time.Now().UnixNano())
		w.Header().Set("X-Request-ID", requestID)
	}
	started := a.observe.Begin()
	stream, _ := payload["stream"].(bool)
	if stream {
		prepared, err := a.codearts.PrepareStreamCandidates(accountID, request)
		if err != nil {
			status := upstreamHTTPStatus(err)
			category := errorCategory(err)
			a.recordObservation(started, r, requestID, model, true, status, false, accountID, nil, conversationContext.ID, category)
			writeError(w, status, err.Error(), category)
			return
		}
		a.streamChatCompletion(w, r, completionID, requestID, accountID, request, payload, started, conversationContext, prepared)
		return
	}
	result, err := a.codearts.Complete(r.Context(), accountID, request)
	if err != nil {
		status := upstreamHTTPStatus(err)
		category := errorCategory(err)
		a.recordObservation(started, r, requestID, model, false, status, false, accountID, nil, conversationContext.ID, category)
		writeError(w, status, err.Error(), category)
		return
	}
	updatedContext, err := a.conversations.Complete(conversationContext, result.AccountID, result.Environment, result.ChatID, messages, result.Text)
	if err != nil {
		a.recordObservation(started, r, requestID, model, false, http.StatusInternalServerError, false, result.AccountID, &result, conversationContext.ID, "conversation_store")
		writeError(w, http.StatusInternalServerError, err.Error(), "conversation_store_error")
		return
	}
	a.recordObservation(started, r, requestID, model, false, http.StatusOK, true, result.AccountID, &result, updatedContext.ID, "")
	var assistantContent any
	if result.Text != "" {
		assistantContent = result.Text
	}
	message := map[string]any{"role": "assistant", "content": assistantContent}
	if result.ReasoningContent != "" {
		message["reasoning_content"] = result.ReasoningContent
	}
	if len(result.ToolCalls) > 0 {
		message["tool_calls"] = result.ToolCalls
	}
	w.Header().Set("X-CodeArts-Account", result.AccountID)
	w.Header().Set("X-CodeArts-Conversation", updatedContext.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                   completionID,
		"object":               "chat.completion",
		"created":              time.Now().Unix(),
		"model":                model,
		"choices":              []any{map[string]any{"index": 0, "message": message, "finish_reason": result.FinishReason}},
		"usage":                result.Usage,
		"conversation_id":      updatedContext.ID,
		"codearts_account":     result.AccountID,
		"codearts_environment": result.Environment,
		"codearts_attempts":    result.Attempts,
	})
}

func (a *API) streamChatCompletion(w http.ResponseWriter, r *http.Request, completionID, requestID, accountID string, request codearts.Request, payload map[string]any, started time.Time, conversationContext conversation.Context, prepared []auth.AccountRecord) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.recordObservation(started, r, requestID, request.Model, true, http.StatusInternalServerError, false, accountID, nil, conversationContext.ID, "stream_error")
		writeError(w, http.StatusInternalServerError, "streaming is not supported", "stream_error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-CodeArts-Conversation", conversationContext.ID)
	created := time.Now().Unix()
	_ = writeSSE(w, map[string]any{
		"id": completionID, "object": "chat.completion.chunk", "created": created, "model": request.Model,
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	})
	flusher.Flush()
	finishSeen := false
	result, err := a.codearts.StreamPrepared(r.Context(), accountID, request, prepared, func(chunk codearts.Chunk) error {
		if chunk.FinishReason != nil {
			finishSeen = true
		}
		if err := writeSSE(w, map[string]any{
			"id": completionID, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "delta": chunk.Delta, "finish_reason": chunk.FinishReason}},
		}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		a.recordObservation(started, r, requestID, request.Model, true, http.StatusOK, false, accountID, nil, conversationContext.ID, errorCategory(err))
		_ = writeSSE(w, map[string]any{"error": map[string]any{"message": err.Error(), "type": "upstream_error"}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	updatedContext, storeErr := a.conversations.Complete(conversationContext, result.AccountID, result.Environment, result.ChatID, request.Messages, result.Text)
	if storeErr != nil {
		a.recordObservation(started, r, requestID, request.Model, true, http.StatusOK, false, result.AccountID, &result, conversationContext.ID, "conversation_store")
		_ = writeSSE(w, map[string]any{"error": map[string]any{"message": storeErr.Error(), "type": "conversation_store_error"}})
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return
	}
	a.recordObservation(started, r, requestID, request.Model, true, http.StatusOK, true, result.AccountID, &result, updatedContext.ID, "")
	if !finishSeen {
		_ = writeSSE(w, map[string]any{
			"id": completionID, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": result.FinishReason}},
		})
	}
	if includeUsage(payload) {
		_ = writeSSE(w, map[string]any{
			"id": completionID, "object": "chat.completion.chunk", "created": created, "model": request.Model,
			"choices": []any{}, "usage": result.Usage,
		})
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (a *API) recordObservation(started time.Time, r *http.Request, requestID, model string, stream bool, status int, success bool, accountID string, result *codearts.Result, conversationID, errorCategoryValue string) {
	environment := ""
	attempts := 0
	usage := map[string]any{}
	if result != nil {
		accountID = result.AccountID
		environment = result.Environment
		attempts = result.Attempts
		usage = result.Usage
	}
	prompt := usageInt(usage, "prompt_tokens")
	completion := usageInt(usage, "completion_tokens")
	total := usageInt(usage, "total_tokens")
	if total <= 0 {
		total = prompt + completion
	}
	latencyMS := float64(time.Since(started).Microseconds()) / 1000
	a.observe.AppendUsage(observe.UsageEvent{
		AccountID: accountID, Environment: environment, Model: model, Success: success,
		ErrorCategory: errorCategoryValue, PromptTokens: prompt, CompletionTokens: completion,
		TotalTokens: total, LatencyMS: latencyMS,
	})
	accountName := ""
	if accountID != "" {
		if record, err := a.auth.LoadAccount(accountID); err == nil {
			accountName = record.UserName
		}
	}
	a.observe.Finish(started, observe.RequestEntry{
		RequestID: requestID, Status: status, Success: success, Stream: stream, Model: model,
		AccountID: accountID, AccountName: accountName, Environment: environment,
		SourceIP: sourceIP(r), UserAgent: r.UserAgent(), PromptTokens: prompt,
		CompletionTokens: completion, TotalTokens: total, Attempts: attempts,
		ConversationID: conversationID, ErrorCategory: errorCategoryValue,
	})
}

func usageInt(usage map[string]any, key string) int {
	if usage == nil {
		return 0
	}
	switch value := usage[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	}
	return 0
}

func errorCategory(err error) string {
	if upstream, ok := err.(*codearts.UpstreamError); ok && upstream.Category != "" {
		return upstream.Category
	}
	return "upstream_error"
}

func upstreamHTTPStatus(err error) int {
	if upstream, ok := err.(*codearts.UpstreamError); ok {
		if upstream.Category == "no_account" || upstream.Category == "retry_budget" {
			return http.StatusServiceUnavailable
		}
	}
	return http.StatusBadGateway
}

func isManagementPath(path string) bool {
	return strings.HasPrefix(path, "/api/ui/") || path == "/auth/login" || path == "/auth/login-url"
}

func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if idx := strings.Index(forwarded, ","); idx >= 0 {
			forwarded = forwarded[:idx]
		}
		if forwarded == "http" || forwarded == "https" {
			scheme = forwarded
		}
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func sourceIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		if idx := strings.Index(forwarded, ","); idx >= 0 {
			forwarded = forwarded[:idx]
		}
		return strings.TrimSpace(forwarded)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func includeUsage(payload map[string]any) bool {
	options, _ := payload["stream_options"].(map[string]any)
	value, _ := options["include_usage"].(bool)
	return value
}

func writeSSE(w http.ResponseWriter, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", encoded)
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message, code string) {
	writeJSON(w, status, map[string]any{
		"detail": message,
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"code":    code,
		},
	})
}
