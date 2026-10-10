from __future__ import annotations

import os
from dataclasses import dataclass
from functools import lru_cache
from urllib.parse import urlencode

from dotenv import load_dotenv

load_dotenv()


def _split_csv(value: str) -> tuple[str, ...]:
    return tuple(item.strip() for item in value.split(",") if item.strip())


@dataclass(frozen=True)
class Settings:
    base_url: str = os.getenv("CODEARTS_BASE_URL", "https://snap-access.cn-north-4.myhuaweicloud.com").rstrip("/")
    access_key_id: str = os.getenv("CODEARTS_ACCESS_KEY_ID", "")
    secret_access_key: str = os.getenv("CODEARTS_SECRET_ACCESS_KEY", "")
    security_token: str = os.getenv("CODEARTS_SECURITY_TOKEN", "")
    domain_id: str = os.getenv("CODEARTS_DOMAIN_ID", "")

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
    portal_base_url: str = os.getenv("CODEARTS_PORTAL_BASE_URL", "https://codearts.huaweicloud.com").rstrip("/")
    sts_base_url: str = os.getenv("CODEARTS_STS_BASE_URL", "https://sts.cn-north-4.myhuaweicloud.com").rstrip("/")
    credentials_file: str = os.getenv("CODEARTS_CREDENTIALS_FILE", "credentials.json")
    pending_auth_file: str = os.getenv("CODEARTS_PENDING_AUTH_FILE", "auth_pending.json")
    refresh_skew_seconds: int = int(os.getenv("CODEARTS_REFRESH_SKEW_SECONDS", "120"))

    api_key: str = os.getenv("CODEARTS2API_API_KEY", "")
    host: str = os.getenv("CODEARTS2API_HOST", "127.0.0.1")
    port: int = int(os.getenv("CODEARTS2API_PORT", "8000"))

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
    def callback_url(self) -> str:
        override = os.getenv("CODEARTS_AUTH_CALLBACK_URL", "").strip()
        return override or f"http://127.0.0.1:{self.port}/oauth/callback"

    @property
    def authorize_url(self) -> str:
        return f"{self.portal_base_url}/portal/authorize"

    @property
    def login_success_url(self) -> str:
        query = urlencode(
            {
                "login_succeed": "true",
                "locale": self.language,
                "uri_scheme": self.auth_uri_scheme,
            }
        )
        return f"{self.portal_base_url}/portal/login?{query}"

    @property
    def env_credentials_ready(self) -> bool:
        return bool(self.access_key_id and self.secret_access_key)


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    return Settings()
