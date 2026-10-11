from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass
from typing import Any

from app.codearts.auth import AuthManager
from app.codearts.client import CodeArtsClient
from app.config import Settings


@dataclass
class CachedSubscription:
    expires_at: float
    value: dict[str, Any]


class SubscriptionCatalog:
    def __init__(self, settings: Settings, auth: AuthManager) -> None:
        self.settings = settings
        self.auth = auth
        self._cache: dict[str, CachedSubscription] = {}
        self._lock = asyncio.Lock()

    @staticmethod
    def _number(value: Any) -> int:
        try:
            return max(0, int(value or 0))
        except (TypeError, ValueError):
            return 0

    @classmethod
    def _parse(cls, payload: dict[str, Any], account: dict[str, Any]) -> dict[str, Any]:
        package = payload.get("package") if isinstance(payload.get("package"), dict) else {}
        metrics = payload.get("metrics") if isinstance(payload.get("metrics"), list) else []
        usage_metric = next(
            (item for item in metrics if isinstance(item, dict) and item.get("name") == "usageTokenChatMessages"),
            {},
        )
        used = cls._number(usage_metric.get("usage_token_num"))
        quota = cls._number(usage_metric.get("package_token_amount"))
        ratio = used / quota if quota > 0 else None
        model_metrics: list[dict[str, Any]] = []
        for item in payload.get("model_metrics") or []:
            if not isinstance(item, dict):
                continue
            model_metrics.append(
                {
                    "model_id": str(item.get("model_id") or ""),
                    "model_type": str(item.get("model_type") or ""),
                    "usage_token": item.get("usage_token"),
                    "usage_token_num": cls._number(item.get("usage_token_num")),
                    "model_quota_num": cls._number(item.get("model_quota_num")),
                    "enable": bool(item.get("enable")),
                    "status": str(item.get("status") or ""),
                }
            )
        return {
            "available": bool(usage_metric),
            "account_id": str(account.get("id") or ""),
            "environment": str(account.get("environment") or ""),
            "package_name": str(package.get("package_name_en") or package.get("package_name_zh_cn") or ""),
            "package_status": str(package.get("status") or ""),
            "spec_code": str(package.get("spec_code") or ""),
            "used_tokens": used,
            "quota_tokens": quota,
            "usage_percent": round(ratio * 100.0, 2) if ratio is not None else None,
            "progress_percent": round(min(1.0, ratio) * 100.0, 2) if ratio is not None else None,
            "over_limit": bool(quota > 0 and used > quota),
            "overage_tokens": max(0, used - quota) if quota > 0 else 0,
            "start_date": str(payload.get("start_date") or ""),
            "end_date": str(payload.get("end_date") or ""),
            "model_metrics": model_metrics,
        }

    async def _fetch(self, record: dict[str, Any], *, force: bool = False) -> dict[str, Any]:
        account_id = str(record.get("id") or "")
        cached = self._cache.get(account_id)
        if not force and cached and cached.expires_at > time.monotonic():
            return cached.value
        credentials = await self.auth.get_account_credentials(account_id)
        if credentials is None:
            value = {"available": False, "account_id": account_id, "environment": str(record.get("environment") or "")}
        else:
            environment = str(record.get("environment") or self.settings.default_environment)
            profile = self.settings.profile(environment)
            client = CodeArtsClient(
                self.settings,
                auth_manager=self.auth,
                environment=environment,
                account_id=account_id,
                credentials=credentials,
            )
            try:
                payload = await client.get_json(
                    f"{profile.base_url}/snap-manager/v1/statistics/plugin",
                    agent_type="PromptCenter",
                )
            except Exception:
                value = {"available": False, "account_id": account_id, "environment": environment}
            else:
                value = self._parse(payload, record)
        self._cache[account_id] = CachedSubscription(
            expires_at=time.monotonic() + max(5, self.settings.subscription_cache_ttl_seconds),
            value=value,
        )
        return value

    async def list(self, *, force: bool = False) -> list[dict[str, Any]]:
        async with self._lock:
            results: list[dict[str, Any]] = []
            for record in self.auth.pool.list_records():
                results.append(await self._fetch(record, force=force))
            return results

    async def aggregate(self, *, force: bool = False) -> dict[str, Any]:
        items = [item for item in await self.list(force=force) if item.get("available")]
        used = sum(self._number(item.get("used_tokens")) for item in items)
        quota = sum(self._number(item.get("quota_tokens")) for item in items)
        ratio = used / quota if quota > 0 else None
        package_names = sorted({str(item.get("package_name") or "") for item in items if item.get("package_name")})
        return {
            "available": bool(items),
            "account_count": len(items),
            "used_tokens": used,
            "quota_tokens": quota,
            "usage_percent": round(ratio * 100.0, 2) if ratio is not None else None,
            "progress_percent": round(min(1.0, ratio) * 100.0, 2) if ratio is not None else None,
            "over_limit": bool(quota > 0 and used > quota),
            "overage_tokens": max(0, used - quota) if quota > 0 else 0,
            "package_names": package_names,
            "items": items,
        }


_catalogs: dict[tuple[int, int], SubscriptionCatalog] = {}


def get_subscription_catalog(settings: Settings, auth: AuthManager) -> SubscriptionCatalog:
    key = (id(settings), id(auth))
    catalog = _catalogs.get(key)
    if catalog is None:
        catalog = SubscriptionCatalog(settings, auth)
        _catalogs[key] = catalog
    return catalog
