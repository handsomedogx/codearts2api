from __future__ import annotations

import json
import os
import random
import threading
import uuid
from pathlib import Path
from typing import Any

from app.config import ENV_CN, Settings, normalize_environment


class AccountPool:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.accounts_dir = Path(settings.accounts_dir)
        self.routing_file = Path(settings.routing_file)
        self._rr_lock = threading.Lock()
        self._rr_index = 0

    def _read_json(self, path: Path) -> dict[str, Any] | None:
        if not path.exists():
            return None
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return None
        return payload if isinstance(payload, dict) else None

    def _atomic_write_json(self, path: Path, payload: dict[str, Any]) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        temp = path.with_name(f".{path.name}.{uuid.uuid4().hex}.tmp")
        temp.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        os.replace(temp, path)

    def account_path(self, account_id: str) -> Path:
        safe = "".join(ch for ch in account_id if ch.isalnum() or ch in "-_")
        if not safe or safe != account_id:
            raise ValueError("Invalid account id")
        return self.accounts_dir / f"{safe}.json"

    def save(self, record: dict[str, Any], account_id: str | None = None) -> dict[str, Any]:
        account_id = account_id or str(record.get("id") or uuid.uuid4().hex)
        environment = normalize_environment(str(record.get("environment") or ENV_CN))
        payload = dict(record)
        payload["id"] = account_id
        payload["environment"] = environment
        self._atomic_write_json(self.account_path(account_id), payload)
        return payload

    def get(self, account_id: str) -> dict[str, Any] | None:
        return self._read_json(self.account_path(account_id))

    def delete(self, account_id: str) -> bool:
        path = self.account_path(account_id)
        existed = path.exists()
        try:
            path.unlink(missing_ok=True)
        except OSError:
            return False
        return existed

    def list_records(self) -> list[dict[str, Any]]:
        if not self.accounts_dir.exists():
            return []
        records: list[dict[str, Any]] = []
        for path in sorted(self.accounts_dir.glob("*.json")):
            payload = self._read_json(path)
            if not payload:
                continue
            account_id = str(payload.get("id") or path.stem)
            try:
                environment = normalize_environment(str(payload.get("environment") or ENV_CN))
            except ValueError:
                continue
            payload["id"] = account_id
            payload["environment"] = environment
            records.append(payload)
        records.sort(key=lambda item: str(item.get("updated_at") or item.get("created_at") or ""), reverse=True)
        return records

    def public(self, record: dict[str, Any]) -> dict[str, Any]:
        environment = normalize_environment(str(record.get("environment") or ENV_CN))
        return {
            "id": str(record.get("id") or ""),
            "environment": environment,
            "environment_label": self.settings.profile(environment).label,
            "authenticated": bool(record.get("access_key_id") and record.get("secret_access_key")),
            "source": "oauth",
            "expires_at": str(record.get("expiration") or ""),
            "refresh_available": bool(record.get("refresh_token") and record.get("auth_context")),
            "user_id": str(record.get("user_id") or ""),
            "user_name": str(record.get("user_name") or ""),
            "domain_id": str(record.get("domain_id") or ""),
            "updated_at": str(record.get("updated_at") or ""),
        }

    def list_public(self) -> list[dict[str, Any]]:
        return [self.public(record) for record in self.list_records()]

    def routing(self) -> dict[str, Any]:
        payload = self._read_json(self.routing_file) or {}
        strategy = str(payload.get("strategy") or self.settings.routing_strategy or "round_robin").strip().lower()
        if strategy not in {"round_robin", "random"}:
            strategy = "round_robin"
        return {"strategy": strategy}

    def set_routing(self, strategy: str) -> dict[str, Any]:
        normalized = strategy.strip().lower()
        if normalized not in {"round_robin", "random"}:
            raise ValueError("Unsupported routing strategy")
        payload = {"strategy": normalized}
        self._atomic_write_json(self.routing_file, payload)
        return payload

    def choose(self, account_id: str | None = None) -> dict[str, Any] | None:
        if account_id:
            record = self.get(account_id)
            if record and record.get("access_key_id") and record.get("secret_access_key"):
                return record
            return None
        records = [
            item
            for item in self.list_records()
            if item.get("access_key_id") and item.get("secret_access_key")
        ]
        if not records:
            return None
        strategy = self.routing()["strategy"]
        if strategy == "random":
            return random.choice(records)
        with self._rr_lock:
            record = records[self._rr_index % len(records)]
            self._rr_index = (self._rr_index + 1) % len(records)
            return record
