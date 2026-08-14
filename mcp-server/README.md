# StellarFlow MCP Server

A Model Context Protocol server that lets any MCP-compatible AI client
(Claude Code, Codex, Cursor, ...) consume live public-signal analysis
**by paying per call via x402 on Stellar**.

The agent's wallet IS the identity. No signup, no API keys, no tracking.
Each tool invocation pays the operator's treasury directly in USDC.

## What you get

The current hosted product exposes one paid tool:

| Tool                          | Price (USDC) | Returns |
|-------------------------------|--------------|---------|
| `stellarflow_sentiment`          | $0.05        | Live public stories, sentiment signal, keywords, and source count |

Each response ends with an x402 receipt so the LLM can mention the live
payment: `_Paid $0.05 USDC via openzeppelin_`.

## Install

```bash
cd mcp-server
npm install
npm run build
```

## Connect to Claude Code

Once built, register with Claude Code:

```bash
claude mcp add stellarflow \
  --command "node $PWD/dist/index.js" \
  --env STELLARFLOW_BASE_URL=http://localhost:8086
```

Or, edit `~/.config/claude-code/mcp.json` directly:

```json
{
  "mcpServers": {
    "stellarflow": {
      "command": "node",
      "args": ["/absolute/path/to/mcp-server/dist/index.js"],
      "env": {
        "STELLARFLOW_BASE_URL": "https://x402-mcp-stellar-template-main.vercel.app",
        "STELLARFLOW_DRY_RUN": "false",
        "BUDGET_USDC_PER_SESSION": "1.00"
      }
    }
  }
}
```

Restart Claude Code and the `stellarflow_sentiment` tool will be available.

## Configuration

| Env var                     | Default                       | Purpose |
|-----------------------------|-------------------------------|---------|
| `STELLARFLOW_BASE_URL`         | `http://localhost:8086`       | Base URL of the stellarflow backend |
| `STELLARFLOW_AGENT_ADDRESS`    | `GMCPDEMOAGENT...` (56-char placeholder) | Stellar G-address used as the payer in `X-PAYMENT` |
| `STELLARFLOW_NETWORK`          | `stellar:testnet`             | Stellar testnet only |
| `STELLARFLOW_CLIENT_ID`        | `claude-code`                 | Forwarded as `X-StellarFlow-Client` and shown in the operator live feed |
| `STELLARFLOW_DRY_RUN`          | `true`                        | When true, the X-PAYMENT header is a stub envelope (no real signing) |
| `BUDGET_USDC_PER_CALL`      | `0.20`                        | Per-call ceiling — anything above is rejected outright |
| `BUDGET_USDC_PER_SESSION`   | `1.00`                        | Process-lifetime ceiling — when hit, all subsequent calls fail |
| `BUDGET_USDC_PER_DAY`       | `5.00`                        | Rolling 24h ceiling kept in process memory |

## Demo flow

1. Start the stellarflow backend: `cd ../backend && make run`
2. Open the operator dashboard: `http://localhost:8086`
3. In Claude Code (with the MCP installed), ask:
   > Plan an Instagram strategy for a new fitness brand competing with
   > Gymshark, Nike, and Alo Yoga. Use the stellarflow tools.
4. Claude Code calls `stellarflow_compare_competitors`, then
   `stellarflow_posting_heatmap` and `stellarflow_trending_hashtags` for the
   relevant brands. Each call appears live on the dashboard with the
   payer wallet, price, reasoning, and tx hash (in live mode).
5. Total cost shows in Claude Code's response footer: about $0.45 for a
   typical 4-call research session.

## Live mode (Day 4)

Right now `STELLARFLOW_DRY_RUN=true` is the safe default and the X-PAYMENT
header is a stub envelope that the stellarflow backend's middleware accepts
without contacting the OpenZeppelin facilitator.

To go live (Day 4 of the hackathon plan):

1. The backend flips its own `X402_DRY_RUN=false`
2. The MCP server starts signing real Soroban auth entries via
   `x402-stellar` (the npm package — drops into `src/client.ts`'s
   `buildPaymentHeader`)
3. The agent wallet (`STELLARFLOW_AGENT_ADDRESS`) needs USDC on the chosen
   network
4. The OpenZeppelin facilitator verifies the signature, settles the
   payment on Stellar, and the response includes a real `tx_hash`
   linking to `stellarchain.io/transactions/...`

The budget enforcement (per-call / per-session / per-day) is the safety
net during this transition. Don't disable it.
