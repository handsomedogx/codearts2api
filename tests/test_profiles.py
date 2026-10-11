from __future__ import annotations

import asyncio
from urllib.parse import parse_qs, urlparse

import httpx
from fastapi.testclient import TestClient

from app.codearts.auth import AuthManager
from app.codearts.client import CodeArtsClient
from app.config import ENV_CN, ENV_INTERNATIONAL, Settings, get_settings
from app.main import app


def _settings(tmp_path) -> Settings:
    return Settings(
        port=8123,
        pending_auth_file=str(tmp_path / "auth_pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
    )


def _record(account_id: str, environment: str, user: str) -> dict:
    return {
        "id": account_id,
        "environment": environment,
        "access_key_id": f"ak-{account_id}",
        "secret_access_key": f"sk-{account_id}",
        "security_token": f"st-{account_id}",
        "expiration": "2099-01-01T00:00:00Z",
        "refresh_token": "",
        "domain_id": f"domain-{account_id}",
        "user_id": f"user-{account_id}",
        "user_name": user,
        "auth_context": {},
    }


def test_official_cn_and_international_profiles(tmp_path) -> None:
    settings = _settings(tmp_path)
    cn = settings.profile(ENV_CN)
    intl = settings.profile(ENV_INTERNATIONAL)

    assert cn.base_url == "https://snap-access.cn-north-4.myhuaweicloud.com"
    assert cn.portal_base_url == "https://codearts.huaweicloud.com"
    assert cn.sts_base_url == "https://sts.cn-north-4.myhuaweicloud.com"
    assert cn.quickcompfalg is False

    assert intl.base_url == "https://snap-access.ap-southeast-1.myhuaweicloud.com"
    assert intl.portal_base_url == "https://codearts.ap-southeast-1.huaweicloud.com"
    assert intl.sts_base_url == "https://sts.cn-north-4.myhuaweicloud.com"
    assert intl.quickcompfalg is True


def test_international_login_creates_pending_account_context(tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    authorize_url = manager.begin_login(ENV_INTERNATIONAL)
    parsed = urlparse(authorize_url)
    query = parse_qs(parsed.query)

    assert parsed.netloc == "codearts.ap-southeast-1.huaweicloud.com"
    assert query["quickcompfalg"] == ["1"]
    assert query["client_id"] == ["codearts-snap"]
    assert manager.pending_path.exists()
    pending = manager._read_json(manager.pending_path)
    assert pending is not None
    assert pending["environment"] == ENV_INTERNATIONAL
    assert len(pending["account_id"]) == 32
    assert manager.list_accounts() == []


def test_international_exchange_adds_account_to_pool(tmp_path) -> None:
    expiration = "2099-01-01T00:00:00Z"

    def handler(request: httpx.Request) -> httpx.Response:
        if str(request.url).endswith("/v1/oauth2/tokens"):
            assert request.url.host == "sts.cn-north-4.myhuaweicloud.com"
            form = parse_qs(request.content.decode())
            assert form["grant_type"] == ["authorization_code"]
            return httpx.Response(
                200,
                json={
                    "credentials": {
                        "access_key_id": "intl-ak",
                        "secret_access_key": "intl-sk",
                        "security_token": "intl-token",
                        "expiration": expiration,
                    },
                    "refresh_token": "intl-refresh",
                },
            )
        if str(request.url).endswith("/v5/caller-identity"):
            return httpx.Response(
                200,
                json={
                    "account_id": "intl-domain",
                    "principal_id": "intl-user",
                    "principal_urn": "urn:huaweicloud:iam::intl-domain:user/intl-user",
                },
            )
        raise AssertionError(f"unexpected request: {request.method} {request.url}")

    settings = _settings(tmp_path)
    manager = AuthManager(settings, transport=httpx.MockTransport(handler))
    manager.begin_login(ENV_INTERNATIONAL)
    status = asyncio.run(manager.exchange_code("intl-code"))

    assert status["authenticated"] is True
    assert status["environment"] == ENV_INTERNATIONAL
    assert status["id"]
    stored = manager.pool.get(status["id"])
    assert stored is not None
    assert stored["access_key_id"] == "intl-ak"
    assert stored["refresh_token"] == "intl-refresh"
    assert "intl-ak" not in str(status)
    assert "intl-refresh" not in str(status)


def test_two_oauth_logins_same_site_create_two_accounts(tmp_path) -> None:
    counter = {"token": 0}

    def handler(request: httpx.Request) -> httpx.Response:
        if str(request.url).endswith("/v1/oauth2/tokens"):
            counter["token"] += 1
            idx = counter["token"]
            return httpx.Response(
                200,
                json={
                    "credentials": {
                        "access_key_id": f"ak-{idx}",
                        "secret_access_key": f"sk-{idx}",
                        "security_token": f"st-{idx}",
                        "expiration": "2099-01-01T00:00:00Z",
                    },
                    "refresh_token": f"refresh-{idx}",
                },
            )
        if str(request.url).endswith("/v5/caller-identity"):
            idx = counter["token"]
            return httpx.Response(
                200,
                json={
                    "account_id": f"domain-{idx}",
                    "principal_id": f"user-{idx}",
                    "principal_urn": f"urn:huaweicloud:iam::domain-{idx}:user/user-{idx}",
                },
            )
        raise AssertionError(f"unexpected request: {request.method} {request.url}")

    manager = AuthManager(_settings(tmp_path), transport=httpx.MockTransport(handler))
    manager.begin_login(ENV_CN)
    first = asyncio.run(manager.exchange_code("code-1"))
    manager.begin_login(ENV_CN)
    second = asyncio.run(manager.exchange_code("code-2"))

    assert first["id"] != second["id"]
    accounts = manager.list_accounts()
    assert len(accounts) == 2
    assert all(account["environment"] == ENV_CN for account in accounts)
    assert {account["user_id"] for account in accounts} == {"user-1", "user-2"}


def test_account_pool_supports_multiple_accounts_per_site(tmp_path) -> None:
    manager = AuthManager(_settings(tmp_path))
    manager.pool.save(_record("cn-a", ENV_CN, "alice"), "cn-a")
    manager.pool.save(_record("cn-b", ENV_CN, "bob"), "cn-b")
    manager.pool.save(_record("intl-a", ENV_INTERNATIONAL, "carol"), "intl-a")

    accounts = manager.list_accounts()
    assert len(accounts) == 3
    assert [a["environment"] for a in accounts].count(ENV_CN) == 2
    assert [a["environment"] for a in accounts].count(ENV_INTERNATIONAL) == 1
    assert {a["user_name"] for a in accounts} == {"alice", "bob", "carol"}


def test_ui_exposes_pool_and_routing_without_global_environment(tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("cn-a", ENV_CN, "alice"), "cn-a")
    manager.pool.save(_record("intl-a", ENV_INTERNATIONAL, "carol"), "intl-a")
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        meta = client.get("/api/ui/meta").json()
        assert "environment" not in meta
        assert len(meta["environments"]) == 2

        accounts = client.get("/api/ui/accounts")
        assert accounts.status_code == 200
        body = accounts.json()
        assert len(body["items"]) == 2
        assert body["routing"]["strategy"] == "round_robin"

        routing = client.post("/api/ui/routing", json={"strategy": "random"})
        assert routing.status_code == 200
        assert routing.json()["strategy"] == "random"

        login = client.get("/auth/login-url?environment=international").json()
        assert login["environment"] == ENV_INTERNATIONAL
        assert "codearts.ap-southeast-1.huaweicloud.com/portal/authorize" in login["authorize_url"]
        assert "quickcompfalg=1" in login["authorize_url"]
        assert "environment" not in client.get("/api/ui/meta").json()
    finally:
        app.dependency_overrides.clear()


def test_round_robin_router_crosses_regions(tmp_path) -> None:
    manager = AuthManager(_settings(tmp_path))
    manager.pool.save(_record("a", ENV_CN, "alice"), "a")
    manager.pool.save(_record("b", ENV_INTERNATIONAL, "bob"), "b")
    manager.pool.save(_record("c", ENV_CN, "carol"), "c")

    picked = [manager.pool.choose()["id"] for _ in range(4)]
    assert picked == ["a", "b", "c", "a"]


def test_explicit_account_header_routes_by_account_environment(monkeypatch, tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("cn-a", ENV_CN, "alice"), "cn-a")
    manager.pool.save(_record("intl-a", ENV_INTERNATIONAL, "carol"), "intl-a")

    async def fake_chat(self, *, messages, model, user=None):
        assert self.account_id == "intl-a"
        assert self.environment == ENV_INTERNATIONAL
        return "ok"

    monkeypatch.setattr(CodeArtsClient, "chat", fake_chat)
    app.dependency_overrides[get_settings] = lambda: settings
    try:
        client = TestClient(app)
        response = client.post(
            "/v1/chat/completions",
            headers={"X-CodeArts-Account": "intl-a"},
            json={"model": "codearts-auto", "messages": [{"role": "user", "content": "hello"}]},
        )
        assert response.status_code == 200
        assert response.json()["choices"][0]["message"]["content"] == "ok"
    finally:
        app.dependency_overrides.clear()
