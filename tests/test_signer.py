from __future__ import annotations

from datetime import datetime, timezone

from app.codearts.signer import sign_request


def test_signer_builds_huawei_sdk_hmac_headers() -> None:
    signed = sign_request(
        method="POST",
        url="https://example.com/api/v2/chat/completions?z=2&a=hello world",
        headers={"Content-Type": "application/json", "Accept": "text/event-stream"},
        data={"hello": "世界"},
        access_key_id="test-ak",
        secret_access_key="test-sk",
        security_token="test-token",
        now=datetime(2026, 10, 11, 6, 30, 0, tzinfo=timezone.utc),
    )

    assert signed.headers["X-Sdk-Date"] == "20261011T063000Z"
    assert signed.headers["host"] == "example.com"
    assert signed.headers["X-Security-Token"] == "test-token"
    assert signed.headers["Authorization"].startswith("SDK-HMAC-SHA256 Access=test-ak, SignedHeaders=")
    assert "POST\n/api/v2/chat/completions/\na=hello%20world&z=2\n" in signed.canonical_request
    assert signed.body == '{"hello":"世界"}'.encode("utf-8")


def test_signer_is_deterministic() -> None:
    kwargs = dict(
        method="POST",
        url="https://example.com/api/v2/chat/completions",
        headers={"Content-Type": "application/json"},
        data={"messages": [{"role": "user", "content": "hi"}]},
        access_key_id="ak",
        secret_access_key="sk",
        security_token="token",
        now=datetime(2026, 1, 2, 3, 4, 5, tzinfo=timezone.utc),
    )
    first = sign_request(**kwargs)
    second = sign_request(**kwargs)
    assert first.headers["Authorization"] == second.headers["Authorization"]
    assert first.canonical_request == second.canonical_request
