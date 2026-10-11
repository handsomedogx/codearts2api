from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, AsyncIterator


@dataclass
class UpstreamEvent:
    event: str | None
    data: str


async def parse_sse(lines: AsyncIterator[str]) -> AsyncIterator[UpstreamEvent]:
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
            data = line[5:].lstrip()
            if data == "[DONE]" or data.startswith("{") or data.startswith("["):
                if data_lines:
                    yield UpstreamEvent(event_name, "\n".join(data_lines))
                    data_lines = []
                yield UpstreamEvent(event_name, data)
                event_name = None
            else:
                data_lines.append(data)
            continue
        if line.startswith("{") or line.startswith("["):
            if data_lines:
                yield UpstreamEvent(event_name, "\n".join(data_lines))
                event_name = None
                data_lines = []
            yield UpstreamEvent(None, line)

    if data_lines:
        yield UpstreamEvent(event_name, "\n".join(data_lines))


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
