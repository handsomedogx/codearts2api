from __future__ import annotations

import asyncio

from app.codearts.sse import decode_payload, parse_sse


async def _lines(items: list[str]):
    for item in items:
        yield item
        await asyncio.sleep(0)


def test_v2_sse_without_blank_lines_yields_each_json_frame() -> None:
    async def run() -> list[tuple[str | None, dict | None]]:
        source = [
            'data: {"choices":[{"delta":{"content":"Hel"},"finish_reason":null}]}',
            'data: {"choices":[{"delta":{"content":"lo"},"finish_reason":null}]}',
            'data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":97,"completion_tokens":2,"total_tokens":99}}',
            'data: [DONE]',
        ]
        output = []
        async for event in parse_sse(_lines(source)):
            output.append(decode_payload(event))
        return output

    output = asyncio.run(run())
    assert output[0][1]["choices"][0]["delta"]["content"] == "Hel"
    assert output[1][1]["choices"][0]["delta"]["content"] == "lo"
    assert output[2][1]["usage"]["total_tokens"] == 99
    assert output[3] == ("done", {"done": True})


def test_standard_sse_event_name_is_still_supported() -> None:
    async def run():
        source = ["event: chunk", 'data: {"choices":[]}', ""]
        return [decode_payload(event) async for event in parse_sse(_lines(source))]

    output = asyncio.run(run())
    assert output == [("chunk", {"choices": []})]


def test_raw_json_lines_are_supported() -> None:
    async def run():
        source = ['{"choices":[{"delta":{"reasoning_content":"thinking"}}]}']
        return [decode_payload(event) async for event in parse_sse(_lines(source))]

    output = asyncio.run(run())
    assert output[0][1]["choices"][0]["delta"]["reasoning_content"] == "thinking"
