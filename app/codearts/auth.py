from __future__ import annotations

import asyncio
import base64
import hashlib
import json
import os
import secrets
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlencode, urlparse

import httpx
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature

from app.codearts.signer import sign_request
from app.config import Settings, get_settings


def _b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def _b64url_decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))


def _json_segment(value: dict[str, Any]) -> str:
    return _b64url(json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))


def generate_pkce() -> dict[str, str]:
    # CodeArts Agent 26.7.2: 64 random bytes -> 128-char hex verifier.
    verifier = secrets.token_hex(64)
    challenge = _b64url(hashlib.sha256(verifier.encode("ascii")).digest())
    return {
        "code_verifier": verifier,
        "code_challenge": challenge,
        "code_challenge_method": "SHA-256",
    }


def generate_dpop_keypair() -> dict[str, dict[str, str]]:
    private_key = ec.generate_private_key(ec.SECP256R1())
    numbers = private_key.private_numbers()
    public = numbers.public_numbers

    def enc_int(value: int) -> str:
        return _b64url(value.to_bytes(32, "big"))

    public_jwk = {
        "kty": "EC",
        "crv": "P-256",
        "x": enc_int(public.x),
        "y": enc_int(public.y),
    }
    private_jwk = {**public_jwk, "d": enc_int(numbers.private_value)}
    return {"private_jwk": private_jwk, "public_jwk": public_jwk}


def _private_key_from_jwk(jwk: dict[str, str]) -> ec.EllipticCurvePrivateKey:
    private_value = int.from_bytes(_b64url_decode(jwk["d"]), "big")
    return ec.derive_private_key(private_value, ec.SECP256R1())


def sign_dpop(
    *,
    keypair: dict[str, dict[str, str]],
    method: str,
    url: str,
    now: int | None = None,
    jti: str | None = None,
) -> str:
    header = {
        "alg": "ES256",
        "typ": "dpop+jwt",
        "jwk": keypair["public_jwk"],
    }
    payload = {
        "htm": method.upper(),
        "htu": url,
        "iat": int(time.time() if now is None else now),
        "jti": jti or secrets.token_hex(32),
    }
    signing_input = f"{_json_segment(header)}.{_json_segment(payload)}"
    signature_der = _private_key_from_jwk(keypair["private_jwk"]).sign(
        signing_input.encode("ascii"),
        ec.ECDSA(hashes.SHA256()),
    )
    r, s = decode_dss_signature(signature_der)
    signature_raw = r.to_bytes(32, "big") + s.to_bytes(32, "big")
    return f"{signing_input}.{_b64url(signature_raw)}"


@dataclass(frozen=True)
class ActiveCredentials:
    access_key_id: str
    secret_access_key: str
    security_token: str = ""
    domain_id: str = ""
    expires_at: str = ""
    source: str = "persisted"


class AuthManager:
    def __init__(
        self,
        settings: Settings,
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        self.settings = settings
        self._transport = transport
        self._refresh_lock = asyncio.Lock()

    @property
    def credentials_path(self) -> Path:
        return Path(self.settings.credentials_file)

    @property
    def pending_path(self) -> Path:
        return Path(self.settings.pending_auth_file)

    def _read_json(self, path: Path) -> dict[str, Any] | None:
        if not path.exists():
            return None
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return None
        return value if isinstance(value, dict) else None

    def _atomic_write_json(self, path: Path, value: dict[str, Any]) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        temp = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
        temp.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")
        os.replace(temp, path)

    def begin_login(self) -> str:
        context = {
            "pkce_pair": generate_pkce(),
            "dpop_key_pair": generate_dpop_keypair(),
            "ticket_id": str(uuid.uuid4()),
            "created_at": datetime.now(timezone.utc).isoformat(),
        }
        self._atomic_write_json(self.pending_path, context)

        parsed_callback = urlparse(self.settings.callback_url)
        port = parsed_callback.port or self.settings.port
        params = {
            "theme": self.settings.auth_theme,
            "locale": self.settings.language,
            "uri_scheme": self.settings.auth_uri_scheme,
            "client_id": self.settings.auth_client_id,
            "port": str(port),
            "code_challenge": context["pkce_pair"]["code_challenge"],
            "code_challenge_method": context["pkce_pair"]["code_challenge_method"],
            "ticket_id": context["ticket_id"],
            "auth_callback_url": self.settings.callback_url,
            "plugin-name": self.settings.plugin_name,
            "plugin-version": self.settings.plugin_version,
        }
        return f"{self.settings.authorize_url}?{urlencode(params)}"

    async def _token_request(self, context: dict[str, Any], form: dict[str, str]) -> dict[str, Any]:
        dpop = sign_dpop(
            keypair=context["dpop_key_pair"],
            method="POST",
            url=self.settings.token_url,
        )
        timeout = httpx.Timeout(15.0)
        async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
            response = await client.post(
                self.settings.token_url,
                headers={
                    "DPoP": dpop,
                    "Content-Type": "application/x-www-form-urlencoded",
                },
                data=form,
            )
        if response.status_code != 200:
            detail = response.text[:1200]
            raise RuntimeError(f"CodeArts OAuth token HTTP {response.status_code}: {detail}")
        payload = response.json()
        if not isinstance(payload, dict) or not isinstance(payload.get("credentials"), dict):
            raise RuntimeError("CodeArts OAuth response is missing credentials")
        return payload

    async def _query_identity(self, credentials: dict[str, Any]) -> dict[str, Any]:
        signed = sign_request(
            method="GET",
            url=self.settings.caller_identity_url,
            headers={"Content-Type": "application/json"},
            data=None,
            access_key_id=str(credentials.get("access_key_id", "")),
            secret_access_key=str(credentials.get("secret_access_key", "")),
            security_token=str(credentials.get("security_token", "")),
        )
        timeout = httpx.Timeout(15.0)
        async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
            response = await client.get(self.settings.caller_identity_url, headers=signed.headers)
        if response.status_code >= 400:
            raise RuntimeError(f"CodeArts caller identity HTTP {response.status_code}: {response.text[:1200]}")
        payload = response.json()
        return payload if isinstance(payload, dict) else {}

    def _build_record(
        self,
        *,
        token_payload: dict[str, Any],
        context: dict[str, Any],
        previous: dict[str, Any] | None = None,
        identity: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        credentials = token_payload["credentials"]
        identity = identity or {}
        previous = previous or {}
        principal_urn = str(identity.get("principal_urn") or previous.get("principal_urn") or "")
        user_name = principal_urn.rsplit(":", 1)[-1] if principal_urn else str(previous.get("user_name") or "")
        refresh_token = token_payload.get("refresh_token") or previous.get("refresh_token") or ""
        return {
            "access_key_id": str(credentials.get("access_key_id", "")),
            "secret_access_key": str(credentials.get("secret_access_key", "")),
            "security_token": str(credentials.get("security_token", "")),
            "expiration": str(credentials.get("expiration", "")),
            "refresh_token": str(refresh_token),
            "domain_id": str(identity.get("account_id") or previous.get("domain_id") or ""),
            "user_id": str(identity.get("principal_id") or previous.get("user_id") or ""),
            "user_name": user_name,
            "principal_urn": principal_urn,
            "auth_context": {
                "pkce_pair": context["pkce_pair"],
                "dpop_key_pair": context["dpop_key_pair"],
            },
            "updated_at": datetime.now(timezone.utc).isoformat(),
        }

    async def exchange_code(self, code: str) -> dict[str, Any]:
        context = self._read_json(self.pending_path)
        if not context:
            raise RuntimeError("No pending CodeArts login. Open /auth/login first.")
        form = {
            "client_id": self.settings.auth_client_id,
            "code": code,
            "code_verifier": context["pkce_pair"]["code_verifier"],
            "grant_type": "authorization_code",
            "redirect_uri": self.settings.callback_url,
        }
        payload = await self._token_request(context, form)
        identity = await self._query_identity(payload["credentials"])
        record = self._build_record(token_payload=payload, context=context, identity=identity)
        self._atomic_write_json(self.credentials_path, record)
        try:
            self.pending_path.unlink(missing_ok=True)
        except OSError:
            pass
        return self.public_status(record)

    def _parse_expiration(self, value: str) -> datetime | None:
        if not value:
            return None
        try:
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError:
            return None
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=timezone.utc)
        return parsed.astimezone(timezone.utc)

    def _needs_refresh(self, record: dict[str, Any]) -> bool:
        expiration = self._parse_expiration(str(record.get("expiration", "")))
        if expiration is None:
            return False
        return expiration.timestamp() <= time.time() + self.settings.refresh_skew_seconds

    async def refresh(self, *, force: bool = True) -> dict[str, Any]:
        async with self._refresh_lock:
            record = self._read_json(self.credentials_path)
            if not record:
                raise RuntimeError("No persisted CodeArts credentials")
            if not force and not self._needs_refresh(record):
                return self.public_status(record)
            context = record.get("auth_context")
            refresh_token = str(record.get("refresh_token", ""))
            if not isinstance(context, dict) or not refresh_token:
                raise RuntimeError("Persisted credentials cannot be refreshed; login again")
            form = {
                "client_id": self.settings.auth_client_id,
                "code_verifier": context["pkce_pair"]["code_verifier"],
                "grant_type": "refresh_token",
                "refresh_token": refresh_token,
            }
            payload = await self._token_request(context, form)
            new_record = self._build_record(
                token_payload=payload,
                context=context,
                previous=record,
            )
            # One atomic replace is critical because CodeArts rotates refresh_token.
            self._atomic_write_json(self.credentials_path, new_record)
            return self.public_status(new_record)

    async def get_credentials(self) -> ActiveCredentials | None:
        record = self._read_json(self.credentials_path)
        if record:
            if self._needs_refresh(record) and record.get("refresh_token"):
                await self.refresh(force=False)
                record = self._read_json(self.credentials_path) or record
            access_key_id = str(record.get("access_key_id", ""))
            secret_access_key = str(record.get("secret_access_key", ""))
            if access_key_id and secret_access_key:
                return ActiveCredentials(
                    access_key_id=access_key_id,
                    secret_access_key=secret_access_key,
                    security_token=str(record.get("security_token", "")),
                    domain_id=str(record.get("domain_id", "")),
                    expires_at=str(record.get("expiration", "")),
                    source="persisted",
                )

        if self.settings.env_credentials_ready:
            return ActiveCredentials(
                access_key_id=self.settings.access_key_id,
                secret_access_key=self.settings.secret_access_key,
                security_token=self.settings.security_token,
                domain_id=self.settings.domain_id,
                source="env",
            )
        return None

    def public_status(self, record: dict[str, Any] | None = None) -> dict[str, Any]:
        record = record if record is not None else self._read_json(self.credentials_path)
        if record and record.get("access_key_id") and record.get("secret_access_key"):
            return {
                "authenticated": True,
                "source": "persisted",
                "expires_at": str(record.get("expiration", "")),
                "refresh_available": bool(record.get("refresh_token") and record.get("auth_context")),
                "user_id": str(record.get("user_id", "")),
                "user_name": str(record.get("user_name", "")),
                "domain_id": str(record.get("domain_id", "")),
            }
        if self.settings.env_credentials_ready:
            return {
                "authenticated": True,
                "source": "env",
                "expires_at": "",
                "refresh_available": False,
                "user_id": "",
                "user_name": "",
                "domain_id": self.settings.domain_id,
            }
        return {
            "authenticated": False,
            "source": "none",
            "expires_at": "",
            "refresh_available": False,
            "user_id": "",
            "user_name": "",
            "domain_id": "",
        }

    def logout(self) -> None:
        for path in (self.credentials_path, self.pending_path):
            try:
                path.unlink(missing_ok=True)
            except OSError:
                pass


_auth_manager: AuthManager | None = None


def get_auth_manager() -> AuthManager:
    global _auth_manager
    if _auth_manager is None:
        _auth_manager = AuthManager(get_settings())
    return _auth_manager
