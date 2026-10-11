from __future__ import annotations

import json
import threading
import time
from collections import deque
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from app.config import Settings


_TIME_RANGES = {
    "1h": 3600,
    "24h": 86400,
    "3d": 3 * 86400,
    "7d": 7 * 86400,
    "30d": 30 * 86400,
}


class RequestLogStore:
    """Append-only, prompt-free request log for user-facing gateway history."""

    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.path = Path(settings.request_logs_file)
        self._lock = threading.RLock()
        self._active = 0

    @staticmethod
    def _now_iso() -> str:
        return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

    def begin(self) -> float:
        with self._lock:
            self._active += 1
        return time.perf_counter()

    def finish(self, started_at: float, entry: dict[str, Any]) -> dict[str, Any]:
        duration_ms = max(0.0, (time.perf_counter() - started_at) * 1000.0)
        row = self._sanitize({**entry, "duration_ms": duration_ms})
        with self._lock:
            self._active = max(0, self._active - 1)
            try:
                self.path.parent.mkdir(parents=True, exist_ok=True)
                with self.path.open("a", encoding="utf-8") as handle:
                    handle.write(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n")
            except OSError:
                # Observability must never take down an otherwise successful model request.
                pass
        return row

    def _sanitize(self, entry: dict[str, Any]) -> dict[str, Any]:
        now = time.time()
        usage = entry.get("usage") if isinstance(entry.get("usage"), dict) else {}
        return {
            "time": str(entry.get("time") or self._now_iso()),
            "ts": float(entry.get("ts") or now),
            "request_id": str(entry.get("request_id") or "")[:96],
            "method": "POST",
            "path": "/v1/chat/completions",
            "status": int(entry.get("status") or 0),
            "success": bool(entry.get("success")),
            "stream": bool(entry.get("stream")),
            "model": str(entry.get("model") or "")[:160],
            "account_id": str(entry.get("account_id") or "")[:128],
            "account_name": str(entry.get("account_name") or "")[:160],
            "environment": str(entry.get("environment") or "")[:32],
            "source_ip": str(entry.get("source_ip") or "")[:128],
            "user_agent": str(entry.get("user_agent") or "")[:768],
            "duration_ms": round(float(entry.get("duration_ms") or 0.0), 1),
            "prompt_tokens": max(0, int(entry.get("prompt_tokens") or usage.get("prompt_tokens") or 0)),
            "completion_tokens": max(0, int(entry.get("completion_tokens") or usage.get("completion_tokens") or 0)),
            "total_tokens": max(0, int(entry.get("total_tokens") or usage.get("total_tokens") or 0)),
            "attempts": max(0, int(entry.get("attempts") or 0)),
            "conversation_id": str(entry.get("conversation_id") or "")[:128],
            "error_category": str(entry.get("error_category") or "")[:96],
        }

    def active_count(self) -> int:
        with self._lock:
            return self._active

    def archive_size(self) -> int:
        try:
            return self.path.stat().st_size
        except OSError:
            return 0

    @staticmethod
    def _search_blob(row: dict[str, Any]) -> str:
        fields = (
            "request_id", "model", "account_id", "account_name", "environment",
            "source_ip", "user_agent", "conversation_id", "error_category",
        )
        return "\n".join(str(row.get(field) or "") for field in fields).lower()

    @staticmethod
    def _time_matches(row: dict[str, Any], time_range: str, now: float) -> bool:
        if time_range == "all":
            return True
        seconds = _TIME_RANGES.get(time_range)
        if seconds is None:
            return True
        try:
            ts = float(row.get("ts") or 0)
        except (TypeError, ValueError):
            return False
        return ts >= now - seconds

    def query(
        self,
        *,
        q: str = "",
        result: str = "all",
        time_range: str = "24h",
        limit: int = 100,
    ) -> dict[str, Any]:
        q_norm = q.strip().lower()
        result = result.strip().lower()
        if result not in {"all", "success", "failure"}:
            result = "all"
        if time_range not in {"all", *_TIME_RANGES.keys()}:
            time_range = "24h"
        limit = max(1, min(500, int(limit)))
        now = time.time()
        recent: deque[dict[str, Any]] = deque(maxlen=limit)
        completed = successes = http_successes = 0
        total_latency = 0.0
        total_matched = 0

        if self.path.exists():
            try:
                with self.path.open("r", encoding="utf-8") as handle:
                    for line in handle:
                        try:
                            row = json.loads(line)
                        except json.JSONDecodeError:
                            continue
                        if not isinstance(row, dict) or not self._time_matches(row, time_range, now):
                            continue
                        if q_norm and q_norm not in self._search_blob(row):
                            continue

                        # Summary follows time/search filters, but intentionally ignores the
                        # success/failure dropdown so success rate remains meaningful.
                        completed += 1
                        ok = bool(row.get("success"))
                        successes += 1 if ok else 0
                        status = int(row.get("status") or 0)
                        http_successes += 1 if 200 <= status < 300 else 0
                        total_latency += max(0.0, float(row.get("duration_ms") or 0.0))

                        if result == "success" and not ok:
                            continue
                        if result == "failure" and ok:
                            continue
                        total_matched += 1
                        recent.append(row)
            except OSError:
                pass

        failures = max(0, completed - successes)
        return {
            "items": list(reversed(recent)),
            "total_matched": total_matched,
            "summary": {
                "completed": completed,
                "successes": successes,
                "failures": failures,
                "success_rate": round(successes / completed * 100.0, 1) if completed else None,
                "http_success_rate": round(http_successes / completed * 100.0, 1) if completed else None,
                "avg_latency_ms": round(total_latency / completed, 1) if completed else None,
                "in_progress": self.active_count(),
            },
            "archive_size_bytes": self.archive_size(),
            "filters": {"q": q, "result": result, "time_range": time_range, "limit": limit},
        }


_stores: dict[str, RequestLogStore] = {}
_stores_lock = threading.Lock()


def get_request_log_store(settings: Settings) -> RequestLogStore:
    key = str(Path(settings.request_logs_file).resolve())
    with _stores_lock:
        store = _stores.get(key)
        if store is None:
            store = RequestLogStore(settings)
            _stores[key] = store
        return store
