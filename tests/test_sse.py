from __future__ import annotations

import asyncio

from app.codearts.sse import TextDeltaTracker, decode_payload, parse_sse


async def _lines(items: list[str]):
    for item in items:
        yield item
        await asyncio.sleep(0)


def test_parse_sse_and_convert_cumulative_text_to_delta() -> None:
    async def run() -> list[str]:
        tracker = TextDeltaTracker()
        output: list[str] = []
        source = [
            "event: onAnswer",
            'data: {"text":"Hel"}',
            "",
            "event: onAnswer",
            'data: {"text":"Hello"}',
            "",
            "event: done",
            'data: {"done":true}',
            "",
        ]
        async for event in parse_sse(_lines(source)):
            kind, payload = decode_payload(event)
            delta, done = tracker.extract(kind, payload)
            if delta:
                output.append(delta)
            if done:
                break
        return output

    assert asyncio.run(run()) == ["Hel", "lo"]


def test_explicit_delta_is_not_deduplicated() -> None:
    tracker = TextDeltaTracker()
    d1, _ = tracker.extract(None, {"delta": {"content": "Hi"}})
    d2, _ = tracker.extract(None, {"delta": {"content": "!"}})
    assert d1 + d2 == "Hi!"


def test_done_sentinel_wrapped_as_text_is_not_emitted() -> None:
    tracker = TextDeltaTracker()
    delta, done = tracker.extract(None, {"text": "[DONE]"})
    assert delta == ""
    assert done is True


def test_done_sentinel_wrapped_as_content_is_not_emitted() -> None:
    tracker = TextDeltaTracker()
    delta, done = tracker.extract(None, {"content": "[DONE]"})
    assert delta == ""
    assert done is True
