"""
SEP-10 Wallet Authentication for FastAPI — the operator-side companion
to the x402 paywall.

Usage:

    from fastapi import FastAPI, Request
    from x402_middleware import sep10_auth

    app = FastAPI()

    # Auto-registers /api/auth/challenge (GET) and /api/auth/token (POST)
    sep10_auth.mount(app)

    # Protect operator endpoints
    @app.get("/operator/stats")
    @sep10_auth.protect(allowed_wallets=["GBLDFWEL..."])
    async def stats(request: Request):
        # request.state.wallet is set to the signed-in wallet G-address
        return {"wallet": request.state.wallet}

Configuration is read from environment variables set by `python -m x402_middleware init`:
    SEP10_SERVER_SIGNING_KEY  — server signing keypair S-key
    SEP10_SERVER_ADDRESS      — server signing keypair G-address
    SEP10_HOME_DOMAIN         — the home domain string included in challenges
    X402_JWT_SECRET           — HMAC secret for session JWTs
    X402_NETWORK              — "stellar:testnet"
"""

from __future__ import annotations

import functools
import os
import secrets as _secrets
import time
from typing import Any, Callable

try:
    from stellar_sdk import (
        Account,
        Keypair,
        ManageData,
        Network,
        TransactionBuilder,
        Transaction,
        TransactionEnvelope,
    )
except ImportError:
    raise ImportError("sep10_auth requires stellar-sdk: pip install stellar-sdk")

try:
    import jwt
except ImportError:
    raise ImportError("sep10_auth requires pyjwt: pip install pyjwt")

# ─── In-memory challenge store ─────────────────────────────────

_pending_challenges: dict[str, dict[str, Any]] = {}
_CHALLENGE_TTL = 300  # 5 minutes


def _gc_challenges() -> None:
    now = time.time()
    expired = [k for k, v in _pending_challenges.items() if v["expires_at"] < now]
    for k in expired:
        _pending_challenges.pop(k, None)


# ─── Config ─────────────────────────────────────────────────────

def _get_config() -> dict[str, Any]:
    server_key = os.environ.get("SEP10_SERVER_SIGNING_KEY", "")
    jwt_secret = os.environ.get("X402_JWT_SECRET", "")
    if not server_key:
        raise RuntimeError(
            "sep10_auth: SEP10_SERVER_SIGNING_KEY not set. "
            "Run `python -m x402_middleware init` to generate one."
        )
    if not jwt_secret:
        raise RuntimeError(
            "sep10_auth: X402_JWT_SECRET not set. "
            "Run `python -m x402_middleware init` to generate one."
        )
    network = os.environ.get("X402_NETWORK", "stellar:testnet")
    passphrase = (
        Network.TESTNET_NETWORK_PASSPHRASE
        if "testnet" in network
        else Network.PUBLIC_NETWORK_PASSPHRASE
    )
    return {
        "server_key": server_key,
        "jwt_secret": jwt_secret,
        "home_domain": os.environ.get("SEP10_HOME_DOMAIN", "localhost"),
        "network_passphrase": passphrase,
        "session_ttl": int(os.environ.get("X402_SESSION_TTL", "86400")),
    }


# ─── Challenge construction ────────────────────────────────────

def _build_challenge_xdr(
    server_kp: Keypair, client_account: str, home_domain: str, passphrase: str
) -> str:
    nonce = _secrets.token_bytes(48)

    # SEP-10 convention: the server creates a fake source account
    # with sequence -1 for the challenge tx. It's never submitted
    # to the network.
    server_account = Account(server_kp.public_key, -1)

    tx = (
        TransactionBuilder(
            source_account=server_account,
            network_passphrase=passphrase,
            base_fee=100,
        )
        .append_manage_data_op(
            data_name=f"{home_domain} auth",
            data_value=nonce,
            source=client_account,
        )
        .append_manage_data_op(
            data_name="web_auth_domain",
            data_value=home_domain.encode("utf-8"),
            source=server_kp.public_key,
        )
        .set_timeout(300)
        .build()
    )

    tx.sign(server_kp)
    return tx.to_xdr()


def _verify_challenge(
    signed_xdr: str, expected_account: str, server_key: str, passphrase: str
) -> bool:
    try:
        envelope = TransactionEnvelope.from_xdr(signed_xdr, passphrase)
        tx = envelope.transaction

        signatures = envelope.signatures
        if len(signatures) < 2:
            return False

        tx_hash = envelope.hash()

        # Client signature check
        client_kp = Keypair.from_public_key(expected_account)
        signed_by_client = any(
            _verify_sig(client_kp, tx_hash, sig.signature) for sig in signatures
        )
        if not signed_by_client:
            return False

        # Server signature check
        server_kp = Keypair.from_secret(server_key)
        signed_by_server = any(
            _verify_sig(server_kp, tx_hash, sig.signature) for sig in signatures
        )
        return signed_by_server
    except Exception:
        return False


def _verify_sig(kp: Keypair, tx_hash: bytes, sig: bytes) -> bool:
    try:
        kp.verify(tx_hash, sig)
        return True
    except Exception:
        return False


# ─── Public API ────────────────────────────────────────────────

def mount(app: Any, prefix: str = "/api/auth") -> None:
    """Register /api/auth/challenge and /api/auth/token on a FastAPI app."""
    try:
        from fastapi import Request
        from fastapi.responses import JSONResponse
    except ImportError:
        raise ImportError("sep10_auth.mount() requires fastapi: pip install fastapi")

    cfg = _get_config()
    server_kp = Keypair.from_secret(cfg["server_key"])

    @app.get(f"{prefix}/challenge")
    async def sep10_challenge(account: str):
        if not account or len(account) != 56 or not account.startswith("G"):
            return JSONResponse(
                {"error": "invalid 'account' query parameter"}, status_code=400
            )

        xdr = _build_challenge_xdr(
            server_kp, account, cfg["home_domain"], cfg["network_passphrase"]
        )
        cid = _secrets.token_hex(16)
        _pending_challenges[cid] = {
            "xdr": xdr,
            "account": account,
            "expires_at": time.time() + _CHALLENGE_TTL,
        }
        return {
            "id": cid,
            "transaction": xdr,
            "network_passphrase": cfg["network_passphrase"],
            "home_domain": cfg["home_domain"],
            "expires_at_seconds": _CHALLENGE_TTL,
        }

    @app.post(f"{prefix}/token")
    async def sep10_token(request: Request):
        body = await request.json()
        cid = body.get("id", "")
        signed_xdr = body.get("transaction", "")
        if not cid or not signed_xdr:
            return JSONResponse(
                {"error": "missing id or transaction"}, status_code=400
            )

        _gc_challenges()
        pending = _pending_challenges.get(cid)
        if not pending:
            return JSONResponse(
                {"error": "challenge not found or expired"}, status_code=401
            )
        if pending["expires_at"] < time.time():
            _pending_challenges.pop(cid, None)
            return JSONResponse({"error": "challenge expired"}, status_code=401)

        valid = _verify_challenge(
            signed_xdr,
            pending["account"],
            cfg["server_key"],
            cfg["network_passphrase"],
        )
        if not valid:
            return JSONResponse(
                {"error": "signature verification failed"}, status_code=401
            )

        _pending_challenges.pop(cid, None)

        # Issue JWT
        now = int(time.time())
        payload = {
            "wallet": pending["account"],
            "iat": now,
            "exp": now + cfg["session_ttl"],
        }
        token = jwt.encode(payload, cfg["jwt_secret"], algorithm="HS256")

        return {
            "token": token,
            "wallet": pending["account"],
            "expires_in": cfg["session_ttl"],
        }


def protect(allowed_wallets: list[str] | None = None) -> Callable:
    """Decorator that enforces SEP-10 JWT session on a FastAPI route."""
    def decorator(func: Callable) -> Callable:
        @functools.wraps(func)
        async def wrapper(*args: Any, **kwargs: Any) -> Any:
            from fastapi import Request
            from fastapi.responses import JSONResponse

            request = kwargs.get("request") or (args[0] if args else None)
            if not isinstance(request, Request):
                raise RuntimeError(
                    "sep10_auth.protect requires a 'request: Request' parameter"
                )

            cfg = _get_config()
            auth_header = request.headers.get("authorization", "")
            if not auth_header.startswith("Bearer "):
                return JSONResponse(
                    {"error": "missing Authorization: Bearer <token>"},
                    status_code=401,
                )

            token = auth_header[7:]
            try:
                payload = jwt.decode(token, cfg["jwt_secret"], algorithms=["HS256"])
            except jwt.InvalidTokenError as e:
                return JSONResponse(
                    {"error": f"invalid token: {e}"}, status_code=401
                )

            wallet = payload.get("wallet", "")
            if not wallet:
                return JSONResponse(
                    {"error": "invalid token: no wallet claim"}, status_code=401
                )

            if allowed_wallets and wallet not in allowed_wallets:
                return JSONResponse(
                    {"error": "wallet not in allowlist"}, status_code=403
                )

            request.state.wallet = wallet
            return await func(*args, **kwargs)

        return wrapper

    return decorator


# Namespace access: sep10_auth.mount(), sep10_auth.protect()
class _Sep10Auth:
    mount = staticmethod(mount)
    protect = staticmethod(protect)


sep10_auth = _Sep10Auth()
