from __future__ import annotations

import asyncio
from contextlib import suppress
from typing import Any

from app.codearts.auth import AuthManager
from app.config import Settings


class KeepaliveService:
    def __init__(self, settings: Settings, auth: AuthManager) -> None:
        self.settings = settings
        self.auth = auth
        self._task: asyncio.Task[None] | None = None

    async def run_once(self, *, due_only: bool = True) -> list[dict[str, Any]]:
        if not self.settings.keepalive_enabled:
            return []
        return await self.auth.probe_accounts(due_only=due_only)

    async def _loop(self) -> None:
        # Delay the first background pass: startup and TestClient creation must stay cheap.
        while True:
            await asyncio.sleep(max(5, int(self.settings.keepalive_poll_seconds)))
            try:
                await self.run_once(due_only=True)
            except asyncio.CancelledError:
                raise
            except Exception:
                # Per-account probe details are recorded by AuthManager; the scheduler itself
                # must survive a transient filesystem/network failure.
                continue

    def start(self) -> None:
        if not self.settings.keepalive_enabled or self._task is not None:
            return
        self._task = asyncio.create_task(self._loop(), name="codearts2api-keepalive")

    async def stop(self) -> None:
        if self._task is None:
            return
        self._task.cancel()
        with suppress(asyncio.CancelledError):
            await self._task
        self._task = None
