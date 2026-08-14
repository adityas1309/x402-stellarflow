# x402 Express demo app

A minimal Express.js application that uses `@stellarflow/x402-middleware`
end-to-end: paywall + SEP-10 wallet auth + embedded operator dashboard.

This is the **Act 3 demo** from `VIDEO_SCRIPT.md`. It exists to show
a real "npm install → one line of middleware → it works" flow.

## What this shows in ~30 lines

```javascript
const { x402, sep10Auth, dashboard } = require("@stellarflow/x402-middleware");

// SEP-10 wallet sign-in (operator login)
app.use(sep10Auth.routes());

// Embedded dashboard at /x402/dashboard
dashboard.mount(app);

// Paid endpoint — THE ONE LINE that adds the paywall
app.post("/api/sentiment",
  x402({ price: 0.05 }),
  (req, res) => res.json({ sentiment: "positive", paidBy: req.x402.payer })
);
```

## Running it

**One-time setup (do this once before the video, as root of repo):**

```bash
cd middleware/node
npm install
npm run build
```

This builds the library at `middleware/node/dist/` so the demo app's
`file:../../middleware/node` dependency resolves correctly.

**Start the demo app:**

```bash
cd demo/express-app
npm install                 # pulls in express + the middleware
npx x402-init               # interactive setup (generates .env with keypair + JWT secret)
node server.js              # listens on http://localhost:3000
```

You should see:

```
  ╔════════════════════════════════════════════════════════════╗
  ║  x402 Express demo                                          ║
  ╚════════════════════════════════════════════════════════════╝

  Server:     http://localhost:3000
  Catalog:    http://localhost:3000/api/catalog
  Paywall:    POST http://localhost:3000/api/sentiment
  Dashboard:  http://localhost:3000/x402/dashboard
```

## Verify the paywall

```bash
# Public catalog — always accessible
curl http://localhost:3000/api/catalog

# Paid endpoint without payment header — expect 402
curl -X POST http://localhost:3000/api/sentiment \
  -H "Content-Type: application/json" \
  -d '{"topic":"stellar"}'

# → HTTP 402 Payment Required, with accepts[] describing:
#   - scheme: "exact"
#   - network: "stellar:testnet"
#   - amount: "500000" (stroops, 0.05 USDC)
#   - payTo: GBLDFWEL... (testnet treasury address)
#   - asset: CBIELTKS... (testnet USDC SAC)
```

## Verify the dashboard

Open `http://localhost:3000/x402/dashboard` in a browser. You'll see:
- Stellar-themed dark dashboard
- "Sign in with Freighter" button
- SEP-10 flow (challenge → sign → JWT)
- Live revenue counter + recent calls table (empty until you make paid
  calls)

To see the revenue counter tick up, run the MCP smoke test against
this local server (from the `mcp-server/` directory):

```bash
cd ../../mcp-server
# Dry-run (no real USDC, just stub payments)
STELLARFLOW_BASE_URL=http://localhost:3000 node scripts/smoke-test.mjs

# Or LIVE settlement on Stellar testnet (requires the local app to
# have X402_FACILITATOR_KEY + X402_DRY_RUN=false in .env, and the
# burner wallet at ~/.config/x402-mcp/agent.json to have USDC)
LIVE=true STELLARFLOW_BASE_URL=http://localhost:3000 \
  node scripts/smoke-test.mjs
```

## Going live (real USDC settlement from this local app)

Edit `demo/express-app/.env` after running `npx x402-init`:

```bash
# Replace the defaults x402-init generated with your real OZ facilitator key
X402_FACILITATOR_KEY=<your OZ key from https://channels.openzeppelin.com/gen>
X402_DRY_RUN=false
```

Then the local Express app will verify + settle real x402 payments
on Stellar testnet via the OpenZeppelin facilitator, moving USDC
from the agent wallet to `X402_TREASURY_ADDRESS`.

## Clean slate (re-record the demo)

```bash
cd demo/express-app
rm -rf node_modules .env
# Then run the "Start the demo app" steps again
```
