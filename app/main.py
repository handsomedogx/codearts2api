from __future__ import annotations

import json
import time
import uuid
from collections import deque
from datetime import datetime, timezone
from pathlib import Path
from typing import AsyncIterator

from fastapi import Depends, FastAPI, Header, HTTPException, Query, Request
from fastapi.responses import FileResponse, RedirectResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles

from app.codearts.auth import AuthManager, get_auth_manager
from app.codearts.client import CodeArtsClient
from app.config import Settings, get_settings
from app.models import ChatCompletionRequest

APP_VERSION = "0.3.0"
WEB_DIR = Path(__file__).resolve().parent / "web"
REQUEST_LOGS: deque[dict] = deque(maxlen=200)

app = FastAPI(title="codearts2api", version=APP_VERSION)
app.mount("/static", StaticFiles(directory=WEB_DIR), name="static")


def require_local_api_key(
    authorization: str | None = Header(default=None),
    settings: Settings = Depends(get_settings),
) -> None:
    if not settings.api_key:
        return
    expected = f"Bearer {settings.api_key}"
    if authorization != expected:
        raise HTTPException(status_code=401, detail="Invalid API key")


def get_request_auth_manager(settings: Settings = Depends(get_settings)) -> AuthManager:
    if settings == get_settings():
        return get_auth_manager()
    return AuthManager(settings)


def _messages(request: ChatCompletionRequest) -> list[dict]:
    return [message.model_dump(exclude_none=True) for message in request.messages]


def _model_list(settings: Settings) -> list[str]:
    items = list(settings.models)
    if settings.default_model not in items:
        items.insert(0, settings.default_model)
    return items or ["codearts-auto"]


@app.middleware("http")
async def request_activity_log(request: Request, call_next):
    started = time.perf_counter()
    status_code = 500
    try:
        response = await call_next(request)
        status_code = response.status_code
        return response
    finally:
        path = request.url.path
        if path.startswith("/v1/") or path.startswith("/auth/") or path == "/health":
            REQUEST_LOGS.appendleft(
                {
                    "time": datetime.now(timezone.utc).isoformat(),
                    "method": request.method,
                    "path": path,
                    "status": status_code,
                    "duration_ms": round((time.perf_counter() - started) * 1000, 1),
                }
            )


@app.get("/", include_in_schema=False)
async def web_ui():
    return FileResponse(WEB_DIR / "index.html")


@app.get("/api/ui/meta", include_in_schema=False)
async def ui_meta(settings: Settings = Depends(get_settings)) -> dict:
    return {
        "name": "codearts2api",
        "version": APP_VERSION,
        "api_base": "/v1",
        "api_key_enabled": bool(settings.api_key),
        "upstream": settings.base_url,
        "callback_url": settings.callback_url,
    }


@app.get("/api/ui/logs", dependencies=[Depends(require_local_api_key)], include_in_schema=False)
async def ui_logs(limit: int = Query(default=50, ge=1, le=200)) -> dict:
    return {"items": list(REQUEST_LOGS)[:limit]}


@app.get("/health")
async def health(
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    status = auth.public_status()
    return {
        "status": "ok",
        "credentials_configured": status["authenticated"],
        "auth_source": status["source"],
        "upstream": settings.base_url,
    }


@app.get("/auth/login", dependencies=[Depends(require_local_api_key)])
async def auth_login(auth: AuthManager = Depends(get_request_auth_manager)):
    return RedirectResponse(auth.begin_login(), status_code=307)


@app.get("/auth/login-url", dependencies=[Depends(require_local_api_key)])
async def auth_login_url(
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
) -> dict:
    return {
        "authorize_url": auth.begin_login(),
        "callback_url": settings.callback_url,
    }


@app.get("/oauth/callback")
async def oauth_callback(
    code: str | None = Query(default=None),
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
):
    if not code:
        raise HTTPException(status_code=400, detail="OAuth callback is missing code")
    try:
        await auth.exchange_code(code)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc
    return RedirectResponse(settings.login_success_url, status_code=307)


@app.get("/auth/status", dependencies=[Depends(require_local_api_key)])
async def auth_status(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    return auth.public_status()


@app.post("/auth/refresh", dependencies=[Depends(require_local_api_key)])
async def auth_refresh(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    try:
        return await auth.refresh(force=True)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc


@app.post("/auth/logout", dependencies=[Depends(require_local_api_key)])
async def auth_logout(auth: AuthManager = Depends(get_request_auth_manager)) -> dict:
    auth.logout()
    return {"authenticated": False, "source": "none"}


@app.get("/v1/models", dependencies=[Depends(require_local_api_key)])
async def models(settings: Settings = Depends(get_settings)) -> dict:
    now = int(time.time())
    return {
        "object": "list",
        "data": [
            {"id": model, "object": "model", "created": now, "owned_by": "codearts"}
            for model in _model_list(settings)
        ],
    }


def _chunk(completion_id: str, model: str, content: str | None = None, finish_reason: str | None = None) -> str:
    delta = {} if content is None else {"content": content}
    payload = {
        "id": completion_id,
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }
    return f"data: {json.dumps(payload, ensure_ascii=False, separators=(',', ':'))}\n\n"


@app.post("/v1/chat/completions", dependencies=[Depends(require_local_api_key)])
async def chat_completions(
    request: ChatCompletionRequest,
    settings: Settings = Depends(get_settings),
    auth: AuthManager = Depends(get_request_auth_manager),
):
    if await auth.get_credentials() is None:
        raise HTTPException(
            status_code=503,
            detail="CodeArts credentials are not configured. Open /auth/login to authenticate, or configure the manual credential fallback.",
        )

    client = CodeArtsClient(settings, auth_manager=auth)
    completion_id = f"chatcmpl-{uuid.uuid4().hex}"
    messages = _messages(request)

    if request.stream:
        async def event_stream() -> AsyncIterator[str]:
            yield _chunk(completion_id, request.model, None, None)
            try:
                async for delta in client.chat_stream(messages=messages, model=request.model, user=request.user):
                    yield _chunk(completion_id, request.model, delta, None)
                yield _chunk(completion_id, request.model, None, "stop")
                yield "data: [DONE]\n\n"
            except Exception as exc:
                error = {"error": {"message": str(exc), "type": "upstream_error"}}
                yield f"data: {json.dumps(error, ensure_ascii=False)}\n\n"
                yield "data: [DONE]\n\n"

        return StreamingResponse(event_stream(), media_type="text/event-stream")

    try:
        content = await client.chat(messages=messages, model=request.model, user=request.user)
    except Exception as exc:
        raise HTTPException(status_code=502, detail=str(exc)) from exc

    return {
        "id": completion_id,
        "object": "chat.completion",
        "created": int(time.time()),
        "model": request.model,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
    }
