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

from app.codearts.account_pool import AccountPool
from app.codearts.signer import sign_request
from app.config import Settings, get_settings, normalize_environment


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
        self.pool = AccountPool(settings)

    def _environment(self, environment: str | None = None) -> str:
        return normalize_environment(environment or self.settings.default_environment)

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

    def begin_login_session(self, environment: str | None = None) -> dict[str, str]:
        selected = self._environment(environment)
        profile = self.settings.profile(selected)
        account_id = uuid.uuid4().hex
        context = {
            "account_id": account_id,
            "environment": selected,
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
        if profile.quickcompfalg:
            params["quickcompfalg"] = "1"
        return {
            "account_id": account_id,
            "environment": selected,
            "authorize_url": f"{profile.authorize_url}?{urlencode(params)}",
        }

    def begin_login(self, environment: str | None = None) -> str:
        return self.begin_login_session(environment)["authorize_url"]

    async def _token_request(
        self,
        context: dict[str, Any],
        form: dict[str, str],
        environment: str,
    ) -> dict[str, Any]:
        profile = self.settings.profile(environment)
        dpop = sign_dpop(
            keypair=context["dpop_key_pair"],
            method="POST",
            url=profile.token_url,
        )
        timeout = httpx.Timeout(15.0)
        async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
            response = await client.post(
                profile.token_url,
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

    async def _query_identity(self, credentials: dict[str, Any], environment: str) -> dict[str, Any]:
        profile = self.settings.profile(environment)
        signed = sign_request(
            method="GET",
            url=profile.caller_identity_url,
            headers={"Content-Type": "application/json"},
            data=None,
            access_key_id=str(credentials.get("access_key_id", "")),
            secret_access_key=str(credentials.get("secret_access_key", "")),
            security_token=str(credentials.get("security_token", "")),
        )
        timeout = httpx.Timeout(15.0)
        async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
            response = await client.get(profile.caller_identity_url, headers=signed.headers)
        if response.status_code >= 400:
            raise RuntimeError(f"CodeArts caller identity HTTP {response.status_code}: {response.text[:1200]}")
        payload = response.json()
        return payload if isinstance(payload, dict) else {}

    def _build_record(
        self,
        *,
        token_payload: dict[str, Any],
        context: dict[str, Any],
        environment: str,
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
            "environment": environment,
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
            # Preserve operational state across OAuth refreshes.
            "stats": dict(previous.get("stats") or {}),
            "health": dict(previous.get("health") or {}),
            "model_health": dict(previous.get("model_health") or {}),
            "updated_at": datetime.now(timezone.utc).isoformat(),
        }

    async def exchange_code(self, code: str, environment: str | None = None) -> dict[str, Any]:
        context = self._read_json(self.pending_path)
        if not context:
            raise RuntimeError("No pending CodeArts login. Open /auth/login first.")
        selected = normalize_environment(str(context.get("environment") or self.settings.default_environment))
        if environment is not None and normalize_environment(environment) != selected:
            raise RuntimeError("Pending CodeArts login environment does not match callback environment")
        form = {
            "client_id": self.settings.auth_client_id,
            "code": code,
            "code_verifier": context["pkce_pair"]["code_verifier"],
            "grant_type": "authorization_code",
            "redirect_uri": self.settings.callback_url,
        }
        payload = await self._token_request(context, form, selected)
        identity = await self._query_identity(payload["credentials"], selected)
        record = self._build_record(
            token_payload=payload,
            context=context,
            environment=selected,
            identity=identity,
        )
        account_id = str(context.get("account_id") or uuid.uuid4().hex)
        record["id"] = account_id
        record = self.pool.save(record, account_id)
        try:
            self.pending_path.unlink(missing_ok=True)
        except OSError:
            pass
        return self.pool.public(record)

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


    def list_accounts(self) -> list[dict[str, Any]]:
        return self.pool.list_public()

    def account_status(self, account_id: str) -> dict[str, Any]:
        record = self.pool.get(account_id)
        if not record:
            raise RuntimeError("CodeArts account not found")
        return self.pool.public(record)

    async def refresh_account(self, account_id: str, *, force: bool = True) -> dict[str, Any]:
        async with self._refresh_lock:
            record = self.pool.get(account_id)
            if not record:
                raise RuntimeError("CodeArts account not found")
            selected = normalize_environment(str(record.get("environment") or "cn"))
            if not force and not self._needs_refresh(record):
                return self.pool.public(record)
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
            payload = await self._token_request(context, form, selected)
            new_record = self._build_record(
                token_payload=payload,
                context=context,
                environment=selected,
                previous=record,
            )
            new_record["id"] = account_id
            new_record = self.pool.save(new_record, account_id)
            return self.pool.public(new_record)

    async def get_account_credentials(self, account_id: str) -> ActiveCredentials | None:
        record = self.pool.get(account_id)
        if not record:
            return None
        if self._needs_refresh(record) and record.get("refresh_token"):
            await self.refresh_account(account_id, force=False)
            record = self.pool.get(account_id) or record
        access_key_id = str(record.get("access_key_id", ""))
        secret_access_key = str(record.get("secret_access_key", ""))
        if not access_key_id or not secret_access_key:
            return None
        return ActiveCredentials(
            access_key_id=access_key_id,
            secret_access_key=secret_access_key,
            security_token=str(record.get("security_token", "")),
            domain_id=str(record.get("domain_id", "")),
            expires_at=str(record.get("expiration", "")),
            source="account_pool",
        )

    async def account_candidates(
        self,
        *,
        account_id: str | None = None,
        preferred_account_id: str | None = None,
        exclude_ids: set[str] | None = None,
        model: str = "",
    ) -> list[tuple[dict[str, Any], ActiveCredentials]]:
        candidates: list[tuple[dict[str, Any], ActiveCredentials]] = []
        records = self.pool.candidates(
            account_id=account_id,
            preferred_account_id=preferred_account_id,
            exclude_ids=exclude_ids,
            model=model,
        )
        for record in records:
            selected_id = str(record.get("id") or "")
            try:
                credentials = await self.get_account_credentials(selected_id)
            except Exception:
                self.pool.mark_failure(selected_id, category="credential_refresh")
                continue
            if credentials is None:
                self.pool.mark_failure(selected_id, category="credentials_unavailable")
                continue
            refreshed = self.pool.get(selected_id) or record
            candidates.append((self.pool.public(refreshed), credentials))

        if candidates or account_id:
            return candidates

        for environment in ("cn", "international"):
            if self.settings.env_credentials_ready_for(environment):
                credentials = ActiveCredentials(
                    access_key_id=self.settings.access_key_id,
                    secret_access_key=self.settings.secret_access_key,
                    security_token=self.settings.security_token,
                    domain_id=self.settings.domain_id,
                    source="env",
                )
                candidates.append(({
                    "id": f"manual-{environment}",
                    "environment": environment,
                    "environment_label": self.settings.profile(environment).label,
                    "authenticated": True,
                    "source": "env",
                    "expires_at": "",
                    "refresh_available": False,
                    "user_id": "",
                    "user_name": "",
                    "domain_id": self.settings.domain_id,
                    "health": {"status": "healthy"},
                }, credentials))
        return candidates

    async def choose_account(self, account_id: str | None = None, *, model: str = "") -> tuple[dict[str, Any], ActiveCredentials] | None:
        candidates = await self.account_candidates(account_id=account_id, model=model)
        return candidates[0] if candidates else None

    async def probe_account(self, account_id: str) -> dict[str, Any]:
        record = self.pool.get(account_id)
        if not record:
            raise RuntimeError("CodeArts account not found")
        environment = normalize_environment(str(record.get("environment") or "cn"))
        access_key_id = str(record.get("access_key_id") or "")
        secret_access_key = str(record.get("secret_access_key") or "")
        if not access_key_id or not secret_access_key:
            self.pool.mark_failure(account_id, category="auth_probe", status_code=401)
            self.pool.record_probe(account_id, ok=False, category="credentials_unavailable", status_code=401, needs_relogin=True)
            return self.account_status(account_id)

        from app.codearts.client import CodeArtsClient
        from app.codearts.errors import CodeArtsUpstreamError

        def credentials_from(current: dict[str, Any]) -> ActiveCredentials:
            return ActiveCredentials(
                access_key_id=str(current.get("access_key_id") or ""),
                secret_access_key=str(current.get("secret_access_key") or ""),
                security_token=str(current.get("security_token") or ""),
                domain_id=str(current.get("domain_id") or ""),
                expires_at=str(current.get("expiration") or ""),
                source="account_pool",
            )

        async def lightweight_probe(current: dict[str, Any]) -> None:
            profile = self.settings.profile(environment)
            last_error: Exception | None = None
            for attempt in range(2):
                client = CodeArtsClient(
                    self.settings,
                    auth_manager=self,
                    environment=environment,
                    account_id=account_id,
                    credentials=credentials_from(current),
                )
                try:
                    await client.get_json(f"{profile.base_url}/v1/model/builtin", agent_type="PromptCenter")
                    return
                except CodeArtsUpstreamError as exc:
                    last_error = exc
                    if exc.category != "network" or attempt:
                        raise
                    await asyncio.sleep(0.25)
            if last_error:
                raise last_error

        try:
            await lightweight_probe(record)
        except CodeArtsUpstreamError as exc:
            # An auth rejection may simply mean the temporary AK/SK expired. Refresh once,
            # then retry the lightweight probe before declaring that login is dead.
            if (exc.status_code in {401, 403} or exc.category == "auth") and record.get("refresh_token") and record.get("auth_context"):
                try:
                    await self.refresh_account(account_id, force=True)
                    self.pool.record_refresh_probe(account_id, ok=True)
                    refreshed = self.pool.get(account_id) or record
                    await lightweight_probe(refreshed)
                except Exception as refresh_exc:
                    text = str(refresh_exc).lower()
                    terminal = any(marker in text for marker in (
                        "invalid_grant", "offline user session not found", "refresh token failed",
                    ))
                    category = "auth_probe" if terminal else ("network" if isinstance(refresh_exc, httpx.RequestError) else "refresh_error")
                    self.pool.record_refresh_probe(account_id, ok=False, category=category)
                    if terminal:
                        self.pool.mark_failure(account_id, category="auth_probe", status_code=401)
                    self.pool.record_probe(account_id, ok=False, category=category, status_code=401 if terminal else None, needs_relogin=terminal)
                    return self.account_status(account_id)
            else:
                needs_relogin = exc.status_code in {401, 403} or exc.category == "auth"
                if needs_relogin:
                    self.pool.mark_failure(account_id, category="auth_probe", status_code=exc.status_code)
                self.pool.record_probe(
                    account_id,
                    ok=False,
                    category=exc.category or "probe",
                    status_code=exc.status_code,
                    needs_relogin=needs_relogin,
                )
                return self.account_status(account_id)
        except httpx.RequestError:
            self.pool.record_probe(account_id, ok=False, category="network", needs_relogin=False)
            return self.account_status(account_id)
        except Exception:
            self.pool.record_probe(account_id, ok=False, category="probe_error", needs_relogin=False)
            return self.account_status(account_id)

        # The account is demonstrably usable. Record that fact before a best-effort refresh,
        # so STS/network jitter cannot turn a healthy account into a false failed probe.
        self.pool.record_probe(account_id, ok=True)
        current = self.pool.get(account_id) or record
        if current.get("refresh_token") and current.get("auth_context"):
            try:
                await self.refresh_account(account_id, force=True)
            except httpx.RequestError:
                self.pool.record_refresh_probe(account_id, ok=False, category="network")
            except Exception as exc:
                text = str(exc).lower()
                terminal = any(marker in text for marker in (
                    "invalid_grant", "offline user session not found", "refresh token failed",
                ))
                self.pool.record_refresh_probe(account_id, ok=False, category="auth_probe" if terminal else "refresh_error")
            else:
                self.pool.record_refresh_probe(account_id, ok=True)
        return self.account_status(account_id)

    async def probe_accounts(self, *, due_only: bool = False) -> list[dict[str, Any]]:
        now = datetime.now(timezone.utc)
        results: list[dict[str, Any]] = []
        for index, record in enumerate(self.pool.list_records()):
            account_id = str(record.get("id") or "")
            if not account_id:
                continue
            if due_only:
                health = record.get("health") if isinstance(record.get("health"), dict) else {}
                last_probe = str(health.get("last_probe_at") or "")
                # Successful probes use the long keepalive interval. A transient failed
                # probe is retried on the next scheduler poll instead of being suppressed
                # for a full day.
                if last_probe and str(health.get("probe_status") or "") == "ok":
                    try:
                        parsed = datetime.fromisoformat(last_probe.replace("Z", "+00:00"))
                    except ValueError:
                        parsed = None
                    if parsed is not None and (now - parsed.astimezone(timezone.utc)).total_seconds() < max(60, self.settings.keepalive_interval_seconds):
                        continue
            if index and self.settings.keepalive_account_gap_seconds > 0:
                await asyncio.sleep(self.settings.keepalive_account_gap_seconds)
            results.append(await self.probe_account(account_id))
        return results

    def delete_account(self, account_id: str) -> bool:
        return self.pool.delete(account_id)



_auth_manager: AuthManager | None = None


def get_auth_manager() -> AuthManager:
    global _auth_manager
    if _auth_manager is None:
        _auth_manager = AuthManager(get_settings())
    return _auth_manager
