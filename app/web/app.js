const $ = (id) => document.getElementById(id);

const state = {
  meta: null,
  auth: null,
  models: [],
  responseText: "",
  sending: false,
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
  $("runtimeDot").className = `status-dot ${ok ? "online" : "error"}`;
  $("runtimeText").textContent = ok ? "Gateway running" : "Gateway unavailable";
  $("gatewayStatus").textContent = ok ? "Online" : "Offline";
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
  if (diff <= 0) return "Expired";
  const mins = Math.round(diff / 60000);
  if (mins < 60) return `${mins} min`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours} hr`;
  return date.toLocaleString();
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
  $("configAuth").textContent = protectedApi ? "Bearer <local API key>" : "None (localhost)";
}

async function loadMeta() {
  try {
    state.meta = await apiFetch("/api/ui/meta");
    setRuntime(true);
    $("versionText").textContent = `v${state.meta.version}`;
    $("settingsVersion").textContent = state.meta.version;
    $("settingsUpstream").textContent = state.meta.upstream;
    $("settingsCallback").textContent = state.meta.callback_url;
    $("upstreamHost").textContent = new URL(state.meta.upstream).host;
    $("apiKeyState").textContent = state.meta.api_key_enabled ? "Enabled" : "Disabled";
    $("apiKeyBanner").classList.toggle("hidden", !(state.meta.api_key_enabled && !getLocalKey()));
    updateSnippets();
    updateApiKeySettings();
  } catch (error) {
    setRuntime(false);
    toast(`Gateway metadata failed: ${error.message}`, "error");
  }
}

function renderAuth(status) {
  state.auth = status;
  const loggedIn = Boolean(status?.authenticated);
  const pill = $("authPill");
  pill.className = `pill ${loggedIn ? "success" : "neutral"}`;
  pill.textContent = loggedIn ? "Connected" : "Not connected";

  $("accountName").textContent = loggedIn ? (status.user_name || shortId(status.user_id) || "CodeArts account") : "Not connected";
  $("accountMeta").textContent = loggedIn
    ? (status.domain_id ? `Domain ${shortId(status.domain_id)}` : "Huawei Cloud credentials active")
    : "Sign in with your Huawei Cloud account";
  $("accountAvatar").textContent = loggedIn ? ((status.user_name || "CA").slice(0, 2).toUpperCase()) : "CA";
  $("authSource").textContent = status?.source || "—";
  $("authExpiry").textContent = expiryLabel(status?.expires_at);
  $("authExpiry").title = status?.expires_at || "";
  $("authRefresh").textContent = status?.refresh_available ? "Available" : "—";

  $("loginBtn").classList.toggle("hidden", loggedIn);
  $("topLoginBtn").classList.toggle("hidden", loggedIn);
  $("refreshTokenBtn").classList.toggle("hidden", !loggedIn || !status.refresh_available);
  $("logoutBtn").classList.toggle("hidden", !loggedIn);
}

async function loadAuth() {
  try {
    renderAuth(await apiFetch("/auth/status"));
    $("apiKeyBanner").classList.add("hidden");
  } catch (error) {
    if (String(error.message).includes("401")) {
      renderAuth({ authenticated: false, source: "locked" });
      return;
    }
    toast(`Auth status failed: ${error.message}`, "error");
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
    empty.textContent = "No models configured.";
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
    owner.textContent = `owned by ${model.owned_by || "codearts"}`;
    copy.append(name, owner);
    info.append(symbol, copy);
    const badge = document.createElement("span");
    badge.className = "pill neutral";
    badge.textContent = model.id === "codearts-auto" ? "Default" : "Model";
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
    if (!String(error.message).includes("401")) toast(`Models failed: ${error.message}`, "error");
  }
}

function renderLogs(items) {
  const body = $("logsBody");
  body.replaceChildren();
  if (!items.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 5;
    cell.className = "empty-cell";
    cell.textContent = "No requests yet.";
    row.appendChild(cell);
    body.appendChild(row);
    return;
  }
  items.forEach((item) => {
    const row = document.createElement("tr");
    const time = document.createElement("td");
    time.textContent = new Date(item.time).toLocaleTimeString();
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
    if (!String(error.message).includes("401")) toast(`Logs failed: ${error.message}`, "error");
  }
}

async function loadAll() {
  await loadMeta();
  await Promise.all([loadAuth(), loadModels(), loadLogs()]);
}

function navigate(view) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.view === view));
  document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
  const [eyebrow, title] = pageMeta[view] || pageMeta.dashboard;
  $("pageEyebrow").textContent = eyebrow;
  $("pageTitle").textContent = title;
  if (view === "logs") loadLogs();
}

async function login() {
  try {
    const body = await apiFetch("/auth/login-url");
    location.href = body.authorize_url;
  } catch (error) {
    toast(`Cannot start login: ${error.message}`, "error");
    if (String(error.message).includes("401")) navigate("settings");
  }
}

async function refreshToken() {
  const btn = $("refreshTokenBtn");
  btn.disabled = true;
  btn.textContent = "Refreshing…";
  try {
    const status = await apiFetch("/auth/refresh", { method: "POST" });
    renderAuth(status);
    toast("CodeArts credentials refreshed", "success");
  } catch (error) {
    toast(`Refresh failed: ${error.message}`, "error");
  } finally {
    btn.disabled = false;
    btn.textContent = "Refresh token";
  }
}

async function logout() {
  try {
    await apiFetch("/auth/logout", { method: "POST" });
    renderAuth({ authenticated: false, source: "none" });
    toast("Logged out", "success");
  } catch (error) {
    toast(`Logout failed: ${error.message}`, "error");
  }
}

function updateApiKeySettings() {
  const key = getLocalKey();
  $("localApiKey").value = key;
  if (!state.meta?.api_key_enabled) {
    $("apiKeySettingStatus").textContent = "Server-side API key protection is disabled.";
  } else if (key) {
    $("apiKeySettingStatus").textContent = "A key is stored in this browser.";
  } else {
    $("apiKeySettingStatus").textContent = "Server requires a key; none is stored in this browser.";
  }
}

async function saveApiKey() {
  const key = $("localApiKey").value.trim();
  if (key) localStorage.setItem("codearts2api.apiKey", key);
  else localStorage.removeItem("codearts2api.apiKey");
  updateApiKeySettings();
  await Promise.all([loadAuth(), loadModels(), loadLogs()]);
  toast("Local API key saved", "success");
}

async function clearApiKey() {
  localStorage.removeItem("codearts2api.apiKey");
  $("localApiKey").value = "";
  updateApiKeySettings();
  if (state.meta?.api_key_enabled) $("apiKeyBanner").classList.remove("hidden");
  toast("Browser API key cleared");
}

function setStreamState(mode, text) {
  const dot = $("streamDot");
  dot.className = `status-dot ${mode}`.trim();
  $("streamState").textContent = text;
}

function consumeSseEvent(raw) {
  const data = raw.split(/\r?\n/)
    .filter((line) => line.startsWith("data:"))
    .map((line) => line.slice(5).trimStart())
    .join("\n");
  if (!data || data === "[DONE]") return data === "[DONE]";
  let payload;
  try { payload = JSON.parse(data); } catch (_) { return false; }
  if (payload.error) throw new Error(payload.error.message || "Upstream error");
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
    toast("先输入一条消息", "error");
    return;
  }

  const messages = [];
  const systemPrompt = $("systemPrompt").value.trim();
  if (systemPrompt) messages.push({ role: "system", content: systemPrompt });
  messages.push({ role: "user", content: prompt });

  state.sending = true;
  state.responseText = "";
  const sendBtn = $("sendBtn");
  sendBtn.disabled = true;
  sendBtn.textContent = "Streaming…";
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
    if (!response.body) throw new Error("Streaming response body is unavailable");

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
    if (!state.responseText) $("responseOutput").textContent = "Completed with an empty response.";
    await loadLogs();
  } catch (error) {
    setStreamState("error", "Failed");
    $("responseOutput").textContent = `Error: ${error.message}`;
    $("responseTime").textContent = `${Math.round(performance.now() - started)} ms`;
    toast(`Chat failed: ${error.message}`, "error");
  } finally {
    state.sending = false;
    sendBtn.disabled = false;
    sendBtn.innerHTML = '<svg viewBox="0 0 24 24"><path d="m4 4 17 8-17 8 3-8-3-8Z"/><path d="M7 12h14"/></svg> Send';
  }
}

async function copyText(value, label = "Copied") {
  try {
    await navigator.clipboard.writeText(value);
    toast(label, "success");
  } catch (_) {
    toast("Clipboard access failed", "error");
  }
}

document.querySelectorAll(".nav-item").forEach((item) => item.addEventListener("click", () => navigate(item.dataset.view)));
document.querySelectorAll("[data-goto]").forEach((item) => item.addEventListener("click", () => navigate(item.dataset.goto)));
document.querySelectorAll("[data-copy-target]").forEach((item) => item.addEventListener("click", () => copyText($(item.dataset.copyTarget).textContent)));

$("loginBtn").addEventListener("click", login);
$("topLoginBtn").addEventListener("click", login);
$("refreshTokenBtn").addEventListener("click", refreshToken);
$("logoutBtn").addEventListener("click", logout);
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

loadAll();
setInterval(() => {
  if ($("view-logs").classList.contains("active")) loadLogs();
}, 5000);
