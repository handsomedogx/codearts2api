from __future__ import annotations

from app.codearts.client import CodeArtsClient
from app.config import ENV_INTERNATIONAL, Settings


def test_chat_url_uses_v2_openai_endpoint() -> None:
    settings = Settings()
    assert settings.chat_url_for(ENV_INTERNATIONAL).endswith("/api/v2/chat/completions")
    assert "/v1/chat/chat" not in settings.chat_url_for(ENV_INTERNATIONAL)


def test_v2_body_preserves_openai_roles_and_system_identity() -> None:
    client = CodeArtsClient(Settings())
    messages = [
        {"role": "system", "content": "Your name is Little Code Sauce"},
        {"role": "user", "content": "one"},
        {"role": "assistant", "content": "two"},
        {"role": "user", "content": "three"},
    ]
    body = client._body(messages=messages, model="GLM-5.2", chat_id="chat-1")
    assert body["messages"] == messages
    assert body["model"] == "GLM-5.2"
    assert body["chat_id"] == "chat-1"
    assert body["prompt_cache_key"] == "chat-1"
    assert body["stream"] is True
    assert body["tool_stream"] is True
    assert "task" not in body
    assert "task_parameters" not in body
    assert "parent_message_id" not in body


def test_v2_body_preserves_tool_history_and_supported_options() -> None:
    client = CodeArtsClient(Settings())
    messages = [
        {"role": "assistant", "content": None, "tool_calls": [{"id": "call-1", "type": "function", "function": {"name": "weather", "arguments": "{}"}}]},
        {"role": "tool", "tool_call_id": "call-1", "content": "sunny"},
    ]
    tools = [{"type": "function", "function": {"name": "weather", "parameters": {"type": "object"}}}]
    body = client._body(
        messages=messages,
        model="GLM-5.2",
        options={"tools": tools, "tool_choice": "auto", "temperature": 0.2, "unknown_option": "drop"},
    )
    assert body["messages"] == messages
    assert body["tools"] == tools
    assert body["tool_choice"] == "auto"
    assert body["temperature"] == 0.2
    assert "unknown_option" not in body


def test_v2_chat_headers_do_not_select_legacy_chat_agent() -> None:
    class Credential:
        security_token = "security-token"

    client = CodeArtsClient(Settings())
    headers = client._chat_headers(Credential())  # type: ignore[arg-type]
    assert headers["app-id"] == "CodeAgent3.0"
    assert headers["x-auth-token"] == "security-token"
    assert "Agent-Type" not in headers


def test_response_metadata_and_usage_are_captured() -> None:
    client = CodeArtsClient(Settings())
    client._capture_metadata(
        {
            "chat_id": "chat-42",
            "usage": {
                "prompt_tokens": 97,
                "completion_tokens": 2,
                "total_tokens": 99,
                "prompt_tokens_details": {"cached_tokens": 11},
                "completion_tokens_details": {"reasoning_tokens": 1},
            },
        }
    )
    assert client.last_chat_id == "chat-42"
    assert client.last_usage == {"prompt_tokens": 97, "completion_tokens": 2, "total_tokens": 99}
    assert client.last_usage_details["prompt_tokens_details"]["cached_tokens"] == 11
    assert client.last_usage_details["completion_tokens_details"]["reasoning_tokens"] == 1
    assert client.usage_payload()["total_tokens"] == 99


def test_reasoning_and_tool_call_deltas_are_accumulated() -> None:
    client = CodeArtsClient(Settings())
    client._capture_delta({"reasoning_content": "think ", "tool_calls": [{"index": 0, "id": "call-1", "type": "function", "function": {"name": "wea", "arguments": "{\"c"}}]})
    client._capture_delta({"reasoning_content": "more", "tool_calls": [{"index": 0, "function": {"name": "ther", "arguments": "ity\":\"SZ\"}"}}]})
    assert client.last_reasoning_content == "think more"
    assert client.last_tool_calls == [
        {"id": "call-1", "type": "function", "function": {"name": "weather", "arguments": "{\"city\":\"SZ\"}"}}
    ]
