(() => {
  const STORAGE_KEY = "codearts2api.language";
  const ZH = {
    "Local Gateway":"本地网关","Dashboard":"概览","Playground":"调试台","Logs":"日志","Settings":"设置","Checking service…":"正在检查服务…",
    "OVERVIEW":"概览","TEST CONSOLE":"测试控制台","ACTIVITY":"活动","CONFIGURATION":"配置","Login CodeArts":"登录 CodeArts","Add Account":"添加账号","CODEARTS LOGIN":"CODEARTS 登录","Choose login site":"选择登录站点","Each login adds a new account to the pool. Choose the account site below.":"每次登录都会向账号池添加一个新账号，请选择账号所属站点。","Add CodeArts account":"添加 CodeArts 账号","Choose the account region to start authorization.":"选择账号所属区域以开始授权。","Authorize CodeArts account":"授权 CodeArts 账号","Copy the authorization address or open it directly, then complete sign-in on CodeArts.":"复制授权地址或直接打开，然后在 CodeArts 完成登录授权。","International CodeArts service":"国际站 CodeArts 服务","Selecting a region only prepares an authorization link. Nothing opens automatically.":"选择区域只会生成授权链接，不会自动打开任何页面。","Change region":"更换区域","Authorization address":"授权地址","Copy this address, or open it in a separate window to authorize your CodeArts account.":"复制此地址，或在独立窗口中打开以授权你的 CodeArts 账号。","Copy address":"复制地址","Open authorization page ↗":"打开授权页面 ↗","Waiting for authorization":"等待授权","Keep this window open. The account pool will update automatically after authorization succeeds.":"请保持此窗口打开。授权成功后账号池会自动更新。","Authorization complete":"授权完成","The account has been added to the pool and is ready to use.":"账号已加入账号池，可以开始使用。","Authorization failed":"授权失败","Authorization did not complete. You can reopen the authorization page and try again.":"授权未完成，你可以重新打开授权页面后再试。","Preparing authorization":"正在准备授权","Generating a secure CodeArts authorization address…":"正在生成安全的 CodeArts 授权地址…","Authorization address copied":"授权地址已复制","Popup blocked. Copy the authorization address and open it manually.":"弹窗被浏览器拦截，请复制授权地址后手动打开。","Done":"完成",
    "Local API Key is enabled":"已启用本地 API Key","Enter the local gateway API key to read account status and call models.":"输入本地网关 API Key 后才能读取账号状态和调用模型。","Configure":"配置",
    "CODEARTS ACCOUNT":"CODEARTS 账号","CODEARTS ACCOUNTS":"CODEARTS 账号","CODEARTS ACCOUNT POOL":"CODEARTS 账号池","Authentication":"身份认证","Accounts":"账号","Account Pool":"账号池","Checking":"检查中","Not connected":"未连接","Sign in with your Huawei Cloud account":"使用华为云账号登录",
    "Source":"来源","Expires":"过期时间","Refresh":"刷新","Probe":"检测","Account probe succeeded":"账号检测通过","Account probe failed":"账号检测失败","Relogin required":"需要重新登录","Refresh token":"刷新凭证","Logout":"退出登录","OPENAI COMPATIBLE":"OPENAI 兼容","API Endpoint":"API 端点","Ready":"就绪","Copy":"复制",
    "OpenAI SDK, Cherry Studio, and other clients can point directly to this endpoint.":"OpenAI SDK、Cherry Studio 等客户端可直接指向此地址。",
    "Gateway":"网关","Online":"在线","Offline":"离线","FastAPI local service":"本地 FastAPI 服务","Models":"模型","Configured aliases":"已配置别名","Upstream":"上游","Configured":"已配置","Accounts":"账号",
    "Local API Key":"本地 API Key","Gateway access protection":"网关访问保护","AVAILABLE":"可用","Open Playground →":"打开调试台 →","QUICK START":"快速开始","Client configuration":"客户端配置",
    "API format":"API 格式","Streaming":"流式传输","REQUEST":"请求","Model":"模型","System prompt":"系统提示词","optional":"可选","Message":"消息","Ctrl + Enter to send":"Ctrl + Enter 发送","Send":"发送",
    "RESPONSE":"响应","Live output":"实时输出","Idle":"空闲","Response will stream here.":"响应将在此实时显示。","Copy response":"复制响应","Request logs":"请求日志","Sanitized · no bodies or credentials":"已脱敏 · 不含请求体或凭证",
    "Time":"时间","Method":"方法","Path":"路径","Status":"状态","Duration":"耗时","Account":"账号","Success / Failure":"成功 / 失败","In flight":"在途","Concurrency":"并发","Usage":"用量","Recent success":"最近成功","Actions":"操作","Just now":"刚刚","requests":"次","avg":"均值","No requests yet.":"暂无请求。","SECURITY":"安全","API Key":"API Key","Save":"保存","Clear":"清除","Not configured in browser.":"浏览器中未配置。",
    "USAGE OVERVIEW":"用量总览","Usage Overview":"用量总览","Last 24 hours":"近 24 小时","Last 3 days":"近 3 天","Last 7 days":"近 7 天","Last 30 days":"近 30 天","Requests":"请求数","Total tokens":"总 token","Prompt":"prompt","Completion":"completion","Failed attempts":"失败尝试","Average latency":"平均延迟","failures":"次失败","success":"成功率","CODEARTS PACKAGE":"CODEARTS 套餐","Official quota unavailable":"暂未获取官方额度","Package usage is read from CodeArts directly.":"套餐用量直接读取自 CodeArts。","Token timeline":"Token 时序","No usage recorded in this range yet.":"当前时间范围暂无用量记录。","Official":"官方","context":"上下文","input":"输入","max output":"最大输出","account":"个账号","accounts":"个账号","CodeArts package":"CodeArts 套餐","Package reference quota exceeded; CodeArts may still allow usage.":"已超过套餐参考额度，但 CodeArts 仍可能继续允许使用。","Official CodeArts package usage.":"CodeArts 官方套餐用量。","Last {days} days":"近 {days} 天","points":"个点","peak":"峰值","average":"均值","share":"占比",
    "SYSTEM":"系统","Gateway details":"网关详情","Version":"版本","Account routing":"账号路由","Routing strategy":"路由策略","Environment":"环境","China":"中国站","International":"国际站","CodeArts environment":"CodeArts 环境","OAuth callback":"OAuth 回调",
    "If CODEARTS2API_API_KEY is configured in .env, enter the same key here. It is stored only in this browser's localStorage.":"如果 .env 中配置了 CODEARTS2API_API_KEY，请在这里输入相同的 Key。它只会保存在当前浏览器的 localStorage。",
    "Gateway running":"网关运行中","Gateway unavailable":"网关不可用","Enabled":"已启用","Disabled":"未启用","Connected":"已连接","Cooldown":"冷却中","Cooldown until":"冷却至","Degraded":"状态异常","CodeArts account":"CodeArts 账号","Huawei Cloud credentials active":"华为云凭证已生效","Popup blocked. Allow popups for this local page and try again.":"弹窗被浏览器拦截，请允许此本地页面打开弹窗后重试。","Login successful":"登录成功","Login failed":"登录失败","Reconnect":"重新登录","Login":"登录","Remove":"移除","Account removed":"账号已移除","Current API environment":"当前 API 环境","Active API":"当前 API","Use for API":"用于 API","Round Robin":"轮询","Random":"随机","Weighted":"智能加权","routing":"路由","Round Robin routing":"轮询路由","Requests are routed automatically across usable accounts.":"请求会自动在可用账号之间路由。","No accounts yet":"暂无账号","Add a China or International CodeArts account to start routing requests.":"添加中国站或国际站 CodeArts 账号后即可开始路由请求。",
    "Available":"可用","Expired":"已过期","No models configured.":"尚未配置模型。","Default":"默认","owned by {owner}":"提供方：{owner}","Model":"模型",
    "Server-side API key protection is disabled.":"服务端未启用 API Key 保护。","A key is stored in this browser.":"当前浏览器已保存 Key。","Server requires a key; none is stored in this browser.":"服务端要求 API Key，但当前浏览器尚未保存。",
    "Refreshing…":"正在刷新…","CodeArts credentials refreshed":"CodeArts 凭证已刷新","Logged out":"已退出登录","Local API key saved":"本地 API Key 已保存","Browser API key cleared":"浏览器中的 API Key 已清除",
    "Streaming…":"输出中…","Streaming":"正在输出","Completed":"已完成","Failed":"失败","Enter a message first":"请先输入一条消息","Streaming response body is unavailable":"流式响应正文不可用","Upstream error":"上游服务错误",
    "Completed with an empty response.":"请求已完成，但响应为空。","Copied":"已复制","Clipboard access failed":"无法访问剪贴板",
    "You are a helpful coding assistant.":"你是一个乐于助人的编程助手。","Send a message to CodeArts…":"向 CodeArts 发送一条消息…"
  };
  let language = "en";
  const originalText = new WeakMap();
  const originalAttrs = new WeakMap();
  const normalize = (lang) => lang === "zh-CN" ? "zh-CN" : "en";
  const format = (value, vars = {}) => {
    let out = value;
    Object.entries(vars).forEach(([key, replacement]) => { out = out.replaceAll(`{${key}}`, String(replacement)); });
    return out;
  };
  const tr = (source, vars = {}) => format(language === "zh-CN" ? (ZH[source] || source) : source, vars);
  const detect = () => {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (saved === "zh-CN" || saved === "en") return saved;
    const browser = (navigator.languages?.[0] || navigator.language || "en").toLowerCase();
    return browser.startsWith("zh") ? "zh-CN" : "en";
  };
  const translateDom = () => {
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const parent = node.parentElement;
      if (!parent || ["SCRIPT","STYLE","CODE","PRE"].includes(parent.tagName)) continue;
      if (!originalText.has(node)) originalText.set(node, node.nodeValue);
      const source = originalText.get(node);
      const trimmed = source.trim();
      if (!ZH[trimmed]) continue;
      const lead = source.match(/^\s*/)?.[0] || "";
      const tail = source.match(/\s*$/)?.[0] || "";
      node.nodeValue = `${lead}${tr(trimmed)}${tail}`;
    }
    const apiKeyHelp = document.getElementById("apiKeyHelp");
    if (apiKeyHelp) apiKeyHelp.innerHTML = language === "zh-CN"
      ? '如果 <code>CODEARTS2API_API_KEY</code> 配置在 <code>.env</code> 中，请在这里输入相同的 Key。它只会保存在当前浏览器的 localStorage。'
      : "If <code>CODEARTS2API_API_KEY</code> is configured in <code>.env</code>, enter the same key here. It is stored only in this browser's localStorage.";
    document.querySelectorAll("[placeholder],[title],[aria-label]").forEach((el) => {
      if (!originalAttrs.has(el)) originalAttrs.set(el, {placeholder:el.getAttribute("placeholder"),title:el.getAttribute("title"),ariaLabel:el.getAttribute("aria-label")});
      const attrs = originalAttrs.get(el);
      if (attrs.placeholder && ZH[attrs.placeholder]) el.setAttribute("placeholder", tr(attrs.placeholder));
      if (attrs.title === "Refresh") el.setAttribute("title", language === "zh-CN" ? "刷新" : "Refresh");
      if (attrs.ariaLabel === "Main navigation") el.setAttribute("aria-label", language === "zh-CN" ? "主导航" : "Main navigation");
      if (attrs.ariaLabel === "Language") el.setAttribute("aria-label", language === "zh-CN" ? "语言" : "Language");
      if (attrs.ariaLabel === "CodeArts environment") el.setAttribute("aria-label", language === "zh-CN" ? "CodeArts 环境" : "CodeArts environment");
    });
  };
  const setLanguage = (next, persist = true) => {
    language = normalize(next);
    if (persist) localStorage.setItem(STORAGE_KEY, language);
    document.documentElement.lang = language;
    translateDom();
    document.querySelectorAll("[data-language]").forEach((button) => {
      const active = button.dataset.language === language;
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
    window.dispatchEvent(new CustomEvent("codearts2api:languagechange", {detail:{language}}));
  };
  window.codeartsI18n = { tr, setLanguage, getLanguage: () => language };
  document.querySelectorAll("[data-language]").forEach((button) => button.addEventListener("click", () => setLanguage(button.dataset.language)));
  setLanguage(detect(), false);
})();
