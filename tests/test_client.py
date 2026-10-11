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
            "content": "System:\nBe concise\n\nUser:\none\n\nAssistant:\ntwo\n\nUser:\nthree",
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
    assert "mcp_servers" not in body
