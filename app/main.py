from __future__ import annotations

import html
import ipaddress
import json
import time
import uuid
from contextlib import asynccontextmanager
from pathlib import Path
from typing import AsyncIterator

from fastapi import Body, Depends, FastAPI, Header, HTTPException, Query, Request, Response
from fastapi.responses import FileResponse, HTMLResponse, RedirectResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles

from app.codearts.auth import AuthManager, get_auth_manager
from app.codearts.errors import CodeArtsUpstreamError
from app.codearts.gateway import CodeArtsGateway
from app.codearts.keepalive import KeepaliveService
from app.codearts.model_catalog import get_model_catalog
from app.codearts.request_logs import get_request_log_store
from app.codearts.subscription import get_subscription_catalog
from app.codearts.usage import get_usage_store
from app.config import Settings, get_settings, normalize_environment
from app.gateway_settings import get_gateway_settings_store
from app.models import ChatCompletionRequest

APP_VERSION = "0.9.0"
WEB_DIR = Path(__file__).resolve().parent / "web"

@asynccontextmanager
async def app_lifespan(_: FastAPI):
    settings = get_settings()
    get_gateway_settings_store(settings).ensure_api_key()
    service = KeepaliveService(settings, get_auth_manager())
    service.start()
    try:
        yield
    finally:
        await service.stop()


app = FastAPI(title="codearts2api", version=APP_VERSION, lifespan=app_lifespan)
app.mount("/static", StaticFiles(directory=WEB_DIR), name="static")


def require_local_api_key(
    authorization: str | None = Header(default=None),
    settings: Settings = Depends(get_settings),
) -> None:
    candidate = ""
    if authorization and authorization.lower().startswith("bearer "):
        candidate = authorization[7:].strip()
    if not get_gateway_settings_store(settings).verify(candidate):
        raise HTTPException(status_code=401, detail="Invalid API key")


def require_loopback_admin(request: Request) -> None:
    host = request.client.host if request.client else ""
    if host in {"localhost", "testclient"}:
        return
    try:
        if ipaddress.ip_address(host).is_loopback:
            return
    except ValueError:
        pass
    raise HTTPException(status_code=403, detail="Dashboard management APIs are available only from this machine")


def get_request_auth_manager(settings: Settings = Depends(get_settings)) -> AuthManager:
    if settings == get_settings():
        return get_auth_manager()
    return AuthManager(settings)


def _source_ip(request: Request) -> str:
    forwarded = request.headers.get("x-forwarded-for", "")
    if forwarded:
        first = forwarded.split(",", 1)[0].strip()
        if first:
            return first[:128]
    return (request.client.host if request.client else "")[:128]


def _account_log_name(auth: AuthManager, account_id: str) -> str:
    if not account_id:
        return ""
    record = auth.pool.get(account_id)
    if not record:
        return ""
    return str(record.get("user_name") or record.get("user_id") or "")[:160]


def _messages(request: ChatCompletionRequest) -> list[dict]:
    return [message.model_dump(exclude_none=True) for message in request.messages]


def _chat_options(request: ChatCompletionRequest) -> dict:
    raw = request.model_dump(exclude_none=True)
    for key in ("model", "messages", "stream", "stream_options", "user", "conversation_id"):
        raw.pop(key, None)
    return raw


def _model_list(settings: Settings) -> list[str]:
    items = list(settings.models)
    if settings.default_model not in items:
        items.insert(0, settings.default_model)
    return items or ["GLM-5.2"]


def _login_environment_or_400(value: str | None, settings: Settings) -> str:
    try:
        return normalize_environment(value or settings.default_environment)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


def _oauth_result_page(
    *,
    success: bool,
    environment: str = "",
    environment_label: str = "",
    account_id: str = "",
    error: str = "",
) -> str:
    title = "登录成功 / Login successful" if success else "登录失败 / Login failed"
    message = (
        "CodeArts 登录已完成，正在返回 codearts2api。 / CodeArts login completed. Returning to codearts2api."
        if success
        else "CodeArts 登录未完成。 / CodeArts login did not complete."
    )
    detail = html.escape(error[:800]) if error else ""
    payload = {
        "type": "codearts2api:oauth-complete",
        "success": success,
        "environment": environment,
        "environment_label": environment_label,
        "account_id": account_id,
    }
    if not success:
        payload["error"] = "OAuth login failed"
    payload_json = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    detail_html = f'<pre class="detail">{detail}</pre>' if detail else ""
    status_class = "success" if success else "error"
    return f'''<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{title}</title>
  <style>
    :root {{ color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, sans-serif; }}
    body {{ margin:0; min-height:100vh; display:grid; place-items:center; background:#0b0d12; color:#e8e9ee; }}
    .card {{ width:min(520px,calc(100vw - 40px)); border:1px solid #252a35; background:#11141b; border-radius:18px; padding:28px; box-shadow:0 22px 70px #0008; }}
    .dot {{ width:11px; height:11px; border-radius:50%; display:inline-block; margin-right:8px; vertical-align:1px; }}
    .success .dot {{ background:#49d49d; box-shadow:0 0 18px #49d49d66; }}
    .error .dot {{ background:#ff6b79; box-shadow:0 0 18px #ff6b7966; }}
    h1 {{ font-size:20px; margin:0 0 10px; }}
    p {{ color:#9298a6; line-height:1.65; margin:0; }}
    .detail {{ margin:16px 0 0; padding:12px; white-space:pre-wrap; word-break:break-word; color:#c3c7d0; background:#0b0d12; border-radius:10px; border:1px solid #242936; font-size:11px; }}
    button {{ margin-top:20px; border:1px solid #353b49; background:#191d26; color:#e8e9ee; border-radius:9px; padding:9px 13px; cursor:pointer; }}
  </style>
</head>
<body>
  <main class="card {status_class}">
    <h1><span class="dot"></span>{title}</h1>
    <p>{message}</p>
    {detail_html}
    <button type="button" onclick="window.close()">关闭窗口 / Close window</button>
  </main>
  <script>
    const payload = {payload_json};
    try {{
      localStorage.setItem("codearts2api.oauthEvent", JSON.stringify({{...payload, at: Date.now()}}));
    }} catch (_) {{}}
    if (window.opener && !window.opener.closed) {{
      window.opener.postMessage(payload, window.location.origin);
    }}
    if (payload.success) setTimeout(() => window.close(), 550);
  </script>
</body>
</html>'''


@app.middleware("http")
async def request_context(request: Request, call_next):
    request_id = f"req-{uuid.uuid4().hex[:16]}"
    request.state.request_id = request_id
    response = await call_next(request)
    response.headers.setdefault("X-Request-ID", request_id)
    return response


@app.get("/", include_in_schema=False)
async def web_ui():
    return FileResponse(WEB_DIR / "index.html")


@app.get("/api/ui/meta", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_meta(settings: Settings = Depends(get_settings)) -> dict:
    return {
        "name": "codearts2api",
        "version": APP_VERSION,
        "api_base": "/v1",
        "api_key_enabled": True,
        "environments": settings.public_environments(),
        "callback_url": settings.callback_url,
    }


@app.get("/api/ui/access", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_access(request: Request, settings: Settings = Depends(get_settings)) -> dict:
    access = get_gateway_settings_store(settings).ensure_api_key()
    return {
        **access,
        "base_url": f"{str(request.base_url).rstrip('/')}/v1",
    }


@app.post("/api/ui/access/regenerate", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_access_regenerate(request: Request, settings: Settings = Depends(get_settings)) -> dict:
    try:
        access = get_gateway_settings_store(settings).regenerate_api_key()
    except RuntimeError as exc:
        raise HTTPException(status_code=409, detail=str(exc)) from exc
    return {
        **access,
        "base_url": f"{str(request.base_url).rstrip('/')}/v1",
    }


@app.get("/api/ui/accounts", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_accounts(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    return {"items": auth.list_accounts(), "routing": auth.pool.routing()}


@app.post("/api/ui/accounts/{account_id}/refresh", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_account_refresh(account_id: str, auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    try:
        return await auth.refresh_account(account_id, force=True)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@app.delete("/api/ui/accounts/{account_id}", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_account_delete(account_id: str, auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    if not auth.delete_account(account_id):
        raise HTTPException(status_code=404, detail="CodeArts account not found")
    return {"deleted": True, "account_id": account_id}


@app.post("/api/ui/accounts/{account_id}/probe", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_account_probe(account_id: str, auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    try:
        return await auth.probe_account(account_id)
    except RuntimeError as exc:
        raise HTTPException(status_code=404, detail=str(exc)) from exc


@app.post("/api/ui/accounts/probe", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_accounts_probe(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    return {"items": await auth.probe_accounts(due_only=False)}


@app.get("/api/ui/routing", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_routing(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    return auth.pool.routing()


@app.post("/api/ui/routing", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_routing_update(payload: dict = Body(...), auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    try:
        return auth.pool.set_routing(str(payload.get("strategy", "")))
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@app.get("/api/ui/logs", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_logs(
    q: str = Query(default="", max_length=200),
    result: str = Query(default="all"),
    time_range: str = Query(default="24h"),
    limit: int = Query(default=100, ge=1, le=500),
    settings: Settings = Depends(get_settings),
) -> dict:
    return get_request_log_store(settings).query(q=q, result=result, time_range=time_range, limit=limit)


@app.get("/api/ui/usage", dependencies=[Depends(require_loopback_admin)], include_in_schema=False)
async def ui_usage(
    days: int = Query(default=3, ge=1, le=90),
    refresh_quota: bool = Query(default=False),
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    summary = get_usage_store(settings).summary(days=days, points=30)
    official = await get_subscription_catalog(settings, auth).aggregate(force=refresh_quota)
    return {**summary, "official_quota": official}


@app.get("/health")
async def health(
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    accounts = auth.list_accounts()
    return {
        "status": "ok",
        "credentials_configured": any(item.get("authenticated") for item in accounts),
        "account_count": len(accounts),
        "routing_strategy": auth.pool.routing()["strategy"],
    }


@app.get("/auth/login", dependencies=[Depends(require_loopback_admin)])
async def auth_login(
    environment: str | None = Query(default=None),
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
):
    selected = _login_environment_or_400(environment, settings)
    return RedirectResponse(auth.begin_login(selected), status_code=307)


@app.get("/auth/login-url", dependencies=[Depends(require_loopback_admin)])
async def auth_login_url(
    environment: str | None = Query(default=None),
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    selected = _login_environment_or_400(environment, settings)
    session = auth.begin_login_session(selected)
    return {
        **session,
        "callback_url": settings.callback_url,
    }


@app.get("/oauth/callback", response_class=HTMLResponse)
async def oauth_callback(
    code: str | None = Query(default=None),
    auth: AuthManager = Depends(get_request_auth_manager),
):
    if not code:
        return HTMLResponse(
            _oauth_result_page(success=False, error="OAuth callback is missing code"),
            status_code=400,
        )
    try:
        status = await auth.exchange_code(code)
    except Exception as exc:
        return HTMLResponse(
            _oauth_result_page(success=False, error=str(exc)),
            status_code=502,
        )
    return HTMLResponse(
        _oauth_result_page(
            success=True,
            environment=status["environment"],
            environment_label=status["environment_label"],
            account_id=status.get("id", ""),
        )
    )


@app.get("/v1/models", dependencies=[Depends(require_local_api_key)])
async def models(
    refresh: bool = Query(default=False),
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    now = int(time.time())
    items = await get_model_catalog(settings, auth).list_models(force=refresh)
    return {
        "object": "list",
        "data": [
            {
                "id": item["id"],
                "object": "model",
                "created": now,
                "owned_by": item.get("owned_by") or "codearts",
                "display_name": item.get("display_name") or item["id"],
                "codearts_sources": item.get("sources", []),
                "codearts_environments": item.get("environments", []),
                "context_window": item.get("context_window"),
                "input_length": item.get("input_length"),
                "max_tokens": item.get("max_tokens"),
                "thinking_type": item.get("thinking_type"),
            }
            for item in items
        ],
    }


def _usage_chunk(completion_id: str, model: str, usage: dict) -> str:
    payload = {
        "id": completion_id,
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [],
        "usage": usage,
    }
    return f"data: {json.dumps(payload, ensure_ascii=False, separators=(',', ':'))}\n\n"


def _chunk(
    completion_id: str,
    model: str,
    delta: dict | None = None,
    finish_reason: str | None = None,
) -> str:
    payload = {
        "id": completion_id,
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "delta": delta or {}, "finish_reason": finish_reason}],
    }
    return f"data: {json.dumps(payload, ensure_ascii=False, separators=(',', ':'))}\n\n"


@app.post("/v1/chat/completions", dependencies=[Depends(require_local_api_key)])
async def chat_completions(
    request: ChatCompletionRequest,
    raw_request: Request,
    response: Response,
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
    x_codearts_account: str | None = Header(default=None, alias="X-CodeArts-Account"),
    x_codearts_conversation: str | None = Header(default=None, alias="X-CodeArts-Conversation"),
):
    completion_id = f"chatcmpl-{uuid.uuid4().hex}"
    request_id = str(getattr(raw_request.state, "request_id", "") or f"req-{uuid.uuid4().hex[:16]}")
    log_store = get_request_log_store(settings)
    log_started = log_store.begin()
    source_ip = _source_ip(raw_request)
    user_agent = raw_request.headers.get("user-agent", "")
    messages = _messages(request)
    options = _chat_options(request)
    gateway = CodeArtsGateway(settings, auth)
    requested_conversation_id = x_codearts_conversation or request.conversation_id

    def finish_log(
        *,
        status: int,
        success: bool,
        account_id: str = "",
        environment: str = "",
        attempts: int = 0,
        usage: dict | None = None,
        conversation_id: str = "",
        error_category: str = "",
    ) -> None:
        log_store.finish(
            log_started,
            {
                "request_id": request_id,
                "status": status,
                "success": success,
                "stream": bool(request.stream),
                "model": request.model,
                "account_id": account_id,
                "account_name": _account_log_name(auth, account_id),
                "environment": environment,
                "source_ip": source_ip,
                "user_agent": user_agent,
                "usage": usage or {},
                "attempts": attempts,
                "conversation_id": conversation_id,
                "error_category": error_category,
            },
        )

    try:
        conversation = gateway.resolve_conversation(messages, requested_conversation_id)
    except ValueError as exc:
        finish_log(status=400, success=False, error_category="invalid_request")
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    conversation_id = str(conversation["id"])

    if request.stream:
        include_usage = bool(request.stream_options and request.stream_options.include_usage)
        try:
            prepared_candidates = await gateway.prepare_stream_candidates(
                explicit_account_id=x_codearts_account,
                context=conversation,
                model=request.model,
            )
        except CodeArtsUpstreamError as exc:
            status_code = 503 if exc.category in {"no_account", "retry_budget"} else 502
            finish_log(status=status_code, success=False, error_category=exc.category)
            raise HTTPException(status_code=status_code, detail=str(exc)) from exc

        async def event_stream() -> AsyncIterator[str]:
            completed = False
            error_category = "stream_interrupted"
            finish_seen = False
            yield _chunk(completion_id, request.model, {"role": "assistant"}, None)
            try:
                async for upstream_chunk in gateway.stream(
                    messages=messages,
                    model=request.model,
                    user=request.user,
                    context=conversation,
                    explicit_account_id=x_codearts_account,
                    options=options,
                    prepared_candidates=prepared_candidates,
                ):
                    delta = upstream_chunk.get("delta") if isinstance(upstream_chunk, dict) else {}
                    finish_reason = upstream_chunk.get("finish_reason") if isinstance(upstream_chunk, dict) else None
                    if finish_reason is not None:
                        finish_seen = True
                    yield _chunk(completion_id, request.model, delta if isinstance(delta, dict) else {}, finish_reason)
                completed = True
                error_category = ""
                if not finish_seen:
                    finish_reason = str((gateway.last_trace or {}).get("finish_reason") or "stop")
                    yield _chunk(completion_id, request.model, {}, finish_reason)
                if include_usage:
                    trace = gateway.last_trace or {}
                    usage_details = trace.get("usage_details") if isinstance(trace.get("usage_details"), dict) else trace.get("usage")
                    yield _usage_chunk(completion_id, request.model, usage_details if isinstance(usage_details, dict) else {})
                yield "data: [DONE]\n\n"
            except Exception as exc:
                error_category = exc.category if isinstance(exc, CodeArtsUpstreamError) else "upstream_error"
                error = {"error": {"message": str(exc), "type": "upstream_error"}}
                yield f"data: {json.dumps(error, ensure_ascii=False)}\n\n"
                yield "data: [DONE]\n\n"
            finally:
                trace = gateway.last_trace or {}
                finish_log(
                    status=200,
                    success=completed,
                    account_id=str(trace.get("account_id") or ""),
                    environment=str(trace.get("environment") or ""),
                    attempts=int(trace.get("attempts") or 0),
                    usage=trace.get("usage") if isinstance(trace.get("usage"), dict) else {},
                    conversation_id=conversation_id,
                    error_category=error_category or str(trace.get("error_category") or ""),
                )

        return StreamingResponse(
            event_stream(),
            media_type="text/event-stream",
            headers={"X-CodeArts-Conversation": conversation_id, "X-Request-ID": request_id},
        )

    try:
        result = await gateway.complete(
            messages=messages,
            model=request.model,
            user=request.user,
            context=conversation,
            explicit_account_id=x_codearts_account,
            options=options,
        )
    except CodeArtsUpstreamError as exc:
        status_code = 503 if exc.category in {"no_account", "retry_budget"} else 502
        trace = gateway.last_trace or {}
        finish_log(
            status=status_code,
            success=False,
            account_id=str(trace.get("account_id") or ""),
            environment=str(trace.get("environment") or ""),
            attempts=int(trace.get("attempts") or 0),
            usage=trace.get("usage") if isinstance(trace.get("usage"), dict) else {},
            conversation_id=conversation_id,
            error_category=exc.category,
        )
        raise HTTPException(status_code=status_code, detail=str(exc)) from exc
    except Exception as exc:
        trace = gateway.last_trace or {}
        finish_log(
            status=502,
            success=False,
            account_id=str(trace.get("account_id") or ""),
            environment=str(trace.get("environment") or ""),
            attempts=int(trace.get("attempts") or 0),
            usage=trace.get("usage") if isinstance(trace.get("usage"), dict) else {},
            conversation_id=conversation_id,
            error_category="upstream_error",
        )
        raise HTTPException(status_code=502, detail=str(exc)) from exc

    finish_log(
        status=200,
        success=True,
        account_id=result.account_id,
        environment=result.environment,
        attempts=result.attempts,
        usage=result.usage,
        conversation_id=result.conversation_id,
    )
    response.headers["X-CodeArts-Conversation"] = result.conversation_id
    response.headers["X-Request-ID"] = request_id
    assistant_message: dict = {"role": "assistant", "content": result.text if result.text else None}
    if result.reasoning_content:
        assistant_message["reasoning_content"] = result.reasoning_content
    if result.tool_calls:
        assistant_message["tool_calls"] = result.tool_calls
    return {
        "id": completion_id,
        "object": "chat.completion",
        "created": int(time.time()),
        "model": request.model,
        "choices": [
            {
                "index": 0,
                "message": assistant_message,
                "finish_reason": result.finish_reason or "stop",
            }
        ],
        "usage": result.usage_details or result.usage,
        "conversation_id": result.conversation_id,
        "codearts_account": result.account_id,
        "codearts_environment": result.environment,
        "codearts_attempts": result.attempts,
    }
