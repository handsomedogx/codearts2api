const $ = (id) => document.getElementById(id);
const tr = (text, vars = {}) => window.codeartsI18n.tr(text, vars);

const state = {
  meta: null,
  accounts: [],
  routing: { strategy: "round_robin" },
  models: [],
  logs: [],
  usage: null,
  usageDays: 3,
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
  if (strategy === "weighted") return tr("Weighted");
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

function createAccountAction(label, action, accountId, extraClass = "ghost") {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `button ${extraClass} small account-action-button`.trim();
  button.textContent = label;
  button.dataset.accountAction = action;
  button.dataset.accountId = accountId;
  return button;
}

function relativeTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  const seconds = Math.max(0, Math.round((Date.now() - date.getTime()) / 1000));
  const zh = window.codeartsI18n.getLanguage() === "zh-CN";
  if (seconds < 45) return tr("Just now");
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return zh ? `${minutes} 分钟前` : `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return zh ? `${hours} 小时前` : `${hours} hr ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return zh ? `${days} 天前` : `${days} d ago`;
  return date.toLocaleDateString(zh ? "zh-CN" : "en-US");
}

function formatLatency(value) {
  if (!Number.isFinite(value) || value < 0) return "—";
  if (value < 1000) return `${Math.round(value)} ms`;
  const seconds = value / 1000;
  return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)} s`;
}

function formatCount(value) {
  if (!Number.isFinite(value)) return "—";
  return new Intl.NumberFormat(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US", {
    notation: value >= 10000 ? "compact" : "standard",
    maximumFractionDigits: 1,
  }).format(value);
}

function formatTokenCount(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) return "—";
  const abs = Math.abs(number);
  if (abs >= 1e9) return `${(number / 1e9).toFixed(abs >= 1e10 ? 1 : 2).replace(/\.0+$|(?<=\.[0-9])0+$/g, "")}G`;
  if (abs >= 1e6) return `${(number / 1e6).toFixed(abs >= 1e8 ? 1 : 2).replace(/\.0+$|(?<=\.[0-9])0+$/g, "")}M`;
  if (abs >= 1e3) return `${(number / 1e3).toFixed(abs >= 1e5 ? 1 : 2).replace(/\.0+$|(?<=\.[0-9])0+$/g, "")}K`;
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 }).format(number);
}

function quotaForAccount(accountId) {
  const items = state.usage?.official_quota?.items || [];
  return items.find((item) => item.account_id === accountId) || null;
}

function createMetricBadge(text, className = "") {
  const badge = document.createElement("span");
  badge.className = `account-metric-badge ${className}`.trim();
  badge.textContent = text;
  return badge;
}

function accountStatusPill(account) {
  const status = document.createElement("span");
  const healthStatus = account.health?.status || "healthy";
  if (!account.authenticated) {
    status.className = "pill neutral";
    status.textContent = tr("Not connected");
  } else if (account.health?.needs_relogin) {
    status.className = "pill danger";
    status.textContent = tr("Relogin required");
  } else if (healthStatus === "cooldown") {
    status.className = "pill danger";
    status.textContent = tr("Cooldown");
    if (account.health?.cooldown_until) status.title = `${tr("Cooldown until")} ${account.health.cooldown_until}`;
  } else if (healthStatus === "degraded") {
    status.className = "pill warning";
    status.textContent = tr("Degraded");
  } else {
    status.className = "pill success";
    status.textContent = tr("Available");
  }
  return status;
}

function renderAccounts() {
  const list = $("accountList");
  list.replaceChildren();
  $("accountCount").textContent = String(state.accounts.length);
  $("routingPill").textContent = routingLabel(state.routing.strategy);
  $("routingSummary").textContent = `${routingLabel(state.routing.strategy)} ${tr("routing")}`;
  $("routingStrategy").value = state.routing.strategy;

  if (!state.accounts.length) {
    const row = document.createElement("tr");
    const cell = document.createElement("td");
    cell.colSpan = 7;
    const empty = document.createElement("div");
    empty.className = "account-pool-empty";
    const title = document.createElement("strong");
    title.textContent = tr("No accounts yet");
    const hint = document.createElement("span");
    hint.textContent = tr("Add a China or International CodeArts account to start routing requests.");
    empty.append(title, hint);
    cell.appendChild(empty);
    row.appendChild(cell);
    list.appendChild(row);
    return;
  }

  state.accounts.forEach((account) => {
    const row = document.createElement("tr");
    row.className = `account-table-row health-${account.health?.status || "healthy"}`;

    const identityCell = document.createElement("td");
    const identity = document.createElement("div");
    identity.className = "account-identity";
    const name = document.createElement("span");
    name.className = "account-identity-name";
    name.textContent = account.user_name || shortId(account.user_id) || tr("CodeArts account");
    const meta = document.createElement("div");
    meta.className = "account-identity-meta";
    const env = document.createElement("span");
    env.className = "account-env-tag";
    env.textContent = account.environment === "international" ? "INTL" : "CN";
    const id = document.createElement("span");
    id.textContent = shortId(account.id);
    id.title = account.id;
    meta.append(env, id);
    identity.append(name, meta);
    identityCell.appendChild(identity);

    const statusCell = document.createElement("td");
    statusCell.appendChild(accountStatusPill(account));

    const resultCell = document.createElement("td");
    resultCell.className = "account-result-cell";
    const success = document.createElement("span");
    success.className = "account-success-count";
    success.textContent = formatCount(Number(account.stats?.success_count ?? account.health?.total_successes ?? 0));
    const divider = document.createElement("span");
    divider.className = "account-count-divider";
    divider.textContent = "/";
    const failure = document.createElement("span");
    failure.className = "account-failure-count";
    failure.textContent = formatCount(Number(account.stats?.failure_count ?? account.health?.total_failures ?? 0));
    resultCell.append(success, divider, failure);

    const inFlightCell = document.createElement("td");
    inFlightCell.className = "account-inflight-cell";
    const concurrency = Number(account.stats?.concurrency ?? account.stats?.in_flight ?? 0);
    const maxConcurrency = Number(account.stats?.max_concurrency || 0);
    inFlightCell.textContent = maxConcurrency > 0 ? `${formatCount(concurrency)} / ${formatCount(maxConcurrency)}` : formatCount(concurrency);

    const activityCell = document.createElement("td");
    const metrics = document.createElement("div");
    metrics.className = "account-metrics";
    const requestCount = Number(account.stats?.request_count || 0);
    if (requestCount > 0) metrics.appendChild(createMetricBadge(`${formatCount(requestCount)} ${tr("requests")}`, "requests"));
    if (Number.isFinite(account.stats?.avg_latency_ms)) metrics.appendChild(createMetricBadge(`${formatLatency(account.stats.avg_latency_ms)} ${tr("avg")}`, "latency"));
    if (Number.isFinite(account.stats?.total_tokens) && account.stats.total_tokens > 0) metrics.appendChild(createMetricBadge(`${formatTokenCount(account.stats.total_tokens)} tok`, "tokens"));
    if (Number.isFinite(account.stats?.avg_output_tps) && account.stats.avg_output_tps > 0) metrics.appendChild(createMetricBadge(`${account.stats.avg_output_tps.toFixed(1)} tok/s`, "throughput"));
    const quota = quotaForAccount(account.id);
    if (quota?.available) {
      const quotaRow = document.createElement("div");
      quotaRow.className = `account-quota${quota.over_limit ? " over" : ""}`;
      const quotaBar = document.createElement("span");
      quotaBar.className = "account-quota-bar";
      const quotaFill = document.createElement("span");
      quotaFill.style.width = `${Math.min(100, Number(quota.progress_percent || 0))}%`;
      quotaBar.appendChild(quotaFill);
      const quotaText = document.createElement("span");
      quotaText.textContent = `${tr("Official")} ${formatTokenCount(quota.used_tokens)} / ${formatTokenCount(quota.quota_tokens)}`;
      quotaRow.append(quotaBar, quotaText);
      metrics.appendChild(quotaRow);
    }
    if (!metrics.childElementCount) {
      const emptyMetric = document.createElement("span");
      emptyMetric.className = "account-empty-value";
      emptyMetric.textContent = "—";
      metrics.appendChild(emptyMetric);
    }
    activityCell.appendChild(metrics);

    const recentCell = document.createElement("td");
    recentCell.className = "account-recent-cell";
    recentCell.textContent = relativeTime(account.health?.last_success_at);
    if (account.health?.last_success_at) recentCell.title = account.health.last_success_at;

    const actionCell = document.createElement("td");
    const actions = document.createElement("div");
    actions.className = "account-row-actions";
    actions.appendChild(createAccountAction(tr("Probe"), "probe", account.id));
    if (account.refresh_available) actions.appendChild(createAccountAction(tr("Refresh"), "refresh", account.id));
    actions.appendChild(createAccountAction(tr("Remove"), "delete", account.id, "danger ghost"));
    actionCell.appendChild(actions);

    row.append(identityCell, statusCell, resultCell, inFlightCell, activityCell, recentCell, actionCell);
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
    const limits = [
      model.context_window ? `${formatTokenCount(model.context_window)} ${tr("context")}` : "",
      model.input_length ? `${formatTokenCount(model.input_length)} ${tr("input")}` : "",
      model.max_tokens ? `${formatTokenCount(model.max_tokens)} ${tr("max output")}` : "",
    ].filter(Boolean);
    if (limits.length) {
      const limitLine = document.createElement("div");
      limitLine.className = "model-limits";
      limitLine.textContent = limits.join(" · ");
      copy.appendChild(limitLine);
    }
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

function renderOfficialQuota(official) {
  const panel = $("officialQuotaPanel");
  panel.classList.toggle("over-limit", Boolean(official?.over_limit));
  const bar = $("officialQuotaBar");
  if (!official?.available) {
    $("officialQuotaTitle").textContent = tr("Official quota unavailable");
    $("officialQuotaMeta").textContent = tr("Package usage is read from CodeArts directly.");
    $("officialQuotaPercent").textContent = "—";
    bar.style.width = "0%";
    return;
  }
  const packages = (official.package_names || []).join(" + ") || tr("CodeArts package");
  $("officialQuotaTitle").textContent = `${formatTokenCount(official.used_tokens)} / ${formatTokenCount(official.quota_tokens)} tok · ${packages}`;
  const periodItems = official.items || [];
  const starts = periodItems.map((item) => item.start_date).filter(Boolean).sort();
  const ends = periodItems.map((item) => item.end_date).filter(Boolean).sort();
  const period = starts.length && ends.length ? `${starts[0]} → ${ends[ends.length - 1]}` : "";
  const accountText = `${official.account_count} ${tr(official.account_count === 1 ? "account" : "accounts")}`;
  const limitText = official.over_limit ? tr("Package reference quota exceeded; CodeArts may still allow usage.") : tr("Official CodeArts package usage.");
  $("officialQuotaMeta").textContent = [accountText, period, limitText].filter(Boolean).join(" · ");
  $("officialQuotaPercent").textContent = Number.isFinite(official.usage_percent) ? `${official.usage_percent.toLocaleString("en-US", { maximumFractionDigits: 1 })}%` : "—";
  bar.style.width = `${Math.min(100, Number(official.progress_percent || 0))}%`;
}

function renderUsageChart(usage) {
  const canvas = $("usageChart");
  const empty = $("usageChartEmpty");
  const buckets = usage?.buckets || [];
  const hasData = buckets.some((bucket) => Number(bucket.total_tokens || 0) > 0 || Number(bucket.requests || 0) > 0);
  empty.classList.toggle("hidden", hasData);
  const cssWidth = Math.max(320, canvas.parentElement.clientWidth - 20);
  const cssHeight = 250;
  const dpr = Math.max(1, window.devicePixelRatio || 1);
  canvas.width = Math.floor(cssWidth * dpr);
  canvas.height = Math.floor(cssHeight * dpr);
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, cssWidth, cssHeight);

  const left = 54, right = 12, top = 14, bottom = 28;
  const plotW = Math.max(1, cssWidth - left - right);
  const plotH = Math.max(1, cssHeight - top - bottom);
  const maxValue = Math.max(1, ...buckets.map((item) => Number(item.total_tokens || 0)), Number(usage?.average_bucket_tokens || 0));
  const axisMax = maxValue * 1.08;
  ctx.font = "9px ui-monospace, SFMono-Regular, Consolas, monospace";
  ctx.textAlign = "right";
  ctx.textBaseline = "middle";
  for (let i = 0; i <= 4; i += 1) {
    const y = top + plotH * (i / 4);
    const value = axisMax * (1 - i / 4);
    ctx.strokeStyle = "#202530";
    ctx.lineWidth = 1;
    ctx.beginPath(); ctx.moveTo(left, y); ctx.lineTo(cssWidth - right, y); ctx.stroke();
    ctx.fillStyle = "#61697a";
    ctx.fillText(formatTokenCount(value), left - 8, y);
  }

  if (buckets.length) {
    const slot = plotW / buckets.length;
    const barW = Math.max(2, Math.min(18, slot * 0.66));
    buckets.forEach((bucket, index) => {
      const x = left + slot * index + (slot - barW) / 2;
      const prompt = Number(bucket.prompt_tokens || 0);
      const completion = Number(bucket.completion_tokens || 0);
      const promptH = prompt / axisMax * plotH;
      const completionH = completion / axisMax * plotH;
      const baseY = top + plotH;
      if (promptH > 0) {
        ctx.fillStyle = "#607cf0";
        ctx.fillRect(x, baseY - promptH, barW, promptH);
      }
      if (completionH > 0) {
        ctx.fillStyle = "#43c793";
        ctx.fillRect(x, baseY - promptH - completionH, barW, Math.max(1, completionH));
      }
    });

    const avg = Number(usage?.average_bucket_tokens || 0);
    if (avg > 0) {
      const y = top + plotH - avg / axisMax * plotH;
      ctx.strokeStyle = "#d49b44";
      ctx.setLineDash([5, 5]);
      ctx.beginPath(); ctx.moveTo(left, y); ctx.lineTo(cssWidth - right, y); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = "#c7964b";
      ctx.textAlign = "left";
      ctx.fillText(`${tr("avg")} ${formatTokenCount(avg)}`, left + 4, Math.max(top + 7, y - 8));
    }

    const tickIndices = [...new Set([0, Math.floor((buckets.length - 1) / 4), Math.floor((buckets.length - 1) / 2), Math.floor((buckets.length - 1) * 3 / 4), buckets.length - 1])];
    ctx.fillStyle = "#61697a";
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    tickIndices.forEach((index) => {
      const date = new Date(buckets[index].start);
      const label = Number(usage?.range_days || 3) <= 3
        ? date.toLocaleString(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US", { weekday: "short", hour: "2-digit", minute: "2-digit" })
        : date.toLocaleDateString(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US", { month: "short", day: "numeric" });
      ctx.fillText(label, left + slot * index + slot / 2, top + plotH + 8);
    });
  }
}

function renderUsage() {
  const usage = state.usage;
  if (!usage) return;
  $("usageRequests").textContent = formatCount(Number(usage.request_count || 0));
  $("usageRequestSub").textContent = `${formatCount(Number(usage.failure_count || 0))} ${tr("failures")}`;
  $("usageTotalTokens").textContent = formatTokenCount(usage.total_tokens);
  $("usageTokenShare").textContent = `prompt ${Number(usage.prompt_share || 0).toFixed(1)}% · completion ${Number(usage.completion_share || 0).toFixed(1)}%`;
  $("usagePromptTokens").textContent = formatTokenCount(usage.prompt_tokens);
  $("usagePromptShare").textContent = `${tr("share")} ${Number(usage.prompt_share || 0).toFixed(1)}%`;
  $("usageCompletionTokens").textContent = formatTokenCount(usage.completion_tokens);
  $("usageCompletionShare").textContent = `${tr("share")} ${Number(usage.completion_share || 0).toFixed(1)}%`;
  $("usageFailures").textContent = formatCount(Number(usage.failure_count || 0));
  $("usageSuccessRate").textContent = `${Number(usage.success_rate || 0).toFixed(1)}% ${tr("success")}`;
  $("usageLatency").textContent = formatLatency(Number(usage.avg_latency_ms || 0));
  $("usageThroughput").textContent = `${Number(usage.avg_output_tps || 0).toFixed(1)} tok/s`;
  $("usageRangeMeta").textContent = `${tr("Last {days} days", { days: usage.range_days })} · ${usage.points} ${tr("points")}`;
  const peakTime = usage.peak_at ? new Date(usage.peak_at).toLocaleString(window.codeartsI18n.getLanguage() === "zh-CN" ? "zh-CN" : "en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) : "—";
  $("usageChartMeta").textContent = `${usage.points} ${tr("points")} · ${tr("peak")} ${formatTokenCount(usage.peak_tokens)} @ ${peakTime} · ${tr("average")} ${formatTokenCount(usage.average_bucket_tokens)}`;
  renderOfficialQuota(usage.official_quota);
  renderUsageChart(usage);
  renderAccounts();
}

async function loadUsage(forceQuota = false) {
  try {
    const suffix = forceQuota ? "&refresh_quota=true" : "";
    state.usage = await apiFetch(`/api/ui/usage?days=${state.usageDays}${suffix}`);
    renderUsage();
  } catch (error) {
    if (!String(error.message).includes("401")) toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `获取用量失败：${error.message}` : `Usage failed: ${error.message}`), "error");
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
  await Promise.all([loadAccounts(), loadModels(), loadLogs(), loadUsage(false)]);
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

async function probeAccount(accountId) {
  try {
    const status = await apiFetch(`/api/ui/accounts/${encodeURIComponent(accountId)}/probe`, { method: "POST" });
    const ok = status.health?.probe_status === "ok";
    toast(ok ? tr("Account probe succeeded") : tr("Account probe failed"), ok ? "success" : "error");
    await loadAccounts();
  } catch (error) {
    toast((window.codeartsI18n.getLanguage() === "zh-CN" ? `账号检测失败：${error.message}` : `Account probe failed: ${error.message}`), "error");
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
  await Promise.all([loadAccounts(), loadModels(), loadLogs(), loadUsage(false)]);
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
    await Promise.all([loadLogs(), loadAccounts(), loadUsage(false)]);
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
  if (action === "probe") probeAccount(accountId);
  else if (action === "refresh") refreshAccount(accountId);
  else if (action === "delete") deleteAccount(accountId);
});
$("routingStrategy").addEventListener("change", updateRouting);
$("refreshAllBtn").addEventListener("click", loadAll);
$("refreshLogsBtn").addEventListener("click", loadLogs);
$("refreshUsageBtn").addEventListener("click", () => loadUsage(true));
$("usageRange").addEventListener("change", () => {
  state.usageDays = Number($("usageRange").value || 3);
  loadUsage(false);
});
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
  if (state.usage) renderUsage();
  renderLogs(state.logs);
  setStreamState(state.streamMode, state.streamState);
  if (!state.sending) {
    $("sendBtn").innerHTML = `<svg viewBox="0 0 24 24"><path d="m4 4 17 8-17 8 3-8-3-8Z"/><path d="M7 12h14"/></svg> ${tr("Send")}`;
    if (state.lastError) $("responseOutput").textContent = window.codeartsI18n.getLanguage() === "zh-CN" ? `错误：${state.lastError}` : `Error: ${state.lastError}`;
    else if (!state.responseText && state.streamState === "Completed") $("responseOutput").textContent = tr("Completed with an empty response.");
    else if (!state.responseText && state.streamState === "Idle") $("responseOutput").textContent = tr("Response will stream here.");
  }
});

window.addEventListener("resize", () => { if (state.usage) renderUsageChart(state.usage); });

window.codeartsI18n.setLanguage(window.codeartsI18n.getLanguage(), false);
loadAll();
setInterval(() => {
  if ($("view-logs").classList.contains("active")) loadLogs();
}, 5000);
setInterval(() => {
  if ($("view-dashboard").classList.contains("active")) loadAccounts();
}, 2500);
