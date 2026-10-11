from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass
from typing import Any

from app.codearts.auth import AuthManager
from app.codearts.client import CodeArtsClient
from app.codearts.errors import CodeArtsUpstreamError
from app.config import Settings


@dataclass
class CachedModels:
    expires_at: float
    items: list[dict[str, Any]]


class ModelCatalog:
    def __init__(self, settings: Settings, auth: AuthManager) -> None:
        self.settings = settings
        self.auth = auth
        self._cache: dict[str, CachedModels] = {}
        self._lock = asyncio.Lock()

    @staticmethod
    def _model_id(item: dict[str, Any]) -> str:
        for key in ("model_id", "modelId", "id", "model_alias", "modelAlias", "name", "model_name"):
            value = item.get(key)
            if isinstance(value, (str, int)) and str(value).strip():
                return str(value).strip()
        return ""

    @classmethod
    def _extract_models(cls, value: Any, *, source: str, environment: str) -> list[dict[str, Any]]:
        results: list[dict[str, Any]] = []

        def visit(node: Any) -> None:
            if isinstance(node, dict):
                model_id = cls._model_id(node)
                modelish = model_id and any(
                    key in node
                    for key in (
                        "model_id", "modelId", "model_alias", "modelAlias", "model_name",
                        "provider", "provider_name", "vendor", "vendor_name", "model_parameters",
                    )
                )
                if modelish:
                    owner = (
                        node.get("provider_name")
                        or node.get("provider")
                        or node.get("vendor_name")
                        or node.get("vendor")
                        or "codearts"
                    )
                    display = node.get("display_name") or node.get("model_name") or node.get("name") or model_id
                    parameters = node.get("model_parameters") if isinstance(node.get("model_parameters"), dict) else node
                    context_window = parameters.get("context_window")
                    input_length = parameters.get("input_length") or parameters.get("input_context_window")
                    max_tokens = parameters.get("max_tokens") or parameters.get("output_context_window")
                    results.append(
                        {
                            "id": model_id,
                            "display_name": str(display),
                            "owned_by": str(owner),
                            "source": source,
                            "environment": environment,
                            "context_window": int(context_window) if isinstance(context_window, (int, float)) else None,
                            "input_length": int(input_length) if isinstance(input_length, (int, float)) else None,
                            "max_tokens": int(max_tokens) if isinstance(max_tokens, (int, float)) else None,
                            "thinking_type": parameters.get("thinking_type"),
                        }
                    )
                for nested in node.values():
                    if isinstance(nested, (dict, list)):
                        visit(nested)
            elif isinstance(node, list):
                for nested in node:
                    visit(nested)

        visit(value)
        seen: set[tuple[str, str]] = set()
        unique: list[dict[str, Any]] = []
        for item in results:
            key = (item["id"], item["source"])
            if key in seen:
                continue
            seen.add(key)
            unique.append(item)
        return unique

    async def _fetch_agent_detail_models(
        self,
        client: CodeArtsClient,
        *,
        base_url: str,
        environment: str,
    ) -> list[dict[str, Any]]:
        try:
            agents = await client.get_json(
                f"{base_url}/v1/agent-center/agents/useragents",
                agent_type="AgentCenter",
            )
        except CodeArtsUpstreamError:
            return []
        raw_agents = agents.get("agents")
        if not isinstance(raw_agents, list):
            raw_agents = agents.get("data", {}).get("agents") if isinstance(agents.get("data"), dict) else []
        candidates = [item for item in raw_agents or [] if isinstance(item, dict) and item.get("agent_id")]
        candidates.sort(
            key=lambda item: (
                0 if str(item.get("agent_name") or "") == "CodeArts Doer For Coding" else
                1 if str(item.get("agent_name") or "") == "CodeAgent" else
                2 if item.get("show_in_ide") else 3
            )
        )
        gathered: list[dict[str, Any]] = []
        for agent in candidates[:6]:
            try:
                detail = await client.get_json(
                    f"{base_url}/v1/agent-center/agents/detail",
                    params={"agent_id": str(agent["agent_id"])},
                    agent_type="AgentCenter",
                )
            except CodeArtsUpstreamError:
                continue
            extracted = self._extract_models(detail, source="agent_detail", environment=environment)
            gathered.extend(extracted)
            if any(item.get("context_window") for item in extracted):
                break
        return gathered

    async def _fetch_account(self, account: dict[str, Any], credentials: Any) -> list[dict[str, Any]]:
        account_id = str(account["id"])
        cached = self._cache.get(account_id)
        if cached and cached.expires_at > time.monotonic():
            return cached.items

        environment = str(account["environment"])
        profile = self.settings.profile(environment)
        client = CodeArtsClient(
            self.settings,
            auth_manager=self.auth,
            environment=environment,
            account_id=account_id,
            credentials=credentials,
        )
        items: list[dict[str, Any]] = []
        builtin = await client.get_json(f"{profile.base_url}/v1/model/builtin", agent_type="PromptCenter")
        items.extend(self._extract_models(builtin, source="builtin", environment=environment))
        items.extend(
            await self._fetch_agent_detail_models(
                client,
                base_url=profile.base_url,
                environment=environment,
            )
        )

        try:
            third_party = await client.get_json(
                f"{profile.base_url}/api/v1/third-party/model/list",
                params={"page_num": 1, "page_size": 50},
                agent_type="PromptCenter",
            )
        except CodeArtsUpstreamError:
            # Third-party models are optional. A failure here must not discard
            # the already-fetched built-in and agent-detail model metadata.
            pass
        else:
            items.extend(self._extract_models(third_party, source="third_party", environment=environment))

        self._cache[account_id] = CachedModels(
            expires_at=time.monotonic() + max(1, self.settings.model_cache_ttl_seconds),
            items=items,
        )
        return items

    async def list_models(self, *, force: bool = False) -> list[dict[str, Any]]:
        if force:
            self._cache.clear()
        async with self._lock:
            candidates = await self.auth.account_candidates()
            gathered: list[dict[str, Any]] = []
            for account, credentials in candidates:
                try:
                    gathered.extend(await self._fetch_account(account, credentials))
                except Exception:
                    continue

            merged: dict[str, dict[str, Any]] = {}
            for item in gathered:
                model_id = item["id"]
                current = merged.get(model_id)
                if current is None:
                    merged[model_id] = {
                        "id": model_id,
                        "display_name": item["display_name"],
                        "owned_by": item["owned_by"],
                        "sources": [item["source"]],
                        "environments": [item["environment"]],
                        "context_window": item.get("context_window"),
                        "input_length": item.get("input_length"),
                        "max_tokens": item.get("max_tokens"),
                        "thinking_type": item.get("thinking_type"),
                    }
                    continue
                if item["source"] not in current["sources"]:
                    current["sources"].append(item["source"])
                if item["environment"] not in current["environments"]:
                    current["environments"].append(item["environment"])
                for field in ("context_window", "input_length", "max_tokens", "thinking_type"):
                    if current.get(field) in (None, "") and item.get(field) not in (None, ""):
                        current[field] = item[field]

            ordered = sorted(merged.values(), key=lambda item: (item["display_name"].lower(), item["id"].lower()))
            if not ordered:
                return [
                    {
                        "id": model,
                        "display_name": model,
                        "owned_by": "codearts",
                        "sources": ["fallback"],
                        "environments": [],
                        "context_window": None,
                        "input_length": None,
                        "max_tokens": None,
                        "thinking_type": None,
                    }
                    for model in dict.fromkeys((self.settings.default_model, *self.settings.models))
                ]
            if self.settings.default_model == "codearts-auto" and "codearts-auto" not in merged:
                ordered.insert(
                    0,
                    {
                        "id": "codearts-auto",
                        "display_name": "CodeArts Auto",
                        "owned_by": "codearts",
                        "sources": ["virtual"],
                        "environments": sorted({env for item in ordered for env in item["environments"]}),
                        "context_window": max((int(item.get("context_window") or 0) for item in ordered), default=0) or None,
                        "input_length": max((int(item.get("input_length") or 0) for item in ordered), default=0) or None,
                        "max_tokens": max((int(item.get("max_tokens") or 0) for item in ordered), default=0) or None,
                        "thinking_type": None,
                    },
                )
            return ordered


_catalog: ModelCatalog | None = None


def get_model_catalog(settings: Settings, auth: AuthManager) -> ModelCatalog:
    global _catalog
    if _catalog is None or _catalog.settings is not settings or _catalog.auth is not auth:
        _catalog = ModelCatalog(settings, auth)
    return _catalog
