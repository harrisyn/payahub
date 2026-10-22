"""Payahub public API.

Everything (dashboard, document channel, Serenity) goes through these routes;
there is no private back door. Storage is in-memory until the Postgres layer
lands in phase 0.
"""

from __future__ import annotations

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

from .errors import ApiError
from .routes import claims, health, payers


def create_app() -> FastAPI:
    app = FastAPI(
        title="Payahub API",
        version="0.1.0",
        description="Send any claim, in any form, to any supported payer.",
        openapi_url="/v1/openapi.json",
        docs_url="/v1/docs",
    )

    @app.exception_handler(ApiError)
    async def _api_error(_: Request, exc: ApiError) -> JSONResponse:
        return JSONResponse(status_code=exc.status_code, content={"error": exc.body()})

    app.include_router(health.router, prefix="/v1")
    app.include_router(payers.router, prefix="/v1")
    app.include_router(claims.router, prefix="/v1")
    return app


app = create_app()
