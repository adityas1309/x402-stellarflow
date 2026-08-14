#!/usr/bin/env bash
# record.sh — orchestrates the full stellarflow demo for video
# recording. Chains all the moving parts with narration-friendly sleeps so
# the video can be recorded in one take.
#
# Prerequisites (check with `demo/check-prereqs.sh` first):
#   - Postgres running with the stellarflow_x402 DB accessible
#   - backend/.env configured with APIFY_TOKEN + OPENAI_API_KEY
#   - `stellar` CLI with the `stellarflow` identity funded on testnet
#   - mcp-server and the middleware/node example both built
#
# Run:
#   ./demo/record.sh
#
# Or record a specific act:
#   ./demo/record.sh act1   # catalog + 402 challenge
#   ./demo/record.sh act2   # MCP init + paid call + dashboard
#   ./demo/record.sh act3   # Soroban safety rail demo
#
# Env:
#   STEP_DELAY=3          Seconds between narration steps (default: 3)
#   SKIP_BACKEND=1        Don't try to start the backend (already running)
#   FAST=1                STEP_DELAY=0.5, no narration sleeps

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# ─── Colors ───────────────────────────────────────────────────
if [[ -t 1 ]]; then
  C_RESET='\033[0m' C_BOLD='\033[1m' C_DIM='\033[2m'
  C_CYAN='\033[36m' C_GREEN='\033[32m' C_YELLOW='\033[33m' C_BLUE='\033[34m'
else
  C_RESET='' C_BOLD='' C_DIM='' C_CYAN='' C_GREEN='' C_YELLOW='' C_BLUE=''
fi

STEP_DELAY="${STEP_DELAY:-3}"
if [[ "${FAST:-}" == "1" ]]; then
  STEP_DELAY=0.5
fi

header() {
  echo
  printf '%b%s%b\n' "$C_CYAN" "$(printf '━%.0s' {1..76})" "$C_RESET"
  printf '%b  %b%s%b\n' "$C_CYAN" "$C_BOLD" "$1" "$C_RESET"
  printf '%b%s%b\n' "$C_CYAN" "$(printf '━%.0s' {1..76})" "$C_RESET"
  echo
}

step() {
  printf '\n%b[%s]%b %b%s%b\n' "$C_CYAN" "$1" "$C_RESET" "$C_BOLD" "$2" "$C_RESET"
}

narrate() {
  printf '%b  %s%b\n' "$C_DIM" "$1" "$C_RESET"
}

cmd() {
  printf '%b  $ %s%b\n' "$C_YELLOW" "$1" "$C_RESET"
}

pause() {
  local n="${1:-$STEP_DELAY}"
  if [[ "$n" != "0" && "$n" != "0.0" ]]; then
    sleep "$n"
  fi
}

check() {
  if command -v "$1" >/dev/null 2>&1; then
    printf '%b  ✓%b %s\n' "$C_GREEN" "$C_RESET" "$1 installed"
  else
    printf '%b  ✗%b %s NOT installed\n' '\033[31m' "$C_RESET" "$1"
    return 1
  fi
}

# ─── Preflight ────────────────────────────────────────────────
preflight() {
  header "Preflight check"
  check curl
  check node
  check stellar
  check go
  check python3

  # Backend reachable?
  if curl -sS --max-time 2 http://localhost:8086/api/catalog >/dev/null 2>&1; then
    printf '%b  ✓%b backend reachable at localhost:8086\n' "$C_GREEN" "$C_RESET"
  else
    printf '%b  ✗%b backend NOT reachable — run: cd backend && make run\n' '\033[31m' "$C_RESET"
    if [[ "${SKIP_BACKEND:-}" != "1" ]]; then
      echo "    (or run this script with SKIP_BACKEND=1 to ignore)"
      exit 1
    fi
  fi

  # Wallet file present?
  if [[ -f ~/.config/x402-mcp/agent.json ]]; then
    local addr
    addr=$(python3 -c 'import json; print(json.load(open("'"$HOME"'/.config/x402-mcp/agent.json"))["address"])')
    printf '%b  ✓%b wallet loaded: %s\n' "$C_GREEN" "$C_RESET" "$addr"
  else
    printf '%b  ✗%b no wallet at ~/.config/x402-mcp/agent.json — run: x402-mcp init\n' '\033[31m' "$C_RESET"
  fi

  pause 2
}

# ─── Act 1: The paywall in action ─────────────────────────────
act1() {
  header "Act 1 — An HTTP API gated by x402"
  narrate "StellarFlow is a sentiment-analysis SaaS. One endpoint: /api/x402/sentiment."
  narrate "Agents pay \$0.10 USDC per call. The treasury is a Stellar wallet."
  pause

  step "1.1" "Discover the public catalog"
  cmd "curl -s http://localhost:8086/api/catalog | jq"
  curl -sS http://localhost:8086/api/catalog | python3 -m json.tool
  pause

  step "1.2" "Try to call the endpoint WITHOUT paying"
  cmd "curl -i -X POST http://localhost:8086/api/x402/sentiment -d '{\"topic\":\"stellar\"}'"
  curl -sS -i -X POST http://localhost:8086/api/x402/sentiment \
    -H "Content-Type: application/json" \
    -d '{"topic":"stellar"}' | head -15
  echo
  narrate "→ HTTP 402 Payment Required (HTTP spec RFC 7231, 'reserved for future use')"
  narrate "→ accepts[]: USDC SAC contract, integer stroops, areFeesSponsored=true"
  pause
}

# ─── Act 2: The MCP client pays and gets real sentiment ───────
act2() {
  header "Act 2 — The agent pays the API via MCP"
  narrate "The MCP server is the glue between an LLM (Claude / Cursor / Codex)"
  narrate "and any x402 backend. Zero lines of x402 code in the agent."
  pause

  step "2.1" "Agent signs the payment + resubmits via MCP smoke test"
  cmd "cd mcp-server && STELLARFLOW_BASE_URL=http://localhost:8086 node scripts/smoke-test.mjs"
  cd "$ROOT/mcp-server"
  STELLARFLOW_BASE_URL=http://localhost:8086 node scripts/smoke-test.mjs 2>&1
  cd "$ROOT"
  pause
}

# ─── Act 3: On-chain safety rail ──────────────────────────────
act3() {
  header "Act 3 — On-chain safety rail (Soroban smart contract)"
  narrate "The operator set a daily spending cap for the agent on-chain."
  narrate "Every paid call first checks the cap via a Soroban contract."
  narrate "Over-cap spends are rejected BEFORE any USDC moves."
  pause

  step "3.1" "Run the live budget demo against testnet"
  cmd "cd backend && STEP_DELAY_SECONDS=1 go run ./cmd/budget-demo"
  cd "$ROOT/backend"
  STEP_DELAY_SECONDS=1 go run ./cmd/budget-demo 2>&1
  cd "$ROOT"
  pause
}

# ─── Closing ──────────────────────────────────────────────────
closing() {
  header "The template, in one import"
  narrate "For Node devs:"
  printf '%b    npm install @stellarflow/x402-middleware && npx x402-init%b\n' "$C_CYAN" "$C_RESET"
  printf '%b    import { x402, sep10Auth, dashboard } from "@stellarflow/x402-middleware"%b\n' "$C_CYAN" "$C_RESET"
  narrate ""
  narrate "For Python devs:"
  printf '%b    pip install x402-middleware && python -m x402_middleware init%b\n' "$C_CYAN" "$C_RESET"
  printf '%b    from x402_middleware import x402_paywall, sep10_auth, dashboard%b\n' "$C_CYAN" "$C_RESET"
  narrate ""
  narrate "For Go devs / any other stack:"
  printf '%b    ./x402-sidecar --endpoints endpoints.yaml --target http://your-api%b\n' "$C_CYAN" "$C_RESET"
  narrate ""
  narrate "Repo:  https://github.com/your-org/stellarflow"
  narrate "Live:  https://stellarflow.example.com"
  echo
}

# ─── Main ─────────────────────────────────────────────────────
case "${1:-all}" in
  preflight|check)
    preflight
    ;;
  act1)
    preflight
    act1
    ;;
  act2)
    act2
    ;;
  act3)
    act3
    ;;
  all)
    preflight
    act1
    act2
    act3
    closing
    ;;
  *)
    echo "Usage: $0 [preflight|act1|act2|act3|all]"
    exit 2
    ;;
esac
