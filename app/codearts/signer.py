from __future__ import annotations

import hashlib
import hmac
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Iterable
from urllib.parse import parse_qs, quote, urlparse

SDK_SIGNING_ALGORITHM = "SDK-HMAC-SHA256"
EMPTY_BODY_SHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"


def _sha256_hex(value: bytes | str) -> str:
    if isinstance(value, str):
        value = value.encode("utf-8")
    return hashlib.sha256(value).hexdigest()


def _hmac_sha256_hex(secret: str, value: str) -> str:
    return hmac.new(secret.encode("utf-8"), value.encode("utf-8"), hashlib.sha256).hexdigest()


def _header_get(headers: dict[str, str], name: str) -> str | None:
    lower = name.lower()
    for key, value in headers.items():
        if key.lower() == lower:
            return value
    return None


def _canonical_uri(path: str) -> str:
    # Matches Huawei's bundled JS SDK: encodeURIComponent on each path segment,
    # then ensure the canonical URI ends in '/'.
    if not path:
        return path
    encoded = "/".join(quote(part, safe="-_.!~*'()") for part in path.split("/"))
    if not encoded.endswith("/"):
        encoded += "/"
    return encoded


def _query_encode(value: Any) -> str:
    # Huawei SDK canonical query uses RFC3986 unreserved characters.
    return quote(str(value), safe="-_.~")


def _canonical_query(query_items: dict[str, Any]) -> str:
    parts: list[str] = []
    for key in sorted(query_items):
        value = query_items[key]
        encoded_key = _query_encode(key)
        if isinstance(value, (list, tuple)):
            for item in sorted(str(v) for v in value):
                parts.append(f"{encoded_key}={_query_encode(item)}")
        else:
            parts.append(f"{encoded_key}={_query_encode(value)}")
    return "&".join(parts)


def _canonical_headers(headers: dict[str, str]) -> tuple[str, str]:
    ordered = sorted(headers.items(), key=lambda item: item[0].lower())
    canonical = "".join(f"{key.lower()}:{value}\n" for key, value in ordered)
    signed = ";".join(key.lower() for key, _ in ordered)
    return canonical, signed


def _json_bytes(data: Any) -> bytes:
    return json.dumps(data, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


@dataclass(frozen=True)
class SignedRequest:
    headers: dict[str, str]
    body: bytes
    canonical_request: str
    string_to_sign: str


def sign_request(
    *,
    method: str,
    url: str,
    headers: dict[str, str] | None,
    data: Any,
    access_key_id: str,
    secret_access_key: str,
    security_token: str = "",
    domain_id: str = "",
    now: datetime | None = None,
) -> SignedRequest:
    """Sign one request exactly like the Huawei SDK bundled in CodeArts Agent.

    The function deliberately returns canonical_request/string_to_sign for tests
    and troubleshooting, but callers must never log secret_access_key.
    """
    if not access_key_id:
        raise ValueError("access_key_id is required")
    if not secret_access_key:
        raise ValueError("secret_access_key is required")

    parsed = urlparse(url)
    request_headers = {str(k): str(v) for k, v in (headers or {}).items() if v is not None}

    sdk_date = _header_get(request_headers, "X-Sdk-Date")
    if not sdk_date:
        instant = now or datetime.now(timezone.utc)
        sdk_date = instant.astimezone(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        request_headers["X-Sdk-Date"] = sdk_date

    if not _header_get(request_headers, "host"):
        request_headers["host"] = parsed.netloc
    if security_token and not _header_get(request_headers, "X-Security-Token"):
        request_headers["X-Security-Token"] = security_token
    if domain_id and not _header_get(request_headers, "X-Domain-Id"):
        request_headers["X-Domain-Id"] = domain_id

    body = _json_bytes(data) if data is not None else b""

    query_raw = parse_qs(parsed.query, keep_blank_values=True)
    query: dict[str, Any] = {
        key: values if len(values) > 1 else values[0]
        for key, values in query_raw.items()
    }

    canonical_uri = _canonical_uri(parsed.path)
    canonical_query = _canonical_query(query)
    canonical_headers, signed_headers = _canonical_headers(request_headers)
    payload_hash = _header_get(request_headers, "X-Sdk-Content-Sha256") or (
        _sha256_hex(body) if body else EMPTY_BODY_SHA256
    )
    canonical_request = "\n".join(
        [method.upper(), canonical_uri, canonical_query, canonical_headers, signed_headers, payload_hash]
    )
    string_to_sign = "\n".join(
        [SDK_SIGNING_ALGORITHM, sdk_date, _sha256_hex(canonical_request)]
    )
    signature = _hmac_sha256_hex(secret_access_key, string_to_sign)
    request_headers["Authorization"] = (
        f"{SDK_SIGNING_ALGORITHM} Access={access_key_id}, "
        f"SignedHeaders={signed_headers}, Signature={signature}"
    )

    return SignedRequest(
        headers=request_headers,
        body=body,
        canonical_request=canonical_request,
        string_to_sign=string_to_sign,
    )
