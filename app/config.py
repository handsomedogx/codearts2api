from __future__ import annotations

import os
from dataclasses import dataclass
from functools import lru_cache
from urllib.parse import urlencode

from dotenv import load_dotenv

load_dotenv()

ENV_CN = "cn"
ENV_INTERNATIONAL = "international"


def _split_csv(value: str) -> tuple[str, ...]:
    return tuple(item.strip() for item in value.split(",") if item.strip())


def normalize_environment(value: str | None) -> str:
    normalized = (value or "").strip().lower()
    aliases = {
        "cn": ENV_CN,
        "china": ENV_CN,
        "domestic": ENV_CN,
        "zh-cn": ENV_CN,
        "international": ENV_INTERNATIONAL,
        "intl": ENV_INTERNATIONAL,
        "global": ENV_INTERNATIONAL,
        "hk": ENV_INTERNATIONAL,
        "ap-southeast-1": ENV_INTERNATIONAL,
    }
    if normalized in aliases:
        return aliases[normalized]
    raise ValueError(f"Unsupported CodeArts environment: {value}")


@dataclass(frozen=True)
class CodeArtsProfile:
    environment: str
    label: str
    base_url: str
    portal_base_url: str
    sts_base_url: str
    quickcompfalg: bool = False

    @property
    def chat_url(self) -> str:
        return f"{self.base_url}/v1/chat/chat"

    @property
    def token_url(self) -> str:
        return f"{self.sts_base_url}/v1/oauth2/tokens"

    @property
    def caller_identity_url(self) -> str:
        return f"{self.sts_base_url}/v5/caller-identity"

    @property
    def authorize_url(self) -> str:
        return f"{self.portal_base_url}/portal/authorize"


@dataclass(frozen=True)
class Settings:
    # Official CodeArts Agent 26.7.2 commercial profiles.
    cn_base_url: str = os.getenv(
        "CODEARTS_CN_BASE_URL",
        "https://snap-access.cn-north-4.myhuaweicloud.com",
    ).rstrip("/")
    cn_portal_base_url: str = os.getenv(
        "CODEARTS_CN_PORTAL_BASE_URL",
        "https://codearts.huaweicloud.com",
    ).rstrip("/")
    cn_sts_base_url: str = os.getenv(
        "CODEARTS_CN_STS_BASE_URL",
        "https://sts.cn-north-4.myhuaweicloud.com",
    ).rstrip("/")

    intl_base_url: str = os.getenv(
        "CODEARTS_INTL_BASE_URL",
        "https://snap-access.ap-southeast-1.myhuaweicloud.com",
    ).rstrip("/")
    intl_portal_base_url: str = os.getenv(
        "CODEARTS_INTL_PORTAL_BASE_URL",
        "https://codearts.ap-southeast-1.huaweicloud.com",
    ).rstrip("/")
    # The official international/HK profile still uses this STS endpoint.
    intl_sts_base_url: str = os.getenv(
        "CODEARTS_INTL_STS_BASE_URL",
        "https://sts.cn-north-4.myhuaweicloud.com",
    ).rstrip("/")

    default_environment: str = os.getenv("CODEARTS_DEFAULT_ENVIRONMENT", ENV_CN)
    accounts_dir: str = os.getenv("CODEARTS_ACCOUNTS_DIR", "data/accounts")
    routing_file: str = os.getenv("CODEARTS_ROUTING_FILE", "data/routing.json")
    routing_strategy: str = os.getenv("CODEARTS_ROUTING_STRATEGY", "round_robin")

    access_key_id: str = os.getenv("CODEARTS_ACCESS_KEY_ID", "")
    secret_access_key: str = os.getenv("CODEARTS_SECRET_ACCESS_KEY", "")
    security_token: str = os.getenv("CODEARTS_SECURITY_TOKEN", "")
    domain_id: str = os.getenv("CODEARTS_DOMAIN_ID", "")
    manual_credentials_environment: str = os.getenv("CODEARTS_MANUAL_CREDENTIAL_ENVIRONMENT", ENV_CN)

    default_model: str = os.getenv("CODEARTS_DEFAULT_MODEL", "codearts-auto")
    models: tuple[str, ...] = _split_csv(os.getenv("CODEARTS_MODELS", "codearts-auto"))

    client_version: str = os.getenv("CODEARTS_CLIENT_VERSION", "26.7.2")
    plugin_name: str = os.getenv("CODEARTS_PLUGIN_NAME", "codearts-snap_vscode")
    plugin_version: str = os.getenv("CODEARTS_PLUGIN_VERSION", "26.7.2")
    ide_name: str = os.getenv("CODEARTS_IDE_NAME", "CodeArts Agent")
    ide_version: str = os.getenv("CODEARTS_IDE_VERSION", "26.7.1")
    language: str = os.getenv("CODEARTS_LANGUAGE", "zh-cn")

    auth_client_id: str = os.getenv("CODEARTS_AUTH_CLIENT_ID", "codearts-snap")
    auth_uri_scheme: str = os.getenv("CODEARTS_AUTH_URI_SCHEME", "codearts-snap")
    auth_theme: str = os.getenv("CODEARTS_AUTH_THEME", "2")
    pending_auth_file: str = os.getenv("CODEARTS_PENDING_AUTH_FILE", "data/oauth_pending.json")
    refresh_skew_seconds: int = int(os.getenv("CODEARTS_REFRESH_SKEW_SECONDS", "120"))

    api_key: str = os.getenv("CODEARTS2API_API_KEY", "")
    host: str = os.getenv("CODEARTS2API_HOST", "127.0.0.1")
    port: int = int(os.getenv("CODEARTS2API_PORT", "8000"))

    def _normalize_or_default(self, value: str | None) -> str:
        try:
            return normalize_environment(value)
        except ValueError:
            return ENV_CN

    def profile(self, environment: str | None = None) -> CodeArtsProfile:
        selected = normalize_environment(environment) if environment else self._normalize_or_default(self.default_environment)
        if selected == ENV_INTERNATIONAL:
            return CodeArtsProfile(
                environment=selected,
                label="International",
                base_url=self.intl_base_url,
                portal_base_url=self.intl_portal_base_url,
                sts_base_url=self.intl_sts_base_url,
                quickcompfalg=True,
            )
        return CodeArtsProfile(
            environment=ENV_CN,
            label="China",
            base_url=self.cn_base_url,
            portal_base_url=self.cn_portal_base_url,
            sts_base_url=self.cn_sts_base_url,
            quickcompfalg=False,
        )

    def public_environments(self) -> list[dict[str, str | bool]]:
        return [
            {
                "id": p.environment,
                "label": p.label,
                "upstream": p.base_url,
                "portal": p.portal_base_url,
                "quickcompfalg": p.quickcompfalg,
            }
            for p in (self.profile(ENV_CN), self.profile(ENV_INTERNATIONAL))
        ]

    @property
    def base_url(self) -> str:
        return self.profile().base_url

    @property
    def chat_url(self) -> str:
        return self.profile().chat_url

    def chat_url_for(self, environment: str) -> str:
        return self.profile(environment).chat_url

    @property
    def token_url(self) -> str:
        return self.profile().token_url

    @property
    def caller_identity_url(self) -> str:
        return self.profile().caller_identity_url

    @property
    def callback_url(self) -> str:
        override = os.getenv("CODEARTS_AUTH_CALLBACK_URL", "").strip()
        return override or f"http://127.0.0.1:{self.port}/oauth/callback"

    @property
    def authorize_url(self) -> str:
        return self.profile().authorize_url

    def login_success_url_for(self, environment: str) -> str:
        profile = self.profile(environment)
        query = urlencode(
            {
                "login_succeed": "true",
                "locale": self.language,
                "uri_scheme": self.auth_uri_scheme,
            }
        )
        return f"{profile.portal_base_url}/portal/login?{query}"

    @property
    def login_success_url(self) -> str:
        return self.login_success_url_for(self._normalize_or_default(self.default_environment))

    def env_credentials_ready_for(self, environment: str) -> bool:
        selected = normalize_environment(environment)
        manual_environment = self._normalize_or_default(self.manual_credentials_environment)
        return bool(self.access_key_id and self.secret_access_key and selected == manual_environment)

    @property
    def env_credentials_ready(self) -> bool:
        return self.env_credentials_ready_for(self._normalize_or_default(self.default_environment))


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
