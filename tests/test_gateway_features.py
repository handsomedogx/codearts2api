from __future__ import annotations

import asyncio

from app.codearts.auth import AuthManager
from app.codearts.conversations import ConversationStore
from app.codearts.errors import CodeArtsUpstreamError
from app.codearts.gateway import CodeArtsGateway
from app.codearts.model_catalog import ModelCatalog
from app.config import ENV_CN, ENV_INTERNATIONAL, Settings


def _settings(tmp_path) -> Settings:
    return Settings(
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
        pending_auth_file=str(tmp_path / "pending.json"),
        conversations_dir=str(tmp_path / "conversations"),
        usage_file=str(tmp_path / "usage.jsonl"),
        routing_max_attempts=3,
        health_cooldown_base_seconds=30,
        health_cooldown_max_seconds=300,
    )


def _record(account_id: str, environment: str) -> dict:
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
        "user_name": account_id,
        "auth_context": {},
    }


def test_account_health_cooldown_is_public_and_affects_routing(tmp_path) -> None:
    manager = AuthManager(_settings(tmp_path))
    manager.pool.save(_record("a", ENV_CN), "a")
    manager.pool.save(_record("b", ENV_INTERNATIONAL), "b")

    manager.pool.mark_failure("a", category="server", status_code=503)
    public_a = manager.account_status("a")
    assert public_a["health"]["status"] == "cooldown"
    assert public_a["health"]["consecutive_failures"] == 1
    assert public_a["health"]["last_status_code"] == 503
    assert public_a["health"]["cooldown_until"]
    assert "access_key_id" not in public_a
    assert "secret_access_key" not in public_a

    candidates = manager.pool.candidates()
    assert [item["id"] for item in candidates] == ["b"]

    manager.pool.mark_success("a")
    assert manager.account_status("a")["health"]["status"] == "healthy"


def test_account_runtime_stats_track_real_attempts_without_secrets(tmp_path) -> None:
    manager = AuthManager(_settings(tmp_path))
    manager.pool.save(_record("a", ENV_CN), "a")

    started = manager.pool.begin_request("a")
    active = manager.account_status("a")
    assert active["stats"]["in_flight"] == 1
    assert active["stats"]["request_count"] == 0

    manager.pool.finish_request("a", started)
    finished = manager.account_status("a")
    assert finished["stats"]["in_flight"] == 0
    assert finished["stats"]["request_count"] == 1
    assert finished["stats"]["avg_latency_ms"] is not None
    assert finished["stats"]["avg_latency_ms"] >= 0
    assert finished["stats"]["last_latency_ms"] is not None
    assert finished["stats"]["last_request_at"]
    assert "access_key_id" not in finished
    assert "secret_access_key" not in finished


def test_conversation_store_reuses_full_openai_history_without_storing_prompts(tmp_path) -> None:
    settings = _settings(tmp_path)
    store = ConversationStore(settings)
    first_messages = [{"role": "user", "content": "secret question 731"}]
    context = store.resolve(first_messages)
    assert not store.path(context["id"]).exists()
    completed = store.complete(
        context,
        account_id="a",
        environment=ENV_CN,
        chat_id="chat-1",
        response_message_id="message-1",
        messages=first_messages,
        assistant_text="secret answer",
    )
    raw = store.path(context["id"]).read_text(encoding="utf-8")
    assert "secret question 731" not in raw
    assert "secret answer" not in raw
    assert completed["parent_message_id"] == "message-1"

    second = store.resolve(
        [
            {"role": "user", "content": "secret question 731"},
            {"role": "assistant", "content": "secret answer"},
            {"role": "user", "content": "what was it?"},
        ]
    )
    assert second["id"] == context["id"]
    assert second["chat_id"] == "chat-1"
    assert second["parent_message_id"] == "message-1"


def test_model_catalog_extracts_builtin_and_third_party_shapes(tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    builtin = {
        "data": {
            "models": [
                {
                    "model_id": "builtin-1",
                    "model_name": "Builtin One",
                    "provider_name": "Huawei",
                    "model_parameters": {
                        "model_id": "builtin-1",
                        "context_window": 202752,
                        "input_length": 196608,
                        "max_tokens": 131072,
                    },
                },
                {"model_parameters": {"model_id": "builtin-2"}, "model_id": "builtin-2", "name": "Builtin Two"},
            ]
        }
    }
    third = {"data": {"list": [{"id": "third-1", "model_name": "Third One", "vendor": "Vendor"}]}}
    a = ModelCatalog._extract_models(builtin, source="builtin", environment=ENV_CN)
    b = ModelCatalog._extract_models(third, source="third_party", environment=ENV_INTERNATIONAL)
    assert {item["id"] for item in a} >= {"builtin-1", "builtin-2"}
    builtin_one = next(item for item in a if item["id"] == "builtin-1")
    assert builtin_one["context_window"] == 202752
    assert builtin_one["input_length"] == 196608
    assert builtin_one["max_tokens"] == 131072
    assert {item["id"] for item in b} == {"third-1"}


def test_gateway_fails_over_and_marks_health(monkeypatch, tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("a", ENV_CN), "a")
    manager.pool.save(_record("b", ENV_INTERNATIONAL), "b")

    class FakeClient:
        def __init__(self, settings, **kwargs):
            self.account_id = kwargs["account_id"]
            self.environment = kwargs["environment"]
            self.last_response_message_id = ""
            self.last_chat_id = ""

        async def chat(self, **kwargs):
            if self.account_id == "a":
                raise CodeArtsUpstreamError(
                    "temporary server failure",
                    status_code=503,
                    category="server",
                    retryable=True,
                )
            self.last_chat_id = kwargs["chat_id"]
            self.last_response_message_id = "response-b"
            return "ok"

    monkeypatch.setattr("app.codearts.gateway.CodeArtsClient", FakeClient)
    gateway = CodeArtsGateway(settings, manager)
    messages = [{"role": "user", "content": "hello"}]
    context = gateway.resolve_conversation(messages)
    result = asyncio.run(
        gateway.complete(
            messages=messages,
            model="codearts-auto",
            user=None,
            context=context,
        )
    )
    assert result.text == "ok"
    assert result.account_id == "b"
    assert result.attempts == 3
    assert manager.account_status("a")["health"]["status"] == "cooldown"
    assert manager.account_status("b")["health"]["status"] == "healthy"
    assert manager.account_status("a")["stats"]["request_count"] == 2
    assert manager.account_status("b")["stats"]["request_count"] == 1
    saved = ConversationStore(settings).get(context["id"])
    assert saved is not None
    assert saved["account_id"] == "b"
    assert saved["parent_message_id"] == "response-b"


def test_transient_network_error_retries_same_account_before_cooldown(monkeypatch, tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("a", ENV_CN), "a")
    calls = 0

    class FakeClient:
        def __init__(self, settings, **kwargs):
            self.account_id = kwargs["account_id"]
            self.environment = kwargs["environment"]
            self.last_response_message_id = ""
            self.last_chat_id = ""

        async def chat(self, **kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                raise CodeArtsUpstreamError("connection reset", category="network", retryable=True)
            self.last_chat_id = kwargs["chat_id"]
            self.last_response_message_id = "response-a"
            return "ok"

    monkeypatch.setattr("app.codearts.gateway.CodeArtsClient", FakeClient)
    gateway = CodeArtsGateway(settings, manager)
    messages = [{"role": "user", "content": "hello"}]
    context = gateway.resolve_conversation(messages)
    result = asyncio.run(gateway.complete(messages=messages, model="codearts-auto", user=None, context=context))

    assert result.text == "ok"
    assert result.account_id == "a"
    assert result.attempts == 2
    assert calls == 2
    health = manager.account_status("a")["health"]
    assert health["status"] == "healthy"
    assert health["total_failures"] == 0


def test_gateway_reuses_bound_account_chat_and_parent(monkeypatch, tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("a", ENV_CN), "a")
    calls: list[dict] = []

    class FakeClient:
        def __init__(self, settings, **kwargs):
            self.account_id = kwargs["account_id"]
            self.environment = kwargs["environment"]
            self.last_response_message_id = ""
            self.last_chat_id = ""

        async def chat(self, **kwargs):
            calls.append(kwargs)
            self.last_chat_id = kwargs["chat_id"]
            self.last_response_message_id = f"message-{len(calls)}"
            return "answer-1" if len(calls) == 1 else "answer-2"

    monkeypatch.setattr("app.codearts.gateway.CodeArtsClient", FakeClient)
    gateway = CodeArtsGateway(settings, manager)
    first = [{"role": "user", "content": "one"}]
    context = gateway.resolve_conversation(first, "conversation-1")
    asyncio.run(gateway.complete(messages=first, model="codearts-auto", user=None, context=context))

    second = [
        {"role": "user", "content": "one"},
        {"role": "assistant", "content": "answer-1"},
        {"role": "user", "content": "two"},
    ]
    context2 = gateway.resolve_conversation(second, "conversation-1")
    asyncio.run(gateway.complete(messages=second, model="codearts-auto", user=None, context=context2))

    assert calls[1]["chat_id"] == calls[0]["chat_id"]
    assert calls[1]["parent_message_id"] == "message-1"
    assert calls[1]["native_continuation"] is True


def test_streaming_fails_over_before_first_delta(monkeypatch, tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("a", ENV_CN), "a")
    manager.pool.save(_record("b", ENV_INTERNATIONAL), "b")

    class FakeClient:
        def __init__(self, settings, **kwargs):
            self.account_id = kwargs["account_id"]
            self.environment = kwargs["environment"]
            self.last_response_message_id = ""
            self.last_chat_id = ""

        async def chat_stream(self, **kwargs):
            if self.account_id == "a":
                raise CodeArtsUpstreamError(
                    "temporary network failure",
                    category="network",
                    retryable=True,
                )
            self.last_chat_id = kwargs["chat_id"]
            self.last_response_message_id = "response-b"
            yield "Hel"
            yield "lo"

    monkeypatch.setattr("app.codearts.gateway.CodeArtsClient", FakeClient)
    gateway = CodeArtsGateway(settings, manager)
    messages = [{"role": "user", "content": "hello"}]
    context = gateway.resolve_conversation(messages)

    async def collect() -> str:
        parts: list[str] = []
        async for part in gateway.stream(
            messages=messages,
            model="codearts-auto",
            user=None,
            context=context,
        ):
            parts.append(part)
        return "".join(parts)

    assert asyncio.run(collect()) == "Hello"
    assert manager.account_status("a")["health"]["status"] == "cooldown"
    saved = ConversationStore(settings).get(context["id"])
    assert saved is not None
    assert saved["account_id"] == "b"


def test_account_usage_stats_and_time_series_use_real_tokens(tmp_path) -> None:
    settings = _settings(tmp_path)
    manager = AuthManager(settings)
    manager.pool.save(_record("a", ENV_CN), "a")

    started = manager.pool.begin_request("a")
    manager.pool.finish_request(
        "a",
        started,
        success=True,
        prompt_tokens=97,
        completion_tokens=2,
        total_tokens=99,
        model="GLM-5.2",
        environment=ENV_CN,
    )

    public = manager.account_status("a")
    assert public["stats"]["concurrency"] == 0
    assert public["stats"]["prompt_tokens"] == 97
    assert public["stats"]["completion_tokens"] == 2
    assert public["stats"]["total_tokens"] == 99
    assert public["stats"]["success_count"] == 1
    assert public["stats"]["failure_count"] == 0

    from app.codearts.usage import UsageStore

    summary = UsageStore(settings).summary(days=3, points=30)
    assert summary["request_count"] == 1
    assert summary["prompt_tokens"] == 97
    assert summary["completion_tokens"] == 2
    assert summary["total_tokens"] == 99
    assert summary["failure_count"] == 0
    assert any(bucket["total_tokens"] == 99 for bucket in summary["buckets"])


def test_official_free_package_quota_can_report_over_100_percent(tmp_path) -> None:
    from app.codearts.subscription import SubscriptionCatalog

    payload = {
        "package": {
            "status": "normal",
            "package_name_en": "Free",
            "spec_code": "codearts.agent.trial",
        },
        "start_date": "2026-09-13",
        "end_date": "2026-10-14",
        "metrics": [
            {
                "name": "usageTokenChatMessages",
                "usage_token_num": 449124235,
                "package_token_amount": 5000000,
            }
        ],
    }
    parsed = SubscriptionCatalog._parse(payload, {"id": "a", "environment": ENV_INTERNATIONAL})
    assert parsed["used_tokens"] == 449124235
    assert parsed["quota_tokens"] == 5000000
    assert parsed["usage_percent"] > 100
    assert parsed["progress_percent"] == 100
    assert parsed["over_limit"] is True
    assert parsed["overage_tokens"] == 444124235
