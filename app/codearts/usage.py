from __future__ import annotations

import json
import math
import threading
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from app.config import Settings


class UsageStore:
    def __init__(self, settings: Settings) -> None:
        self.path = Path(settings.usage_file)
        self._lock = threading.RLock()

    @staticmethod
    def _now() -> datetime:
        return datetime.now(timezone.utc)

    @staticmethod
    def _iso(value: datetime) -> str:
        return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")

    @staticmethod
    def _parse_time(value: Any) -> datetime | None:
        if not isinstance(value, str) or not value:
            return None
        try:
            parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
        except ValueError:
            return None
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=timezone.utc)
        return parsed.astimezone(timezone.utc)

    def append(self, event: dict[str, Any]) -> None:
        payload = {
            "time": str(event.get("time") or self._iso(self._now())),
            "account_id": str(event.get("account_id") or ""),
            "environment": str(event.get("environment") or ""),
            "model": str(event.get("model") or ""),
            "success": bool(event.get("success")),
            "error_category": str(event.get("error_category") or "")[:80],
            "prompt_tokens": max(0, int(event.get("prompt_tokens") or 0)),
            "completion_tokens": max(0, int(event.get("completion_tokens") or 0)),
            "total_tokens": max(0, int(event.get("total_tokens") or 0)),
            "latency_ms": max(0.0, float(event.get("latency_ms") or 0.0)),
        }
        if payload["total_tokens"] <= 0:
            payload["total_tokens"] = payload["prompt_tokens"] + payload["completion_tokens"]
        line = json.dumps(payload, ensure_ascii=False, separators=(",", ":")) + "\n"
        with self._lock:
            self.path.parent.mkdir(parents=True, exist_ok=True)
            with self.path.open("a", encoding="utf-8", newline="") as handle:
                handle.write(line)

    def _events(self, cutoff: datetime) -> list[dict[str, Any]]:
        if not self.path.exists():
            return []
        events: list[dict[str, Any]] = []
        with self._lock:
            try:
                lines = self.path.read_text(encoding="utf-8").splitlines()
            except OSError:
                return []
        for line in lines:
            if not line.strip():
                continue
            try:
                item = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not isinstance(item, dict):
                continue
            when = self._parse_time(item.get("time"))
            if when is None or when < cutoff:
                continue
            item["_when"] = when
            events.append(item)
        return events

    def summary(self, days: int = 3, points: int = 30) -> dict[str, Any]:
        days = min(90, max(1, int(days)))
        points = min(96, max(6, int(points)))
        now = self._now()
        cutoff = now - timedelta(days=days)
        events = self._events(cutoff)

        request_count = len(events)
        failure_count = sum(1 for item in events if not bool(item.get("success")))
        success_count = request_count - failure_count
        prompt_tokens = sum(max(0, int(item.get("prompt_tokens") or 0)) for item in events)
        completion_tokens = sum(max(0, int(item.get("completion_tokens") or 0)) for item in events)
        total_tokens = sum(max(0, int(item.get("total_tokens") or 0)) for item in events)
        if total_tokens <= 0:
            total_tokens = prompt_tokens + completion_tokens
        total_latency_ms = sum(max(0.0, float(item.get("latency_ms") or 0.0)) for item in events)
        avg_latency_ms = total_latency_ms / request_count if request_count else 0.0
        output_tps = completion_tokens / (total_latency_ms / 1000.0) if total_latency_ms > 0 else 0.0

        span_seconds = max(1.0, (now - cutoff).total_seconds())
        bucket_seconds = span_seconds / points
        buckets = [
            {
                "start": self._iso(cutoff + timedelta(seconds=bucket_seconds * idx)),
                "prompt_tokens": 0,
                "completion_tokens": 0,
                "total_tokens": 0,
                "requests": 0,
                "failures": 0,
                "latency_ms": 0.0,
            }
            for idx in range(points)
        ]
        for item in events:
            when = item["_when"]
            idx = int(math.floor((when - cutoff).total_seconds() / bucket_seconds))
            idx = min(points - 1, max(0, idx))
            bucket = buckets[idx]
            prompt = max(0, int(item.get("prompt_tokens") or 0))
            completion = max(0, int(item.get("completion_tokens") or 0))
            total = max(0, int(item.get("total_tokens") or 0)) or prompt + completion
            bucket["prompt_tokens"] += prompt
            bucket["completion_tokens"] += completion
            bucket["total_tokens"] += total
            bucket["requests"] += 1
            bucket["failures"] += 0 if bool(item.get("success")) else 1
            bucket["latency_ms"] += max(0.0, float(item.get("latency_ms") or 0.0))

        for bucket in buckets:
            requests = int(bucket["requests"])
            bucket["avg_latency_ms"] = round(float(bucket.pop("latency_ms")) / requests, 1) if requests else 0.0

        peak = max(buckets, key=lambda item: int(item["total_tokens"]), default=None)
        nonempty = [item for item in buckets if int(item["requests"]) > 0]
        average_bucket_tokens = (
            sum(int(item["total_tokens"]) for item in nonempty) / len(nonempty)
            if nonempty else 0.0
        )
        return {
            "range_days": days,
            "points": points,
            "start": self._iso(cutoff),
            "end": self._iso(now),
            "request_count": request_count,
            "success_count": success_count,
            "failure_count": failure_count,
            "success_rate": round(success_count / request_count * 100.0, 2) if request_count else 100.0,
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": total_tokens,
            "prompt_share": round(prompt_tokens / total_tokens * 100.0, 2) if total_tokens else 0.0,
            "completion_share": round(completion_tokens / total_tokens * 100.0, 2) if total_tokens else 0.0,
            "avg_latency_ms": round(avg_latency_ms, 1),
            "avg_output_tps": round(output_tps, 2),
            "peak_tokens": int(peak["total_tokens"]) if peak else 0,
            "peak_at": str(peak["start"]) if peak else "",
            "average_bucket_tokens": round(average_bucket_tokens, 1),
            "bucket_seconds": round(bucket_seconds, 3),
            "buckets": buckets,
        }


_stores: dict[str, UsageStore] = {}
_stores_lock = threading.Lock()


def get_usage_store(settings: Settings) -> UsageStore:
    key = str(Path(settings.usage_file).resolve())
    with _stores_lock:
        store = _stores.get(key)
        if store is None:
            store = UsageStore(settings)
            _stores[key] = store
        return store
