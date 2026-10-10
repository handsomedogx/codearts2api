# codearts2api

A local OpenAI-compatible gateway for Huawei CodeArts Agent APIs. It uses the user's legitimate CodeArts/Huawei Cloud login and reproduces the authentication flow used by the installed CodeArts Agent client; it does not require the desktop client to stay running after login.

## Current MVP

- local dark Web UI at `http://127.0.0.1:8000/`
- account/login dashboard, model status, streaming Playground and sanitized request logs
- `GET /health`
- `GET /v1/models`
- `POST /v1/chat/completions`
- streaming and non-streaming OpenAI-style responses
- Huawei `SDK-HMAC-SHA256` request signing
- CodeArts OAuth Authorization Code + PKCE + ES256 DPoP login
- persisted temporary AK/SK/Security Token
- serialized rotating `refresh_token` renewal
- lazy refresh before credentials expire
- optional manual temporary-credential fallback

## Install and run

```powershell
cd "$env:USERPROFILE\Desktop\codearts2api"
uv sync --dev
uv run main.py
```

Then open the management UI:

```text
http://127.0.0.1:8000/
```

OpenAI-compatible base URL:

```text
http://127.0.0.1:8000/v1
```

## Web UI

The local dashboard is served by the same FastAPI process and has no external CDN/runtime dependency. It includes:

- CodeArts account login/status/expiry
- refresh and logout actions
- API endpoint and client snippet
- configured model list
- streaming Chat Completions Playground
- sanitized request/activity logs
- local API-key browser configuration

If `CODEARTS2API_API_KEY` is enabled, the dashboard stores the local gateway key only in the browser's `localStorage`; it is not written back to the server.

## Login without CodeArts Agent

With the API running, click **Login CodeArts** in the dashboard. The direct OAuth entry remains available at:

```text
http://127.0.0.1:8000/auth/login
```

The gateway creates a fresh PKCE verifier/challenge and P-256 DPoP key pair, then redirects to the official CodeArts authorization page. After the official site redirects back to:

```text
http://127.0.0.1:8000/oauth/callback
```

`codearts2api` exchanges the authorization code for temporary Huawei credentials and persists them in `credentials.json`. Both `credentials.json` and the temporary `auth_pending.json` are ignored by Git.

Useful auth endpoints:

```text
GET  /auth/login-url
GET  /auth/status
POST /auth/refresh
POST /auth/logout
```

`/auth/status` deliberately exposes only login state, expiry, user/domain identifiers and refresh availability. It never returns AK, SK, Security Token, refresh token, PKCE verifier, or the private DPoP key.

## Credential refresh

The CodeArts refresh token is rotating. The gateway serializes refresh operations with a process-wide async lock and writes the newly returned credentials with an atomic file replacement before they are used by later requests. Chat requests lazily refresh persisted credentials shortly before expiration.

## Manual credential fallback

If OAuth cannot be used, copy `.env.example` to `.env` and fill the temporary credential variables:

```text
CODEARTS_ACCESS_KEY_ID=
CODEARTS_SECRET_ACCESS_KEY=
CODEARTS_SECURITY_TOKEN=
CODEARTS_DOMAIN_ID=
```

Persisted OAuth credentials take priority over this fallback.

## Local API protection

For anything beyond localhost-only development, set:

```text
CODEARTS2API_API_KEY=your-local-gateway-key
```

Then clients must send:

```text
Authorization: Bearer your-local-gateway-key
```

## Tests

```powershell
uv run pytest -q
```

OAuth tests use `httpx.MockTransport`; they do not send fake credentials or fake authorization codes to Huawei services.
