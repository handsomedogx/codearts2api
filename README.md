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
- role-preserving CodeArts v2 inference through `/api/v2/chat/completions`
- OpenAI `system` / `user` / `assistant` / `tool` messages, tools, reasoning, and usage passthrough
- multi-turn cache affinity using a stable CodeArts `chat_id` while always sending full OpenAI history
- Huawei `SDK-HMAC-SHA256` request signing
- CodeArts OAuth Authorization Code + PKCE + ES256 DPoP login
- rotating refresh-token renewal and lazy refresh before expiry
- persistent sanitized request logs with search/filter/summary and bilingual Web UI

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

First-use flow:

1. click **Add Account** and finish the China or International CodeArts OAuth login;
2. open **Settings → External client access**;
3. copy the generated **Base URL** and **API Key** into an OpenAI-compatible client.

Typical local client configuration:

```text
Base URL  http://127.0.0.1:8000/v1
API Key   sk-ca-...
Model     GLM-5.2
```

Python example:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8000/v1",
    api_key="sk-ca-...",
)

reply = client.chat.completions.create(
    model="GLM-5.2",
    messages=[{"role": "user", "content": "hello"}],
)
print(reply.choices[0].message.content)
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

Results are cached for a short TTL. `GET /v1/models?refresh=true` forces a refresh. If model discovery is temporarily unavailable, the configured fallback model list remains available; the default fallback is `GLM-5.2`.

## Upstream inference protocol

Chat inference uses only the CodeArts v2 OpenAI-shaped endpoint:

```text
POST /api/v2/chat/completions
```

`codearts2api` keeps the incoming OpenAI message roles intact instead of converting them to the legacy `task=chat` content-block format. `system` prompts therefore remain real `system` messages, and supported OpenAI fields such as `tools`, `tool_choice`, `temperature`, `top_p`, `max_tokens`, `reasoning_effort`, `stop`, penalties, and `response_format` are passed through to CodeArts v2. Streaming responses preserve `content`, `reasoning_content`, `tool_calls`, `finish_reason`, and real upstream usage. The old `/v1/chat/chat` ChatAgent path is not used as a fallback.

## Multi-turn conversations

Every v2 request sends the full OpenAI message history upstream. A stable CodeArts `chat_id` is reused when the same conversation stays on the same account, which preserves prompt-cache affinity without relying on the legacy `parent_message_id` continuation chain. `codearts2api` stores only routing metadata plus SHA-256 hashes under `data/conversations/`; prompt and response text are not persisted there.

You can explicitly reuse a conversation in either of these ways:

```text
X-CodeArts-Conversation: <conversation-id>
```

or add the compatible extension field:

```json
{"conversation_id":"<conversation-id>"}
```

The response includes the same `conversation_id` and also returns it in the `X-CodeArts-Conversation` header. Standard OpenAI clients that resend full message history can work without this extension: the gateway hashes the known history and automatically matches it to the existing CodeArts conversation while still forwarding the complete role-preserving message list upstream.

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

## Request logs

Completed `/v1/chat/completions` calls are appended to `data/request_logs.jsonl`. The log is deliberately metadata-only: it stores request ID, result, selected model/account, source IP, User-Agent, latency, real upstream token usage, attempts, conversation ID and an error category. Prompt text, assistant output, Authorization headers, AK/SK, Security Token, refresh tokens and DPoP keys are never written.

The Logs page supports free-text search across IP / UA / model / account / request ID, success/failure filtering, time ranges, row limits, archive size, success rate, HTTP success rate, average latency and current process-local in-progress requests. For proxied deployments the source IP uses the first `X-Forwarded-For` value; without that header it falls back to the TCP peer address.

The JSONL path can be changed with `CODEARTS_REQUEST_LOGS_FILE`.

## Credential refresh

CodeArts refresh tokens rotate. Each account is refreshed independently and rewritten atomically. Chat requests lazily refresh the selected account shortly before its credentials expire.

## API access and configuration

The normal setup does **not** require editing `.env`. On first start, `codearts2api` automatically generates a gateway API key and stores it in the Git-ignored `data/settings.json`. The same key is reused across restarts.

The local Dashboard exposes the full Base URL and API key under **Settings → External client access**. The management endpoints that can reveal or rotate the key accept only loopback connections; remote clients cannot read the key from the gateway.

OpenAI-compatible clients send:

```text
Authorization: Bearer <codearts2api API key>
```

Click **Regenerate** in Settings to rotate the generated key. The previous key becomes invalid immediately.

`.env.example` intentionally contains only deployment-level host/port settings:

```text
CODEARTS2API_HOST=127.0.0.1
CODEARTS2API_PORT=8000
```

To serve another device on the LAN, intentionally bind `CODEARTS2API_HOST=0.0.0.0`, allow the port in the firewall, and use the machine's LAN address as the Base URL. Keep Dashboard management local to the server. For public deployments, put HTTPS/reverse-proxy access control in front of the gateway instead of exposing the development port directly.

Advanced deployments may set `CODEARTS2API_API_KEY` as an environment override. When that override is present it replaces the generated key and Dashboard regeneration is disabled. CodeArts service endpoints, OAuth metadata, retry/cooldown values and storage paths also retain environment-variable overrides in `app/config.py`, but they are advanced tuning knobs and are deliberately omitted from the normal `.env.example`.

There is no manual AK/SK credential fallback: CodeArts credentials enter the pool only through the supported OAuth account flow.

## Tests

```powershell
uv run pytest -q
```

OAuth tests use `httpx.MockTransport`; they do not send fake credentials or fake authorization codes to Huawei services.
