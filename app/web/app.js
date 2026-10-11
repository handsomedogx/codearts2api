const $ = (id) => document.getElementById(id);
const tr = (text, vars = {}) => window.codeartsI18n.tr(text, vars);

const state = {
  meta: null,
  accounts: [],
  routing: { strategy: "round_robin" },
  models: [],
  logs: [],
  responseText: "",
  sending: false,
  runtimeOk: null,
  streamMode: "",
  streamState: "Idle",
  lastError: "",
  oauth: { environment: "", accountId: "", authorizeUrl: "", status: "idle", pollTimer: null, pollBusy: false },
};
const pageMeta = {
  dashboard: ["OVERVIEW", "Dashboard"],
  playground: ["TEST CONSOLE", "Playground"],
  logs: ["ACTIVITY", "Logs"],
  settings: ["CONFIGURATION", "Settings"],
};

function getLocalKey() {
  return localStorage.getItem("codearts2api.apiKey") || "";
}

function authHeaders(json = false) {
  const headers = {};
  const key = getLocalKey();
  if (key) headers.Authorization = `Bearer ${key}`;
  if (json) headers["Content-Type"] = "application/json";
  return headers;
}

async function apiFetch(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { ...authHeaders(Boolean(options.body)), ...(options.headers || {}) },
  });
  if (response.status === 401) {
    $("apiKeyBanner").classList.remove("hidden");
  }
  if (!response.ok) {
    let detail = `${response.status} ${response.statusText}`;
    try {
      const body = await response.json();
      detail = body.detail || body.error?.message || detail;
    } catch (_) {}
    throw new Error(detail);
  }
  const type = response.headers.get("content-type") || "";
  return type.includes("application/json") ? response.json() : response.text();
}

function toast(message, type = "") {
  const node = document.createElement("div");
  node.className = `toast ${type}`.trim();
  node.textContent = message;
  $("toastStack").appendChild(node);
  setTimeout(() => node.remove(), 3300);
}

function setRuntime(ok) {
  state.runtimeOk = ok;
  $("runtimeDot").className = `status-dot ${ok ? "online" : "error"}`;
  $("runtimeText").textContent = ok ? tr("Gateway running") : tr("Gateway unavailable");
  $("gatewayStatus").textContent = ok ? tr("Online") : tr("Offline");
}

function shortId(value) {
  if (!value) return "—";
  if (value.length <= 16) return value;
  return `${value.slice(0, 7)}…${value.slice(-5)}`;
}

function expiryLabel(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const diff = date.getTime() - Date.now();
  if (diff <= 0) return tr("Expired");
  const mins = Math.round(diff / 60000);
  if (mins < 60) return window.codeartsI18n.getLanguage() === "zh-CN" ? `${mins} 分钟` : `${mins} min`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return window.codeartsI18n.getLanguage() === "zh-CN" ? `${hours} 小时` : `${hours} hr`;
  return date.toLocaleString(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US");
}

function absoluteBaseUrl() {
  return `${location.origin}/v1`;
}

function updateSnippets() {
  const base = absoluteBaseUrl();
  $("apiBaseUrl").textContent = base;
  $("configBase").textContent = base;
  const protectedApi = Boolean(state.meta?.api_key_enabled);
  $("pythonSnippet").textContent = protectedApi
    ? `from openai import OpenAI\nclient = OpenAI(base_url="${base}", api_key="YOUR_LOCAL_API_KEY")`
    : `from openai import OpenAI\nclient = OpenAI(base_url="${base}", api_key="local")`;
  $("configAuth").textContent = protectedApi ? "Bearer <local API key>" : (window.codeartsI18n.getLanguage() === "zh-CN" ? "无（仅本机）" : "None (localhost)");
}


function environmentLabel(environment) {
  return environment === "international" ? tr("International") : tr("China");
}

function routingLabel(strategy) {
  return strategy === "random" ? tr("Random") : tr("Round Robin");
}

async function loadMeta() {
  try {
    state.meta = await apiFetch("/api/ui/meta");
    setRuntime(true);
    $("versionText").textContent = `v${state.meta.version}`;
    $("settingsVersion").textContent = state.meta.version;
    $("settingsCallback").textContent = state.meta.callback_url;
    $("apiKeyState").textContent = state.meta.api_key_enabled ? tr("Enabled") : tr("Disabled");
    $("apiKeyBanner").classList.toggle("hidden", !(state.meta.api_key_enabled && !getLocalKey()));
    updateSnippets();
    updateApiKeySettings();
  } catch (error) {
    setRuntime(false);
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `获取网关信息失败：${error.message}` : `Gateway metadata failed: ${error.message}`), "error");
  }
}

function accountHost(environment) {
  return environment === "international"
    ? "codearts.ap-southeast-1.huaweicloud.com"
    : "codearts.huaweicloud.com";
}

function createAccountAction(label, action, accountId, extraClass = "ghost") {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `button ${extraClass} small account-action-button`.trim();
  button.textContent = label;
  button.dataset.accountAction = action;
  button.dataset.accountId = accountId;
  return button;
}

function renderAccounts() {
  const list = $("accountList");
  list.replaceChildren();
  $("accountCount").textContent = String(state.accounts.length);
  $("routingPill").textContent = routingLabel(state.routing.strategy);
  $("routingSummary").textContent = `${routingLabel(state.routing.strategy)} ${tr("routing")}`;
  $("routingStrategy").value = state.routing.strategy;

  if (!state.accounts.length) {
    const empty = document.createElement("div");
    empty.className = "account-pool-empty";
    empty.innerHTML = `<strong>${tr("No accounts yet")}</strong><span>${tr("Add a China or International CodeArts account to start routing requests.")}</span>`;
    list.appendChild(empty);
    return;
  }

  state.accounts.forEach((account) => {
    const row = document.createElement("div");
    row.className = "account-site-row";
    const main = document.createElement("div");
    main.className = "account-site-main";
    const badge = document.createElement("div");
    badge.className = "account-site-badge";
    badge.textContent = account.environment === "international" ? "INTL" : "CN";
    const copy = document.createElement("div");
    copy.className = "account-site-copy";
    const title = document.createElement("div");
    title.className = "account-site-title";
    const name = document.createElement("span");
    name.textContent = account.user_name || shortId(account.user_id) || tr("CodeArts account");
    const env = document.createElement("span");
    env.className = "pill neutral";
    env.textContent = environmentLabel(account.environment);
    title.append(name, env);
    const meta = document.createElement("div");
    meta.className = "account-site-user";
    meta.textContent = `${accountHost(account.environment)} · ${expiryLabel(account.expires_at)} · ${shortId(account.id)}`;
    meta.title = account.id;
    copy.append(title, meta);
    main.append(badge, copy);

    const actions = document.createElement("div");
    actions.className = "account-site-actions";
    const status = document.createElement("span");
    status.className = `pill ${account.authenticated ? "success" : "neutral"}`;
    status.textContent = account.authenticated ? tr("Connected") : tr("Not connected");
    actions.appendChild(status);
    if (account.refresh_available) actions.appendChild(createAccountAction(tr("Refresh token"), "refresh", account.id));
    actions.appendChild(createAccountAction(tr("Remove"), "delete", account.id, "danger ghost"));
    row.append(main, actions);
    list.appendChild(row);
  });
}

async function loadAccounts() {
  try {
    const body = await apiFetch("/api/ui/accounts");
    state.accounts = body.items || [];
    state.routing = body.routing || { strategy: "round_robin" };
    renderAccounts();
    $("apiKeyBanner").classList.add("hidden");
  } catch (error) {
    if (String(error.message).includes("401")) {
      state.accounts = [];
      renderAccounts();
      return;
    }
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `获取账号池失败：${error.message}` : `Account pool failed: ${error.message}`), "error");
  }
}

async function updateRouting() {
  const select = $("routingStrategy");
  const previous = state.routing.strategy;
  try {
    const body = await apiFetch("/api/ui/routing", {
      method: "POST",
      body: JSON.stringify({ strategy: select.value }),
    });
    state.routing = body;
    renderAccounts();
    toast(`${tr("Routing strategy")} · ${routingLabel(body.strategy)}`, "success");
  } catch (error) {
    select.value = previous;
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `路由策略更新失败：${error.message}` : `Routing update failed: ${error.message}`), "error");
  }
}

function renderModels(items) {
  state.models = items;
  $("modelCount").textContent = String(items.length);
  const list = $("modelList");
  const select = $("modelSelect");
  list.replaceChildren();
  select.replaceChildren();

  if (!items.length) {
    const empty = document.createElement("div");
    empty.className = "muted";
    empty.textContent = tr("No models configured.");
    list.appendChild(empty);
    return;
  }

  items.forEach((model) => {
    const row = document.createElement("div");
    row.className = "model-row";
    const info = document.createElement("div");
    info.className = "model-info";
    const symbol = document.createElement("div");
    symbol.className = "model-symbol";
    symbol.textContent = "AI";
    const copy = document.createElement("div");
    const name = document.createElement("div");
    name.className = "model-name";
    name.textContent = model.id;
    const owner = document.createElement("div");
    owner.className = "model-owner";
    owner.textContent = tr("owned by {owner}", { owner: model.owned_by || "codearts" });
    copy.append(name, owner);
    info.append(symbol, copy);
    const badge = document.createElement("span");
    badge.className = "pill neutral";
    badge.textContent = model.id === "codearts-auto" ? tr("Default") : tr("Model");
    row.append(info, badge);
    list.appendChild(row);

    const option = document.createElement("option");
    option.value = model.id;
    option.textContent = model.id;
    select.appendChild(option);
  });
}

async function loadModels() {
  try {
    const body = await apiFetch("/v1/models");
    renderModels(body.data || []);
  } catch (error) {
    if (!String(error.message).includes("401")) toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `获取模型失败：${error.message}` : `Models failed: ${error.message}`), "error");
  }
}

function renderLogs(items) {
  state.logs = items;
  const body = $("logsBody");
  body.replaceChildren();
  if (!items.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 5;
    cell.className = "empty-cell";
    cell.textContent = tr("No requests yet.");
    row.appendChild(cell);
    body.appendChild(row);
    return;
  }
  items.forEach((item) => {
    const row = document.createElement("tr");
    const time = document.createElement("td");
    time.textContent = new Date(item.time).toLocaleTimeString(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US");
    const method = document.createElement("td");
    const methodSpan = document.createElement("span");
    methodSpan.className = "log-method";
    methodSpan.textContent = item.method;
    method.appendChild(methodSpan);
    const path = document.createElement("td");
    const code = document.createElement("code");
    code.textContent = item.path;
    path.appendChild(code);
    const status = document.createElement("td");
    const badge = document.createElement("span");
    badge.className = `status-code ${item.status < 400 ? "ok" : "bad"}`;
    badge.textContent = String(item.status);
    status.appendChild(badge);
    const duration = document.createElement("td");
    duration.textContent = `${item.duration_ms} ms`;
    row.append(time, method, path, status, duration);
    body.appendChild(row);
  });
}

async function loadLogs() {
  try {
    const body = await apiFetch("/api/ui/logs?limit=100");
    renderLogs(body.items || []);
  } catch (error) {
    if (!String(error.message).includes("401")) toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `获取日志失败：${error.message}` : `Logs failed: ${error.message}`), "error");
  }
}

async function loadAll() {
  await loadMeta();
  await Promise.all([loadAccounts(), loadModels(), loadLogs()]);
}

function navigate(view) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.view === view));
  document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
  const [eyebrow, title] = pageMeta[view] || pageMeta.dashboard;
  $("pageEyebrow").textContent = tr(eyebrow);
  $("pageTitle").textContent = tr(title);
  if (view === "logs") loadLogs();
}

function stopOAuthPolling() {
  if (state.oauth.pollTimer) clearInterval(state.oauth.pollTimer);
  state.oauth.pollTimer = null;
  state.oauth.pollBusy = false;
}

function oauthStatusCopy() {
  if (state.oauth.status === "success") {
    return [tr("Authorization complete"), tr("The account has been added to the pool and is ready to use.")];
  }
  if (state.oauth.status === "error") {
    return [tr("Authorization failed"), tr("Authorization did not complete. You can reopen the authorization page and try again.")];
  }
  if (state.oauth.status === "preparing") {
    return [tr("Preparing authorization"), tr("Generating a secure CodeArts authorization address…")];
  }
  return [tr("Waiting for authorization"), tr("Keep this window open. The account pool will update automatically after authorization succeeds.")];
}

function renderOAuthWizard() {
  const selecting = state.oauth.status === "idle";
  $("loginSiteStep").classList.toggle("hidden", !selecting);
  $("loginAuthStep").classList.toggle("hidden", selecting);
  $("loginModalTitle").textContent = selecting ? tr("Add CodeArts account") : tr("Authorize CodeArts account");
  $("loginModalSubtitle").textContent = selecting
    ? tr("Choose the account region to start authorization.")
    : tr("Copy the authorization address or open it directly, then complete sign-in on CodeArts.");
  if (selecting) return;

  $("loginSelectedRegion").textContent = environmentLabel(state.oauth.environment);
  $("loginAuthorizeUrl").value = state.oauth.authorizeUrl;
  $("copyAuthorizeUrlBtn").disabled = !state.oauth.authorizeUrl || state.oauth.status === "success";
  $("openAuthorizeUrlBtn").disabled = !state.oauth.authorizeUrl || state.oauth.status === "success";
  const [title, text] = oauthStatusCopy();
  $("oauthWaitTitle").textContent = title;
  $("oauthWaitText").textContent = text;
  $("oauthWaitCard").className = `oauth-wait-card${state.oauth.status === "success" ? " success" : state.oauth.status === "error" ? " error" : ""}`;
  $("oauthSuccessActions").classList.toggle("hidden", state.oauth.status !== "success");
}

function resetLoginWizard() {
  stopOAuthPolling();
  state.oauth.environment = "";
  state.oauth.accountId = "";
  state.oauth.authorizeUrl = "";
  state.oauth.status = "idle";
  renderOAuthWizard();
}

function openLoginModal() {
  resetLoginWizard();
  $("loginModal").classList.remove("hidden");
  document.body.classList.add("modal-open");
}

function closeLoginModal() {
  stopOAuthPolling();
  $("loginModal").classList.add("hidden");
  document.body.classList.remove("modal-open");
}

async function checkOAuthCompletion() {
  if (!state.oauth.accountId || state.oauth.pollBusy || state.oauth.status === "success") return;
  state.oauth.pollBusy = true;
  try {
    const body = await apiFetch("/api/ui/accounts");
    state.accounts = body.items || [];
    state.routing = body.routing || state.routing;
    renderAccounts();
    const account = state.accounts.find((item) => item.id === state.oauth.accountId);
    if (account) {
      completeOAuthWizard(account);
    }
  } catch (_) {
    // Keep waiting; normal account loading surfaces gateway/API-key errors elsewhere.
  } finally {
    state.oauth.pollBusy = false;
  }
}

function startOAuthPolling() {
  stopOAuthPolling();
  state.oauth.pollTimer = setInterval(checkOAuthCompletion, 1500);
}

function completeOAuthWizard(account = null) {
  stopOAuthPolling();
  state.oauth.status = "success";
  renderOAuthWizard();
  const label = account?.user_name || shortId(account?.user_id || "") || environmentLabel(state.oauth.environment);
  toast(`${label} · ${tr("Login successful")}`, "success");
}

async function prepareLogin(environment) {
  const selected = environment || "cn";
  state.oauth.environment = selected;
  state.oauth.accountId = "";
  state.oauth.authorizeUrl = "";
  state.oauth.status = "preparing";
  renderOAuthWizard();
  try {
    const body = await apiFetch(`/auth/login-url?environment=${encodeURIComponent(selected)}`);
    state.oauth.environment = body.environment || selected;
    state.oauth.accountId = body.account_id || "";
    state.oauth.authorizeUrl = body.authorize_url || "";
    state.oauth.status = "waiting";
    renderOAuthWizard();
    startOAuthPolling();
  } catch (error) {
    state.oauth.status = "error";
    renderOAuthWizard();
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `无法准备授权：${error.message}` : `Cannot prepare authorization: ${error.message}`), "error");
    if (String(error.message).includes("401")) navigate("settings");
  }
}

function openAuthorizationPage() {
  if (!state.oauth.authorizeUrl) return;
  const width = 920;
  const height = 760;
  const left = Math.max(0, Math.round((window.screenX || 0) + (window.outerWidth - width) / 2));
  const top = Math.max(0, Math.round((window.screenY || 0) + (window.outerHeight - height) / 2));
  const popup = window.open(state.oauth.authorizeUrl, "codearts2api-oauth", `popup=yes,width=${width},height=${height},left=${left},top=${top},resizable=yes,scrollbars=yes`);
  if (!popup) {
    toast(tr("Popup blocked. Copy the authorization address and open it manually."), "error");
    return;
  }
  popup.focus();
}

async function refreshAccount(accountId) {
  try {
    const status = await apiFetch(`/api/ui/accounts/${encodeURIComponent(accountId)}/refresh`, { method: "POST" });
    toast(`${status.user_name || shortId(status.user_id) || tr("CodeArts account")} · ${tr("CodeArts credentials refreshed")}`, "success");
    await loadAccounts();
  } catch (error) {
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `刷新失败：${error.message}` : `Refresh failed: ${error.message}`), "error");
  }
}

async function deleteAccount(accountId) {
  try {
    await apiFetch(`/api/ui/accounts/${encodeURIComponent(accountId)}`, { method: "DELETE" });
    toast(tr("Account removed"), "success");
    await loadAccounts();
  } catch (error) {
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `删除账号失败：${error.message}` : `Remove account failed: ${error.message}`), "error");
  }
}

function updateApiKeySettings() {
  const key = getLocalKey();
  $("localApiKey").value = key;
  if (!state.meta?.api_key_enabled) {
    $("apiKeySettingStatus").textContent = tr("Server-side API key protection is disabled.");
  } else if (key) {
    $("apiKeySettingStatus").textContent = tr("A key is stored in this browser.");
  } else {
    $("apiKeySettingStatus").textContent = tr("Server requires a key; none is stored in this browser.");
  }
}

async function saveApiKey() {
  const key = $("localApiKey").value.trim();
  if (key) localStorage.setItem("codearts2api.apiKey", key);
  else localStorage.removeItem("codearts2api.apiKey");
  updateApiKeySettings();
  await Promise.all([loadAccounts(), loadModels(), loadLogs()]);
  toast(tr("Local API key saved"), "success");
}

async function clearApiKey() {
  localStorage.removeItem("codearts2api.apiKey");
  $("localApiKey").value = "";
  updateApiKeySettings();
  if (state.meta?.api_key_enabled) $("apiKeyBanner").classList.remove("hidden");
  toast(tr("Browser API key cleared"));
}

function streamLabel(text) {
  if (text === "Streaming") return window.codeartsI18n.getLanguage() === "zh-CN" ? "正在输出" : "Streaming";
  return tr(text);
}

function setStreamState(mode, text) {
  state.streamMode = mode;
  state.streamState = text;
  const dot = $("streamDot");
  dot.className = `status-dot ${mode}`.trim();
  $("streamState").textContent = streamLabel(text);
}

function consumeSseEvent(raw) {
  const data = raw.split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n");
  if (!data || data === "[DONE]") return data === "[DONE]";
  let payload;
  try { payload = JSON.parse(data); } catch (_) { return false; }
  if (payload.error) throw new Error(payload.error.message || tr("Upstream error"));
  const delta = payload.choices?.[0]?.delta?.content;
  if (typeof delta === "string") {
    state.responseText += delta;
    $("responseOutput").textContent = state.responseText;
  }
  return false;
}

async function sendPlayground() {
  if (state.sending) return;
  const prompt = $("userPrompt").value.trim();
  if (!prompt) {
    toast(tr("Enter a message first"), "error");
    return;
  }

  const messages = [];
  const systemPrompt = $("systemPrompt").value.trim();
  if (systemPrompt) messages.push({ role: "system", content: systemPrompt });
  messages.push({ role: "user", content: prompt });

  state.sending = true;
  state.responseText = "";
  state.lastError = "";
  const sendBtn = $("sendBtn");
  sendBtn.disabled = true;
  sendBtn.textContent = tr("Streaming…");
  $("responseOutput").classList.remove("empty");
  $("responseOutput").textContent = "";
  $("responseTime").textContent = "—";
  setStreamState("busy", "Streaming");
  const started = performance.now();

  try {
    const response = await fetch("/v1/chat/completions", {
      method: "POST",
      headers: { ...authHeaders(true) },
      body: JSON.stringify({ model: $("modelSelect").value || "codearts-auto", messages, stream: true }),
    });
    if (!response.ok) {
      let detail = `${response.status} ${response.statusText}`;
      try { detail = (await response.json()).detail || detail; } catch (_) {}
      throw new Error(detail);
    }
    if (!response.body) throw new Error(tr("Streaming response body is unavailable"));

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let done = false;
    while (!done) {
      const chunk = await reader.read();
      done = chunk.done;
      buffer += decoder.decode(chunk.value || new Uint8Array(), { stream: !done });
      const events = buffer.split(/\r?\n\r?\n/);
      buffer = events.pop() || "";
      for (const event of events) {
        if (consumeSseEvent(event)) {
          done = true;
          break;
        }
      }
    }
    if (buffer.trim()) consumeSseEvent(buffer);
    setStreamState("online", "Completed");
    $("responseTime").textContent = `${Math.round(performance.now() - started)} ms total`;
    if (!state.responseText) $("responseOutput").textContent = tr("Completed with an empty response.");
    await loadLogs();
  } catch (error) {
    setStreamState("error", "Failed");
    state.lastError = error.message;
    $("responseOutput").textContent = window.codeartsI18n.getLanguage() === "zh-CN" ? `错误：${error.message}` : `Error: ${error.message}`;
    $("responseTime").textContent = `${Math.round(performance.now() - started)} ms`;
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `对话失败：${error.message}` : `Chat failed: ${error.message}`), "error");
  } finally {
    state.sending = false;
    sendBtn.disabled = false;
    sendBtn.innerHTML = `<svg viewBox="0 0 24 24"><path d="m4 4 17 8-17 8 3-8-3-8Z"/><path d="M7 12h14"/></svg> ${tr("Send")}`;
  }
}

async function copyText(value, label = null) {
  try {
    await navigator.clipboard.writeText(value);
    toast(label || tr("Copied"), "success");
  } catch (_) {
    toast(tr("Clipboard access failed"), "error");
  }
}

document.querySelectorAll(".nav-item").forEach((item) => item.addEventListener("click", () => navigate(item.dataset.view)));
document.querySelectorAll("[data-goto]").forEach((item) => item.addEventListener("click", () => navigate(item.dataset.goto)));
document.querySelectorAll("[data-copy-target]").forEach((item) => item.addEventListener("click", () => copyText($(item.dataset.copyTarget).textContent)));

$("loginBtn").addEventListener("click", openLoginModal);
$("topLoginBtn").addEventListener("click", openLoginModal);
$("closeLoginModalBtn").addEventListener("click", closeLoginModal);
$("loginModal").addEventListener("click", (event) => { if (event.target === $("loginModal")) closeLoginModal(); });
document.querySelectorAll("[data-login-environment]").forEach((button) => button.addEventListener("click", () => prepareLogin(button.dataset.loginEnvironment)));
$("loginBackBtn").addEventListener("click", resetLoginWizard);
$("copyAuthorizeUrlBtn").addEventListener("click", () => copyText(state.oauth.authorizeUrl, tr("Authorization address copied")));
$("openAuthorizeUrlBtn").addEventListener("click", openAuthorizationPage);
$("oauthDoneBtn").addEventListener("click", closeLoginModal);
$("accountList").addEventListener("click", (event) => {
  const button = event.target.closest("[data-account-action]");
  if (!button) return;
  const accountId = button.dataset.accountId;
  const action = button.dataset.accountAction;
  if (action === "refresh") refreshAccount(accountId);
  else if (action === "delete") deleteAccount(accountId);
});
$("routingStrategy").addEventListener("change", updateRouting);
$("refreshAllBtn").addEventListener("click", loadAll);
$("refreshLogsBtn").addEventListener("click", loadLogs);
$("saveApiKeyBtn").addEventListener("click", saveApiKey);
$("clearApiKeyBtn").addEventListener("click", clearApiKey);
$("sendBtn").addEventListener("click", sendPlayground);
$("copyResponseBtn").addEventListener("click", () => copyText(state.responseText || $("responseOutput").textContent));
$("copyPythonBtn").addEventListener("click", () => copyText($("pythonSnippet").textContent));
$("userPrompt").addEventListener("keydown", (event) => {
  if (event.key === "Enter" && event.ctrlKey) {
    event.preventDefault();
    sendPlayground();
  }
});

async function handleOAuthResult(payload) {
  if (!payload || payload.type !== "codearts2api:oauth-complete") return;
  if (payload.success) {
    await loadAccounts();
    const account = state.accounts.find((item) => item.id === payload.account_id) || null;
    if (state.oauth.accountId && (!payload.account_id || payload.account_id === state.oauth.accountId)) {
      completeOAuthWizard(account);
    } else {
      toast(`${environmentLabel(payload.environment)} ${tr("Login successful")}`, "success");
    }
  } else {
    if (state.oauth.accountId) {
      state.oauth.status = "error";
      renderOAuthWizard();
      stopOAuthPolling();
    }
    toast(`${payload.environment ? `${environmentLabel(payload.environment)} ` : ""}${tr("Login failed")}`, "error");
  }
}

window.addEventListener("message", async (event) => {
  if (event.origin !== location.origin) return;
  await handleOAuthResult(event.data);
});

window.addEventListener("storage", async (event) => {
  if (event.key !== "codearts2api.oauthEvent" || !event.newValue) return;
  try { await handleOAuthResult(JSON.parse(event.newValue)); } catch (_) {}
});

window.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !$("loginModal").classList.contains("hidden")) closeLoginModal();
});

window.addEventListener("codearts2api:languagechange", () => {
  const activeView = document.querySelector(".nav-item.active")?.dataset.view || "dashboard";
  navigate(activeView);
  if (state.runtimeOk !== null) setRuntime(state.runtimeOk);
  if (state.meta) {
    $("apiKeyState").textContent = state.meta.api_key_enabled ? tr("Enabled") : tr("Disabled");
    updateSnippets();
    updateApiKeySettings();
  }
  renderAccounts();
  renderOAuthWizard();
  if (state.models.length) renderModels(state.models);
  renderLogs(state.logs);
  setStreamState(state.streamMode, state.streamState);
  if (!state.sending) {
    $("sendBtn").innerHTML = `<svg viewBox="0 0 24 24"><path d="m4 4 17 8-17 8 3-8-3-8Z"/><path d="M7 12h14"/></svg> ${tr("Send")}`;
    if (state.lastError) $("responseOutput").textContent = window.codeartsI18n.getLanguage() === "zh-CN" ? `错误：${state.lastError}` : `Error: ${state.lastError}`;
    else if (!state.responseText && state.streamState === "Completed") $("responseOutput").textContent = tr("Completed with an empty response.");
    else if (!state.responseText && state.streamState === "Idle") $("responseOutput").textContent = tr("Response will stream here.");
  }
});

window.codeartsI18n.setLanguage(window.codeartsI18n.getLanguage(), false);
loadAll();
setInterval(() => {
  if ($("view-logs").classList.contains("active")) loadLogs();
}, 5000);
