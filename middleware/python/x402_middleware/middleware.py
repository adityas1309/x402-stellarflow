"""
Core x402 paywall decorator for FastAPI / Starlette.

No dependencies beyond the Python standard library + httpx for the
facilitator HTTP calls. Works with any ASGI framework that uses
Starlette's Request object (FastAPI, Starlette, etc).
"""

from __future__ import annotations

import base64
import functools
import json
import math
import os
import asyncio
from typing import Any, Callable

try:
    import httpx
except ImportError:
    httpx = None  # type: ignore

# ─── Constants ─────────────────────────────────────────────────

DEFAULT_FACILITATOR_URL = "https://channels.openzeppelin.com/x402"
DEFAULT_NETWORK = "stellar:testnet"
DEFAULT_USDC_CONTRACT = "CBIELTK6YBZJU5UP2WWQEUCYKLPU6AUNZ2BQ4WWFEIE3USCIHMXQDAMA"
DEFAULT_USDC_ISSUER = "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
X402_VERSION = 2


def _format_amount(usdc: float) -> str:
    """Convert USDC decimal to integer stroops string (7 decimals)."""
    return str(round(usdc * 10_000_000))


def _normalize_address(addr: str) -> str:
    if len(addr) == 56 and addr.startswith("G"):
        return addr
    return "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADRYRUNANONYMOUSXXXXX"


# ─── Decorator factory ─────────────────────────────────────────

def x402_paywall(
    price: float,
    treasury: str | None = None,
    *,
    facilitator_url: str | None = None,
    facilitator_key: str | None = None,
    network: str | None = None,
    usdc_contract: str | None = None,
    usdc_issuer: str | None = None,
    description: str | None = None,
    dry_run: bool | None = None,
) -> Callable:
    """
    Decorator that gates a FastAPI/Starlette endpoint behind an x402 paywall.

    Usage:
        @app.post("/api/sentiment")
        @x402_paywall(price=0.05)  # reads treasury from X402_TREASURY_ADDRESS env
        async def sentiment(request: Request):
            payer = request.state.x402_payer
            return {"result": "..."}
    """
    # Fall back to env vars for everything except price.
    treasury = treasury or os.environ.get("X402_TREASURY_ADDRESS", "")
    facilitator_url = facilitator_url or os.environ.get(
        "X402_FACILITATOR_URL", DEFAULT_FACILITATOR_URL
    )
    facilitator_key = facilitator_key or os.environ.get("X402_FACILITATOR_KEY", "")
    network = network or os.environ.get("X402_NETWORK", DEFAULT_NETWORK)
    usdc_contract = usdc_contract or os.environ.get(
        "X402_USDC_CONTRACT", DEFAULT_USDC_CONTRACT
    )
    usdc_issuer = usdc_issuer or os.environ.get("X402_USDC_ISSUER", DEFAULT_USDC_ISSUER)
    if dry_run is None:
        dry_run = os.environ.get("X402_DRY_RUN", "false").lower() == "true"

    if not treasury:
        raise RuntimeError(
            "x402_paywall: no treasury address. "
            "Set X402_TREASURY_ADDRESS env var or pass treasury= kwarg. "
            "Run `python -m x402_middleware init` to generate one."
        )

    amount_stroops = _format_amount(price)

    requirement = {
        "scheme": "exact",
        "network": network,
        "asset": usdc_contract,
        "amount": amount_stroops,
        "payTo": treasury,
        "maxTimeoutSeconds": 30,
        "extra": {
            "code": "USDC",
            "issuer": usdc_issuer,
            "areFeesSponsored": True,
        },
    }

    def decorator(func: Callable) -> Callable:
        @functools.wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> Any:
            # Find the Request object in args/kwargs (FastAPI passes it as 'request').
            request = kwargs.get("request") or (args[0] if args else None)
            if request is None:
                raise RuntimeError(
                    "x402_paywall: could not find Request object. "
                    "Make sure your handler has a 'request: Request' parameter."
                )

            from starlette.requests import Request as StarletteRequest
            from starlette.responses import JSONResponse

            if not isinstance(request, StarletteRequest):
                raise RuntimeError("x402_paywall: first argument must be a Starlette Request")

            url = str(request.url)
            payment_required = {
                "x402Version": X402_VERSION,
                "error": "",
                "resource": {
                    "url": url,
                    "description": description or request.url.path,
                    "mimeType": "application/json",
                },
                "accepts": [requirement],
            }

            # 1. Check for X-PAYMENT header
            payment_header = request.headers.get("x-payment", "")
            if not payment_header:
                payment_required["error"] = "X-PAYMENT header missing"
                return JSONResponse(payment_required, status_code=402)

            # 2. Decode the payload
            try:
                raw = base64.b64decode(payment_header)
                payload = json.loads(raw)
            except Exception:
                payment_required["error"] = "invalid X-PAYMENT header: base64/JSON decode failed"
                return JSONResponse(payment_required, status_code=402)

            # 3. Validate requirements match
            acc = payload.get("accepted", {})
            if (
                acc.get("scheme") != requirement["scheme"]
                or acc.get("network") != requirement["network"]
                or acc.get("payTo") != requirement["payTo"]
                or acc.get("amount") != requirement["amount"]
            ):
                payment_required["error"] = "payment requirements mismatch"
                return JSONResponse(payment_required, status_code=402)

            # 4. Verify with facilitator
            payer = ""
            if dry_run:
                inner = payload.get("payload", {})
                if isinstance(inner, str):
                    try:
                        inner = json.loads(inner)
                    except Exception:
                        inner = {}
                payer = inner.get("from", "GDRYRUNXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
            else:
                if not facilitator_key:
                    payment_required["error"] = "facilitator not configured"
                    return JSONResponse(payment_required, status_code=402)

                if httpx is None:
                    raise RuntimeError("x402_paywall requires httpx: pip install httpx")

                async with httpx.AsyncClient(timeout=15) as client:
                    verify_resp = await client.post(
                        f"{facilitator_url.rstrip('/')}/verify",
                        json={
                            "x402Version": X402_VERSION,
                            "paymentPayload": payload,
                            "paymentRequirements": requirement,
                        },
                        headers={
                            "Authorization": f"Bearer {facilitator_key}",
                            "Content-Type": "application/json",
                        },
                    )

                if verify_resp.status_code >= 400:
                    payment_required["error"] = f"facilitator error: {verify_resp.text}"
                    return JSONResponse(payment_required, status_code=402)

                verify_data = verify_resp.json()
                if not verify_data.get("isValid"):
                    reason = verify_data.get("invalidReason", "unknown")
                    payment_required["error"] = f"payment rejected: {reason}"
                    return JSONResponse(payment_required, status_code=402)

                payer = verify_data.get("payer", "")

            # 5. Set payer on request state
            normalized_payer = _normalize_address(payer)
            request.state.x402_payer = normalized_payer
            request.state.x402_amount = price

            import time as _time
            started_at = _time.time()

            # 6. Call the handler
            response = await func(*args, **kwargs)

            # Log to the in-memory dashboard logger (if the handler succeeded).
            if getattr(response, "status_code", 200) < 400:
                from .logger import default_logger, PaidCallLog
                default_logger.log(PaidCallLog(
                    timestamp=started_at,
                    endpoint=description or request.url.path,
                    payer=normalized_payer,
                    amount=price,
                    facilitator="openzeppelin" if not dry_run else "dry-run",
                    duration_ms=int((_time.time() - started_at) * 1000),
                ))

            # 7. Settle async (non-blocking)
            if not dry_run and facilitator_key and httpx is not None:
                asyncio.create_task(_settle(
                    facilitator_url, facilitator_key, payload, requirement
                ))

            return response

        return wrapper
    return decorator


async def _settle(
    facilitator_url: str,
    facilitator_key: str,
    payload: dict,
    requirement: dict,
) -> None:
    """Settle the payment on-chain (fire-and-forget after handler response)."""
    try:
        async with httpx.AsyncClient(timeout=30) as client:
            await client.post(
                f"{facilitator_url.rstrip('/')}/settle",
                json={
                    "x402Version": X402_VERSION,
                    "paymentPayload": payload,
                    "paymentRequirements": requirement,
                },
                headers={
                    "Authorization": f"Bearer {facilitator_key}",
                    "Content-Type": "application/json",
                },
            )
    except Exception as e:
        print(f"[x402-middleware] settle error: {e}")
