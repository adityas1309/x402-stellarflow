"""
x402-middleware — Stellar-native middleware for FastAPI / Starlette.

Provides two complementary primitives in a single import:

  • x402_paywall — agents pay USDC per call (x402 v2 spec)
  • sep10_auth   — operators sign in with their Stellar wallet (SEP-10)

Quick start:

    pip install x402-middleware
    python -m x402_middleware init   # interactive setup, writes .env

    from fastapi import FastAPI, Request
    from x402_middleware import x402_paywall, sep10_auth

    app = FastAPI()
    sep10_auth.mount(app)  # registers /api/auth/challenge + /api/auth/token

    @app.post("/api/sentiment")
    @x402_paywall(price=0.05)
    async def sentiment(request: Request):
        return {"score": 0.82, "payer": request.state.x402_payer}
"""

import importlib

from .middleware import x402_paywall

# Lazy-load sep10_auth and dashboard to avoid a hard dependency on
# stellar-sdk/pyjwt/fastapi at import time. The x402_paywall middleware
# works with just httpx; the other surfaces pull in additional deps.

_LAZY = {
    "sep10_auth": ("x402_middleware.sep10", "sep10_auth"),
    "mount": ("x402_middleware.sep10", "mount"),
    "protect": ("x402_middleware.sep10", "protect"),
    "dashboard": ("x402_middleware.dashboard", None),  # submodule
}


def __getattr__(name: str):
    if name in _LAZY:
        module_name, attr = _LAZY[name]
        mod = importlib.import_module(module_name)
        return mod if attr is None else getattr(mod, attr)
    raise AttributeError(f"module 'x402_middleware' has no attribute {name!r}")


__all__ = ["x402_paywall", "sep10_auth", "mount", "protect", "dashboard"]
__version__ = "0.1.0"
