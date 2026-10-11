from __future__ import annotations

import asyncio
import base64
import hashlib
import json
from datetime import datetime, timedelta, timezone
from urllib.parse import parse_qs, urlparse

import httpx
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import encode_dss_signature

from app.codearts.auth import AuthManager, generate_dpop_keypair, generate_pkce, sign_dpop
from app.config import Settings


def _decode_segment(value: str) -> dict:
    raw = base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))
    return json.loads(raw)


def _decode_int(value: str) -> int:
    raw = base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))
    return int.from_bytes(raw, "big")


def test_pkce_matches_codearts_implementation() -> None:
    pair = generate_pkce()
    verifier = pair["code_verifier"]
    assert len(verifier) == 128
    int(verifier, 16)
    expected = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode("ascii")).digest()).rstrip(b"=").decode()
    assert pair["code_challenge"] == expected
    assert pair["code_challenge_method"] == "SHA-256"


def test_dpop_is_es256_with_public_jwk_and_valid_signature() -> None:
    keypair = generate_dpop_keypair()
    token = sign_dpop(
        keypair=keypair,
        method="POST",
        url="https://sts.cn-north-4.myhuaweicloud.com/v1/oauth2/tokens",
        now=1_700_000_000,
        jti="a" * 64,
    )
    encoded_header, encoded_payload, encoded_signature = token.split(".")
    header = _decode_segment(encoded_header)
    payload = _decode_segment(encoded_payload)

    assert header["alg"] == "ES256"
    assert header["typ"] == "dpop+jwt"
    assert header["jwk"]["crv"] == "P-256"
    assert "d" not in header["jwk"]
    assert payload == {
        "htm": "POST",
        "htu": "https://sts.cn-north-4.myhuaweicloud.com/v1/oauth2/tokens",
        "iat": 1_700_000_000,
        "jti": "a" * 64,
    }

    public_key = ec.EllipticCurvePublicNumbers(
        _decode_int(header["jwk"]["x"]),
        _decode_int(header["jwk"]["y"]),
        ec.SECP256R1(),
    ).public_key()
    raw_signature = base64.urlsafe_b64decode(encoded_signature + "=" * (-len(encoded_signature) % 4))
    r = int.from_bytes(raw_signature[:32], "big")
    s = int.from_bytes(raw_signature[32:], "big")
    public_key.verify(
        encode_dss_signature(r, s),
        f"{encoded_header}.{encoded_payload}".encode("ascii"),
        ec.ECDSA(hashes.SHA256()),
    )


def test_begin_login_matches_codearts_authorize_parameters(tmp_path) -> None:
    settings = Settings(
        port=8123,
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
    )
    manager = AuthManager(settings)
    authorize_url = manager.begin_login()
    parsed = urlparse(authorize_url)
    query = parse_qs(parsed.query)

    assert f"{parsed.scheme}://{parsed.netloc}{parsed.path}" == "https://codearts.huaweicloud.com/portal/authorize"
    assert query["client_id"] == ["codearts-snap"]
    assert query["uri_scheme"] == ["codearts-snap"]
    assert query["port"] == ["8123"]
    assert query["code_challenge_method"] == ["SHA-256"]
    assert query["auth_callback_url"] == ["http://127.0.0.1:8123/oauth/callback"]
    assert query["plugin-name"] == ["codearts-snap_vscode"]
    assert query["plugin-version"] == ["26.7.2"]
    assert len(query["ticket_id"][0]) == 36
    assert len(query["code_challenge"][0]) == 43


def test_exchange_and_refresh_rotate_credentials_atomically(tmp_path) -> None:
    calls: list[tuple[str, dict[str, list[str]]]] = []
    refresh_expiration = (datetime.now(timezone.utc) + timedelta(hours=2)).isoformat().replace("+00:00", "Z")

    def handler(request: httpx.Request) -> httpx.Response:
        if str(request.url).endswith("/v1/oauth2/tokens"):
            assert request.headers.get("DPoP")
            form = parse_qs(request.content.decode())
            calls.append(("token", form))
            if form["grant_type"] == ["authorization_code"]:
                assert form["client_id"] == ["codearts-snap"]
                assert form["code"] == ["test-code"]
                assert form["redirect_uri"] == ["http://127.0.0.1:8123/oauth/callback"]
                return httpx.Response(
                    200,
                    json={
                        "credentials": {
                            "access_key_id": "ak-1",
                            "secret_access_key": "sk-1",
                            "security_token": "st-1",
                            "expiration": refresh_expiration,
                        },
                        "refresh_token": "refresh-1",
                    },
                )
            assert form["grant_type"] == ["refresh_token"]
            assert form["refresh_token"] == ["refresh-1"]
            return httpx.Response(
                200,
                json={
                    "credentials": {
                        "access_key_id": "ak-2",
                        "secret_access_key": "sk-2",
                        "security_token": "st-2",
                        "expiration": refresh_expiration,
                    },
                    "refresh_token": "refresh-2",
                },
            )

        if str(request.url).endswith("/v5/caller-identity"):
            assert request.headers["Authorization"].startswith("SDK-HMAC-SHA256 Access=ak-1")
            assert request.headers["X-Security-Token"] == "st-1"
            return httpx.Response(
                200,
                json={
                    "account_id": "domain-1",
                    "principal_id": "user-1",
                    "principal_urn": "urn:huaweicloud:iam::domain-1:user/test-user",
                },
            )
        raise AssertionError(f"unexpected request: {request.method} {request.url}")

    settings = Settings(
        port=8123,
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
    )
    manager = AuthManager(settings, transport=httpx.MockTransport(handler))
    manager.begin_login()

    status = asyncio.run(manager.exchange_code("test-code"))
    assert status["authenticated"] is True
    assert status["user_id"] == "user-1"
    assert "access_key_id" not in status
    assert "refresh_token" not in status

    account_id = status["id"]
    first = manager.pool.get(account_id)
    assert first is not None
    assert first["access_key_id"] == "ak-1"
    assert first["refresh_token"] == "refresh-1"
    assert first["domain_id"] == "domain-1"
    assert "d" in first["auth_context"]["dpop_key_pair"]["private_jwk"]

    refreshed = asyncio.run(manager.refresh_account(account_id, force=True))
    assert refreshed["authenticated"] is True
    second = manager.pool.get(account_id)
    assert second is not None
    assert second["access_key_id"] == "ak-2"
    assert second["refresh_token"] == "refresh-2"
    assert second["secret_access_key"] == "sk-2"
    assert [form["grant_type"][0] for kind, form in calls if kind == "token"] == ["authorization_code", "refresh_token"]


def test_account_credentials_lazily_refresh_expired_record(tmp_path) -> None:
    future = (datetime.now(timezone.utc) + timedelta(hours=1)).isoformat().replace("+00:00", "Z")

    def handler(request: httpx.Request) -> httpx.Response:
        form = parse_qs(request.content.decode())
        assert form["grant_type"] == ["refresh_token"]
        assert form["refresh_token"] == ["refresh-old"]
        return httpx.Response(
            200,
            json={
                "credentials": {
                    "access_key_id": "ak-new",
                    "secret_access_key": "sk-new",
                    "security_token": "st-new",
                    "expiration": future,
                },
                "refresh_token": "refresh-new",
            },
        )

    settings = Settings(
        pending_auth_file=str(tmp_path / "pending.json"),
        accounts_dir=str(tmp_path / "accounts"),
        routing_file=str(tmp_path / "routing.json"),
    )
    manager = AuthManager(settings, transport=httpx.MockTransport(handler))
    manager.begin_login()
    pending = json.loads((tmp_path / "pending.json").read_text(encoding="utf-8"))
    expired = (datetime.now(timezone.utc) - timedelta(minutes=1)).isoformat().replace("+00:00", "Z")
    manager.pool.save(
        {
            "id": "expired-account",
            "environment": "cn",
            "access_key_id": "ak-old",
            "secret_access_key": "sk-old",
            "security_token": "st-old",
            "expiration": expired,
            "refresh_token": "refresh-old",
            "domain_id": "domain-old",
            "auth_context": {
                "pkce_pair": pending["pkce_pair"],
                "dpop_key_pair": pending["dpop_key_pair"],
            },
        },
        "expired-account",
    )

    credentials = asyncio.run(manager.get_account_credentials("expired-account"))
    assert credentials is not None
    assert credentials.access_key_id == "ak-new"
    assert credentials.secret_access_key == "sk-new"
    stored = manager.pool.get("expired-account")
    assert stored is not None
    assert stored["refresh_token"] == "refresh-new"
