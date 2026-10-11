from __future__ import annotations

import hmac
import json
import os
import secrets
import threading
import uuid
from pathlib import Path
from typing import Any

from app.config import Settings


class GatewaySettingsStore:
    """Persistent local gateway settings.

    The normal user-facing API key is generated once and stored outside Git.
    CODEARTS2API_API_KEY remains an advanced deployment override; when present,
    it wins over the persisted key and cannot be regenerated from the UI.
    """

    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.path = Path(settings.gateway_settings_file)
        self._lock = threading.RLock()

    @staticmethod
    def _generated_key() -> str:
        return f"sk-ca-{secrets.token_urlsafe(32)}"

    def _read(self) -> dict[str, Any]:
        if not self.path.exists():
            return {}
        try:
            payload = json.loads(self.path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return {}
        return payload if isinstance(payload, dict) else {}

    def _write(self, payload: dict[str, Any]) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        temp = self.path.with_name(f".{self.path.name}.{uuid.uuid4().hex}.tmp")
        temp.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
        os.replace(temp, self.path)

    @staticmethod
    def environment_override() -> str:
        return os.getenv("CODEARTS2API_API_KEY", "").strip()

    def ensure_api_key(self) -> dict[str, Any]:
        override = self.environment_override()
        if override:
            return {
                "api_key": override,
                "source": "environment",
                "regeneratable": False,
            }
        with self._lock:
            payload = self._read()
            api_key = str(payload.get("api_key") or "").strip()
            if not api_key:
                api_key = self._generated_key()
                payload["api_key"] = api_key
                self._write(payload)
            return {
                "api_key": api_key,
                "source": "generated",
                "regeneratable": True,
            }

    def regenerate_api_key(self) -> dict[str, Any]:
        if self.environment_override():
            raise RuntimeError("API key is managed by CODEARTS2API_API_KEY and cannot be regenerated here")
        with self._lock:
            payload = self._read()
            payload["api_key"] = self._generated_key()
            self._write(payload)
            return {
                "api_key": payload["api_key"],
                "source": "generated",
                "regeneratable": True,
            }

    def verify(self, candidate: str) -> bool:
        expected = str(self.ensure_api_key()["api_key"])
        return bool(candidate) and hmac.compare_digest(candidate, expected)


def get_gateway_settings_store(settings: Settings) -> GatewaySettingsStore:
    return GatewaySettingsStore(settings)
