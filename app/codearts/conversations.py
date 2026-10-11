from __future__ import annotations

import hashlib
import json
import os
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from app.config import Settings


class ConversationStore:
    """Persist only conversation routing metadata and message hashes, never prompt text."""

    def __init__(self, settings: Settings) -> None:
        self.root = Path(settings.conversations_dir)

    @staticmethod
    def _now_iso() -> str:
        return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

    @staticmethod
    def _safe_id(value: str) -> str:
        safe = "".join(ch for ch in value if ch.isalnum() or ch in "-_")
        if not safe or safe != value:
            raise ValueError("Invalid conversation id")
        return safe

    def path(self, conversation_id: str) -> Path:
        return self.root / f"{self._safe_id(conversation_id)}.json"

    @staticmethod
    def messages_hash(messages: list[dict[str, Any]]) -> str:
        canonical = json.dumps(messages, ensure_ascii=False, sort_keys=True, separators=(",", ":"), default=str)
        return hashlib.sha256(canonical.encode("utf-8")).hexdigest()

    def _read(self, path: Path) -> dict[str, Any] | None:
        if not path.exists():
            return None
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return None
        return value if isinstance(value, dict) else None

    def _write(self, value: dict[str, Any]) -> dict[str, Any]:
        conversation_id = self._safe_id(str(value["id"]))
        path = self.path(conversation_id)
        path.parent.mkdir(parents=True, exist_ok=True)
        temp = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
        temp.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")
        os.replace(temp, path)
        return value

    def get(self, conversation_id: str) -> dict[str, Any] | None:
        return self._read(self.path(conversation_id))

    def _find_by_history_hash(self, value: str) -> dict[str, Any] | None:
        if not value or not self.root.exists():
            return None
        matches: list[dict[str, Any]] = []
        for path in self.root.glob("*.json"):
            record = self._read(path)
            if record and record.get("history_hash") == value:
                matches.append(record)
        if not matches:
            return None
        matches.sort(key=lambda item: str(item.get("updated_at") or ""), reverse=True)
        return matches[0]

    def resolve(self, messages: list[dict[str, Any]], conversation_id: str | None = None) -> dict[str, Any]:
        if conversation_id:
            existing = self.get(conversation_id)
            if existing:
                return existing
            now = self._now_iso()
            return {"id": self._safe_id(conversation_id), "created_at": now, "updated_at": now, "turns": 0}

        prefix = messages[:-1] if messages and str(messages[-1].get("role") or "") == "user" else messages
        if prefix:
            existing = self._find_by_history_hash(self.messages_hash(prefix))
            if existing:
                return existing

        now = self._now_iso()
        return {"id": uuid.uuid4().hex, "created_at": now, "updated_at": now, "turns": 0}

    def complete(
        self,
        context: dict[str, Any],
        *,
        account_id: str,
        environment: str,
        chat_id: str,
        messages: list[dict[str, Any]],
        assistant_text: str,
    ) -> dict[str, Any]:
        history = list(messages)
        history.append({"role": "assistant", "content": assistant_text})
        updated = dict(context)
        updated.update(
            {
                "account_id": account_id,
                "environment": environment,
                "chat_id": chat_id,
                "history_hash": self.messages_hash(history),
                "turns": int(updated.get("turns") or 0) + 1,
                "updated_at": self._now_iso(),
            }
        )
        return self._write(updated)
