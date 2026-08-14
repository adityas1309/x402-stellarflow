"""
Interactive setup CLI for x402-middleware.

Run with:
    python -m x402_middleware init

Walks the developer through:
    1. Generating or importing a Stellar treasury wallet
    2. Generating a SEP-10 server signing keypair
    3. Generating a JWT secret for wallet sessions
    4. Configuring the OZ facilitator API key
    5. Saving everything to .env (and adding to .gitignore)
    6. Printing a ready-to-paste code snippet

Zero external dependencies for the UI (uses stdin/stdout).
"""

from __future__ import annotations

import os
import secrets
import sys
from pathlib import Path

# Defer stellar-sdk and httpx imports so we can still show `help` without them.
def _require_stellar_sdk():
    try:
        from stellar_sdk import Keypair
        return Keypair
    except ImportError:
        print("Error: stellar-sdk not installed. Run: pip install stellar-sdk", file=sys.stderr)
        sys.exit(1)


def _try_httpx():
    try:
        import httpx
        return httpx
    except ImportError:
        return None


# ─── Color helpers (no dependencies) ───────────────────────────

class C:
    RESET = "\033[0m"
    BOLD = "\033[1m"
    DIM = "\033[2m"
    CYAN = "\033[36m"
    GREEN = "\033[32m"
    YELLOW = "\033[33m"
    RED = "\033[31m"
    BLUE = "\033[34m"


def header(text: str) -> None:
    print()
    print(f"{C.CYAN}{'═' * 68}{C.RESET}")
    print(f"{C.CYAN}  {C.BOLD}{text}{C.RESET}")
    print(f"{C.CYAN}{'═' * 68}{C.RESET}")
    print()


def step(n: int, text: str) -> None:
    print(f"{C.BLUE}[{n}/5]{C.RESET} {C.BOLD}{text}{C.RESET}")


def info(text: str) -> None:
    print(f"{C.DIM}  {text}{C.RESET}")


def ok(text: str) -> None:
    print(f"{C.GREEN}  ✓{C.RESET} {text}")


def warn(text: str) -> None:
    print(f"{C.YELLOW}  ⚠{C.RESET} {text}")


def ask(prompt: str, default: str = "") -> str:
    suffix = f" {C.DIM}({default}){C.RESET}" if default else ""
    response = input(f"{prompt}{suffix} ").strip()
    return response or default


def ask_yn(prompt: str, default_yes: bool = True) -> bool:
    suffix = f" {C.DIM}[{'Y/n' if default_yes else 'y/N'}]{C.RESET}"
    response = input(f"{prompt}{suffix} ").strip().lower()
    if not response:
        return default_yes
    return response.startswith("y")


def short_addr(addr: str) -> str:
    if len(addr) < 12:
        return addr
    return f"{addr[:6]}…{addr[-6:]}"


# ─── Main flow ─────────────────────────────────────────────────

def cmd_init() -> None:
    Keypair = _require_stellar_sdk()
    httpx = _try_httpx()
    header("x402-middleware — interactive setup")
    print("This CLI will configure your Stellar-native API paywall in under 2 minutes.")
    print("It sets up TWO things:")
    print(f"  {C.CYAN}•{C.RESET} {C.BOLD}x402 paywall{C.RESET} — agents pay USDC per call")
    print(f"  {C.CYAN}•{C.RESET} {C.BOLD}SEP-10 wallet login{C.RESET} — operators sign in with their Stellar wallet")
    print()

    # ── Step 1: Treasury wallet ──────────────────────────────
    step(1, "Treasury wallet (where you receive USDC payments)")
    has_treasury = ask_yn("Do you already have a Stellar treasury wallet?", False)

    treasury_address = ""
    treasury_secret = ""
    if has_treasury:
        treasury_address = ask("  Treasury G-address:")
        if not treasury_address.startswith("G") or len(treasury_address) != 56:
            warn("That doesn't look like a valid Stellar G-address. Continuing anyway.")
        save_secret = ask_yn("  Save the S-key to .env for settlement?", True)
        if save_secret:
            treasury_secret = ask("  Treasury S-key:")
        ok(f"Using treasury: {short_addr(treasury_address)}")
    else:
        kp = Keypair.random()
        treasury_address = kp.public_key
        treasury_secret = kp.secret
        ok(f"Generated treasury wallet: {short_addr(treasury_address)}")
        info("Full address and S-key will be saved to .env")
        print()
        warn("IMPORTANT: fund this wallet with ~5 XLM + USDC reserve before going live.")
        warn("  1. Buy XLM on a CEX (Kraken, Coinbase, Binance) or use an existing wallet")
        warn(f"  2. Send to: {C.BOLD}{treasury_address}{C.RESET}")
        warn("  3. Also send some USDC (Circle) via Stellar to the same address")
    print()

    # ── Step 2: SEP-10 server signing keypair ────────────────
    step(2, "SEP-10 server signing keypair (for wallet sign-in)")
    info("This is a SEPARATE keypair used only to sign SEP-10 challenge")
    info("transactions. It never holds funds. Generated automatically.")
    sep10_kp = Keypair.random()
    ok(f"Generated SEP-10 signer: {short_addr(sep10_kp.public_key)}")
    print()

    # ── Step 3: JWT secret ────────────────────────────────────
    step(3, "JWT secret (for wallet session tokens)")
    jwt_secret = secrets.token_hex(32)
    ok("Generated 256-bit JWT secret")
    print()

    # ── Step 4: OZ facilitator API key ────────────────────────
    step(4, "OpenZeppelin Built-on-Stellar facilitator")
    info("The facilitator verifies and settles x402 payments on-chain.")
    info("It's free and takes one curl command to set up.")
    print()
    get_fac_key = ask_yn("  Fetch a facilitator API key now?", True)

    facilitator_key = ""
    if get_fac_key and httpx is not None:
        info("Calling https://channels.openzeppelin.com/gen ...")
        try:
            resp = httpx.post("https://channels.openzeppelin.com/gen", timeout=10)
            if resp.status_code < 400:
                data = resp.json()
                facilitator_key = data.get("apiKey", "")
                if facilitator_key:
                    ok(f"Got facilitator API key: {facilitator_key[:8]}...{facilitator_key[-4:]}")
                else:
                    warn("Response didn't contain apiKey. Get one manually.")
            else:
                warn(f"Facilitator gen returned {resp.status_code}. Get a key manually.")
        except Exception as e:
            warn(f"Failed to auto-fetch: {e}")
    elif get_fac_key:
        warn("httpx not available, can't auto-fetch")

    if not facilitator_key:
        info("You can get one manually with:")
        info(f"  {C.CYAN}curl -X POST https://channels.openzeppelin.com/gen{C.RESET}")
        manual_key = ask("  Paste the API key here (or ENTER to skip):")
        if manual_key:
            facilitator_key = manual_key

    if not facilitator_key:
        warn("No facilitator key — you'll need to run in dry-run mode until you add one")
    print()

    # ── Step 5: Write .env ───────────────────────────────────
    step(5, "Save configuration")
    env_path = ask("  Path to .env file:", ".env")
    full_env_path = Path.cwd() / env_path

    env_contents = build_env_contents(
        treasury_address=treasury_address,
        treasury_secret=treasury_secret,
        sep10_signer_secret=sep10_kp.secret,
        sep10_signer_address=sep10_kp.public_key,
        jwt_secret=jwt_secret,
        facilitator_key=facilitator_key,
        network="stellar:testnet",
        dry_run=("false" if facilitator_key else "true"),
    )

    if full_env_path.exists():
        overwrite = ask_yn(f"  .env already exists at {full_env_path}. Append to it?", True)
        if overwrite:
            with full_env_path.open("a") as f:
                f.write("\n\n" + env_contents)
            ok(f"Appended to {env_path}")
        else:
            alt = Path(str(full_env_path) + ".x402")
            alt.write_text(env_contents)
            ok(f"Saved to {env_path}.x402 (copy the values into your .env manually)")
    else:
        full_env_path.write_text(env_contents)
        ok(f"Saved to {env_path}")

    # Add to .gitignore
    gi_path = Path.cwd() / ".gitignore"
    if gi_path.exists():
        gi_contents = gi_path.read_text()
        if ".env" not in gi_contents:
            with gi_path.open("a") as f:
                f.write("\n.env\n")
            ok("Added .env to .gitignore")
        else:
            info(".gitignore already excludes .env")

    # Next steps
    print_next_steps(treasury_address, sep10_kp.public_key, bool(facilitator_key))


def build_env_contents(
    treasury_address: str,
    treasury_secret: str,
    sep10_signer_secret: str,
    sep10_signer_address: str,
    jwt_secret: str,
    facilitator_key: str,
    network: str,
    dry_run: str,
) -> str:
    treasury_secret_line = (
        f"X402_TREASURY_SECRET={treasury_secret}"
        if treasury_secret
        else "# X402_TREASURY_SECRET=  (not saved)"
    )
    fac_line = (
        f"X402_FACILITATOR_KEY={facilitator_key}"
        if facilitator_key
        else "# X402_FACILITATOR_KEY=  (get one: curl -X POST https://channels.openzeppelin.com/gen)"
    )
    return f"""# x402-middleware configuration (generated by x402-init)
# ─────────────────────────────────────────────────────────

# Treasury wallet — receives USDC payments from agents
X402_TREASURY_ADDRESS={treasury_address}
{treasury_secret_line}

# SEP-10 server signing keypair — signs wallet login challenges
SEP10_SERVER_SIGNING_KEY={sep10_signer_secret}
SEP10_SERVER_ADDRESS={sep10_signer_address}
SEP10_HOME_DOMAIN=localhost

# JWT secret — signs wallet session tokens (256-bit)
X402_JWT_SECRET={jwt_secret}

# OpenZeppelin Built-on-Stellar facilitator
X402_FACILITATOR_URL=https://channels.openzeppelin.com/x402
{fac_line}

# Network configuration
X402_NETWORK={network}
X402_DRY_RUN={dry_run}
"""


def print_next_steps(treasury: str, sep10_signer: str, has_fac_key: bool) -> None:
    header("Setup complete. Next steps.")

    print(f"  {C.BOLD}1. Add the paywall to your FastAPI app:{C.RESET}")
    print()
    print(f"     {C.BLUE}from{C.RESET} fastapi {C.BLUE}import{C.RESET} FastAPI, Request")
    print(f"     {C.BLUE}from{C.RESET} x402_middleware {C.BLUE}import{C.RESET} x402_paywall, sep10_auth")
    print()
    print(f"     app = {C.CYAN}FastAPI{C.RESET}()")
    print()
    print(f"     {C.DIM}# SEP-10 auto-registers /api/auth/challenge + /api/auth/token{C.RESET}")
    print(f"     {C.CYAN}sep10_auth{C.RESET}.mount(app)")
    print()
    print(f"     {C.DIM}# Paid endpoint — agents pay USDC per call{C.RESET}")
    print(f"     {C.CYAN}@app.post({C.GREEN}\"/api/sentiment\"{C.RESET}{C.CYAN}){C.RESET}")
    print(f"     {C.CYAN}@x402_paywall(price={C.YELLOW}0.05{C.RESET}{C.CYAN}){C.RESET}")
    print(f"     {C.BLUE}async def{C.RESET} sentiment(request: Request):")
    print(f"         {C.BLUE}return{C.RESET} {{{C.GREEN}\"score\"{C.RESET}: {C.YELLOW}0.82{C.RESET}}}")
    print()
    print(f"     {C.DIM}# Operator dashboard — SEP-10 wallet sign-in{C.RESET}")
    print(f"     {C.CYAN}@app.get({C.GREEN}\"/operator/stats\"{C.RESET}{C.CYAN}){C.RESET}")
    print(f"     {C.CYAN}@sep10_auth.protect(allowed_wallets=[{C.GREEN}\"{treasury[:8]}...\"{C.RESET}{C.CYAN}]){C.RESET}")
    print(f"     {C.BLUE}async def{C.RESET} stats(request: Request):")
    print(f"         {C.BLUE}return{C.RESET} {{{C.GREEN}\"wallet\"{C.RESET}: request.state.wallet}}")
    print()

    print(f"  {C.BOLD}2. Start your server:{C.RESET}")
    print(f"     {C.CYAN}uvicorn{C.RESET} main:app --port 3000")
    print()

    print(f"  {C.BOLD}3. Test it:{C.RESET}")
    print(f"     {C.CYAN}curl{C.RESET} -X POST http://localhost:3000/api/sentiment")
    print(f"     {C.DIM}→ 402 Payment Required (expected, no payment header){C.RESET}")
    print()

    if not has_fac_key:
        warn("You're in dry-run mode. Get a facilitator key to go live:")
        warn("  curl -X POST https://channels.openzeppelin.com/gen")
        warn("Then set X402_FACILITATOR_KEY in .env and X402_DRY_RUN=false")
        print()

    print(f"  {C.DIM}Treasury G-address:  {treasury}{C.RESET}")
    print(f"  {C.DIM}SEP-10 signer:       {sep10_signer}{C.RESET}")
    print()
    print(f"{C.GREEN}  Happy building. May your USDC flow freely.{C.RESET}")
    print()


def print_usage() -> None:
    print("""Usage: python -m x402_middleware [command]

Commands:
  init       Interactive setup (default). Generates wallets, JWT secret,
             fetches a facilitator API key, and writes .env.
  help       Show this message.

Run from your project root (where you want .env to live).
""")


def main() -> None:
    cmd = sys.argv[1] if len(sys.argv) > 1 else "init"
    if cmd in ("help", "--help", "-h"):
        print_usage()
        return
    if cmd != "init":
        print(f"Unknown command: {cmd}", file=sys.stderr)
        print_usage()
        sys.exit(2)
    try:
        cmd_init()
    except KeyboardInterrupt:
        print(f"\n{C.YELLOW}Aborted.{C.RESET}")
        sys.exit(130)
    except Exception as e:
        print(f"{C.RED}Error: {e}{C.RESET}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
