from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, AsyncIterator


@dataclass
class UpstreamEvent:
    event: str | None
    data: str


async def parse_sse(lines: AsyncIterator[str]) -> AsyncIterator[UpstreamEvent]:
    """Parse standard SSE while also tolerating JSON-lines responses."""
    event_name: str | None = None
    data_lines: list[str] = []

    async for raw in lines:
        line = raw.rstrip("\r\n")
        if not line:
            if data_lines:
                yield UpstreamEvent(event_name, "\n".join(data_lines))
            event_name = None
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        if line.startswith("event:"):
            event_name = line[6:].strip()
            continue
        if line.startswith("data:"):
            data_lines.append(line[5:].lstrip())
            continue
        # Some CodeArts builds can surface already-decoded JSON lines.
        if line.startswith("{") or line.startswith("["):
            if data_lines:
                yield UpstreamEvent(event_name, "\n".join(data_lines))
                event_name = None
                data_lines = []
            yield UpstreamEvent(None, line)

    if data_lines:
        yield UpstreamEvent(event_name, "\n".join(data_lines))


def _nested_get(obj: Any, path: tuple[str, ...]) -> Any:
    cur = obj
    for key in path:
        if not isinstance(cur, dict) or key not in cur:
            return None
        cur = cur[key]
    return cur


def decode_payload(event: UpstreamEvent) -> tuple[str | None, dict[str, Any] | None]:
    if event.data == "[DONE]":
        return "done", {"done": True}
    try:
        payload = json.loads(event.data)
    except json.JSONDecodeError:
        return event.event, {"text": event.data}
    if isinstance(payload, dict):
        event_type = event.event or payload.get("event") or payload.get("type") or payload.get("status")
        return str(event_type) if event_type is not None else None, payload
    return event.event, {"data": payload}


class TextDeltaTracker:
    """Converts both cumulative and already-delta CodeArts text events to deltas."""

    def __init__(self) -> None:
        self.previous = ""

    def extract(self, event_type: str | None, payload: dict[str, Any] | None) -> tuple[str, bool]:
        if payload is None:
            return "", False
        lower_type = (event_type or "").lower()
        done = lower_type in {"done", "finish", "finished", "end", "completed"} or bool(payload.get("done"))

        # Some CodeArts SSE variants wrap the sentinel in a text/content field
        # instead of emitting a bare `data: [DONE]` event. Never surface that
        # transport marker as assistant content.
        sentinel_values = (payload.get("text"), payload.get("content"), payload.get("data"))
        if any(value == "[DONE]" for value in sentinel_values):
            return "", True

        error_code = payload.get("error_code")
        if error_code not in (None, "", 0, "0"):
            message = payload.get("error_msg") or payload.get("message") or f"CodeArts error {error_code}"
            raise RuntimeError(str(message))

        candidates = [
            ("delta", "content"),
            ("delta", "text"),
            ("data", "delta", "content"),
            ("data", "text"),
            ("data", "content"),
            ("message", "content"),
            ("text",),
            ("content",),
            ("answer",),
        ]
        text: str | None = None
        for path in candidates:
            value = _nested_get(payload, path)
            if isinstance(value, str):
                text = value
                break
        if text is None:
            return "", done

        # delta.content is normally an actual delta. Plain text is commonly cumulative.
        is_explicit_delta = _nested_get(payload, ("delta", "content")) is not None or _nested_get(payload, ("data", "delta", "content")) is not None
        if is_explicit_delta:
            self.previous += text
            return text, done
        if text.startswith(self.previous):
            delta = text[len(self.previous):]
            self.previous = text
            return delta, done
        self.previous += text
        return text, done
