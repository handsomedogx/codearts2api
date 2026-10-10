from __future__ import annotations

import uuid
from typing import Any, AsyncIterator

import httpx

from app.codearts.auth import AuthManager, get_auth_manager
from app.codearts.signer import sign_request
from app.codearts.sse import TextDeltaTracker, decode_payload, parse_sse
from app.config import Settings


class CodeArtsClient:
    def __init__(
        self,
        settings: Settings,
        transport: httpx.AsyncBaseTransport | None = None,
        auth_manager: AuthManager | None = None,
    ) -> None:
        self.settings = settings
        self._transport = transport
        self._auth_manager = auth_manager or get_auth_manager()

    def _headers(self) -> dict[str, str]:
        return {
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
            "client_version": self.settings.client_version,
            "Agent-Type": "ChatAgent",
            "X-Language": self.settings.language,
            "x-snap-traceid": uuid.uuid4().hex,
            "plugin-name": self.settings.plugin_name,
            "plugin-version": self.settings.plugin_version,
            "ide-name": self.settings.ide_name,
            "ide-version": self.settings.ide_version,
            "heartbeat-enable": "true",
            "is_confidential": "false",
        }

    def _body(self, *, messages: list[dict[str, Any]], model: str, user: str | None = None) -> dict[str, Any]:
        body: dict[str, Any] = {
            "chat_id": uuid.uuid4().hex,
            "messages": messages,
            "client": "IDE",
            "task_parameters": {"ide": self.settings.ide_name},
            "batch_task_parameters": [],
            "attempt": 1,
            "master_body": {},
            "mcp_servers": [],
            "query_optimize": True,
            "is_delta_response": False,
            "not_allow_external_model": False,
        }
        if user:
            body["user_id"] = user
        if model and model != "codearts-auto":
            body["model_id"] = model
        return body

    async def chat_stream(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None = None,
    ) -> AsyncIterator[str]:
        credentials = await self._auth_manager.get_credentials()
        if credentials is None:
            raise RuntimeError("CodeArts credentials are not configured; open /auth/login first")

        body = self._body(messages=messages, model=model, user=user)
        signed = sign_request(
            method="POST",
            url=self.settings.chat_url,
            headers=self._headers(),
            data=body,
            access_key_id=credentials.access_key_id,
            secret_access_key=credentials.secret_access_key,
            security_token=credentials.security_token,
            domain_id=credentials.domain_id,
        )

        tracker = TextDeltaTracker()
        timeout = httpx.Timeout(connect=20.0, read=300.0, write=30.0, pool=30.0)
        async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
            async with client.stream(
                "POST",
                self.settings.chat_url,
                headers=signed.headers,
                content=signed.body,
            ) as response:
                if response.status_code >= 400:
                    raw = await response.aread()
                    text = raw.decode("utf-8", errors="replace")[:2000]
                    raise RuntimeError(f"CodeArts upstream HTTP {response.status_code}: {text}")

                async for upstream_event in parse_sse(response.aiter_lines()):
                    event_type, payload = decode_payload(upstream_event)
                    delta, done = tracker.extract(event_type, payload)
                    if delta:
                        yield delta
                    if done:
                        break

    async def chat(self, *, messages: list[dict[str, Any]], model: str, user: str | None = None) -> str:
        parts: list[str] = []
        async for delta in self.chat_stream(messages=messages, model=model, user=user):
            parts.append(delta)
        return "".join(parts)
