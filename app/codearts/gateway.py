from __future__ import annotations

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


class CodeArtsGateway:
    def __init__(self, settings: Settings, auth: AuthManager) -> None:
        self.settings = settings
        self.auth = auth
        self.conversations = ConversationStore(settings)

    def resolve_conversation(
        self,
        messages: list[dict[str, Any]],
        conversation_id: str | None = None,
    ) -> dict[str, Any]:
        return self.conversations.resolve(messages, conversation_id)

    def _mark_success(self, account_id: str) -> None:
        if self.auth.pool.get(account_id):
            self.auth.pool.mark_success(account_id)

    def _mark_failure(self, account_id: str, exc: CodeArtsUpstreamError) -> None:
        if self.auth.pool.get(account_id):
            self.auth.pool.mark_failure(
                account_id,
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

    def _can_retry_same_account(self, exc: CodeArtsUpstreamError, retry_index: int) -> bool:
        return (
            exc.retryable
            and exc.category in {"network", "timeout", "server"}
            and retry_index < max(0, self.settings.upstream_retries_per_account)
        )

    @staticmethod
    def _session_params(context: dict[str, Any], account_id: str) -> tuple[str, str | None, bool]:
        same_account = bool(context.get("account_id") == account_id and context.get("chat_id"))
        if same_account:
            return (
                str(context["chat_id"]),
                str(context.get("parent_message_id") or "") or None,
                int(context.get("turns") or 0) > 0,
            )
        return uuid.uuid4().hex, None, False

    async def _candidates(
        self,
        *,
        explicit_account_id: str | None,
        context: dict[str, Any],
    ) -> list[tuple[dict[str, Any], Any]]:
        preferred = None if explicit_account_id else str(context.get("account_id") or "") or None
        candidates = await self.auth.account_candidates(
            account_id=explicit_account_id,
            preferred_account_id=preferred,
        )
        max_attempts = max(1, self.settings.routing_max_attempts)
        return candidates[:max_attempts]

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
            response_message_id=client.last_response_message_id,
            messages=messages,
            assistant_text=assistant_text,
        )

    async def complete(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None,
        context: dict[str, Any],
        explicit_account_id: str | None = None,
    ) -> CompletionResult:
        candidates = await self._candidates(explicit_account_id=explicit_account_id, context=context)
        if not candidates:
            raise CodeArtsUpstreamError(
                "No usable CodeArts account is available. Add an account or wait for cooldown to expire.",
                category="no_account",
                retryable=False,
            )

        last_error: CodeArtsUpstreamError | None = None
        total_attempts = 0
        for account, credentials in candidates:
            account_id = str(account["id"])
            chat_id, parent_message_id, native_continuation = self._session_params(context, account_id)
            retry_index = 0
            while True:
                total_attempts += 1
                client = CodeArtsClient(
                    self.settings,
                    auth_manager=self.auth,
                    environment=str(account["environment"]),
                    account_id=account_id,
                    credentials=credentials,
                )
                request_started_at = self.auth.pool.begin_request(account_id)
                attempt_success = False
                error_category = ""
                try:
                    text = await client.chat(
                        messages=messages,
                        model=model,
                        user=user,
                        chat_id=chat_id,
                        parent_message_id=parent_message_id,
                        native_continuation=native_continuation,
                    )
                    attempt_success = True
                except CodeArtsUpstreamError as exc:
                    last_error = exc
                    error_category = exc.category
                    if self._can_retry_same_account(exc, retry_index):
                        retry_index += 1
                        continue
                    self._mark_failure(account_id, exc)
                    if explicit_account_id or not exc.retryable:
                        raise
                    break
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

                self._mark_success(account_id)
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
                    attempts=total_attempts,
                    usage=self._usage(client),
                )

        if last_error:
            raise last_error
        raise CodeArtsUpstreamError("No CodeArts account completed the request", category="no_account")

    async def stream(
        self,
        *,
        messages: list[dict[str, Any]],
        model: str,
        user: str | None,
        context: dict[str, Any],
        explicit_account_id: str | None = None,
    ) -> AsyncIterator[str]:
        candidates = await self._candidates(explicit_account_id=explicit_account_id, context=context)
        if not candidates:
            raise CodeArtsUpstreamError(
                "No usable CodeArts account is available. Add an account or wait for cooldown to expire.",
                category="no_account",
                retryable=False,
            )

        last_error: CodeArtsUpstreamError | None = None
        for account, credentials in candidates:
            account_id = str(account["id"])
            chat_id, parent_message_id, native_continuation = self._session_params(context, account_id)
            retry_index = 0
            while True:
                client = CodeArtsClient(
                    self.settings,
                    auth_manager=self.auth,
                    environment=str(account["environment"]),
                    account_id=account_id,
                    credentials=credentials,
                )
                emitted = False
                collected: list[str] = []
                request_started_at = self.auth.pool.begin_request(account_id)
                attempt_success = False
                error_category = ""
                try:
                    async for delta in client.chat_stream(
                        messages=messages,
                        model=model,
                        user=user,
                        chat_id=chat_id,
                        parent_message_id=parent_message_id,
                        native_continuation=native_continuation,
                    ):
                        emitted = True
                        collected.append(delta)
                        yield delta
                    attempt_success = True
                except CodeArtsUpstreamError as exc:
                    last_error = exc
                    error_category = exc.category
                    # Once bytes have reached the caller, switching accounts could duplicate text.
                    if emitted:
                        self._mark_failure(account_id, exc)
                        raise
                    if self._can_retry_same_account(exc, retry_index):
                        retry_index += 1
                        continue
                    self._mark_failure(account_id, exc)
                    if explicit_account_id or not exc.retryable:
                        raise
                    break
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

                self._mark_success(account_id)
                self._complete_context(
                    context,
                    account=account,
                    client=client,
                    chat_id=chat_id,
                    messages=messages,
                    assistant_text="".join(collected),
                )
                return

        if last_error:
            raise last_error
        raise CodeArtsUpstreamError("No CodeArts account completed the request", category="no_account")
