"""HTTP adapter: /health, /ready and POST /v1/extract.

Request text is untrusted data: it is never logged, and validation errors
report field locations and error kinds only, never input values.
"""
from __future__ import annotations

import asyncio
import contextlib
from concurrent.futures import ThreadPoolExecutor
import hmac
import logging
import threading
import time
from typing import AsyncIterator, Sequence

from pydantic import ValidationError
from starlette.applications import Starlette
from starlette.middleware import Middleware
from starlette.middleware.trustedhost import TrustedHostMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse
from starlette.routing import Route

from .adapter import DeadlineExceeded, extract_request
from .chunking import SchemaTooLarge
from .limits import Limits, LimitError, check_request
from .model import ModelHolder
from .schemas import ExtractRequest, ExtractResponse

KEY_HEADER = "x-veda-extractor-key"
OUTER_GRACE = 1.0
LOOPBACK_HOSTS = ("127.0.0.1", "localhost", "::1", "[::1]")
log = logging.getLogger("knowledge_extractor.http")


class Busy(Exception):
    """No inference slot became free before the request deadline."""


def error(status: int, code: str, message: str) -> JSONResponse:
    return JSONResponse({"error": {"code": code, "message": message}}, status_code=status)


def describe(exc: ValidationError) -> str:
    parts = []
    for item in exc.errors(include_input=False, include_url=False)[:5]:
        loc = ".".join(str(part) for part in item.get("loc", ()))
        parts.append(f"{loc or 'body'}: {item.get('type', 'invalid')}")
    return "invalid request: " + "; ".join(parts)


async def read_body(request: Request, limit: int) -> bytes:
    declared = request.headers.get("content-length")
    if declared is not None:
        if not declared.isdigit():
            raise LimitError(400, "bad_request", "invalid Content-Length")
        if int(declared) > limit:
            raise LimitError(413, "body_too_large", f"request body exceeds {limit} bytes")
    chunks, size = [], 0
    async for part in request.stream():
        size += len(part)
        if size > limit:
            raise LimitError(413, "body_too_large", f"request body exceeds {limit} bytes")
        chunks.append(part)
    return b"".join(chunks)


class Service:
    def __init__(self, holder: ModelHolder, key: bytes, limits: Limits) -> None:
        self.holder = holder
        self.key = key
        self.limits = limits
        self.slots = threading.BoundedSemaphore(limits.inference_concurrency)
        self.executor = ThreadPoolExecutor(max_workers=limits.inference_concurrency * 2 + 2,
                                           thread_name_prefix="inference")

    async def health(self, request: Request) -> JSONResponse:
        return JSONResponse({"status": "ok"})

    async def ready(self, request: Request) -> JSONResponse:
        state, reason, model = self.holder.status()
        if state == "ready" and model is not None:
            return JSONResponse({"status": "ready", "model": model.info.model_dump(),
                                 "load_seconds": self.holder.load_seconds})
        return JSONResponse({"status": state, "reason": reason}, status_code=503)

    def authorized(self, request: Request) -> bool:
        provided = request.headers.get(KEY_HEADER, "")
        return hmac.compare_digest(provided.encode("utf-8", "surrogateescape"), self.key)

    async def extract(self, request: Request) -> JSONResponse:
        started = time.monotonic()
        if not self.authorized(request):
            return error(401, "unauthorized", "missing or invalid service key")
        state, reason, model = self.holder.status()
        if state != "ready" or model is None:
            return error(503, "not_ready", reason or "model is not ready")
        if not request.headers.get("content-type", "").startswith("application/json"):
            return error(415, "unsupported_media_type", "Content-Type must be application/json")
        try:
            body = await read_body(request, self.limits.max_body_bytes)
            parsed = ExtractRequest.model_validate_json(body)
            check_request(parsed, self.limits)
        except LimitError as exc:
            return error(exc.status, exc.code, exc.message)
        except ValidationError as exc:
            return error(422, "invalid_request", describe(exc))
        deadline = started + self.limits.request_timeout
        status = "error"
        try:
            response = await self._run(model, parsed, deadline)
            status = "ok"
            return JSONResponse(response.model_dump(mode="json"))
        except (asyncio.TimeoutError, DeadlineExceeded):
            status = "timeout"
            return error(504, "timeout", "extraction exceeded the request time limit")
        except Busy:
            status = "busy"
            return error(503, "busy", "no inference slot became free in time")
        except SchemaTooLarge:
            status = "schema_too_large"
            return error(422, "schema_too_large", "schema prompt leaves no room in the token window")
        except Exception as exc:  # never echo internals or text
            log.error("request %s failed: %s", parsed.request_id, type(exc).__name__)
            return error(500, "internal", "extraction failed")
        finally:
            log.info("request %s blocks=%d chars=%d status=%s %.3fs", parsed.request_id,
                     len(parsed.blocks), sum(len(b.text) for b in parsed.blocks), status,
                     time.monotonic() - started)

    async def _run(self, model, parsed: ExtractRequest, deadline: float) -> ExtractResponse:
        def check_deadline() -> None:
            if time.monotonic() > deadline:
                raise DeadlineExceeded()

        def work() -> ExtractResponse:
            if not self.slots.acquire(timeout=max(0.0, deadline - time.monotonic())):
                raise Busy()
            try:
                return extract_request(model, parsed, self.limits, check_deadline)
            finally:
                self.slots.release()

        future = asyncio.get_running_loop().run_in_executor(self.executor, work)
        # The worker enforces the deadline between chunks and frees its slot itself;
        # the outer wait (shielded, with a grace period) only bounds one slow chunk.
        return await asyncio.wait_for(asyncio.shield(future),
                                      timeout=max(0.0, deadline - time.monotonic()) + OUTER_GRACE)


def create_app(holder: ModelHolder, key: bytes, limits: Limits,
               allowed_hosts: Sequence[str] = LOOPBACK_HOSTS) -> Starlette:
    service = Service(holder, key, limits)

    @contextlib.asynccontextmanager
    async def lifespan(app: Starlette) -> AsyncIterator[None]:
        try:
            yield
        finally:
            service.executor.shutdown(wait=False, cancel_futures=True)

    app = Starlette(
        routes=[Route("/health", service.health, methods=["GET"]),
                Route("/ready", service.ready, methods=["GET"]),
                Route("/v1/extract", service.extract, methods=["POST"])],
        middleware=[Middleware(TrustedHostMiddleware, allowed_hosts=list(allowed_hosts))],
        lifespan=lifespan,
    )
    app.state.service = service
    return app
