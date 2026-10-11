from __future__ import annotations

import asyncio
import copy
import random
import time
import uuid
from dataclasses import dataclass
from typing import Any, AsyncIterator

from app.codearts.auth import AuthManager
from app.codearts.client import CodeArtsClient
from app.codearts.conversations import ConversationStore
from app.codearts.errors import CodeArtsUpstreamError
from app.config import Settings


@dataclass
class CompletionResult:
    text: str
    conversation_id: str
    account_id: str
    environment: str
    attempts: int
    usage: dict[str, int]
    usage_details: dict[str, Any]
    reasoning_content: str = ""
    tool_calls: list[dict[str, Any]] | None = None
    finish_reason: str = "stop"


class RetryBudget:
    def __init__(self, attempts: int, window_seconds: float) -> None:
        self.limit = max(1, int(attempts))
        self.window_seconds = max(0.1, float(window_seconds))
        self.started = time.monotonic()
        self.used = 0

    def available(self) -> bool:
        return self.used < self.limit and self.remaining_seconds() > 0

    def consume(self) -> bool:
        if not self.available():
            return False
        self.used += 1
        return True

    def remaining_seconds(self) -> float:
        return max(0.0, self.window_seconds - (time.monotonic() - self.started))


class CodeArtsGateway:
    def __init__(self, settings: Settings, auth: AuthManager) -> None:
        self.settings = settings
        self.auth = auth
        self.conversations = ConversationStore(settings)
        self.last_trace: dict[str, Any] = {}

    def resolve_conversation(
        self,
        messages: list[dict[str, Any]],
        conversation_id: str | None = None,
    ) -> dict[str, Any]:
        return self.conversations.resolve(messages, conversation_id)

    def _mark_success(self, account_id: str, model: str) -> None:
        if self.auth.pool.get(account_id):
            self.auth.pool.mark_success(account_id)
            self.auth.pool.mark_model_success(account_id, model)

    def _mark_failure(self, account_id: str, model: str, exc: CodeArtsUpstreamError) -> None:
        if not self.auth.pool.get(account_id):
            return
        # Client/request-shape errors must not poison account/model health.
        if not exc.retryable and exc.status_code not in {401, 403}:
            return
        # Transport jitter is request-scoped: record it in runtime/request stats, but do not
        # make a healthy account unavailable to subsequent requests.
        if exc.category in {"network", "timeout"}:
            return
        # Authentication/credential failures describe the whole account. Model/rate/server
        # failures are scoped to account+model so other models remain usable.
        if exc.status_code in {401, 403} or exc.category in {
            "auth", "credential_refresh", "credentials_unavailable",
        }:
            self.auth.pool.mark_failure(
                account_id,
                category=exc.category,
                status_code=exc.status_code,
                retry_after_seconds=exc.retry_after_seconds,
            )
            return
        self.auth.pool.mark_model_failure(
            account_id,
            model,
            category=exc.category,
            status_code=exc.status_code,
            retry_after_seconds=exc.retry_after_seconds,
        )

    @staticmethod
    def _usage(client: Any) -> dict[str, int]:
        raw = getattr(client, "last_usage", None)
        raw = raw if isinstance(raw, dict) else {}
        prompt = max(0, int(raw.get("prompt_tokens") or 0))
        completion = max(0, int(raw.get("completion_tokens") or 0))
        total = max(0, int(raw.get("total_tokens") or 0)) or prompt + completion
        return {"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": total}

    @staticmethod
    def _usage_details(client: Any) -> dict[str, Any]:
        factory = getattr(client, "usage_payload", None)
        if callable(factory):
            raw = factory()
        else:
            raw = getattr(client, "last_usage_details", None)
        details = copy.deepcopy(raw) if isinstance(raw, dict) else {}
        details.update(CodeArtsGateway._usage(client))
        return details

    def _can_retry_same_account(self, exc: CodeArtsUpstreamError, retry_index: int) -> bool:
        return (
            exc.retryable
            and exc.category in {"network", "timeout", "server"}
            and retry_index < max(0, self.settings.upstream_retries_per_account)
        )

    def _retry_delay_seconds(self, retry_ordinal: int, exc: CodeArtsUpstreamError | None = None) -> float:
        base_ms = max(0.0, float(self.settings.retry_backoff_base_ms))
        cap_ms = max(base_ms, float(self.settings.retry_backoff_cap_ms))
        delay_ms = min(cap_ms, base_ms * (2 ** max(0, retry_ordinal))) if base_ms else 0.0
        jitter = min(1.0, max(0.0, float(self.settings.retry_backoff_jitter)))
        if delay_ms and jitter:
            delay_ms *= 1.0 + random.uniform(-jitter, jitter)
        delay = max(0.0, delay_ms / 1000.0)
        if exc is not None and exc.retry_after_seconds is not None:
            delay = max(delay, max(0.0, float(exc.retry_after_seconds)))
        return delay

    async def _backoff(self, budget: RetryBudget, retry_ordinal: int, exc: CodeArtsUpstreamError) -> bool:
        delay = self._retry_delay_seconds(retry_ordinal, exc)
        if delay <= 0:
            return budget.available()
        remaining = budget.remaining_seconds()
        if remaining <= delay:
            return False
        await asyncio.sleep(delay)
        return budget.available()

    @staticmethod
    def _session_chat_id(context: dict[str, Any], account_id: str) -> str:
        if context.get("account_id") == account_id and context.get("chat_id"):
            return str(context["chat_id"])
        return uuid.uuid4().hex

    async def _candidates(
        self,
        *,
        explicit_account_id: str | None,
        context: dict[str, Any],
        model: str,
    ) -> list[tuple[dict[str, Any], Any]]:
        preferred = None if explicit_account_id else str(context.get("account_id") or "") or None
        candidates = await self.auth.account_candidates(
            account_id=explicit_account_id,
            preferred_account_id=preferred,
            model=model,
        )
        max_accounts = max(1, self.settings.routing_max_attempts)
        return candidates[:max_accounts]

    async def prepare_stream_candidates(
        self,
        *,
        explicit_account_id: str | None,
        context: dict[str, Any],
        model: str,
    ) -> list[tuple[dict[str, Any], Any]]:
        candidates = await self._candidates(
            explicit_account_id=explicit_account_id,
            context=context,
            model=model,
        )
        if not candidates:
            raise CodeArtsUpstreamError(
                "No usable CodeArts account is available. Accounts may be cooling down or at concurrency capacity.",
                category="no_account",
                retryable=False,
            )
        return candidates

    def _complete_context(
        self,
        context: dict[str, Any],
        *,
        account: dict[str, Any],
        client: CodeArtsClient,
        chat_id: str,
        messages: list[dict[str, Any]],
        assistant_text: str,
    ) -> None:
        self.conversations.complete(
            context,
            account_id=str(account["id"]),
            environment=str(account["environment"]),
            chat_id=client.last_chat_id or chat_id,
            messages=messages,
            assistant_text=assistant_text,
        )

    def _new_budget(self) -> RetryBudget:
        return RetryBudget(self.settings.retry_budget_attempts, self.settings.retry_budget_seconds)

    def _reset_trace(self, model: str) -> None:
        self.last_trace = {
            "success": False,
            "account_id": "",
            "environment": "",
            "attempts": 0,
            "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
            "usage_details": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
            "error_category": "",
            "status_code": None,
            "model": model,
            "finish_reason": "stop",
        }

    async def complete(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None,
        context: dict[str, Any],
        explicit_account_id: str | None = None,
        options: dict[str, Any] | None = None,
    ) -> CompletionResult:
        self._reset_trace(model)
        candidates = await self._candidates(explicit_account_id=explicit_account_id, context=context, model=model)
        if not candidates:
            raise CodeArtsUpstreamError(
                "No usable CodeArts account is available. Accounts may be cooling down or at concurrency capacity.",
                category="no_account",
                retryable=False,
            )

        budget = self._new_budget()
        last_error: CodeArtsUpstreamError | None = None
        retry_ordinal = 0
        for account, credentials in candidates:
            account_id = str(account["id"])
            chat_id = self._session_chat_id(context, account_id)
            retry_index = 0
            while budget.available():
                request_started_at = self.auth.pool.try_begin_request(account_id)
                if request_started_at is None:
                    break
                if not budget.consume():
                    self.auth.pool.release_request(account_id)
                    break
                self.last_trace.update({
                    "account_id": account_id,
                    "environment": str(account["environment"]),
                    "attempts": budget.used,
                })
                client = CodeArtsClient(
                    self.settings,
                    auth_manager=self.auth,
                    environment=str(account["environment"]),
                    account_id=account_id,
                    credentials=credentials,
                )
                attempt_success = False
                error_category = ""
                retry_same = False
                try:
                    text = await client.chat(
                        messages=messages,
                        model=model,
                        user=user,
                        chat_id=chat_id,
                        options=options,
                    )
                    attempt_success = True
                except CodeArtsUpstreamError as exc:
                    last_error = exc
                    error_category = exc.category
                    self.last_trace.update({"error_category": exc.category, "status_code": exc.status_code})
                    retry_same = self._can_retry_same_account(exc, retry_index) and budget.available()
                    if not retry_same:
                        self._mark_failure(account_id, model, exc)
                        if explicit_account_id or not exc.retryable:
                            raise
                finally:
                    usage = self._usage(client)
                    self.auth.pool.finish_request(
                        account_id,
                        request_started_at,
                        success=attempt_success,
                        model=model,
                        environment=str(account["environment"]),
                        error_category=error_category,
                        **usage,
                    )

                if attempt_success:
                    self.last_trace.update({"success": True, "usage": self._usage(client), "usage_details": self._usage_details(client), "error_category": "", "status_code": 200, "finish_reason": getattr(client, "last_finish_reason", "stop") or "stop"})
                    self._mark_success(account_id, model)
                    self._complete_context(
                        context,
                        account=account,
                        client=client,
                        chat_id=chat_id,
                        messages=messages,
                        assistant_text=text,
                    )
                    return CompletionResult(
                        text=text,
                        conversation_id=str(context["id"]),
                        account_id=account_id,
                        environment=str(account["environment"]),
                        attempts=budget.used,
                        usage=self._usage(client),
                        usage_details=self._usage_details(client),
                        reasoning_content=getattr(client, "last_reasoning_content", "") or "",
                        tool_calls=getattr(client, "last_tool_calls", None) or None,
                        finish_reason=getattr(client, "last_finish_reason", "stop") or "stop",
                    )

                if retry_same and last_error is not None:
                    retry_index += 1
                    if not await self._backoff(budget, retry_ordinal, last_error):
                        break
                    retry_ordinal += 1
                    continue
                if last_error is not None and last_error.retryable and budget.available():
                    if not await self._backoff(budget, retry_ordinal, last_error):
                        break
                    retry_ordinal += 1
                break

        if last_error:
            raise last_error
        raise CodeArtsUpstreamError("No CodeArts account completed the request within retry budget", category="retry_budget")

    async def stream(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None,
        context: dict[str, Any],
        explicit_account_id: str | None = None,
        options: dict[str, Any] | None = None,
        prepared_candidates: list[tuple[dict[str, Any], Any]] | None = None,
    ) -> AsyncIterator[dict[str, Any]]:
        self._reset_trace(model)
        candidates = prepared_candidates
        if candidates is None:
            candidates = await self.prepare_stream_candidates(
                explicit_account_id=explicit_account_id,
                context=context,
                model=model,
            )

        budget = self._new_budget()
        last_error: CodeArtsUpstreamError | None = None
        retry_ordinal = 0
        for account, credentials in candidates:
            account_id = str(account["id"])
            chat_id = self._session_chat_id(context, account_id)
            retry_index = 0
            while budget.available():
                request_started_at = self.auth.pool.try_begin_request(account_id)
                if request_started_at is None:
                    break
                if not budget.consume():
                    self.auth.pool.release_request(account_id)
                    break
                self.last_trace.update({
                    "account_id": account_id,
                    "environment": str(account["environment"]),
                    "attempts": budget.used,
                })
                client = CodeArtsClient(
                    self.settings,
                    auth_manager=self.auth,
                    environment=str(account["environment"]),
                    account_id=account_id,
                    credentials=credentials,
                )
                emitted = False
                collected: list[str] = []
                attempt_success = False
                error_category = ""
                retry_same = False
                try:
                    async for chunk in client.chat_stream(
                        messages=messages,
                        model=model,
                        user=user,
                        chat_id=chat_id,
                        options=options,
                    ):
                        emitted = True
                        delta = chunk.get("delta") if isinstance(chunk, dict) else {"content": str(chunk)}
                        content = delta.get("content") if isinstance(delta, dict) else None
                        if isinstance(content, str):
                            collected.append(content)
                        yield chunk if isinstance(chunk, dict) else {"delta": delta, "finish_reason": None}
                    attempt_success = True
                except CodeArtsUpstreamError as exc:
                    last_error = exc
                    error_category = exc.category
                    self.last_trace.update({"error_category": exc.category, "status_code": exc.status_code})
                    if emitted:
                        self._mark_failure(account_id, model, exc)
                        raise
                    retry_same = self._can_retry_same_account(exc, retry_index) and budget.available()
                    if not retry_same:
                        self._mark_failure(account_id, model, exc)
                        if explicit_account_id or not exc.retryable:
                            raise
                finally:
                    usage = self._usage(client)
                    self.auth.pool.finish_request(
                        account_id,
                        request_started_at,
                        success=attempt_success,
                        model=model,
                        environment=str(account["environment"]),
                        error_category=error_category,
                        **usage,
                    )

                if attempt_success:
                    self.last_trace.update({"success": True, "usage": self._usage(client), "usage_details": self._usage_details(client), "error_category": "", "status_code": 200, "finish_reason": getattr(client, "last_finish_reason", "stop") or "stop"})
                    self._mark_success(account_id, model)
                    self._complete_context(
                        context,
                        account=account,
                        client=client,
                        chat_id=chat_id,
                        messages=messages,
                        assistant_text="".join(collected),
                    )
                    return

                if retry_same and last_error is not None:
                    retry_index += 1
                    if not await self._backoff(budget, retry_ordinal, last_error):
                        break
                    retry_ordinal += 1
                    continue
                if last_error is not None and last_error.retryable and budget.available():
                    if not await self._backoff(budget, retry_ordinal, last_error):
                        break
                    retry_ordinal += 1
                break

        if last_error:
            raise last_error
        raise CodeArtsUpstreamError("No CodeArts account completed the request within retry budget", category="retry_budget")
