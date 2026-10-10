from __future__ import annotations

from fastapi.testclient import TestClient

from app.codearts.client import CodeArtsClient
from app.config import Settings, get_settings
from app.main import app


def test_health_and_models_exist(tmp_path) -> None:
    settings = Settings(
        credentials_file=str(tmp_path / "none.json"),
        pending_auth_file=str(tmp_path / "pending.json"),
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
        credentials_file=str(tmp_path / "none.json"),
        pending_auth_file=str(tmp_path / "pending.json"),
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
        credentials_file=str(tmp_path / "none.json"),
        pending_auth_file=str(tmp_path / "pending.json"),
    )


def test_non_streaming_openai_translation(monkeypatch, tmp_path) -> None:
    async def fake_chat(self, *, messages, model, user=None):
        assert messages == [{"role": "user", "content": "hello"}]
        assert model == "codearts-auto"
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
    finally:
        app.dependency_overrides.clear()


def test_streaming_openai_translation(monkeypatch, tmp_path) -> None:
    async def fake_chat_stream(self, *, messages, model, user=None):
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
        credentials_file=str(tmp_path / "credentials.json"),
        pending_auth_file=str(tmp_path / "pending.json"),
    )
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        status = client.get("/auth/status")
        assert status.status_code == 200
        assert status.json()["authenticated"] is False

        login = client.get("/auth/login-url")
        assert login.status_code == 200
        body = login.json()
        assert "codearts.huaweicloud.com/portal/authorize" in body["authorize_url"]
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
    assert body["version"] == "0.3.0"
    assert body["api_base"] == "/v1"
    assert "api_key_enabled" in body


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
