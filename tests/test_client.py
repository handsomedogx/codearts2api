from __future__ import annotations

from app.codearts.client import CodeArtsClient
from app.config import Settings


def test_single_openai_user_message_becomes_codearts_text_block() -> None:
    messages = CodeArtsClient._codearts_messages([{"role": "user", "content": "hello"}])
    assert messages == [{"type": "text", "content": "hello"}]


def test_openai_history_is_folded_into_one_text_block() -> None:
    messages = CodeArtsClient._codearts_messages(
        [
            {"role": "system", "content": "Be concise"},
            {"role": "user", "content": "one"},
            {"role": "assistant", "content": "two"},
            {"role": "user", "content": "three"},
        ]
    )
    assert messages == [
        {
            "type": "text",
            "content": "User:\none\n\nAssistant:\ntwo\n\nUser:\nthree",
        }
    ]


def test_text_content_parts_are_supported() -> None:
    messages = CodeArtsClient._codearts_messages(
        [{"role": "user", "content": [{"type": "text", "text": "hello"}, {"type": "text", "text": "world"}]}]
    )
    assert messages == [{"type": "text", "content": "hello\nworld"}]


def test_chat_body_matches_official_content_block_shape() -> None:
    client = CodeArtsClient(Settings())
    body = client._body(messages=[{"role": "user", "content": "hello"}], model="codearts-auto")
    assert body["messages"] == [{"type": "text", "content": "hello"}]
    assert body["task"] == "chat"
    assert body["task_parameters"]["ide"] == "CodeArts Agent"
    assert body["batch_task_parameters"] == []
    assert body["attempt"] == 1
    assert body["is_delta_response"] is False
    assert "master_body" not in body
    assert "query_optimize" not in body
    assert body["mcp_servers"] == []
    assert body["task_parameters"]["projectLevelPrompt"] == ""


def test_native_continuation_reuses_chat_and_parent_message() -> None:
    client = CodeArtsClient(Settings())
    body = client._body(
        messages=[
            {"role": "system", "content": "Be concise"},
            {"role": "user", "content": "first"},
            {"role": "assistant", "content": "answer"},
            {"role": "user", "content": "second"},
        ],
        model="codearts-auto",
        chat_id="chat-1",
        parent_message_id="message-1",
        native_continuation=True,
    )
    assert body["chat_id"] == "chat-1"
    assert body["parent_message_id"] == "message-1"
    assert body["messages"] == [{"type": "text", "content": "second"}]
    assert body["task_parameters"]["projectLevelPrompt"] == "Be concise"


def test_response_message_id_is_captured_from_nested_event() -> None:
    client = CodeArtsClient(Settings())
    client._capture_metadata({"data": {"response_message_id": "response-42", "chat_id": "chat-42"}})
    assert client.last_response_message_id == "response-42"
    assert client.last_chat_id == "chat-42"


def test_token_usage_is_captured_from_final_sse_usage_event() -> None:
    client = CodeArtsClient(Settings())
    client._capture_metadata({"prompt_tokens": 0})
    client._capture_metadata({"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0})
    client._capture_metadata({"prompt_tokens": 97, "completion_tokens": 2, "total_tokens": 99})
    assert client.last_usage == {"prompt_tokens": 97, "completion_tokens": 2, "total_tokens": 99}
