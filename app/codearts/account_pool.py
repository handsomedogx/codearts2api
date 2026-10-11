from __future__ import annotations

import json
import os
import random
import threading
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from app.config import ENV_CN, Settings, normalize_environment


class AccountPool:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.accounts_dir = Path(settings.accounts_dir)
        self.routing_file = Path(settings.routing_file)
        self._rr_lock = threading.Lock()
        self._stats_lock = threading.RLock()
        self._in_flight: dict[str, int] = {}
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

    @staticmethod
    def _now_iso() -> str:
        return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

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

    def _health(self, record: dict[str, Any]) -> dict[str, Any]:
        value = record.get("health")
        health = dict(value) if isinstance(value, dict) else {}
        cooldown_until_ts = float(health.get("cooldown_until_ts") or 0.0)
        failures = int(health.get("consecutive_failures") or 0)
        if cooldown_until_ts > time.time():
            status = "cooldown"
        elif failures:
            status = "degraded"
        else:
            status = "healthy"
        health["status"] = status
        return health

    def is_usable(self, record: dict[str, Any]) -> bool:
        if not (record.get("access_key_id") and record.get("secret_access_key")):
            return False
        return self._health(record)["status"] != "cooldown"

    def public(self, record: dict[str, Any]) -> dict[str, Any]:
        environment = normalize_environment(str(record.get("environment") or ENV_CN))
        health = self._health(record)
        stats = dict(record.get("stats") or {})
        cooldown_until_ts = float(health.get("cooldown_until_ts") or 0.0)
        cooldown_until = ""
        if cooldown_until_ts > time.time():
            cooldown_until = datetime.fromtimestamp(cooldown_until_ts, timezone.utc).isoformat().replace("+00:00", "Z")
        request_count = int(stats.get("request_count") or 0)
        total_latency_ms = float(stats.get("total_latency_ms") or 0.0)
        avg_latency_ms = round(total_latency_ms / request_count, 1) if request_count else None
        prompt_tokens = int(stats.get("prompt_tokens") or 0)
        completion_tokens = int(stats.get("completion_tokens") or 0)
        total_tokens = int(stats.get("total_tokens") or 0) or prompt_tokens + completion_tokens
        success_count = int(stats.get("success_count") or 0)
        failure_count = int(stats.get("failure_count") or 0)
        success_latency_ms = float(stats.get("success_latency_ms") or 0.0)
        avg_output_tps = (completion_tokens / (success_latency_ms / 1000.0)) if success_latency_ms > 0 else None
        account_id = str(record.get("id") or "")
        with self._stats_lock:
            in_flight = int(self._in_flight.get(account_id, 0))
        return {
            "id": account_id,
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
            "health": {
                "status": health["status"],
                "consecutive_failures": int(health.get("consecutive_failures") or 0),
                "total_failures": int(health.get("total_failures") or 0),
                "total_successes": int(health.get("total_successes") or 0),
                "last_failure_at": str(health.get("last_failure_at") or ""),
                "last_success_at": str(health.get("last_success_at") or ""),
                "last_error_category": str(health.get("last_error_category") or ""),
                "last_status_code": health.get("last_status_code"),
                "cooldown_until": cooldown_until,
            },
            "stats": {
                # in_flight is the live number of requests currently using this account.
                "in_flight": in_flight,
                "concurrency": in_flight,
                "request_count": request_count,
                "success_count": success_count,
                "failure_count": failure_count,
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": total_tokens,
                "avg_output_tps": round(avg_output_tps, 2) if avg_output_tps is not None else None,
                "avg_latency_ms": avg_latency_ms,
                "last_latency_ms": round(float(stats.get("last_latency_ms") or 0.0), 1) if request_count else None,
                "last_request_at": str(stats.get("last_request_at") or ""),
            },
        }

    def list_public(self) -> list[dict[str, Any]]:
        return [self.public(record) for record in self.list_records()]

    def begin_request(self, account_id: str) -> float:
        with self._stats_lock:
            self._in_flight[account_id] = int(self._in_flight.get(account_id, 0)) + 1
        return time.perf_counter()

    def finish_request(
        self,
        account_id: str,
        started_at: float,
        *,
        success: bool = True,
        prompt_tokens: int = 0,
        completion_tokens: int = 0,
        total_tokens: int = 0,
        model: str = "",
        environment: str = "",
        error_category: str = "",
    ) -> dict[str, Any] | None:
        elapsed_ms = max(0.0, (time.perf_counter() - started_at) * 1000.0)
        prompt_tokens = max(0, int(prompt_tokens or 0))
        completion_tokens = max(0, int(completion_tokens or 0))
        total_tokens = max(0, int(total_tokens or 0)) or prompt_tokens + completion_tokens
        now_iso = self._now_iso()
        with self._stats_lock:
            current = int(self._in_flight.get(account_id, 0))
            if current <= 1:
                self._in_flight.pop(account_id, None)
            else:
                self._in_flight[account_id] = current - 1

            record = self.get(account_id)
            if record:
                stats = dict(record.get("stats") or {})
                stats.update(
                    {
                        "request_count": int(stats.get("request_count") or 0) + 1,
                        "success_count": int(stats.get("success_count") or 0) + (1 if success else 0),
                        "failure_count": int(stats.get("failure_count") or 0) + (0 if success else 1),
                        "prompt_tokens": int(stats.get("prompt_tokens") or 0) + prompt_tokens,
                        "completion_tokens": int(stats.get("completion_tokens") or 0) + completion_tokens,
                        "total_tokens": int(stats.get("total_tokens") or 0) + total_tokens,
                        "total_latency_ms": float(stats.get("total_latency_ms") or 0.0) + elapsed_ms,
                        "success_latency_ms": float(stats.get("success_latency_ms") or 0.0) + (elapsed_ms if success else 0.0),
                        "last_latency_ms": elapsed_ms,
                        "last_request_at": now_iso,
                    }
                )
                record["stats"] = stats
                record = self.save(record, account_id)

        # The time-series log is intentionally prompt-free and credential-free.
        from app.codearts.usage import get_usage_store

        get_usage_store(self.settings).append(
            {
                "time": now_iso,
                "account_id": account_id,
                "environment": environment,
                "model": model,
                "success": success,
                "error_category": error_category,
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": total_tokens,
                "latency_ms": elapsed_ms,
            }
        )
        return record

    def mark_success(self, account_id: str) -> dict[str, Any] | None:
        record = self.get(account_id)
        if not record:
            return None
        health = dict(record.get("health") or {})
        health.update(
            {
                "consecutive_failures": 0,
                "total_successes": int(health.get("total_successes") or 0) + 1,
                "last_success_at": self._now_iso(),
                "last_error_category": "",
                "last_status_code": None,
                "cooldown_until_ts": 0.0,
            }
        )
        record["health"] = health
        return self.save(record, account_id)

    def mark_failure(
        self,
        account_id: str,
        *,
        category: str,
        status_code: int | None = None,
        retry_after_seconds: float | None = None,
    ) -> dict[str, Any] | None:
        record = self.get(account_id)
        if not record:
            return None
        health = dict(record.get("health") or {})
        failures = int(health.get("consecutive_failures") or 0) + 1
        base = max(1, self.settings.health_cooldown_base_seconds)
        cooldown = min(self.settings.health_cooldown_max_seconds, base * (2 ** max(0, failures - 1)))
        if status_code == 429:
            cooldown = max(cooldown, self.settings.health_rate_limit_cooldown_seconds)
        elif status_code in {401, 403}:
            cooldown = max(cooldown, self.settings.health_auth_cooldown_seconds)
        if retry_after_seconds is not None:
            cooldown = max(cooldown, max(0.0, retry_after_seconds))
        health.update(
            {
                "consecutive_failures": failures,
                "total_failures": int(health.get("total_failures") or 0) + 1,
                "last_failure_at": self._now_iso(),
                "last_error_category": category[:80],
                "last_status_code": status_code,
                "cooldown_until_ts": time.time() + float(cooldown),
            }
        )
        record["health"] = health
        return self.save(record, account_id)

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

    def candidates(
        self,
        *,
        account_id: str | None = None,
        preferred_account_id: str | None = None,
        exclude_ids: set[str] | None = None,
    ) -> list[dict[str, Any]]:
        excluded = exclude_ids or set()
        if account_id:
            record = self.get(account_id)
            if record and account_id not in excluded and self.is_usable(record):
                return [record]
            return []

        records = [item for item in self.list_records() if str(item.get("id") or "") not in excluded and self.is_usable(item)]
        if not records:
            return []

        preferred: list[dict[str, Any]] = []
        if preferred_account_id:
            preferred = [item for item in records if item.get("id") == preferred_account_id]
            records = [item for item in records if item.get("id") != preferred_account_id]

        strategy = self.routing()["strategy"]
        if strategy == "random":
            random.shuffle(records)
        else:
            with self._rr_lock:
                if records:
                    start = self._rr_index % len(records)
                    records = records[start:] + records[:start]
                    self._rr_index = (self._rr_index + 1) % len(records)
        return preferred + records

    def choose(self, account_id: str | None = None) -> dict[str, Any] | None:
        items = self.candidates(account_id=account_id)
        return items[0] if items else None
