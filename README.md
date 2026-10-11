# codearts2api

A local OpenAI-compatible gateway for Huawei CodeArts Agent APIs. It uses the user's legitimate CodeArts/Huawei Cloud login and reproduces the authentication flow used by the installed CodeArts Agent client; it does not require the desktop client to stay running after login.

## Current MVP

- local dark Web UI at `http://127.0.0.1:8000/`
- multi-account pool containing both China and International CodeArts accounts
- automatic account routing (`round_robin`, `random`, or health-aware `weighted`)
- health-aware failover with per-account concurrency leases, account+model cooldown, retry budgets, and jittered backoff
- background account keepalive / health probes with explicit relogin detection
- optional explicit account selection with `X-CodeArts-Account`
- `GET /health`
- live `GET /v1/models` discovery from CodeArts builtin and third-party model APIs
- `POST /v1/chat/completions`
- streaming and non-streaming OpenAI-style responses
- native multi-turn continuity using CodeArts `chat_id` + `parent_message_id`
- Huawei `SDK-HMAC-SHA256` request signing
- CodeArts OAuth Authorization Code + PKCE + ES256 DPoP login
- rotating refresh-token renewal and lazy refresh before expiry
- sanitized request logs and bilingual Web UI
- optional manual temporary-credential fallback

## Install and run

```powershell
cd "$env:USERPROFILE\Desktop\codearts2api"
uv sync --dev
uv run main.py
```

Then open:

```text
http://127.0.0.1:8000/
```

OpenAI-compatible base URL:

```text
http://127.0.0.1:8000/v1
```

## Account pool

China and International are properties of accounts, not global gateway modes.

The pool can contain any mix of accounts:

```text
Account Pool
├─ China account A
├─ China account B
├─ International account C
└─ International account D
```

A request is routed to an account first. That account's `environment` then determines the correct upstream endpoint and signing context.

Default routing is `round_robin`. `random` and `weighted` can be selected in Settings. `weighted` uses observed health, current concurrency, failure rate, latency, and idle time; CodeArts package usage is intentionally not used as a routing weight because the official 5M package amount is not a hard stop. To force one request through a specific account, send:

```text
X-CodeArts-Account: <account-id>
```

Account records are stored independently under `data/accounts/`, so refresh-token rotation for one account does not overwrite another account.

### Health-aware routing and failover

Each account has a process-local concurrency lease. `CODEARTS_ACCOUNT_MAX_CONCURRENCY` defaults to `4`; once an account reaches the limit it is temporarily removed from routing until a lease is released. This is a hard admission limit, not only a dashboard counter.

Transient network, timeout, and upstream `5xx` failures may retry the same account first. Every client request also has a global retry budget (`CODEARTS_RETRY_BUDGET_ATTEMPTS` + `CODEARTS_RETRY_BUDGET_SECONDS`) so a large pool cannot be swept indefinitely. New attempts use exponential backoff (`base * 2^n`, capped) with configurable jitter.

Health is scoped at two levels. Authentication, credential, and transport failures can cool down the whole account. Model/rate/server failures are stored under `account + model`, so `GLM-5.2` can cool down on one account while `GLM-5.1` remains eligible on the same account. The dashboard exposes the safe public health metadata; credentials and response bodies are never included.

For streaming requests, failover is allowed only before the first response delta has been sent to the caller. Once output has started, the gateway keeps the same account so a retry cannot duplicate already streamed text.

`X-CodeArts-Account` pins a request to one account and therefore intentionally disables cross-account failover for that request.

### Keepalive and active account probes

A lightweight keepalive service runs in the FastAPI lifespan when `CODEARTS_KEEPALIVE_ENABLED=true`. It periodically probes accounts that have not been checked within `CODEARTS_KEEPALIVE_INTERVAL_SECONDS` (default 24 hours). For OAuth accounts it refreshes the rotating token and then calls the lightweight CodeArts builtin-model endpoint to verify the upstream session without creating a chat or consuming model tokens.

The probe distinguishes terminal authentication failures (for example `401/403` or `invalid_grant`) from transient network/probe errors. Terminal failures set `needs_relogin`; transient network errors are recorded without disabling an otherwise healthy account. Accounts are probed serially with `CODEARTS_KEEPALIVE_ACCOUNT_GAP_SECONDS` between accounts.

## Live model discovery

`GET /v1/models` queries the CodeArts model APIs with the selected account credentials and merges models available across usable accounts:

```text
GET /v1/model/builtin
GET /api/v1/third-party/model/list?page_num=1&page_size=50
```

Results are cached for a short TTL. `GET /v1/models?refresh=true` forces a refresh. If model discovery is temporarily unavailable, the configured fallback model list remains available so OpenAI-compatible clients can still use `codearts-auto`.

## Multi-turn conversations

CodeArts chat history is continued with its native `chat_id` and `parent_message_id` chain. `codearts2api` stores only routing metadata plus SHA-256 hashes under `data/conversations/`; prompt and response text are not persisted there.

You can explicitly reuse a conversation in either of these ways:

```text
X-CodeArts-Conversation: <conversation-id>
```

or add the compatible extension field:

```json
{"conversation_id":"<conversation-id>"}
```

The response includes the same `conversation_id` and also returns it in the `X-CodeArts-Conversation` header. Standard OpenAI clients that resend full message history can work without this extension: the gateway hashes the known history and automatically matches it to the existing CodeArts conversation, then sends only the newest user turn upstream.

## China and International profiles

CodeArts commercial service uses separate service profiles:

- **China**: `codearts.huaweicloud.com` + `snap-access.cn-north-4.myhuaweicloud.com`
- **International**: `codearts.ap-southeast-1.huaweicloud.com` + `snap-access.ap-southeast-1.myhuaweicloud.com`

The official International/HK profile still exchanges OAuth tokens through `sts.cn-north-4.myhuaweicloud.com`, and its authorize request includes `quickcompfalg=1`.

There is no global China/International switch in the normal request flow. Each stored account permanently carries its own environment.

## Login without CodeArts Agent

Click **Add Account** in the dashboard, then choose **China** or **International** for the new account. The choice applies only to that account being created.

The dashboard then prepares the official CodeArts authorization URL but does **not** open it automatically. You can copy the URL or click **Open authorization page**. While you authorize, the modal stays in a waiting state and polls the account pool using the pending account ID. A successful callback:

1. exchanges the authorization code for temporary Huawei credentials;
2. creates a new account record in `data/accounts/`;
3. is detected by the dashboard automatically, including when the URL was copied and opened manually;
4. changes the authorization wizard from waiting to completed.

The callback URL is:

```text
http://127.0.0.1:8000/oauth/callback
```

Useful endpoints:

```text
GET    /auth/login-url?environment=cn
GET    /auth/login-url?environment=international
GET    /api/ui/accounts
POST   /api/ui/accounts/{account_id}/refresh
POST   /api/ui/accounts/{account_id}/probe
POST   /api/ui/accounts/probe
DELETE /api/ui/accounts/{account_id}
GET    /api/ui/routing
POST   /api/ui/routing
```

Account APIs deliberately exclude AK, SK, Security Token, refresh token, PKCE verifier, and private DPoP keys.

## Credential refresh

CodeArts refresh tokens rotate. Each account is refreshed independently and rewritten atomically. Chat requests lazily refresh the selected account shortly before its credentials expire.

## Manual credential fallback

If OAuth cannot be used, copy `.env.example` to `.env` and fill:

```text
CODEARTS_ACCESS_KEY_ID=
CODEARTS_SECRET_ACCESS_KEY=
CODEARTS_SECURITY_TOKEN=
CODEARTS_DOMAIN_ID=
```

Manual credentials are scoped to one site with `CODEARTS_MANUAL_CREDENTIAL_ENVIRONMENT=cn` (or `international`) and are used only as a fallback when the account pool has no usable account.

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
