from __future__ import annotations

from fastapi.testclient import TestClient

from app.codearts.client import CodeArtsClient
from app.config import Settings, get_settings
from app.main import app


def test_health_and_models_exist(tmp_path) -> None:
    settings = Settings(
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
    )
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        health = client.get("/health")
        assert health.status_code == 200
        assert health.json()["status"] == "ok"

        models = client.get("/v1/models")
        assert models.status_code == 200
        assert models.json()["object"] == "list"
        assert models.json()["data"][0]["id"] == "codearts-auto"
    finally:
        app.dependency_overrides.clear()


def test_chat_requires_credentials(tmp_path) -> None:
    settings = Settings(
        access_key_id="",
        secret_access_key="",
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
    )
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        response = client.post(
            "/v1/chat/completions",
            json={"model": "codearts-auto", "messages": [{"role": "user", "content": "hello"}]},
        )
        assert response.status_code == 503
    finally:
        app.dependency_overrides.clear()


def _credentialed_settings(tmp_path) -> Settings:
    return Settings(
        access_key_id="fake-ak",
        secret_access_key="fake-sk",
        security_token="fake-token",
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
    )


def test_non_streaming_openai_translation(monkeypatch, tmp_path) -> None:
    async def fake_chat(self, *, messages, model, user=None, **kwargs):
        assert messages == [{"role": "user", "content": "hello"}]
        assert model == "codearts-auto"
        self.last_usage = {"prompt_tokens": 97, "completion_tokens": 2, "total_tokens": 99}
        return "Hello from CodeArts"

    monkeypatch.setattr(CodeArtsClient, "chat", fake_chat)
    app.dependency_overrides[get_settings] = lambda: _credentialed_settings(tmp_path)
    try:
        client = TestClient(app)
        response = client.post(
            "/v1/chat/completions",
            json={"model": "codearts-auto", "messages": [{"role": "user", "content": "hello"}]},
        )
        assert response.status_code == 200
        body = response.json()
        assert body["object"] == "chat.completion"
        assert body["choices"][0]["message"]["content"] == "Hello from CodeArts"
        assert body["choices"][0]["finish_reason"] == "stop"
        assert body["usage"] == {"prompt_tokens": 97, "completion_tokens": 2, "total_tokens": 99}
    finally:
        app.dependency_overrides.clear()


def test_streaming_openai_translation(monkeypatch, tmp_path) -> None:
    async def fake_chat_stream(self, *, messages, model, user=None, **kwargs):
        yield "Hel"
        yield "lo"

    monkeypatch.setattr(CodeArtsClient, "chat_stream", fake_chat_stream)
    app.dependency_overrides[get_settings] = lambda: _credentialed_settings(tmp_path)
    try:
        client = TestClient(app)
        with client.stream(
            "POST",
            "/v1/chat/completions",
            json={"model": "codearts-auto", "stream": True, "messages": [{"role": "user", "content": "hello"}]},
        ) as response:
            text = "".join(response.iter_text())
        assert response.status_code == 200
        assert '"content":"Hel"' in text
        assert '"content":"lo"' in text
        assert '"finish_reason":"stop"' in text
        assert "data: [DONE]" in text
    finally:
        app.dependency_overrides.clear()


def test_auth_endpoints_do_not_expose_secrets(tmp_path) -> None:
    settings = Settings(
        port=8123,
        access_key_id="",
        secret_access_key="",
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
    )
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        accounts = client.get("/api/ui/accounts")
        assert accounts.status_code == 200
        assert accounts.json()["items"] == []

        login = client.get("/auth/login-url")
        assert login.status_code == 200
        body = login.json()
        assert "codearts.huaweicloud.com/portal/authorize" in body["authorize_url"]
        assert len(body["account_id"]) == 32
        assert body["callback_url"] == "http://127.0.0.1:8123/oauth/callback"
        assert "secret_access_key" not in login.text
        assert "refresh_token" not in login.text
    finally:
        app.dependency_overrides.clear()


def test_web_ui_and_ui_meta_exist() -> None:
    client = TestClient(app)
    root = client.get("/")
    assert root.status_code == 200
    assert "codearts2api" in root.text
    assert "Dashboard" in root.text
    assert "/static/app.js" in root.text

    meta = client.get("/api/ui/meta")
    assert meta.status_code == 200
    body = meta.json()
    assert body["name"] == "codearts2api"
    assert body["version"] == "0.5.0"
    assert body["api_base"] == "/v1"
    assert "api_key_enabled" in body


def test_web_ui_has_bilingual_i18n_assets() -> None:
    client = TestClient(app)
    root = client.get("/")
    assert root.status_code == 200
    assert 'data-language="zh-CN"' in root.text
    assert 'data-language="en"' in root.text
    assert root.text.index("/static/i18n.js") < root.text.index("/static/app.js")

    i18n = client.get("/static/i18n.js")
    assert i18n.status_code == 200
    assert "codearts2api.language" in i18n.text
    assert "navigator.languages" in i18n.text
    assert "概览" in i18n.text
    assert "调试台" in i18n.text
    assert "请求日志" in i18n.text
    assert "codearts2api:languagechange" in i18n.text

    app_js = client.get("/static/app.js")
    assert app_js.status_code == 200
    assert "codearts2api:languagechange" in app_js.text
    assert "window.codeartsI18n.tr" in app_js.text


def test_account_pool_uses_compact_real_metrics_table() -> None:
    client = TestClient(app)
    root = client.get("/")
    assert root.status_code == 200
    for heading in ("Account", "Status", "Success / Failure", "Concurrency", "Usage", "Recent success", "Actions"):
        assert f">{heading}<" in root.text
    assert "account-table" in root.text

    app_js = client.get("/static/app.js")
    assert app_js.status_code == 200
    assert "account.stats?.request_count" in app_js.text
    assert "account.stats?.avg_latency_ms" in app_js.text
    assert "account.stats?.concurrency" in app_js.text
    assert "account.health?.last_success_at" in app_js.text

    i18n = client.get("/static/i18n.js")
    assert "成功 / 失败" in i18n.text
    assert '"Usage":"用量"' in i18n.text


def test_ui_logs_are_sanitized() -> None:
    client = TestClient(app)
    client.get("/health")
    response = client.get("/api/ui/logs")
    assert response.status_code == 200
    items = response.json()["items"]
    assert items
    allowed = {"time", "method", "path", "status", "duration_ms"}
    assert set(items[0]) == allowed
    serialized = str(items).lower()
    assert "authorization" not in serialized
    assert "access_key" not in serialized
    assert "secret" not in serialized


def test_oauth_callback_returns_popup_completion_page(monkeypatch, tmp_path) -> None:
    from app.codearts.auth import AuthManager

    settings = Settings(
        port=8123,
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
    )

    async def fake_exchange(self, code, environment=None):
        assert code == "test-code"
        return {
            "authenticated": True,
            "environment": "international",
            "environment_label": "International",
            "source": "persisted",
            "expires_at": "",
            "refresh_available": True,
            "user_id": "safe-user",
            "user_name": "safe-user",
            "domain_id": "safe-domain",
        }

    monkeypatch.setattr(AuthManager, "exchange_code", fake_exchange)
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        response = client.get("/oauth/callback?code=test-code", follow_redirects=False)
        assert response.status_code == 200
        assert response.headers["content-type"].startswith("text/html")
        assert "codearts2api:oauth-complete" in response.text
        assert '"environment":"international"' in response.text
        assert '"success":true' in response.text
        assert "window.opener.postMessage" in response.text
        assert "postMessage(payload, window.location.origin)" in response.text
        assert "codearts2api.oauthEvent" in response.text
        assert "window.close()" in response.text
        assert "secret_access_key" not in response.text
        assert "refresh_token" not in response.text
    finally:
        app.dependency_overrides.clear()


def test_web_ui_login_chooser_and_popup_flow_assets() -> None:
    client = TestClient(app)
    root = client.get("/")
    assert root.status_code == 200
    assert 'id="loginModal"' in root.text
    assert 'data-login-environment="cn"' in root.text
    assert 'data-login-environment="international"' in root.text
    assert 'id="loginSiteStep"' in root.text
    assert 'id="loginAuthStep"' in root.text
    assert 'id="loginAuthorizeUrl"' in root.text
    assert 'id="copyAuthorizeUrlBtn"' in root.text
    assert 'id="openAuthorizeUrlBtn"' in root.text
    assert 'id="oauthWaitCard"' in root.text
    assert 'id="accountList"' in root.text
    assert 'id="routingStrategy"' in root.text
    assert 'China mainland CodeArts service' not in root.text
    assert 'International CodeArts service' not in root.text
    assert 'login-site-arrow' not in root.text
    assert 'Selecting a region only prepares an authorization link.' not in root.text
    css = client.get("/static/app.css").text
    assert 'grid-template-columns: repeat(2, minmax(0, 1fr))' in css
    assert '@media (max-width: 460px)' in css

    app_js = client.get("/static/app.js").text
    assert '/auth/login-url?environment=' in app_js
    assert 'body.account_id' in app_js
    assert 'state.oauth.authorizeUrl = body.authorize_url' in app_js
    assert 'window.open(state.oauth.authorizeUrl' in app_js
    assert 'navigator.clipboard.writeText' in app_js
    assert 'setInterval(checkOAuthCompletion, 1500)' in app_js
    assert 'codearts2api:oauth-complete' in app_js
    assert 'window.addEventListener("message"' in app_js
    assert 'window.addEventListener("storage"' in app_js
    assert '/api/ui/accounts' in app_js
    assert 'popup.location.replace(body.authorize_url)' not in app_js
    assert 'location.href = body.authorize_url' not in app_js
    assert 'Use for API' not in app_js
    assert 'environmentSelect' not in app_js


def test_usage_overview_ui_assets_exist() -> None:
    client = TestClient(app)
    root = client.get("/")
    assert root.status_code == 200
    for marker in (
        'id="usageRange"',
        'id="usageRequests"',
        'id="usageTotalTokens"',
        'id="officialQuotaPanel"',
        'id="usageChart"',
    ):
        assert marker in root.text
    assert ">Concurrency<" in root.text

    app_js = client.get("/static/app.js")
    assert app_js.status_code == 200
    assert "/api/ui/usage?days=" in app_js.text
    assert "official_quota" in app_js.text
    assert "account.stats?.concurrency" in app_js.text

    i18n = client.get("/static/i18n.js")
    assert '"Concurrency":"并发"' in i18n.text
    assert '"Usage Overview":"用量总览"' in i18n.text
