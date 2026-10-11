from __future__ import annotations

import copy
import uuid
from typing import Any, AsyncIterator
from urllib.parse import urlencode

import httpx

from app.codearts.auth import ActiveCredentials, AuthManager, get_auth_manager
from app.codearts.errors import CodeArtsUpstreamError, classify_status
from app.codearts.signer import sign_request
from app.codearts.sse import decode_payload, parse_sse
from app.config import Settings


V2_OPTION_KEYS = (
    "temperature",
    "top_p",
    "max_tokens",
    "reasoning_effort",
    "tools",
    "tool_choice",
    "stop",
    "presence_penalty",
    "frequency_penalty",
    "response_format",
)


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
        self.last_chat_id = ""
        self.last_usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
        self.last_usage_details: dict[str, Any] = {}
        self.last_reasoning_content = ""
        self.last_tool_calls: list[dict[str, Any]] = []
        self.last_finish_reason = "stop"
        self._tool_call_parts: dict[int, dict[str, Any]] = {}

    def _headers(self, *, agent_type: str = "PromptCenter", accept: str = "application/json") -> dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "Accept": accept,
            "client_version": self.settings.client_version,
            "X-Language": self.settings.language,
            "x-snap-traceid": uuid.uuid4().hex,
            "plugin-name": self.settings.plugin_name,
            "plugin-version": self.settings.plugin_version,
            "ide-name": self.settings.ide_name,
            "ide-version": self.settings.ide_version,
            "is_confidential": "false",
        }
        if agent_type:
            headers["Agent-Type"] = agent_type
        return headers

    def _chat_headers(self, credentials: ActiveCredentials) -> dict[str, str]:
        return {
            "Content-Type": "application/json",
            "Accept": "text/event-stream",
            "X-Language": self.settings.language,
            "x-snap-traceid": uuid.uuid4().hex,
            "app-id": "CodeAgent3.0",
            "is_confidential": "false",
            "x-auth-token": credentials.security_token,
        }

    def _body(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        chat_id: str | None = None,
        options: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if not messages:
            raise ValueError("At least one chat message is required")
        resolved_chat_id = chat_id or uuid.uuid4().hex
        body = {
            key: copy.deepcopy(value)
            for key, value in (options or {}).items()
            if key in V2_OPTION_KEYS and value is not None
        }
        body.update(
            {
                "model": model,
                "stream": True,
                "messages": copy.deepcopy(messages),
                "chat_id": resolved_chat_id,
                "prompt_cache_key": resolved_chat_id,
                "tool_stream": True,
            }
        )
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

    @staticmethod
    def _find_dict(value: Any, key: str) -> dict[str, Any] | None:
        if isinstance(value, dict):
            direct = value.get(key)
            if isinstance(direct, dict):
                return direct
            for nested in value.values():
                found = CodeArtsClient._find_dict(nested, key)
                if found is not None:
                    return found
        elif isinstance(value, list):
            for nested in value:
                found = CodeArtsClient._find_dict(nested, key)
                if found is not None:
                    return found
        return None

    @staticmethod
    def _merge_dicts(current: dict[str, Any], incoming: dict[str, Any]) -> dict[str, Any]:
        merged = copy.deepcopy(current)
        for key, value in incoming.items():
            if isinstance(value, dict) and isinstance(merged.get(key), dict):
                merged[key] = CodeArtsClient._merge_dicts(merged[key], value)
            else:
                merged[key] = copy.deepcopy(value)
        return merged

    def usage_payload(self) -> dict[str, Any]:
        payload = copy.deepcopy(self.last_usage_details)
        payload.update(self.last_usage)
        return payload

    def _capture_metadata(self, payload: dict[str, Any] | None) -> None:
        if not payload:
            return
        chat_id = self._find_key(payload, "chat_id")
        if chat_id:
            self.last_chat_id = chat_id
        usage_details = self._find_dict(payload, "usage")
        if usage_details is not None:
            self.last_usage_details = self._merge_dicts(self.last_usage_details, usage_details)
        for key in ("prompt_tokens", "completion_tokens", "total_tokens"):
            value = self._find_number(payload, key)
            if value is not None:
                self.last_usage[key] = max(int(self.last_usage.get(key, 0)), value)
        if not self.last_usage["total_tokens"] and (self.last_usage["prompt_tokens"] or self.last_usage["completion_tokens"]):
            self.last_usage["total_tokens"] = self.last_usage["prompt_tokens"] + self.last_usage["completion_tokens"]
        if self.last_usage_details:
            self.last_usage_details.update(self.last_usage)

    def _capture_delta(self, delta: dict[str, Any]) -> None:
        reasoning = delta.get("reasoning_content")
        if isinstance(reasoning, str) and reasoning:
            self.last_reasoning_content += reasoning
        tool_calls = delta.get("tool_calls")
        if not isinstance(tool_calls, list):
            return
        for position, raw in enumerate(tool_calls):
            if not isinstance(raw, dict):
                continue
            index = raw.get("index")
            index = int(index) if isinstance(index, (int, float)) else position
            current = self._tool_call_parts.setdefault(
                index,
                {"id": "", "type": "function", "function": {"name": "", "arguments": ""}},
            )
            if isinstance(raw.get("id"), str) and raw["id"]:
                current["id"] = raw["id"]
            if isinstance(raw.get("type"), str) and raw["type"]:
                current["type"] = raw["type"]
            function = raw.get("function")
            if isinstance(function, dict):
                name = function.get("name")
                if isinstance(name, str) and name:
                    current["function"]["name"] += name
                arguments = function.get("arguments")
                if isinstance(arguments, str) and arguments:
                    current["function"]["arguments"] += arguments
        self.last_tool_calls = [copy.deepcopy(self._tool_call_parts[index]) for index in sorted(self._tool_call_parts)]

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

    @staticmethod
    def _event_error(payload: dict[str, Any]) -> CodeArtsUpstreamError | None:
        error = payload.get("error")
        if isinstance(error, dict):
            message = error.get("message") or error.get("error_msg") or error.get("code") or "CodeArts upstream error"
            code = str(error.get("code") or "")
            retryable = "429" in code or "flow" in str(message).lower() or "busy" in str(message).lower()
            return CodeArtsUpstreamError(str(message), category="upstream_event", retryable=retryable)
        error_code = payload.get("error_code")
        if error_code not in (None, "", 0, "0"):
            message = payload.get("error_msg") or payload.get("message") or f"CodeArts error {error_code}"
            retryable = "429" in str(error_code) or "flow" in str(message).lower() or "busy" in str(message).lower()
            return CodeArtsUpstreamError(str(message), category="upstream_event", retryable=retryable)
        return None

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
        options: dict[str, Any] | None = None,
    ) -> AsyncIterator[dict[str, Any]]:
        credentials = self._credentials
        if credentials is None and self.account_id:
            credentials = await self._auth_manager.get_account_credentials(self.account_id)
        if credentials is None:
            raise RuntimeError("CodeArts credentials are not configured; add an account first")

        self.last_usage = {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
        self.last_usage_details = {}
        self.last_reasoning_content = ""
        self.last_tool_calls = []
        self.last_finish_reason = "stop"
        self._tool_call_parts = {}
        body = self._body(messages=messages, model=model, chat_id=chat_id, options=options)
        signed = sign_request(
            method="POST",
            url=self.chat_url,
            headers=self._chat_headers(credentials),
            data=body,
            access_key_id=credentials.access_key_id,
            secret_access_key=credentials.secret_access_key,
            security_token=credentials.security_token,
            domain_id=credentials.domain_id,
        )

        timeout = httpx.Timeout(connect=20.0, read=300.0, write=30.0, pool=30.0)
        try:
            async with httpx.AsyncClient(timeout=timeout, transport=self._transport) as client:
                async with client.stream("POST", self.chat_url, headers=signed.headers, content=signed.body) as response:
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
                        if event_type == "done":
                            break
                        if not isinstance(payload, dict):
                            continue
                        self._capture_metadata(payload)
                        event_error = self._event_error(payload)
                        if event_error:
                            raise event_error
                        choices = payload.get("choices")
                        if not isinstance(choices, list) or not choices or not isinstance(choices[0], dict):
                            continue
                        choice = choices[0]
                        delta = choice.get("delta")
                        delta = copy.deepcopy(delta) if isinstance(delta, dict) else {}
                        finish_reason = choice.get("finish_reason")
                        if isinstance(finish_reason, str) and finish_reason:
                            self.last_finish_reason = finish_reason
                        if delta:
                            self._capture_delta(delta)
                        if delta or finish_reason is not None:
                            yield {"delta": delta, "finish_reason": finish_reason}
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
        options: dict[str, Any] | None = None,
    ) -> str:
        parts: list[str] = []
        async for chunk in self.chat_stream(
            messages=messages,
            model=model,
            user=user,
            chat_id=chat_id,
            options=options,
        ):
            delta = chunk.get("delta") if isinstance(chunk, dict) else None
            content = delta.get("content") if isinstance(delta, dict) else None
            if isinstance(content, str):
                parts.append(content)
        return "".join(parts)
