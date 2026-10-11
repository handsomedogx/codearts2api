from __future__ import annotations

import uuid
from typing import Any, AsyncIterator
from urllib.parse import urlencode

import httpx

from app.codearts.auth import ActiveCredentials, AuthManager, get_auth_manager
from app.codearts.errors import CodeArtsUpstreamError, classify_status
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
        self.last_response_message_id = ""
        self.last_chat_id = ""
        self.last_usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}

    def _headers(self, *, agent_type: str = "ChatAgent", accept: str = "text/event-stream") -> dict[str, str]:
        return {
            "Content-Type": "application/json",
            "Accept": accept,
            "client_version": self.settings.client_version,
            "Agent-Type": agent_type,
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
    def _system_prompt(cls, messages: list[dict[str, Any]]) -> str:
        return "\n\n".join(
            text
            for message in messages
            if str(message.get("role") or "") == "system"
            if (text := cls._message_text(message.get("content")))
        )

    @classmethod
    def _codearts_messages(
        cls,
        messages: list[dict[str, Any]],
        *,
        native_continuation: bool = False,
    ) -> list[dict[str, str]]:
        normalized: list[tuple[str, str]] = []
        for message in messages:
            role = str(message.get("role") or "user")
            if role == "system":
                continue
            text = cls._message_text(message.get("content"))
            if text:
                normalized.append((role, text))

        if not normalized:
            system = cls._system_prompt(messages)
            if system:
                return [{"type": "text", "content": system}]
            raise ValueError("At least one textual chat message is required")

        if native_continuation:
            for role, text in reversed(normalized):
                if role == "user":
                    return [{"type": "text", "content": text}]
            return [{"type": "text", "content": normalized[-1][1]}]

        if len(normalized) == 1 and normalized[0][0] == "user":
            prompt = normalized[0][1]
        else:
            labels = {"user": "User", "assistant": "Assistant", "tool": "Tool"}
            prompt = "\n\n".join(f"{labels.get(role, role.title())}:\n{text}" for role, text in normalized)
        return [{"type": "text", "content": prompt}]

    def _body(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None = None,
        chat_id: str | None = None,
        parent_message_id: str | None = None,
        native_continuation: bool = False,
    ) -> dict[str, Any]:
        resolved_chat_id = chat_id or uuid.uuid4().hex
        body: dict[str, Any] = {
            "chat_id": resolved_chat_id,
            "messages": self._codearts_messages(messages, native_continuation=native_continuation),
            "client": "IDE",
            "task": "chat",
            "task_parameters": {
                "ide": self.settings.ide_name,
                "preferred_language": self.settings.language,
                "enable_code_interpreter": False,
                "projectLevelPrompt": self._system_prompt(messages),
            },
            "batch_task_parameters": [],
            "attempt": 1,
            "mcp_servers": [],
            "is_delta_response": False,
            "not_allow_external_model": False,
        }
        if user:
            body["user_id"] = user
        if parent_message_id:
            body["parent_message_id"] = parent_message_id
        if model and model != "codearts-auto":
            body["model_id"] = model
        self.last_chat_id = resolved_chat_id
        return body

    @staticmethod
    def _find_key(value: Any, key: str) -> str:
        if isinstance(value, dict):
            direct = value.get(key)
            if isinstance(direct, (str, int)) and str(direct):
                return str(direct)
            for nested in value.values():
                found = CodeArtsClient._find_key(nested, key)
                if found:
                    return found
        elif isinstance(value, list):
            for nested in value:
                found = CodeArtsClient._find_key(nested, key)
                if found:
                    return found
        return ""

    @staticmethod
    def _find_number(value: Any, key: str) -> int | None:
        if isinstance(value, dict):
            direct = value.get(key)
            if isinstance(direct, (int, float)) and not isinstance(direct, bool):
                return max(0, int(direct))
            for nested in value.values():
                found = CodeArtsClient._find_number(nested, key)
                if found is not None:
                    return found
        elif isinstance(value, list):
            for nested in value:
                found = CodeArtsClient._find_number(nested, key)
                if found is not None:
                    return found
        return None

    def _capture_metadata(self, payload: dict[str, Any] | None) -> None:
        if not payload:
            return
        response_message_id = self._find_key(payload, "response_message_id")
        if response_message_id:
            self.last_response_message_id = response_message_id
        chat_id = self._find_key(payload, "chat_id")
        if chat_id:
            self.last_chat_id = chat_id
        for key in ("prompt_tokens", "completion_tokens", "total_tokens"):
            value = self._find_number(payload, key)
            if value is not None:
                # CodeArts emits zero-valued usage placeholders before the final usage event.
                self.last_usage[key] = max(int(self.last_usage.get(key, 0)), value)
        if not self.last_usage["total_tokens"] and (self.last_usage["prompt_tokens"] or self.last_usage["completion_tokens"]):
            self.last_usage["total_tokens"] = self.last_usage["prompt_tokens"] + self.last_usage["completion_tokens"]

    @staticmethod
    def _retry_after(response: httpx.Response) -> float | None:
        value = response.headers.get("retry-after")
        if not value:
            return None
        try:
            return max(0.0, float(value))
        except ValueError:
            return None

    @staticmethod
    def _transport_error(exc: Exception) -> CodeArtsUpstreamError:
        return CodeArtsUpstreamError(
            f"CodeArts transport error: {exc}",
            category="network",
            retryable=True,
        )

    async def get_json(
        self,
        url: str,
        *,
        params: dict[str, Any] | None = None,
        agent_type: str = "PromptCenter",
    ) -> dict[str, Any]:
        credentials = self._credentials
        if credentials is None and self.account_id:
            credentials = await self._auth_manager.get_account_credentials(self.account_id)
        if credentials is None:
            raise RuntimeError("CodeArts credentials are not configured; add an account first")
        query = urlencode([(key, item) for key, value in (params or {}).items() for item in (value if isinstance(value, (list, tuple)) else [value])])
        request_url = f"{url}?{query}" if query else url
        headers = self._headers(agent_type=agent_type, accept="application/json")
        headers["x-auth-token"] = ""
        signed = sign_request(
            method="GET",
            url=request_url,
            headers=headers,
            data=None,
            access_key_id=credentials.access_key_id,
            secret_access_key=credentials.secret_access_key,
            security_token=credentials.security_token,
            domain_id=credentials.domain_id,
        )
        timeout = httpx.Timeout(connect=20.0, read=60.0, write=30.0, pool=30.0)
        try:
            async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
                response = await client.get(request_url, headers=signed.headers)
        except httpx.HTTPError as exc:
            raise self._transport_error(exc) from exc
        if response.status_code >= 400:
            category, retryable = classify_status(response.status_code)
            raise CodeArtsUpstreamError(
                f"CodeArts upstream HTTP {response.status_code}: {response.text[:1200]}",
                status_code=response.status_code,
                category=category,
                retryable=retryable,
                retry_after_seconds=self._retry_after(response),
            )
        payload = response.json()
        return payload if isinstance(payload, dict) else {"data": payload}

    async def chat_stream(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None = None,
        chat_id: str | None = None,
        parent_message_id: str | None = None,
        native_continuation: bool = False,
    ) -> AsyncIterator[str]:
        credentials = self._credentials
        if credentials is None and self.account_id:
            credentials = await self._auth_manager.get_account_credentials(self.account_id)
        if credentials is None:
            raise RuntimeError("CodeArts credentials are not configured; add an account first")

        self.last_response_message_id = ""
        self.last_usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
        body = self._body(
            messages=messages,
            model=model,
            user=user,
            chat_id=chat_id,
            parent_message_id=parent_message_id,
            native_continuation=native_continuation,
        )
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
        try:
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
                        category, retryable = classify_status(response.status_code)
                        raise CodeArtsUpstreamError(
                            f"CodeArts upstream HTTP {response.status_code}: {text}",
                            status_code=response.status_code,
                            category=category,
                            retryable=retryable,
                            retry_after_seconds=self._retry_after(response),
                        )

                    async for upstream_event in parse_sse(response.aiter_lines()):
                        event_type, payload = decode_payload(upstream_event)
                        self._capture_metadata(payload)
                        try:
                            delta, done = tracker.extract(event_type, payload)
                        except RuntimeError as exc:
                            message = str(exc)
                            retryable = "429" in message or "flow" in message.lower() or "busy" in message.lower()
                            raise CodeArtsUpstreamError(
                                message,
                                category="upstream_event",
                                retryable=retryable,
                            ) from exc
                        if delta:
                            yield delta
                        if done:
                            break
        except CodeArtsUpstreamError:
            raise
        except httpx.HTTPError as exc:
            raise self._transport_error(exc) from exc

    async def chat(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None = None,
        chat_id: str | None = None,
        parent_message_id: str | None = None,
        native_continuation: bool = False,
    ) -> str:
        parts: list[str] = []
        async for delta in self.chat_stream(
            messages=messages,
            model=model,
            user=user,
            chat_id=chat_id,
            parent_message_id=parent_message_id,
            native_continuation=native_continuation,
        ):
            parts.append(delta)
        return "".join(parts)
