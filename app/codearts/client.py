from __future__ import annotations

import uuid
from typing import Any, AsyncIterator

import httpx

from app.codearts.auth import ActiveCredentials, AuthManager, get_auth_manager
from app.codearts.signer import sign_request
from app.codearts.sse import TextDeltaTracker, decode_payload, parse_sse
from app.config import Settings


class CodeArtsClient:
    def __init__(
        self,
        settings: Settings,
        transport: httpx.AsyncBaseTransport | None = None,
        auth_manager: AuthManager | None = None,
        environment: str | None = None,
        account_id: str | None = None,
        credentials: ActiveCredentials | None = None,
    ) -> None:
        self.settings = settings
        self._transport = transport
        self._auth_manager = auth_manager or get_auth_manager()
        self.environment = environment or settings.default_environment
        self.account_id = account_id
        self._credentials = credentials
        self.chat_url = settings.chat_url_for(self.environment)

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

    @staticmethod
    def _message_text(content: Any) -> str:
        if isinstance(content, str):
            return content
        if not isinstance(content, list):
            return ""
        parts: list[str] = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
                continue
            if not isinstance(item, dict):
                continue
            if item.get("type") in {None, "text", "input_text"}:
                value = item.get("text") or item.get("content")
                if isinstance(value, str):
                    parts.append(value)
        return "\n".join(part for part in parts if part)

    @classmethod
    def _codearts_messages(cls, messages: list[dict[str, Any]]) -> list[dict[str, str]]:
        """Translate OpenAI chat history to CodeArts content blocks.

        The official CodeArts Agent sends the current question to /v1/chat/chat as
        content blocks such as {type: "text", content: "..."}, not OpenAI
        {role, content} messages. Because this gateway is stateless across OpenAI
        requests for now, earlier turns are folded into one text block with role
        labels while the common single-user-message case remains byte-for-byte
        just the user's text.
        """
        normalized: list[tuple[str, str]] = []
        for message in messages:
            text = cls._message_text(message.get("content"))
            if text:
                normalized.append((str(message.get("role") or "user"), text))
        if not normalized:
            raise ValueError("At least one textual chat message is required")
        if len(normalized) == 1 and normalized[0][0] == "user":
            prompt = normalized[0][1]
        else:
            labels = {"system": "System", "user": "User", "assistant": "Assistant", "tool": "Tool"}
            prompt = "\n\n".join(f"{labels.get(role, role.title())}:\n{text}" for role, text in normalized)
        return [{"type": "text", "content": prompt}]

    def _body(self, *, messages: list[dict[str, Any]], model: str, user: str | None = None) -> dict[str, Any]:
        body: dict[str, Any] = {
            "chat_id": uuid.uuid4().hex,
            "messages": self._codearts_messages(messages),
            "client": "IDE",
            "task": "chat",
            "task_parameters": {
                "ide": self.settings.ide_name,
                "preferred_language": self.settings.language,
                "enable_code_interpreter": False,
                "projectLevelPrompt": "",
            },
            "batch_task_parameters": [],
            "attempt": 1,
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
        credentials = self._credentials
        if credentials is None and self.account_id:
            credentials = await self._auth_manager.get_account_credentials(self.account_id)
        if credentials is None:
            raise RuntimeError("CodeArts credentials are not configured; add an account first")

        body = self._body(messages=messages, model=model, user=user)
        signed = sign_request(
            method="POST",
            url=self.chat_url,
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
                self.chat_url,
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
