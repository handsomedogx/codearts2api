from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, Field


class ChatMessage(BaseModel):
    role: Literal["system", "user", "assistant", "tool"]
    content: str | list[Any] | None = None
    name: str | None = None
    tool_call_id: str | None = None
    tool_calls: list[Any] | None = None

    model_config = {"extra": "allow"}


class StreamOptions(BaseModel):
    include_usage: bool = False

    model_config = {"extra": "allow"}


class ChatCompletionRequest(BaseModel):
    model: str = "GLM-5.2"
    messages: list[ChatMessage] = Field(min_length=1)
    stream: bool = False
    stream_options: StreamOptions | None = None
    temperature: float | None = None
    top_p: float | None = None
    max_tokens: int | None = None
    user: str | None = None
    conversation_id: str | None = None

    model_config = {"extra": "allow"}
